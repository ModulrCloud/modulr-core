// Thread for the final stage of block finalization (last mile)
package threads

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	LAST_MILE_MUTEX    sync.Mutex
	LAST_MILE_WS_CONNS = make(map[string]*websocket.Conn)
	LAST_MILE_GUARDS   = utils.NewWebsocketGuards()
	LAST_MILE_WAITER   *utils.QuorumWaiter
)

func selectLastMileFinalizersForEpoch(epochHandler *structures.EpochDataHandler) []int {

	anchorsCount := len(globals.ANCHORS)

	if anchorsCount == 0 {
		return nil
	}

	count := LAST_MILE_FINALIZERS_COUNT
	if count > anchorsCount {
		count = anchorsCount
	}

	seed := utils.Blake3(fmt.Sprintf("LAST_MILE_FINALIZERS_SELECTION:%d:%s", epochHandler.Id, epochHandler.Hash))

	indices := make([]int, anchorsCount)
	for i := range indices {
		indices[i] = i
	}

	for i := 0; i < count; i++ {
		hashHex := utils.Blake3(seed + "_" + strconv.Itoa(i))
		r := hashHexToUint64ForLastMile(hashHex) % uint64(anchorsCount-i)
		j := i + int(r)
		indices[i], indices[j] = indices[j], indices[i]
	}

	return indices[:count]
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

	if globals.CONFIGURATION.AnchorPubKey == "" {
		return false
	}

	selectedIndices := selectLastMileFinalizersForEpoch(epochHandler)

	for _, idx := range selectedIndices {
		if globals.ANCHORS[idx].Pubkey == globals.CONFIGURATION.AnchorPubKey {
			return true
		}
	}

	return false
}

func openCoreNodeConnections() {

	LAST_MILE_MUTEX.Lock()
	defer LAST_MILE_MUTEX.Unlock()

	for _, conn := range LAST_MILE_WS_CONNS {
		if conn != nil {
			_ = conn.Close()
		}
	}

	LAST_MILE_WS_CONNS = make(map[string]*websocket.Conn)

	for _, anchor := range globals.ANCHORS {
		if anchor.WssCoreNodeUrl == "" {
			continue
		}

		conn, _, err := websocket.DefaultDialer.Dial(anchor.WssCoreNodeUrl, nil)
		if err != nil {
			continue
		}

		LAST_MILE_WS_CONNS[anchor.Pubkey] = conn
	}

	LAST_MILE_GUARDS = utils.NewWebsocketGuards()
	LAST_MILE_WAITER = utils.NewQuorumWaiter(len(globals.ANCHORS), LAST_MILE_GUARDS)
}

func storeLastMileProof(proof *structures.LastMileFinalizationProof) {

	key := []byte(fmt.Sprintf("LAST_MILE_PROOF:%d", proof.AbsoluteHeight))

	if value, err := json.Marshal(proof); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put(key, value, nil)
	}
}

func LoadLastMileProof(absoluteHeight int) *structures.LastMileFinalizationProof {

	key := []byte(fmt.Sprintf("LAST_MILE_PROOF:%d", absoluteHeight))

	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)

	if err != nil {
		return nil
	}

	var proof structures.LastMileFinalizationProof

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

	copy := make(map[string]structures.ExecutionStats, len(data))
	for k, v := range data {
		copy[k] = v
	}

	return copy, true
}

func fetchBlockForLastMile(blockId string) *block_pack.Block {

	if raw, err := databases.BLOCKS.Get([]byte(blockId), nil); err == nil {
		var block block_pack.Block
		if json.Unmarshal(raw, &block) == nil {
			return &block
		}
	}

	response := getBlockAndAfpFromPoD(blockId)

	if response != nil && response.Block != nil {
		return response.Block
	}

	return nil
}

func LastMileFinalizerThread() {

	lastProcessedEpoch := -1
	connectionsReady := false

	tracker := utils.LoadLastMileSequenceState(LAST_MILE_FINALIZER_TRACKER_KEY)

	for {

		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
		epochSnapshot := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		if epochSnapshot.Id != lastProcessedEpoch {

			lastProcessedEpoch = epochSnapshot.Id
			connectionsReady = false

			selectedIndices := selectLastMileFinalizersForEpoch(&epochSnapshot)

			if len(selectedIndices) > 0 {

				pubkeys := make([]string, len(selectedIndices))
				for i, idx := range selectedIndices {
					pubkeys[i] = globals.ANCHORS[idx].Pubkey
				}

				utils.LogWithTime(
					fmt.Sprintf("Last mile finalizer: epoch %d => selected %d anchors %v", epochSnapshot.Id, len(selectedIndices), pubkeys),
					utils.CYAN_COLOR,
				)

			}

		}

		if !weAreLastMileFinalizer(&epochSnapshot) {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if !connectionsReady {
			openCoreNodeConnections()
			connectionsReady = true
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

		block := fetchBlockForLastMile(blockId)

		if block == nil {
			utils.LogWithTimeThrottled(
				"last_mile:block_not_found:"+blockId,
				5*time.Second,
				fmt.Sprintf("Last mile finalizer: can't fetch block %s", blockId),
				utils.YELLOW_COLOR,
			)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		blockHash := block.GetHash()

		proof := tryCollectLastMileProof(int(tracker.NextHeight), blockId, blockHash)

		if proof != nil {

			storeLastMileProof(proof)
			tracker.Advance()
			utils.PersistLastMileSequenceState(LAST_MILE_FINALIZER_TRACKER_KEY, tracker)

			websocket_pack.SendLastMileFinalizationProofToPoD(*proof)

			utils.LogWithTime(
				fmt.Sprintf("Last mile proof collected for height %d => %s (hash: %s...)", proof.AbsoluteHeight, blockId, blockHash[:8]),
				utils.DEEP_GREEN_COLOR,
			)

			continue
		}

		time.Sleep(200 * time.Millisecond)

	}
}

func tryCollectLastMileProof(absoluteHeight int, blockId, blockHash string) *structures.LastMileFinalizationProof {

	majority := utils.GetAnchorsQuorumMajority()

	request := websocket_pack.WsLastMileFinalizationProofRequest{
		Route:          constants.WsRouteGetLastMileFinalizationProof,
		AbsoluteHeight: absoluteHeight,
		BlockId:        blockId,
		BlockHash:      blockHash,
	}

	message, err := json.Marshal(request)

	if err != nil {
		return nil
	}

	LAST_MILE_MUTEX.Lock()
	waiter := LAST_MILE_WAITER
	wsConns := LAST_MILE_WS_CONNS
	LAST_MILE_MUTEX.Unlock()

	if waiter == nil {
		return nil
	}

	anchorPubkeys := make([]string, 0, len(globals.ANCHORS))
	for _, anchor := range globals.ANCHORS {
		anchorPubkeys = append(anchorPubkeys, anchor.Pubkey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	validateLastMileProof := func(id string, raw []byte) bool {
		var response websocket_pack.WsLastMileFinalizationProofResponse

		if json.Unmarshal(raw, &response) != nil {
			return false
		}

		anchorPubkeySet := make(map[string]bool, len(globals.ANCHORS))
		for _, anchor := range globals.ANCHORS {
			anchorPubkeySet[anchor.Pubkey] = true
		}

		if !anchorPubkeySet[response.Voter] {
			return false
		}

		dataToVerify := strings.Join([]string{
			"LAST_MILE_FINALIZATION_PROOF",
			strconv.Itoa(absoluteHeight),
			blockId,
			blockHash,
		}, ":")

		return cryptography.VerifySignature(dataToVerify, response.Voter, response.Sig)
	}

	responses, ok := waiter.SendAndWaitValidated(ctx, message, anchorPubkeys, wsConns, majority, validateLastMileProof)

	if !ok {
		utils.LogWithTimeThrottled(
			fmt.Sprintf("last_mile:majority_failed:%d", absoluteHeight),
			5*time.Second,
			fmt.Sprintf("Last mile: failed to collect majority for height %d (anchors=%d majority=%d)", absoluteHeight, len(globals.ANCHORS), majority),
			utils.YELLOW_COLOR,
		)
		return nil
	}

	proofs := make(map[string]string)

	for _, raw := range responses {
		var response websocket_pack.WsLastMileFinalizationProofResponse

		if json.Unmarshal(raw, &response) == nil {
			proofs[response.Voter] = response.Sig
		}
	}

	if len(proofs) < majority {
		return nil
	}

	return &structures.LastMileFinalizationProof{
		AbsoluteHeight: absoluteHeight,
		BlockId:        blockId,
		BlockHash:      blockHash,
		Proofs:         proofs,
	}
}
