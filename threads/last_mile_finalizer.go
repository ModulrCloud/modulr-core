// Thread for collecting height attestations from quorum and delivering quorum rotation attestations to anchors
package threads

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/modulrcloud/modulr-core/websocket_pack"

	"github.com/gorilla/websocket"
)

const LAST_MILE_FINALIZERS_COUNT = 5

const LAST_MILE_FINALIZER_TRACKER_KEY = "LAST_MILE_FINALIZER_TRACKER"

var (
	LAST_MILE_MUTEX           sync.Mutex
	LAST_MILE_QUORUM_WS_CONNS = make(map[string]*websocket.Conn)
	LAST_MILE_QUORUM_GUARDS   = utils.NewWebsocketGuards()
	LAST_MILE_QUORUM_WAITER   *utils.QuorumWaiter

	LAST_MILE_ANCHOR_WS_CONNS = make(map[string]*websocket.Conn)
)

func selectLastMileFinalizersForEpoch(epochHandler *structures.EpochDataHandler) []string {

	quorum := epochHandler.Quorum

	if len(quorum) == 0 {
		return nil
	}

	count := LAST_MILE_FINALIZERS_COUNT
	if count > len(quorum) {
		count = len(quorum)
	}

	seed := utils.Blake3(fmt.Sprintf("LAST_MILE_FINALIZERS_SELECTION:%d:%s", epochHandler.Id, epochHandler.Hash))

	indices := make([]int, len(quorum))
	for i := range indices {
		indices[i] = i
	}

	for i := 0; i < count; i++ {
		hashHex := utils.Blake3(seed + "_" + strconv.Itoa(i))
		r := hashHexToUint64ForLastMile(hashHex) % uint64(len(quorum)-i)
		j := i + int(r)
		indices[i], indices[j] = indices[j], indices[i]
	}

	result := make([]string, count)
	for i := 0; i < count; i++ {
		result[i] = quorum[indices[i]]
	}

	return result
}

func hashHexToUint64ForLastMile(hashHex string) uint64 {

	if len(hashHex) < 16 {
		return 0
	}

	b, err := hex.DecodeString(hashHex[:16])

	if err != nil {
		return 0
	}

	return binary.BigEndian.Uint64(b)
}

func weAreLastMileFinalizer(epochHandler *structures.EpochDataHandler) bool {

	selected := selectLastMileFinalizersForEpoch(epochHandler)

	return slices.Contains(selected, globals.CONFIGURATION.PublicKey)
}

func openQuorumConnectionsForLastMile(quorum []string) {

	LAST_MILE_MUTEX.Lock()
	defer LAST_MILE_MUTEX.Unlock()

	for _, conn := range LAST_MILE_QUORUM_WS_CONNS {
		if conn != nil {
			_ = conn.Close()
		}
	}

	LAST_MILE_QUORUM_WS_CONNS = make(map[string]*websocket.Conn)

	quorumUrls := utils.GetQuorumUrlsAndPubkeys(&structures.EpochDataHandler{Quorum: quorum})

	for _, node := range quorumUrls {
		if node.Url == "" || node.PubKey == globals.CONFIGURATION.PublicKey {
			continue
		}

		wsUrl := strings.Replace(node.Url, "http://", "ws://", 1)
		wsUrl = strings.Replace(wsUrl, "https://", "wss://", 1)

		conn, _, err := websocket.DefaultDialer.Dial(wsUrl, nil)
		if err != nil {
			continue
		}

		LAST_MILE_QUORUM_WS_CONNS[node.PubKey] = conn
	}

	LAST_MILE_QUORUM_GUARDS = utils.NewWebsocketGuards()
	LAST_MILE_QUORUM_WAITER = utils.NewQuorumWaiter(len(quorum), LAST_MILE_QUORUM_GUARDS)
}

func openAnchorConnectionsForLastMile() {

	LAST_MILE_MUTEX.Lock()
	defer LAST_MILE_MUTEX.Unlock()

	for _, conn := range LAST_MILE_ANCHOR_WS_CONNS {
		if conn != nil {
			_ = conn.Close()
		}
	}

	LAST_MILE_ANCHOR_WS_CONNS = make(map[string]*websocket.Conn)

	for _, anchor := range globals.ANCHORS {
		if anchor.WssAnchorUrl == "" {
			continue
		}

		conn, _, err := websocket.DefaultDialer.Dial(anchor.WssAnchorUrl, nil)
		if err != nil {
			continue
		}

		LAST_MILE_ANCHOR_WS_CONNS[anchor.Pubkey] = conn
	}
}

func storeHeightAttestation(proof *structures.HeightAttestation) {

	key := []byte(fmt.Sprintf("HEIGHT_ATTESTATION:%d", proof.AbsoluteHeight))

	if value, err := json.Marshal(proof); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put(key, value, nil)
	}
}

func LoadHeightAttestation(absoluteHeight int) *structures.HeightAttestation {

	key := []byte(fmt.Sprintf("HEIGHT_ATTESTATION:%d", absoluteHeight))

	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)

	if err != nil {
		return nil
	}

	var proof structures.HeightAttestation

	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}

	return &proof
}

func getEpochHandlerForTracker(epochId int) *structures.EpochDataHandler {

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	if handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.Id == epochId {
		copy := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
		return &copy
	}
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	key := []byte("EPOCH_HANDLER:" + strconv.Itoa(epochId))

	if raw, err := databases.APPROVEMENT_THREAD_METADATA.Get(key, nil); err == nil {
		var snapshot structures.EpochDataSnapshot
		if json.Unmarshal(raw, &snapshot) == nil {
			return &snapshot.EpochDataHandler
		}
	}

	return nil
}

func snapshotAlignmentData() (map[string]structures.ExecutionStats, bool) {

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	data := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders

	if data == nil {
		return nil, false
	}

	cp := make(map[string]structures.ExecutionStats, len(data))
	for k, v := range data {
		cp[k] = v
	}

	return cp, true
}

func LastMileFinalizerThread() {

	lastProcessedEpoch := -1
	quorumConnectionsReady := false
	anchorConnectionsSent := false
	lastRotationEpoch := -1

	tracker := utils.LoadLastMileSequenceState(LAST_MILE_FINALIZER_TRACKER_KEY)

	for {

		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
		epochSnapshot := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		if epochSnapshot.Id != lastProcessedEpoch {

			lastProcessedEpoch = epochSnapshot.Id
			quorumConnectionsReady = false
			anchorConnectionsSent = false

			selected := selectLastMileFinalizersForEpoch(&epochSnapshot)

			if len(selected) > 0 {
				utils.LogWithTime(
					fmt.Sprintf("Last mile finalizer: epoch %d => selected %d from quorum %v", epochSnapshot.Id, len(selected), selected),
					utils.CYAN_COLOR,
				)
			}
		}

		if !weAreLastMileFinalizer(&epochSnapshot) {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if !quorumConnectionsReady {
			openQuorumConnectionsForLastMile(epochSnapshot.Quorum)
			quorumConnectionsReady = true
		}

		// On epoch change, send QuorumRotationAttestation to anchors
		if epochSnapshot.Id > 0 && lastRotationEpoch < epochSnapshot.Id-1 {
			prevEpochId := epochSnapshot.Id - 1
			rotationAttestation := tryCollectQuorumRotation(prevEpochId, epochSnapshot.Id, epochSnapshot.Quorum)
			if rotationAttestation != nil {
				if !anchorConnectionsSent {
					openAnchorConnectionsForLastMile()
					anchorConnectionsSent = true
				}
				deliverQuorumRotationToAnchors(rotationAttestation)
				websocket_pack.SendQuorumRotationAttestationToPoD(*rotationAttestation)
				lastRotationEpoch = prevEpochId
				utils.LogWithTime(
					fmt.Sprintf("Quorum rotation attestation sent for epoch %d->%d", prevEpochId, epochSnapshot.Id),
					utils.DEEP_GREEN_COLOR,
				)
			}
		}

		epochHandler := getEpochHandlerForTracker(tracker.EpochId)

		if epochHandler == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		lastBlocksByLeaders, ok := snapshotAlignmentData()

		if !ok {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		blockId := tracker.CurrentBlockId(epochHandler.LeadersSequence, lastBlocksByLeaders)

		if blockId == "" {

			if tracker.AllLeadersDone(epochHandler.LeadersSequence) {

				nextEpochHandler := getEpochHandlerForTracker(tracker.EpochId + 1)

				if nextEpochHandler != nil {
					tracker.AdvanceToNextEpoch()
					utils.PersistLastMileSequenceState(LAST_MILE_FINALIZER_TRACKER_KEY, tracker)
					continue
				}

			}

			time.Sleep(200 * time.Millisecond)
			continue
		}

		blockHash := getBlockHashById(blockId)

		if blockHash == "" {
			utils.LogWithTimeThrottled(
				"last_mile:block_hash_not_found:"+blockId,
				5*time.Second,
				fmt.Sprintf("Last mile finalizer: can't get hash for block %s", blockId),
				utils.YELLOW_COLOR,
			)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		proof := tryCollectHeightAttestation(int(tracker.NextHeight), blockId, blockHash, tracker.EpochId, epochHandler)

		if proof != nil {

			storeHeightAttestation(proof)
			tracker.Advance()
			utils.PersistLastMileSequenceState(LAST_MILE_FINALIZER_TRACKER_KEY, tracker)

			websocket_pack.SendHeightAttestationToPoD(*proof)

			utils.LogWithTime(
				fmt.Sprintf("Height attestation collected for height %d => %s (hash: %s...)", proof.AbsoluteHeight, blockId, blockHash[:8]),
				utils.DEEP_GREEN_COLOR,
			)

			continue
		}

		time.Sleep(200 * time.Millisecond)

	}
}

func getBlockHashById(blockId string) string {

	raw, err := databases.BLOCKS.Get([]byte(blockId), nil)
	if err != nil {
		return ""
	}

	var block block_pack.Block
	if json.Unmarshal(raw, &block) == nil {
		return block.GetHash()
	}

	return ""
}

func tryCollectHeightAttestation(absoluteHeight int, blockId, blockHash string, epochId int, epochHandler *structures.EpochDataHandler) *structures.HeightAttestation {

	majority := utils.GetQuorumMajority(epochHandler)

	request := websocket_pack.WsHeightAttestationRequest{
		Route:          constants.WsRouteSignHeightAttestation,
		AbsoluteHeight: absoluteHeight,
		BlockId:        blockId,
		BlockHash:      blockHash,
		EpochId:        epochId,
	}

	message, err := json.Marshal(request)

	if err != nil {
		return nil
	}

	LAST_MILE_MUTEX.Lock()
	waiter := LAST_MILE_QUORUM_WAITER
	wsConns := LAST_MILE_QUORUM_WS_CONNS
	LAST_MILE_MUTEX.Unlock()

	if waiter == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	validateProof := func(id string, raw []byte) bool {
		var response websocket_pack.WsHeightAttestationResponse

		if json.Unmarshal(raw, &response) != nil {
			return false
		}

		if !slices.Contains(epochHandler.Quorum, response.Voter) {
			return false
		}

		dataToVerify := strings.Join([]string{
			"HEIGHT_ATTESTATION",
			strconv.Itoa(absoluteHeight),
			blockId,
			blockHash,
			strconv.Itoa(epochId),
		}, ":")

		return cryptography.VerifySignature(dataToVerify, response.Voter, response.Sig)
	}

	responses, ok := waiter.SendAndWaitValidated(ctx, message, epochHandler.Quorum, wsConns, majority, validateProof)

	if !ok {
		utils.LogWithTimeThrottled(
			fmt.Sprintf("last_mile:ha_majority_failed:%d", absoluteHeight),
			5*time.Second,
			fmt.Sprintf("Last mile: failed to collect height attestation majority for height %d (quorum=%d majority=%d)", absoluteHeight, len(epochHandler.Quorum), majority),
			utils.YELLOW_COLOR,
		)
		return nil
	}

	proofs := make(map[string]string)

	for _, raw := range responses {
		var response websocket_pack.WsHeightAttestationResponse

		if json.Unmarshal(raw, &response) == nil {
			proofs[response.Voter] = response.Sig
		}
	}

	if len(proofs) < majority {
		return nil
	}

	return &structures.HeightAttestation{
		AbsoluteHeight: absoluteHeight,
		BlockId:        blockId,
		BlockHash:      blockHash,
		EpochId:        epochId,
		Proofs:         proofs,
	}
}

func tryCollectQuorumRotation(epochId, nextEpochId int, nextQuorum []string) *structures.QuorumRotationAttestation {

	prevEpochHandler := getEpochHandlerForTracker(epochId)
	if prevEpochHandler == nil {
		return nil
	}

	majority := utils.GetQuorumMajority(prevEpochHandler)

	sortedQuorum := make([]string, len(nextQuorum))
	copy(sortedQuorum, nextQuorum)
	sort.Strings(sortedQuorum)

	request := websocket_pack.WsQuorumRotationRequest{
		Route:       constants.WsRouteSignQuorumRotation,
		EpochId:     epochId,
		NextEpochId: nextEpochId,
		NextQuorum:  nextQuorum,
	}

	message, err := json.Marshal(request)
	if err != nil {
		return nil
	}

	LAST_MILE_MUTEX.Lock()
	waiter := LAST_MILE_QUORUM_WAITER
	wsConns := LAST_MILE_QUORUM_WS_CONNS
	LAST_MILE_MUTEX.Unlock()

	if waiter == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dataToVerify := "QUORUM_ROTATION:" + strconv.Itoa(epochId) + ":" + strconv.Itoa(nextEpochId) + ":" + strings.Join(sortedQuorum, ",")

	validateProof := func(id string, raw []byte) bool {
		var response websocket_pack.WsQuorumRotationResponse

		if json.Unmarshal(raw, &response) != nil {
			return false
		}

		if !slices.Contains(prevEpochHandler.Quorum, response.Voter) {
			return false
		}

		return cryptography.VerifySignature(dataToVerify, response.Voter, response.Sig)
	}

	responses, ok := waiter.SendAndWaitValidated(ctx, message, prevEpochHandler.Quorum, wsConns, majority, validateProof)

	if !ok {
		return nil
	}

	proofs := make(map[string]string)

	for _, raw := range responses {
		var response websocket_pack.WsQuorumRotationResponse
		if json.Unmarshal(raw, &response) == nil {
			proofs[response.Voter] = response.Sig
		}
	}

	if len(proofs) < majority {
		return nil
	}

	return &structures.QuorumRotationAttestation{
		EpochId:     epochId,
		NextEpochId: nextEpochId,
		NextQuorum:  nextQuorum,
		Proofs:      proofs,
	}
}

func deliverQuorumRotationToAnchors(attestation *structures.QuorumRotationAttestation) {

	LAST_MILE_MUTEX.Lock()
	conns := LAST_MILE_ANCHOR_WS_CONNS
	LAST_MILE_MUTEX.Unlock()

	message, err := json.Marshal(struct {
		Route       string                                `json:"route"`
		Attestation structures.QuorumRotationAttestation `json:"attestation"`
	}{
		Route:       "accept_quorum_rotation",
		Attestation: *attestation,
	})

	if err != nil {
		return
	}

	for _, conn := range conns {
		if conn != nil {
			_ = conn.WriteMessage(websocket.TextMessage, message)
		}
	}
}
