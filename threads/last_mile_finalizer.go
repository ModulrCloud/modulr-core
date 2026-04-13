// Thread for collecting height attestations and epoch data attestations from quorum
package threads

import (
	"context"
	"encoding/binary"
	"encoding/hex"
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
						storeAggregatedAnchorEpochAckProof(ackProof)
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

		// --- Finalizer-only: quorum connections for proof collection ---
		if isFinalizer && !quorumConnectionsReady {
			openQuorumConnectionsForLastMileFinalizer(&epochSnapshot)
			quorumConnectionsReady = true
		}

		// --- Block sequencing (ALL nodes, mirrors block_execution.go from main) ---

		epochHandler := getEpochHandlerForTracker(tracker.EpochId)

		if epochHandler == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if tracker.LeaderIndex >= len(epochHandler.LeadersSequence) {
			nextEpochHandler := getEpochHandlerForTracker(tracker.EpochId + 1)
			if nextEpochHandler != nil {
				tracker.AdvanceToNextEpoch()
				utils.PersistLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker, tracker)
				continue
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}

		leader := epochHandler.LeadersSequence[tracker.LeaderIndex]
		blockId := fmt.Sprintf("%d:%s:%d", tracker.EpochId, leader, tracker.BlockIndex)

		blockHash := getBlockHashByBlockId(blockId)

		if blockHash == "" {
			lastBlocksByLeaders := snapshotLastBlocksByLeaders()
			lastBlock, known := lastBlocksByLeaders[leader]

			if known && lastBlock.Index < 0 {
				tracker.LeaderIndex++
				tracker.BlockIndex = 0
				utils.PersistLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker, tracker)
				continue
			}

			time.Sleep(200 * time.Millisecond)
			continue
		}

		isLastBlock := false
		confirmed := false

		lastBlocksByLeaders := snapshotLastBlocksByLeaders()
		lastBlock, known := lastBlocksByLeaders[leader]

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

		// Write height mappings (ALL nodes — this is what SignHeightProof reads)
		utils.StoreHeightBlockIdMapping(tracker.NextHeight, blockId)
		utils.StoreHeightInEpochMapping(tracker.NextHeight, tracker.HeightInEpoch)

		// --- Finalizer-only: collect AggregatedHeightProof before advancing ---
		if isFinalizer {
			var previousProof *structures.AggregatedHeightProof
			if tracker.NextHeight > 0 {
				previousProof = LoadAggregatedHeightProof(int(tracker.NextHeight - 1))
				if previousProof == nil {
					time.Sleep(200 * time.Millisecond)
					continue
				}
			}

			proof := tryCollectAggregatedHeightProof(int(tracker.NextHeight), blockId, blockHash, tracker.EpochId, tracker.HeightInEpoch, epochHandler, previousProof)
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
		if isLastBlock {
			tracker.LeaderIndex++
			tracker.BlockIndex = 0
		} else {
			tracker.BlockIndex++
		}
		tracker.NextHeight++
		tracker.HeightInEpoch++

		utils.PersistLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker, tracker)
	}
}

func hashHexToUint64(hashHex string) uint64 {

	if len(hashHex) < 16 {
		return 0
	}

	b, err := hex.DecodeString(hashHex[:16])

	if err != nil {
		return 0
	}

	return binary.BigEndian.Uint64(b)
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
		r := hashHexToUint64(hashHex) % uint64(len(quorum)-i)
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

func storeAggregatedHeightProof(proof *structures.AggregatedHeightProof) {
	key := []byte(fmt.Sprintf(constants.DBKeyPrefixAggregatedHeightProof+"%d", proof.AbsoluteHeight))

	if value, err := json.Marshal(proof); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put(key, value, nil)
	}
}

func LoadAggregatedHeightProof(absoluteHeight int) *structures.AggregatedHeightProof {
	key := []byte(fmt.Sprintf(constants.DBKeyPrefixAggregatedHeightProof+"%d", absoluteHeight))

	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)

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
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	data := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders
	if data == nil {
		return make(map[string]structures.ExecutionStats)
	}

	cp := make(map[string]structures.ExecutionStats, len(data))
	for k, v := range data {
		cp[k] = v
	}
	return cp
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

	request := websocket_pack.WsEpochRotationProofRequest{
		Route:         constants.WsRouteSignEpochRotationProof,
		EpochId:       epochId,
		NextEpochId:   nextEpochId,
		EpochDataHash: epochDataHash,
	}

	message, err := json.Marshal(request)
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dataToVerify := strings.Join([]string{
		constants.SigningPrefixEpochRotationProof,
		strconv.Itoa(epochId),
		strconv.Itoa(nextEpochId),
		epochDataHash,
	}, ":")

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
		EpochId:       epochId,
		NextEpochId:   nextEpochId,
		EpochData:     *localEpochData,
		EpochDataHash: epochDataHash,
		Proofs:        proofs,
	}
}

func storeAggregatedEpochRotationProof(proof *structures.AggregatedEpochRotationProof) {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedEpochRotationProof, proof.EpochId))
	if value, err := json.Marshal(proof); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put(key, value, nil)
	}
}

func LoadAggregatedEpochRotationProof(epochId int) *structures.AggregatedEpochRotationProof {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedEpochRotationProof, epochId))
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
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
	LAST_MILE_MUTEX.Lock()
	conns := LAST_MILE_ANCHOR_WS_CONNS
	LAST_MILE_MUTEX.Unlock()

	message, err := json.Marshal(struct {
		Route string                                  `json:"route"`
		Proof structures.AggregatedEpochRotationProof `json:"proof"`
	}{
		Route: "accept_aggregated_epoch_rotation_proof",
		Proof: *proof,
	})

	if err != nil {
		return nil
	}

	type anchorAck struct {
		Anchor string
		Sig    string
	}

	ackChan := make(chan anchorAck, len(conns))
	var wg sync.WaitGroup

	for anchorKey, conn := range conns {
		if conn == nil {
			continue
		}
		wg.Add(1)
		go func(key string, c *websocket.Conn) {
			defer wg.Done()

			if err := c.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, respBytes, err := c.ReadMessage()
			c.SetReadDeadline(time.Time{})
			if err != nil {
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
		}(anchorKey, conn)
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

func storeAggregatedAnchorEpochAckProof(proof *structures.AggregatedAnchorEpochAckProof) {
	if proof == nil {
		return
	}
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedAnchorEpochAckProof, proof.NextEpochId))
	raw, err := json.Marshal(proof)
	if err != nil {
		return
	}
	_ = databases.FINALIZATION_VOTING_STATS.Put(key, raw, nil)
}

func LoadAggregatedAnchorEpochAckProof(epochId int) *structures.AggregatedAnchorEpochAckProof {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedAnchorEpochAckProof, epochId))
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	if err != nil {
		return nil
	}
	var proof structures.AggregatedAnchorEpochAckProof
	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}
	return &proof
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

	for _, member := range nextEpochHandler.Quorum {
		if member == globals.CONFIGURATION.PublicKey {
			continue
		}
		validatorStorage := utils.GetValidatorFromApprovementThreadState(member)
		if validatorStorage == nil || validatorStorage.WssValidatorUrl == "" {
			continue
		}
		go func(wsUrl string) {
			conn, _, err := websocket.DefaultDialer.Dial(wsUrl, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			_ = conn.WriteMessage(websocket.TextMessage, message)
		}(validatorStorage.WssValidatorUrl)
	}
}
