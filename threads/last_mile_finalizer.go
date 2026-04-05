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

func openQuorumConnectionsForLastMile(epochHandler *structures.EpochDataHandler) {
	LAST_MILE_MUTEX.Lock()
	defer LAST_MILE_MUTEX.Unlock()

	for _, conn := range LAST_MILE_QUORUM_WS_CONNS {
		if conn != nil {
			_ = conn.Close()
		}
	}

	LAST_MILE_QUORUM_WS_CONNS = make(map[string]*websocket.Conn)

	quorumUrls := utils.GetQuorumUrlsAndPubkeys(epochHandler)

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
	LAST_MILE_QUORUM_WAITER = utils.NewQuorumWaiter(len(epochHandler.Quorum), LAST_MILE_QUORUM_GUARDS)
}

func openTemporaryQuorumConnections(epochHandler *structures.EpochDataHandler) (map[string]*websocket.Conn, *utils.QuorumWaiter) {
	conns := make(map[string]*websocket.Conn)

	quorumUrls := utils.GetQuorumUrlsAndPubkeys(epochHandler)

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

		conns[node.PubKey] = conn
	}

	guards := utils.NewWebsocketGuards()
	waiter := utils.NewQuorumWaiter(len(epochHandler.Quorum), guards)

	return conns, waiter
}

func closeTemporaryQuorumConnections(conns map[string]*websocket.Conn) {
	for _, conn := range conns {
		if conn != nil {
			_ = conn.Close()
		}
	}
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
	key := []byte(fmt.Sprintf(constants.DBKeyPrefixHeightAttestation+"%d", proof.AbsoluteHeight))

	if value, err := json.Marshal(proof); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put(key, value, nil)
	}
}

func LoadHeightAttestation(absoluteHeight int) *structures.HeightAttestation {
	key := []byte(fmt.Sprintf(constants.DBKeyPrefixHeightAttestation+"%d", absoluteHeight))

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

	if snapshot := utils.GetEpochSnapshot(epochId); snapshot != nil {
		return &snapshot.EpochDataHandler
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
	lastFirstBlockEpochId := -1

	tracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker)

	if getFirstBlockDataFromDB(tracker.EpochId) != nil {
		lastFirstBlockEpochId = tracker.EpochId
	}

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

		// On epoch change, collect QuorumRotationAttestation from *previous* epoch's quorum
		// and deliver to anchors. Must happen before opening connections to the new quorum.
		if epochSnapshot.Id > 0 && lastRotationEpoch < epochSnapshot.Id-1 {
			prevEpochId := epochSnapshot.Id - 1
			prevEpochHandler := getEpochHandlerForTracker(prevEpochId)

			if prevEpochHandler != nil {
				tmpConns, tmpWaiter := openTemporaryQuorumConnections(prevEpochHandler)

				rotationAttestation := tryCollectQuorumRotationWithConns(
					prevEpochId, epochSnapshot.Id, epochSnapshot.Hash, epochSnapshot.Quorum,
					prevEpochHandler, tmpConns, tmpWaiter,
				)

				closeTemporaryQuorumConnections(tmpConns)

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
		}

		if !quorumConnectionsReady {
			openQuorumConnectionsForLastMile(&epochSnapshot)
			quorumConnectionsReady = true
		}

		epochHandler := getEpochHandlerForTracker(tracker.EpochId)

		if epochHandler == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		lastBlocksByLeaders, _ := snapshotAlignmentData()
		if lastBlocksByLeaders == nil {
			lastBlocksByLeaders = make(map[string]structures.ExecutionStats)
		}

		blockId := tracker.CurrentBlockId(epochHandler.LeadersSequence, lastBlocksByLeaders)

		if blockId == "" {
			if tracker.AllLeadersDone(epochHandler.LeadersSequence) {
				nextEpochHandler := getEpochHandlerForTracker(tracker.EpochId + 1)

				if nextEpochHandler != nil {
					tracker.AdvanceToNextEpoch()
					utils.PersistLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker, tracker)
					continue
				}
			}

			time.Sleep(200 * time.Millisecond)
			continue
		}

		blockHash := getBlockHashById(blockId)

		if blockHash == "" {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		confirmed, _ := tracker.IsBlockConfirmed(epochHandler.LeadersSequence, lastBlocksByLeaders, blockHash, epochHandler)

		if !confirmed {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		proof := tryCollectHeightAttestation(int(tracker.NextHeight), blockId, blockHash, tracker.EpochId, tracker.HeightInEpoch, epochHandler)

		if proof != nil {
			storeHeightAttestation(proof)

			if proof.HeightInEpoch == 0 && proof.EpochId != lastFirstBlockEpochId {
				storeFirstBlockAttestation(proof)
				parts := strings.Split(blockId, ":")
				if len(parts) == 3 {
					_ = storeDataAboutFirstBlockInEpoch(proof.EpochId, &FirstBlockData{
						FirstBlockCreator: parts[1],
						FirstBlockHash:    blockHash,
					})
				}
				lastFirstBlockEpochId = proof.EpochId
				utils.LogWithTime(
					fmt.Sprintf("First core block in epoch %d detected (HeightInEpoch=0): creator=%s, hash=%s...", proof.EpochId, parts[1], utils.ShortHash(blockHash)),
					utils.GREEN_COLOR,
				)
			}

			tracker.Advance()
			utils.PersistLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker, tracker)

			websocket_pack.SendHeightAttestationToPoD(*proof)

			utils.LogWithTime(
				fmt.Sprintf("Height attestation collected for height %d => %s (hash: %s...)", proof.AbsoluteHeight, blockId, utils.ShortHash(blockHash)),
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

func tryCollectHeightAttestation(absoluteHeight int, blockId, blockHash string, epochId int, heightInEpoch int, epochHandler *structures.EpochDataHandler) *structures.HeightAttestation {
	majority := utils.GetQuorumMajority(epochHandler)

	request := websocket_pack.WsHeightAttestationRequest{
		Route:          constants.WsRouteSignHeightAttestation,
		AbsoluteHeight: absoluteHeight,
		BlockId:        blockId,
		BlockHash:      blockHash,
		EpochId:        epochId,
		HeightInEpoch:  heightInEpoch,
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
			constants.SigningPrefixHeightAttestation,
			strconv.Itoa(absoluteHeight),
			blockId,
			blockHash,
			strconv.Itoa(epochId),
			strconv.Itoa(heightInEpoch),
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
		HeightInEpoch:  heightInEpoch,
		Proofs:         proofs,
	}
}

func tryCollectQuorumRotationWithConns(
	epochId, nextEpochId int, nextEpochHash string, nextQuorum []string,
	prevEpochHandler *structures.EpochDataHandler,
	wsConns map[string]*websocket.Conn, waiter *utils.QuorumWaiter,
) *structures.QuorumRotationAttestation {
	if prevEpochHandler == nil || waiter == nil {
		return nil
	}

	majority := utils.GetQuorumMajority(prevEpochHandler)

	sortedQuorum := make([]string, len(nextQuorum))
	copy(sortedQuorum, nextQuorum)
	sort.Strings(sortedQuorum)

	request := websocket_pack.WsQuorumRotationRequest{
		Route:         constants.WsRouteSignQuorumRotation,
		EpochId:       epochId,
		NextEpochId:   nextEpochId,
		NextEpochHash: nextEpochHash,
		NextQuorum:    nextQuorum,
	}

	message, err := json.Marshal(request)
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dataToVerify := constants.SigningPrefixQuorumRotation + strconv.Itoa(epochId) + ":" + strconv.Itoa(nextEpochId) + ":" + nextEpochHash + ":" + strings.Join(sortedQuorum, ",")

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
		EpochId:       epochId,
		NextEpochId:   nextEpochId,
		NextEpochHash: nextEpochHash,
		NextQuorum:    nextQuorum,
		Proofs:        proofs,
	}
}

func deliverQuorumRotationToAnchors(attestation *structures.QuorumRotationAttestation) {
	LAST_MILE_MUTEX.Lock()
	conns := LAST_MILE_ANCHOR_WS_CONNS
	LAST_MILE_MUTEX.Unlock()

	message, err := json.Marshal(struct {
		Route       string                               `json:"route"`
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
