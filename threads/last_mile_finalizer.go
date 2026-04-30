// Thread for collecting height attestations and epoch data attestations from quorum
package threads

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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

// LastMileFinalizerThread runs on ALL quorum member nodes.
// It walks through blocks in leader order (exactly like block_execution.go on main),
// verifies each block via AFP / SequenceAlignmentData, and writes
// LAST_MILE_HEIGHT_MAP:<height> => blockId into the local DB (used by SignHeightProof).
//
// On nodes selected as finalizers (5 per epoch), it additionally collects
// AggregatedHeightProof signatures from the quorum and handles epoch rotation proofs.
func LastMileFinalizerThread() {
	lastProcessedEpoch := -1
	isFinalizer := false
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
			isFinalizer = iAmLastMileFinalizer(&epochSnapshot)
			quorumConnectionsReady = false
			anchorConnectionsSent = false

			if isFinalizer {
				selected := selectLastMileFinalizersForEpoch(&epochSnapshot)
				utils.LogWithTime(
					fmt.Sprintf("Last mile finalizer: epoch %d => selected %d from quorum %v", epochSnapshot.Id, len(selected), selected),
					utils.CYAN_COLOR,
				)
			}
		}

		// --- Finalizer-only: epoch rotation proof collection ---
		if isFinalizer && epochSnapshot.Id > 0 && lastRotationEpoch < epochSnapshot.Id-1 {
			prevEpochId := epochSnapshot.Id - 1
			if utils.HasLocallySequencedFullEpoch(prevEpochId) {
				prevEpochHandler := getEpochHandlerForTracker(prevEpochId)

				if prevEpochHandler != nil {
					tmpConns, tmpWaiter := openTemporaryQuorumConnections(prevEpochHandler)

					epochRotationProof := tryCollectAggregatedEpochRotationProofWithConns(
						prevEpochId, epochSnapshot.Id,
						prevEpochHandler, tmpConns, tmpWaiter,
					)

					closeTemporaryQuorumConnections(tmpConns)

					if epochRotationProof != nil {
						if !anchorConnectionsSent {
							openAnchorConnectionsForLastMile()
							anchorConnectionsSent = true
						}

						ackProof := deliverAggregatedEpochRotationProofToAnchors(epochRotationProof)
						websocket_pack.SendAggregatedEpochRotationProofToPoD(*epochRotationProof)
						storeAggregatedEpochRotationProof(epochRotationProof)

						if ackProof != nil && utils.VerifyAggregatedAnchorEpochAckProof(ackProof) {
							utils.StoreAggregatedAnchorEpochAckProof(ackProof)
							websocket_pack.SendAggregatedAnchorEpochAckProofToPoD(*ackProof)

							nextEpochHandler := getEpochHandlerForTracker(epochSnapshot.Id)
							deliverAggregatedAnchorEpochAckProofToNewQuorum(ackProof, nextEpochHandler)

							utils.LogWithTime(
								fmt.Sprintf("Aggregated anchor epoch ack proof collected and delivered for epoch %d->%d", prevEpochId, epochSnapshot.Id),
								utils.DEEP_GREEN_COLOR,
							)
						}

						lastRotationEpoch = prevEpochId
						utils.LogWithTime(
							fmt.Sprintf("Aggregated epoch rotation proof sent for epoch %d->%d", prevEpochId, epochSnapshot.Id),
							utils.DEEP_GREEN_COLOR,
						)
					}
				}
			}
		}

		// --- Finalizer-only: quorum connections for proof collection ---
		if isFinalizer && !quorumConnectionsReady {
			openQuorumConnectionsForLastMileFinalizer(&epochSnapshot)
			quorumConnectionsReady = true
		}

		if tracker.EpochId < epochSnapshot.Id {
			if syncedTracker, synced := syncLastMileTrackerToCurrentEpochStart(tracker, &epochSnapshot); synced {
				tracker = syncedTracker
				rotateFinalizerEpochIfNeeded(tracker.EpochId)
				continue
			}
		}

		// --- Block sequencing (ALL nodes, mirrors block_execution.go from main) ---

		epochHandler := getEpochHandlerForTracker(tracker.EpochId)

		if epochHandler == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		// Keep FINALIZER_THREAD_METADATA.EpochDataHandler in sync with the tracker.
		// Sequence/anchor threads rely on this for canonical sequencing of the current epoch.
		rotateFinalizerEpochIfNeeded(tracker.EpochId)

		if tracker.LeaderIndex >= len(epochHandler.LeadersSequence) {
			completedBoundary := buildCompletedEpochBoundaryFromTracker(tracker, tracker.EpochId)
			if completedBoundary == nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}

			if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileFinalizerTracker, tracker, completedBoundary); err != nil {
				utils.LogWithTime(
					fmt.Sprintf("Last mile sequencer: failed to persist completed epoch boundary for epoch %d: %v", tracker.EpochId, err),
					utils.RED_COLOR,
				)
				time.Sleep(200 * time.Millisecond)
				continue
			}

			nextEpochHandler := getEpochHandlerForTracker(tracker.EpochId + 1)
			if nextEpochHandler != nil {
				nextTracker := *tracker
				nextTracker.AdvanceToNextEpoch()
				if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileFinalizerTracker, &nextTracker, nil); err != nil {
					utils.LogWithTime(
						fmt.Sprintf("Last mile sequencer: failed to persist tracker advance to epoch %d: %v", nextTracker.EpochId, err),
						utils.RED_COLOR,
					)
					time.Sleep(200 * time.Millisecond)
					continue
				}
				tracker = &nextTracker
				continue
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}

		leader := epochHandler.LeadersSequence[tracker.LeaderIndex]
		blockId := fmt.Sprintf("%d:%s:%d", tracker.EpochId, leader, tracker.BlockIndex)
		lastBlocksByLeaders := snapshotLastBlocksByLeaders()
		lastBlock, known := lastBlocksByLeaders[leader]

		// If alignment already proved that this leader ended earlier, we may have
		// locally produced/fetched extra blocks for the same leader that are not
		// part of the canonical sequence. Skip them and move to the next leader.
		if known && tracker.BlockIndex > lastBlock.Index {
			utils.LogWithTime(
				fmt.Sprintf(
					"Last mile sequencer: skipping non-canonical block position %s because aligned last index for leader is %d",
					blockId,
					lastBlock.Index,
				),
				utils.YELLOW_COLOR,
			)
			nextTracker := *tracker
			nextTracker.LeaderIndex++
			nextTracker.BlockIndex = 0
			if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileFinalizerTracker, &nextTracker, nil); err != nil {
				utils.LogWithTime(
					fmt.Sprintf("Last mile sequencer: failed to persist non-canonical skip for %s: %v", blockId, err),
					utils.RED_COLOR,
				)
				time.Sleep(200 * time.Millisecond)
				continue
			}
			tracker = &nextTracker
			continue
		}

		blockHash := getBlockHashByBlockId(blockId)

		if blockHash == "" {
			nextEpochVisible := getEpochHandlerForTracker(tracker.EpochId+1) != nil

			if known && lastBlock.Index < 0 {
				nextTracker := *tracker
				nextTracker.LeaderIndex++
				nextTracker.BlockIndex = 0
				if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileFinalizerTracker, &nextTracker, nil); err != nil {
					utils.LogWithTime(
						fmt.Sprintf("Last mile sequencer: failed to persist empty-leader skip for epoch %d leader %s: %v", tracker.EpochId, leader, err),
						utils.RED_COLOR,
					)
					time.Sleep(200 * time.Millisecond)
					continue
				}
				tracker = &nextTracker
				continue
			}

			alignmentIndex := -999
			alignmentHash := ""
			if known {
				alignmentIndex = lastBlock.Index
				alignmentHash = lastBlock.Hash
			}

			utils.LogWithTimeThrottled(
				fmt.Sprintf("last_mile:missing_block:%d:%s:%d", tracker.EpochId, leader, tracker.BlockIndex),
				2*time.Second,
				fmt.Sprintf(
					"Last mile sequencer waiting for block body: trackerEpoch=%d currentEpoch=%d nextHeight=%d heightInEpoch=%d leaderIndex=%d/%d leader=%s blockIndex=%d blockId=%s alignmentKnown=%t alignmentIndex=%d alignmentHash=%s nextEpochVisible=%t",
					tracker.EpochId,
					epochSnapshot.Id,
					tracker.NextHeight,
					tracker.HeightInEpoch,
					tracker.LeaderIndex,
					len(epochHandler.LeadersSequence)-1,
					leader,
					tracker.BlockIndex,
					blockId,
					known,
					alignmentIndex,
					utils.ShortHash(alignmentHash),
					nextEpochVisible,
				),
				utils.YELLOW_COLOR,
			)

			time.Sleep(200 * time.Millisecond)
			continue
		}

		isLastBlock := false
		confirmed := false

		lastBlocksByLeaders = snapshotLastBlocksByLeaders()
		lastBlock, known = lastBlocksByLeaders[leader]

		if known && lastBlock.Index == tracker.BlockIndex && lastBlock.Hash == blockHash {
			confirmed = true
			isLastBlock = true
		}

		if !confirmed {
			nextBlockId := fmt.Sprintf("%d:%s:%d", tracker.EpochId, leader, tracker.BlockIndex+1)
			if utils.HasLocalVerifiedAfp(nextBlockId, epochHandler) {
				confirmed = true
			}
		}

		if !confirmed {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		currentHeight := tracker.NextHeight
		currentHeightInEpoch := tracker.HeightInEpoch

		// Persist mappings atomically with the current tracker checkpoint so the
		// height-voter never observes a half-written local sequencing state.
		if isFinalizer {
			currentTrackerCheckpoint := *tracker
			if err := utils.PersistLastMileMappingsAndState(
				constants.DBKeyLastMileFinalizerTracker,
				currentHeight,
				blockId,
				currentHeightInEpoch,
				&currentTrackerCheckpoint,
			); err != nil {
				utils.LogWithTime(
					fmt.Sprintf("Last mile sequencer: failed to persist local height mapping for %s at height %d: %v", blockId, currentHeight, err),
					utils.RED_COLOR,
				)
				time.Sleep(200 * time.Millisecond)
				continue
			}
		}

		// --- Finalizer-only: collect AggregatedHeightProof before advancing ---
		if isFinalizer {
			var previousProof *structures.AggregatedHeightProof
			if currentHeight > 0 {
				previousProof = LoadAggregatedHeightProof(int(currentHeight - 1))
				if previousProof == nil {
					time.Sleep(200 * time.Millisecond)
					continue
				}
			}

			proof := tryCollectAggregatedHeightProof(int(currentHeight), blockId, blockHash, tracker.EpochId, currentHeightInEpoch, epochHandler, previousProof)
			if proof == nil {
				time.Sleep(200 * time.Millisecond)
				continue
			}

			storeAggregatedHeightProof(proof)

			if proof.HeightInEpoch == 0 && proof.EpochId != lastFirstBlockEpochId {
				storeFirstBlockAggregatedHeightProof(proof)
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

			websocket_pack.SendAggregatedHeightProofToPoD(*proof)

			utils.LogWithTime(
				fmt.Sprintf("Aggregated height proof collected for height %d => %s (hash: %s...)", proof.AbsoluteHeight, blockId, utils.ShortHash(blockHash)),
				utils.DEEP_GREEN_COLOR,
			)
		}

		// Advance tracker (ALL nodes — finalizers reach here only after successful proof collection)
		nextTracker := *tracker
		if isLastBlock {
			nextTracker.LeaderIndex++
			nextTracker.BlockIndex = 0
		} else {
			nextTracker.BlockIndex++
		}
		nextTracker.NextHeight = currentHeight + 1
		nextTracker.HeightInEpoch = currentHeightInEpoch + 1

		var completedBoundary *structures.LastMileEpochBoundary
		if nextTracker.LeaderIndex >= len(epochHandler.LeadersSequence) {
			completedBoundary = newLastMileEpochBoundary(epochHandler.Id, currentHeight, blockId, blockHash)
		}

		if isFinalizer {
			if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileFinalizerTracker, &nextTracker, completedBoundary); err != nil {
				utils.LogWithTime(
					fmt.Sprintf("Last mile sequencer: failed to persist tracker advance after height %d: %v", currentHeight, err),
					utils.RED_COLOR,
				)
				time.Sleep(200 * time.Millisecond)
				continue
			}
			tracker = &nextTracker
			continue
		}

		if err := utils.PersistLastMileMappingsAndStateTransition(
			constants.DBKeyLastMileFinalizerTracker,
			currentHeight,
			blockId,
			currentHeightInEpoch,
			&nextTracker,
			completedBoundary,
		); err != nil {
			utils.LogWithTime(
				fmt.Sprintf("Last mile sequencer: failed to persist local sequencing progress for %s at height %d: %v", blockId, currentHeight, err),
				utils.RED_COLOR,
			)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		tracker = &nextTracker
	}
}

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
		r := utils.HashHexToUint64(hashHex) % uint64(len(quorum)-i)
		j := i + int(r)
		indices[i], indices[j] = indices[j], indices[i]
	}

	result := make([]string, count)
	for i := 0; i < count; i++ {
		result[i] = quorum[indices[i]]
	}

	return result
}

func iAmLastMileFinalizer(epochHandler *structures.EpochDataHandler) bool {

	selected := selectLastMileFinalizersForEpoch(epochHandler)

	return slices.Contains(selected, globals.CONFIGURATION.PublicKey)
}

func openQuorumConnectionsForLastMileFinalizer(epochHandler *structures.EpochDataHandler) {
	LAST_MILE_MUTEX.Lock()
	defer LAST_MILE_MUTEX.Unlock()

	for _, conn := range LAST_MILE_QUORUM_WS_CONNS {
		if conn != nil {
			_ = conn.Close()
		}
	}

	LAST_MILE_QUORUM_WS_CONNS = make(map[string]*websocket.Conn)
	LAST_MILE_QUORUM_GUARDS = utils.NewWebsocketGuards()
	utils.OpenWebsocketConnectionsWithQuorum(epochHandler.Quorum, LAST_MILE_QUORUM_WS_CONNS, LAST_MILE_QUORUM_GUARDS)
	LAST_MILE_QUORUM_WAITER = utils.NewQuorumWaiter(len(epochHandler.Quorum), LAST_MILE_QUORUM_GUARDS)
}

func openTemporaryQuorumConnections(epochHandler *structures.EpochDataHandler) (map[string]*websocket.Conn, *utils.QuorumWaiter) {
	conns := make(map[string]*websocket.Conn)
	guards := utils.NewWebsocketGuards()
	utils.OpenWebsocketConnectionsWithQuorum(epochHandler.Quorum, conns, guards)
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

// getOrRedialAnchorConn returns the live websocket to the given anchor, redialing
// lazily if the cached connection is missing. The redial is bounded to a few
// attempts with a small backoff so that a transient anchor hiccup doesn't force
// us to wait for the next epoch rotation to refresh LAST_MILE_ANCHOR_WS_CONNS.
//
// Returns nil if the anchor URL is empty or all redial attempts failed.
func getOrRedialAnchorConn(anchorPubkey, anchorUrl string) *websocket.Conn {
	if anchorUrl == "" {
		return nil
	}

	LAST_MILE_MUTEX.Lock()
	conn := LAST_MILE_ANCHOR_WS_CONNS[anchorPubkey]
	LAST_MILE_MUTEX.Unlock()
	if conn != nil {
		return conn
	}

	const (
		anchorRedialAttempts = 3
		anchorRedialBackoff  = 200 * time.Millisecond
	)

	for attempt := 1; attempt <= anchorRedialAttempts; attempt++ {
		fresh, _, err := websocket.DefaultDialer.Dial(anchorUrl, nil)
		if err == nil {
			LAST_MILE_MUTEX.Lock()
			if existing := LAST_MILE_ANCHOR_WS_CONNS[anchorPubkey]; existing != nil {
				_ = fresh.Close()
				conn = existing
			} else {
				LAST_MILE_ANCHOR_WS_CONNS[anchorPubkey] = fresh
				conn = fresh
			}
			LAST_MILE_MUTEX.Unlock()
			return conn
		}
		utils.LogWithTimeThrottled(
			"last_mile:anchor:redial:"+anchorPubkey,
			2*time.Second,
			fmt.Sprintf("Last mile: anchor %s redial failed (attempt %d/%d): %v", anchorPubkey, attempt, anchorRedialAttempts, err),
			utils.YELLOW_COLOR,
		)
		if attempt < anchorRedialAttempts {
			time.Sleep(anchorRedialBackoff)
		}
	}
	return nil
}

// dropAnchorConn closes and removes the cached connection for the anchor so the
// next getOrRedialAnchorConn call dials fresh. Safe under LAST_MILE_MUTEX.
func dropAnchorConn(anchorPubkey string, expected *websocket.Conn) {
	LAST_MILE_MUTEX.Lock()
	defer LAST_MILE_MUTEX.Unlock()
	if cur, ok := LAST_MILE_ANCHOR_WS_CONNS[anchorPubkey]; ok && (expected == nil || cur == expected) {
		if cur != nil {
			_ = cur.Close()
		}
		delete(LAST_MILE_ANCHOR_WS_CONNS, anchorPubkey)
	}
}

func storeAggregatedHeightProof(proof *structures.AggregatedHeightProof) {
	key := []byte(fmt.Sprintf(constants.DBKeyPrefixAggregatedHeightProof+"%d", proof.AbsoluteHeight))

	if value, err := json.Marshal(proof); err == nil {
		_ = databases.FINALIZATION_THREAD_METADATA.Put(key, value, nil)
	}
}

func LoadAggregatedHeightProof(absoluteHeight int) *structures.AggregatedHeightProof {
	key := []byte(fmt.Sprintf(constants.DBKeyPrefixAggregatedHeightProof+"%d", absoluteHeight))

	raw, err := databases.FINALIZATION_THREAD_METADATA.Get(key, nil)

	if err != nil {
		return nil
	}

	var proof structures.AggregatedHeightProof

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

func snapshotLastBlocksByLeaders() map[string]structures.ExecutionStats {
	handlers.FINALIZER_THREAD_METADATA.RWMutex.RLock()
	defer handlers.FINALIZER_THREAD_METADATA.RWMutex.RUnlock()

	data := handlers.FINALIZER_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders
	if data == nil {
		return make(map[string]structures.ExecutionStats)
	}

	cp := make(map[string]structures.ExecutionStats, len(data))
	for k, v := range data {
		cp[k] = v
	}
	return cp
}

// rotateFinalizerEpochIfNeeded synchronizes FINALIZER_THREAD_METADATA.Handler.EpochDataHandler
// with tracker.EpochId. Whenever LMF advances to a new epoch (either via natural
// LeadersSequence completion or via fast-forward sync), this function loads the
// next epoch's handler from APPROVEMENT_THREAD_METADATA DB and resets SequenceAlignmentData.
//
// Returns true if rotation actually happened. Returns false if no rotation was needed,
// or if the next epoch handler is not yet available locally.
func rotateFinalizerEpochIfNeeded(targetEpochId int) bool {
	handlers.FINALIZER_THREAD_METADATA.RWMutex.RLock()
	currentEpochId := handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler.Id
	handlers.FINALIZER_THREAD_METADATA.RWMutex.RUnlock()

	if currentEpochId == targetEpochId {
		return false
	}

	nextEpochHandler := getEpochHandlerForTracker(targetEpochId)
	if nextEpochHandler == nil {
		return false
	}

	handlers.FINALIZER_THREAD_METADATA.RWMutex.Lock()
	defer handlers.FINALIZER_THREAD_METADATA.RWMutex.Unlock()

	if handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler.Id == targetEpochId {
		return false
	}

	handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler = *nextEpochHandler
	handlers.FINALIZER_THREAD_METADATA.Handler.SequenceAlignmentData = structures.AlignmentDataHandler{
		CurrentAnchorAssumption:         0,
		CurrentAnchorBlockIndexObserved: -1,
		CurrentLeaderToExecBlocksFrom:   0,
		LastBlocksByLeaders:             make(map[string]structures.ExecutionStats),
		LastBlocksByAnchors:             make(map[int]structures.ExecutionStats),
	}

	persistFinalizerThreadMetadataLocked()

	utils.LogWithTime(
		fmt.Sprintf("FINALIZER_THREAD_METADATA: rotated to epoch %d (from %d)", targetEpochId, currentEpochId),
		utils.CYAN_COLOR,
	)

	return true
}

func getBlockHashByBlockId(blockId string) string {
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

func newLastMileEpochBoundary(epochId int, finishedOnHeight int64, finishedOnBlockId, finishedOnHash string) *structures.LastMileEpochBoundary {
	if finishedOnHash == "" {
		finishedOnHash = constants.ZeroHash
	}

	return &structures.LastMileEpochBoundary{
		EpochId:           epochId,
		FinishedOnHeight:  finishedOnHeight,
		FinishedOnBlockId: finishedOnBlockId,
		FinishedOnHash:    finishedOnHash,
	}
}

func buildCompletedEpochBoundaryFromTracker(tracker *utils.LastMileSequenceState, epochId int) *structures.LastMileEpochBoundary {
	if tracker == nil {
		return nil
	}

	finishedOnHeight := tracker.NextHeight - 1
	if finishedOnHeight < 0 {
		return newLastMileEpochBoundary(epochId, -1, "", constants.ZeroHash)
	}

	finishedOnBlockId := utils.LoadHeightBlockIdMapping(finishedOnHeight)
	if finishedOnBlockId == "" {
		utils.LogWithTimeThrottled(
			fmt.Sprintf("last_mile:boundary_missing_block_id:%d", epochId),
			2*time.Second,
			fmt.Sprintf("Last mile sequencer: missing blockId mapping for completed epoch %d at height %d", epochId, finishedOnHeight),
			utils.YELLOW_COLOR,
		)
		return nil
	}

	finishedOnHash := getBlockHashByBlockId(finishedOnBlockId)
	if finishedOnHash == "" {
		utils.LogWithTimeThrottled(
			fmt.Sprintf("last_mile:boundary_missing_hash:%d:%d", epochId, finishedOnHeight),
			2*time.Second,
			fmt.Sprintf("Last mile sequencer: missing block hash for completed epoch %d at height %d blockId=%s", epochId, finishedOnHeight, finishedOnBlockId),
			utils.YELLOW_COLOR,
		)
		return nil
	}

	return newLastMileEpochBoundary(epochId, finishedOnHeight, finishedOnBlockId, finishedOnHash)
}

func syncLastMileTrackerToCurrentEpochStart(
	tracker *utils.LastMileSequenceState,
	currentEpochHandler *structures.EpochDataHandler,
) (*utils.LastMileSequenceState, bool) {
	if tracker == nil || currentEpochHandler == nil || currentEpochHandler.Id <= 0 || tracker.EpochId >= currentEpochHandler.Id {
		return nil, false
	}

	proof := fetchVerifiedAggregatedEpochRotationProof(currentEpochHandler.Id - 1)
	if proof == nil || proof.NextEpochId != currentEpochHandler.Id {
		return nil, false
	}

	nextHeight := proof.FinishedOnHeight + 1
	if nextHeight < tracker.NextHeight {
		utils.LogWithTimeThrottled(
			fmt.Sprintf("last_mile:catchup_regression:%d:%d", tracker.EpochId, currentEpochHandler.Id),
			2*time.Second,
			fmt.Sprintf(
				"Last mile sequencer: refusing tracker fast-forward to epoch %d because proof boundary height %d would regress local nextHeight %d",
				currentEpochHandler.Id,
				proof.FinishedOnHeight,
				tracker.NextHeight,
			),
			utils.YELLOW_COLOR,
		)
		return nil, false
	}

	nextTracker := &utils.LastMileSequenceState{
		EpochId:       currentEpochHandler.Id,
		LeaderIndex:   0,
		BlockIndex:    0,
		NextHeight:    nextHeight,
		HeightInEpoch: 0,
	}

	if err := utils.PersistLastMileStateTransition(constants.DBKeyLastMileFinalizerTracker, nextTracker, nil); err != nil {
		utils.LogWithTime(
			fmt.Sprintf("Last mile sequencer: failed to persist catch-up tracker sync to epoch %d: %v", currentEpochHandler.Id, err),
			utils.RED_COLOR,
		)
		return nil, false
	}

	utils.LogWithTime(
		fmt.Sprintf(
			"Last mile sequencer: fast-forwarded tracker from epoch %d to epoch %d using rotation proof boundary height=%d blockId=%s hash=%s",
			tracker.EpochId,
			currentEpochHandler.Id,
			proof.FinishedOnHeight,
			proof.FinishedOnBlockId,
			utils.ShortHash(proof.FinishedOnHash),
		),
		utils.CYAN_COLOR,
	)

	return nextTracker, true
}

func tryCollectAggregatedHeightProof(absoluteHeight int, blockId, blockHash string, epochId int, heightInEpoch int, epochHandler *structures.EpochDataHandler, previousProof *structures.AggregatedHeightProof) *structures.AggregatedHeightProof {
	majority := utils.GetQuorumMajority(epochHandler)

	request := websocket_pack.WsHeightProofRequest{
		Route:                         constants.WsRouteSignHeightProof,
		AbsoluteHeight:                absoluteHeight,
		BlockId:                       blockId,
		BlockHash:                     blockHash,
		EpochId:                       epochId,
		HeightInEpoch:                 heightInEpoch,
		PreviousAggregatedHeightProof: previousProof,
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
		var response websocket_pack.WsHeightProofResponse

		if json.Unmarshal(raw, &response) != nil {
			return false
		}

		if !slices.Contains(epochHandler.Quorum, response.Voter) {
			return false
		}

		dataToVerify := strings.Join([]string{
			constants.SigningPrefixHeightProof,
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
			fmt.Sprintf("Last mile: failed to collect aggregated height proof majority for height %d (quorum=%d majority=%d)", absoluteHeight, len(epochHandler.Quorum), majority),
			utils.YELLOW_COLOR,
		)
		return nil
	}

	proofs := make(map[string]string)

	for _, raw := range responses {
		var response websocket_pack.WsHeightProofResponse

		if json.Unmarshal(raw, &response) == nil {
			proofs[response.Voter] = response.Sig
		}
	}

	if len(proofs) < majority {
		return nil
	}

	return &structures.AggregatedHeightProof{
		AbsoluteHeight: absoluteHeight,
		BlockId:        blockId,
		BlockHash:      blockHash,
		EpochId:        epochId,
		HeightInEpoch:  heightInEpoch,
		Proofs:         proofs,
	}
}

func tryCollectAggregatedEpochRotationProofWithConns(
	epochId, nextEpochId int,
	prevEpochHandler *structures.EpochDataHandler,
	wsConns map[string]*websocket.Conn, waiter *utils.QuorumWaiter,
) *structures.AggregatedEpochRotationProof {
	if prevEpochHandler == nil || waiter == nil {
		return nil
	}

	localEpochData := utils.LoadNextEpochData(nextEpochId)
	if localEpochData == nil {
		return nil
	}

	epochDataHash := utils.ComputeEpochDataHash(localEpochData)
	if epochDataHash == "" {
		return nil
	}

	majority := utils.GetQuorumMajority(prevEpochHandler)
	boundary := utils.LoadLastMileEpochBoundary(epochId)
	if boundary == nil {
		return nil
	}

	request := websocket_pack.WsEpochRotationProofRequest{
		Route:             constants.WsRouteSignEpochRotationProof,
		EpochId:           epochId,
		NextEpochId:       nextEpochId,
		EpochDataHash:     epochDataHash,
		FinishedOnHeight:  boundary.FinishedOnHeight,
		FinishedOnBlockId: boundary.FinishedOnBlockId,
		FinishedOnHash:    boundary.FinishedOnHash,
	}

	message, err := json.Marshal(request)
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dataToVerify := utils.BuildEpochRotationProofSigningPayload(
		epochId,
		nextEpochId,
		epochDataHash,
		boundary.FinishedOnHeight,
		boundary.FinishedOnBlockId,
		boundary.FinishedOnHash,
	)

	validateProof := func(id string, raw []byte) bool {
		var response websocket_pack.WsEpochRotationProofResponse
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
		var response websocket_pack.WsEpochRotationProofResponse
		if json.Unmarshal(raw, &response) == nil {
			proofs[response.Voter] = response.Sig
		}
	}

	if len(proofs) < majority {
		return nil
	}

	return &structures.AggregatedEpochRotationProof{
		EpochId:           epochId,
		NextEpochId:       nextEpochId,
		EpochData:         *localEpochData,
		EpochDataHash:     epochDataHash,
		FinishedOnHeight:  boundary.FinishedOnHeight,
		FinishedOnBlockId: boundary.FinishedOnBlockId,
		FinishedOnHash:    boundary.FinishedOnHash,
		Proofs:            proofs,
	}
}

func storeAggregatedEpochRotationProof(proof *structures.AggregatedEpochRotationProof) {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedEpochRotationProof, proof.EpochId))
	if value, err := json.Marshal(proof); err == nil {
		_ = databases.FINALIZATION_THREAD_METADATA.Put(key, value, nil)
	}
}

func LoadAggregatedEpochRotationProof(epochId int) *structures.AggregatedEpochRotationProof {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedEpochRotationProof, epochId))
	raw, err := databases.FINALIZATION_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return nil
	}
	var proof structures.AggregatedEpochRotationProof
	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}
	return &proof
}

func deliverAggregatedEpochRotationProofToAnchors(proof *structures.AggregatedEpochRotationProof) *structures.AggregatedAnchorEpochAckProof {
	message, err := json.Marshal(struct {
		Route string                                  `json:"route"`
		Proof structures.AggregatedEpochRotationProof `json:"proof"`
	}{
		Route: constants.WsRouteAcceptAggregatedEpochRotationProof,
		Proof: *proof,
	})

	if err != nil {
		return nil
	}

	type anchorAck struct {
		Anchor string
		Sig    string
	}

	// Iterate over the canonical anchor list (not the conn map) so we also try
	// anchors whose cached connection is currently nil — getOrRedialAnchorConn
	// will attempt a bounded lazy redial for them.
	anchors := globals.ANCHORS

	ackChan := make(chan anchorAck, len(anchors))
	var wg sync.WaitGroup

	for _, anchor := range anchors {
		if anchor.WssAnchorUrl == "" {
			continue
		}
		wg.Add(1)
		go func(pubkey, wsUrl string) {
			defer wg.Done()

			c := getOrRedialAnchorConn(pubkey, wsUrl)
			if c == nil {
				return
			}

			if err := c.WriteMessage(websocket.TextMessage, message); err != nil {
				dropAnchorConn(pubkey, c)
				// Lazy retry: redial once and try the same write again.
				if c2 := getOrRedialAnchorConn(pubkey, wsUrl); c2 != nil {
					if err := c2.WriteMessage(websocket.TextMessage, message); err != nil {
						dropAnchorConn(pubkey, c2)
						return
					}
					c = c2
				} else {
					return
				}
			}

			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, respBytes, err := c.ReadMessage()
			c.SetReadDeadline(time.Time{})
			if err != nil {
				dropAnchorConn(pubkey, c)
				return
			}

			var resp struct {
				Status    string `json:"status"`
				Anchor    string `json:"anchor"`
				Signature string `json:"signature"`
			}
			if json.Unmarshal(respBytes, &resp) != nil || resp.Status != "OK" || resp.Signature == "" || resp.Anchor == "" {
				return
			}

			ackChan <- anchorAck{Anchor: resp.Anchor, Sig: resp.Signature}
		}(anchor.Pubkey, anchor.WssAnchorUrl)
	}

	go func() {
		wg.Wait()
		close(ackChan)
	}()

	proofs := make(map[string]string)
	for ack := range ackChan {
		proofs[ack.Anchor] = ack.Sig
	}

	majority := utils.GetAnchorsQuorumMajority()
	if len(proofs) < majority {
		return nil
	}

	return &structures.AggregatedAnchorEpochAckProof{
		EpochId:       proof.EpochId,
		NextEpochId:   proof.NextEpochId,
		EpochDataHash: proof.EpochDataHash,
		Proofs:        proofs,
	}
}

func deliverAggregatedAnchorEpochAckProofToNewQuorum(proof *structures.AggregatedAnchorEpochAckProof, nextEpochHandler *structures.EpochDataHandler) {
	if proof == nil || nextEpochHandler == nil {
		return
	}

	message, err := json.Marshal(websocket_pack.WsAcceptAggregatedAnchorEpochAckProofRequest{
		Route: constants.WsRouteAcceptAggregatedAnchorEpochAckProof,
		Proof: *proof,
	})
	if err != nil {
		return
	}

	const (
		ackDialAttempts = 3
		ackDialBackoff  = 200 * time.Millisecond
	)

	for _, member := range nextEpochHandler.Quorum {
		if member == globals.CONFIGURATION.PublicKey {
			continue
		}
		validatorStorage := utils.GetValidatorFromApprovementThreadState(member)
		if validatorStorage == nil || validatorStorage.WssValidatorUrl == "" {
			continue
		}
		go func(memberPubkey, wsUrl string) {
			var conn *websocket.Conn
			for attempt := 1; attempt <= ackDialAttempts; attempt++ {
				c, _, dialErr := websocket.DefaultDialer.Dial(wsUrl, nil)
				if dialErr == nil {
					conn = c
					break
				}
				utils.LogWithTimeThrottled(
					"last_mile:ack:dial:"+memberPubkey,
					2*time.Second,
					fmt.Sprintf("Last mile: ack-proof dial to new-quorum member %s failed (attempt %d/%d): %v", memberPubkey, attempt, ackDialAttempts, dialErr),
					utils.YELLOW_COLOR,
				)
				if attempt < ackDialAttempts {
					time.Sleep(ackDialBackoff)
				}
			}
			if conn == nil {
				return
			}
			defer conn.Close()
			_ = conn.WriteMessage(websocket.TextMessage, message)
		}(member, validatorStorage.WssValidatorUrl)
	}
}

// persistFinalizerThreadMetadataLocked serializes handlers.FINALIZER_THREAD_METADATA.Handler
// and writes it to FINALIZATION_THREAD_METADATA under DBKeyFinalizerThreadMetadata.
//
// Caller MUST hold handlers.FINALIZER_THREAD_METADATA.RWMutex (Lock, not RLock).
func persistFinalizerThreadMetadataLocked() {
	payload, err := json.Marshal(&handlers.FINALIZER_THREAD_METADATA.Handler)
	if err != nil {
		utils.LogWithTime("FINALIZER_THREAD_METADATA: marshal failed: "+err.Error(), utils.RED_COLOR)
		return
	}

	if err := databases.FINALIZATION_THREAD_METADATA.Put([]byte(constants.DBKeyFinalizerThreadMetadata), payload, nil); err != nil {
		utils.LogWithTime("FINALIZER_THREAD_METADATA: persist failed: "+err.Error(), utils.RED_COLOR)
	}
}
