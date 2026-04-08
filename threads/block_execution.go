// Thread to execute blocks sequentially driven by HeightAttestations from the quorum.
// The quorum (via LastMileFinalizerThread + SignHeightAttestation) resolves block ordering
// and assigns absolute heights. This thread simply follows that sequence.
package threads

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/anchors_pack"
	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/system_contracts"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/modulrcloud/modulr-core/websocket_pack"

	"github.com/syndtr/goleveldb/leveldb"
)

const (
	POD_MISSES_BEFORE_NETWORK_FALLBACK = 3
	ANCHORS_FALLBACK_MIN_INTERVAL      = 500 * time.Millisecond
)

type AnchorsPodMissState struct {
	Misses       int
	LastFallback time.Time
	LastSeen     time.Time
}

var (
	ANCHORS_POD_MISSES_MUTEX  sync.Mutex
	ANCHORS_POD_MISSES        = make(map[string]*AnchorsPodMissState)
	ANCHORS_HTTP_FALLBACK_RNG = rand.New(rand.NewSource(time.Now().UnixNano()))
)

func BlockExecutionThread() {
	for {
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
		var nextHeight int64
		if handlers.EXECUTION_THREAD_METADATA.Handler.Statistics != nil {
			nextHeight = handlers.EXECUTION_THREAD_METADATA.Handler.Statistics.LastHeight + 1
		}
		currentEpochId := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

		attestation, block := fetchAttestationAndBlock(int(nextHeight))
		if attestation == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		nextAttestation := fetchVerifiedHeightAttestation(int(nextHeight) + 1)
		if nextAttestation == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if attestation.EpochId > currentEpochId {
			epochDataAtt := fetchVerifiedEpochDataAttestation(currentEpochId)
			if epochDataAtt == nil {
				utils.LogWithTimeThrottled(
					"exec:epoch_data_attestation_wait",
					5*time.Second,
					fmt.Sprintf("EXECUTION: waiting for verified epoch data attestation for epoch %d", currentEpochId),
					utils.YELLOW_COLOR,
				)
				time.Sleep(200 * time.Millisecond)
				continue
			}

			handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()
			setupNextEpochFromAttestation(&handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler, &epochDataAtt.EpochData)
			handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

			handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
			currentEpochId = handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
			handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

			if attestation.EpochId != currentEpochId {
				utils.LogWithTimeThrottled(
					"exec:epoch_mismatch",
					5*time.Second,
					fmt.Sprintf("EXECUTION: waiting for epoch %d (currently at %d)", attestation.EpochId, currentEpochId),
					utils.YELLOW_COLOR,
				)
				time.Sleep(200 * time.Millisecond)
				continue
			}
		}

		if block == nil {
			block = fetchBlockForExecution(attestation.BlockId)
		}
		if block == nil {
			utils.LogWithTimeThrottled(
				"exec:block_fetch_fail:"+attestation.BlockId,
				2*time.Second,
				fmt.Sprintf("EXECUTION: can't fetch block %s for height %d", attestation.BlockId, nextHeight),
				utils.YELLOW_COLOR,
			)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if block.GetHash() != attestation.BlockHash {
			utils.LogWithTimeThrottled(
				"exec:hash_mismatch:"+attestation.BlockId,
				5*time.Second,
				fmt.Sprintf("EXECUTION: block hash mismatch for %s at height %d", attestation.BlockId, nextHeight),
				utils.YELLOW_COLOR,
			)
			time.Sleep(1 * time.Second)
			continue
		}

		executeBlock(block)
	}
}

// fetchAttestationAndBlock tries to get both the HeightAttestation and the block for a given height
// in a single PoD round-trip. Falls back to separate fetches if the combined route doesn't return both.
func fetchAttestationAndBlock(absoluteHeight int) (*structures.HeightAttestation, *block_pack.Block) {
	localProof := LoadHeightAttestation(absoluteHeight)
	if localProof != nil {
		epochHandler := getEpochHandlerForTracker(localProof.EpochId)
		if epochHandler != nil && utils.VerifyHeightAttestation(localProof, epochHandler) {
			block := fetchBlockForExecution(localProof.BlockId)
			return localProof, block
		}
	}

	combined := websocket_pack.GetBlockByHeightFromPoD(absoluteHeight)
	if combined != nil && combined.HeightAttestation != nil {
		epochHandler := getEpochHandlerForTracker(combined.HeightAttestation.EpochId)
		if epochHandler != nil && utils.VerifyHeightAttestation(combined.HeightAttestation, epochHandler) {
			storeHeightAttestation(combined.HeightAttestation)
			var block *block_pack.Block
			if combined.Block != nil && combined.Block.VerifySignature() {
				block = combined.Block
			}
			return combined.HeightAttestation, block
		}
	}

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	currentEpochHandler := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	httpProof := fetchHeightAttestationFromCurrentOrNextEpochQuorum(absoluteHeight, &currentEpochHandler)
	if httpProof != nil {
		storeHeightAttestation(httpProof)
		return httpProof, nil
	}

	return nil, nil
}

func fetchVerifiedHeightAttestation(absoluteHeight int) *structures.HeightAttestation {
	proof := LoadHeightAttestation(absoluteHeight)
	if proof != nil {
		epochHandler := getEpochHandlerForTracker(proof.EpochId)
		if epochHandler != nil && utils.VerifyHeightAttestation(proof, epochHandler) {
			return proof
		}
	}

	podProof := websocket_pack.GetHeightAttestationFromPoD(absoluteHeight)
	if podProof != nil {
		epochHandler := getEpochHandlerForTracker(podProof.EpochId)
		if epochHandler != nil && utils.VerifyHeightAttestation(podProof, epochHandler) {
			storeHeightAttestation(podProof)
			return podProof
		}
	}

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	currentEpochHandler := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	httpProof := fetchHeightAttestationFromCurrentOrNextEpochQuorum(absoluteHeight, &currentEpochHandler)
	if httpProof != nil {
		storeHeightAttestation(httpProof)
		return httpProof
	}

	return nil
}

func fetchHeightAttestationFromCurrentOrNextEpochQuorum(absoluteHeight int, currentEpochHandler *structures.EpochDataHandler) *structures.HeightAttestation {
	if currentEpochHandler == nil {
		return nil
	}

	if proof := utils.GetHeightAttestationFromQuorumByHeight(absoluteHeight, currentEpochHandler); proof != nil {
		return proof
	}

	// Boundary fallback: if the next height already belongs to epoch N+1, the current
	// epoch quorum cannot serve it. Use the signed epoch-data attestation from epoch N
	// to discover and verify the next epoch quorum, then retry via that quorum.
	epochDataAtt := fetchVerifiedEpochDataAttestation(currentEpochHandler.Id)
	if epochDataAtt == nil || epochDataAtt.NextEpochId != currentEpochHandler.Id+1 {
		return nil
	}

	nextEpochHandler := buildNextEpochHandlerForBoundaryFetch(currentEpochHandler, &epochDataAtt.EpochData)
	if nextEpochHandler == nil {
		return nil
	}

	return utils.GetHeightAttestationFromQuorumByHeight(absoluteHeight, nextEpochHandler)
}

func buildNextEpochHandlerForBoundaryFetch(currentEpochHandler *structures.EpochDataHandler, nextEpochData *structures.NextEpochDataHandler) *structures.EpochDataHandler {
	if currentEpochHandler == nil || nextEpochData == nil {
		return nil
	}

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	epochDuration := handlers.EXECUTION_THREAD_METADATA.Handler.NetworkParameters.EpochDuration
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	return &structures.EpochDataHandler{
		Id:                 currentEpochHandler.Id + 1,
		Hash:               nextEpochData.NextEpochHash,
		ValidatorsRegistry: nextEpochData.NextEpochValidatorsRegistry,
		Quorum:             nextEpochData.NextEpochQuorum,
		LeadersSequence:    nextEpochData.NextEpochLeadersSequence,
		StartTimestamp:     currentEpochHandler.StartTimestamp + uint64(epochDuration),
		CurrentLeaderIndex: 0,
	}
}

// fetchVerifiedEpochDataAttestation fetches and verifies an EpochDataAttestation for the
// current epoch (signed by epoch N's quorum, containing data for epoch N+1).
// Checks local DB first, then PoD.
func fetchVerifiedEpochDataAttestation(currentEpochId int) *structures.EpochDataAttestation {
	epochHandler := getEpochHandlerForTracker(currentEpochId)
	if epochHandler == nil {
		return nil
	}

	local := LoadEpochDataAttestation(currentEpochId)
	if local != nil && utils.VerifyEpochDataAttestation(local, epochHandler) {
		return local
	}

	fromPoD := websocket_pack.GetEpochDataAttestationFromPoD(currentEpochId)
	if fromPoD != nil && utils.VerifyEpochDataAttestation(fromPoD, epochHandler) {
		storeEpochDataAttestation(fromPoD)
		return fromPoD
	}

	fromHTTP := utils.GetEpochDataAttestationFromQuorumByHTTP(currentEpochId, epochHandler)
	if fromHTTP != nil {
		storeEpochDataAttestation(fromHTTP)
		return fromHTTP
	}

	return nil
}

func fetchBlockForExecution(blockId string) *block_pack.Block {
	blockRaw, err := databases.BLOCKS.Get([]byte(blockId), nil)
	if err == nil {
		var block block_pack.Block
		if json.Unmarshal(blockRaw, &block) == nil && block.VerifySignature() {
			return &block
		}
	}

	response := getBlockAndAfpFromPoD(blockId)
	if response != nil && response.Block != nil && response.Block.VerifySignature() {
		return response.Block
	}

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	epochSnapshot := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if networkBlock := getBlockFromNetworkById(blockId, &epochSnapshot); networkBlock != nil {
		return networkBlock
	}

	return nil
}

func getBlockAndAfpFromPoD(blockID string) *websocket_pack.WsBlockWithAfpResponse {
	req := websocket_pack.WsBlockWithAfpRequest{
		Route:   constants.WsRouteGetBlockWithAfp,
		BlockId: blockID,
	}

	if reqBytes, err := json.Marshal(req); err == nil {
		// Use dedicated PoD websocket connection to avoid blocking other PoD traffic.
		if respBytes, err := utils.SendWebsocketMessageToPoDForBlocks(reqBytes); err == nil {
			var resp websocket_pack.WsBlockWithAfpResponse

			if err := json.Unmarshal(respBytes, &resp); err == nil {
				if resp.Block == nil {
					return nil
				}

				return &resp
			}
		}
	}
	return nil
}

func getAnchorBlockAndAfpFromAnchorsPoD(blockID string, epochHandler *structures.EpochDataHandler) *websocket_pack.WsAnchorBlockWithAfpResponse {
	req := websocket_pack.WsAnchorBlockWithAfpRequest{
		Route:   constants.WsRouteGetAnchorBlockWithAfp,
		BlockId: blockID,
	}

	if reqBytes, err := json.Marshal(req); err == nil {
		if respBytes, err := utils.SendWebsocketMessageToAnchorsPoD(reqBytes); err == nil {
			var resp websocket_pack.WsAnchorBlockWithAfpResponse

			if err := json.Unmarshal(respBytes, &resp); err == nil {
				if resp.Block != nil {
					// Reset miss state on success from PoD.
					resetAnchorsPodMisses(blockID)

					// Fallback: if anchors PoD hasn't received/stored AFP yet, fetch a verified AFP for (blockID+1)
					// directly from anchors via HTTP.
					if resp.Afp == nil && epochHandler != nil {
						if nextID := nextBlockId(blockID); nextID != "" {
							if afp := utils.GetVerifiedAnchorsAggregatedFinalizationProofByBlockId(nextID, epochHandler); afp != nil {
								resp.Afp = afp
							}
						}
					}

					return &resp
				}
			}
		}
	}

	// PoD failed / didn't have the block - maybe fall back to anchors directly after a few misses.
	if shouldFallbackToAnchorsNetwork(blockID) {
		utils.LogWithTimeThrottled(
			"anchors:pod_block_fallback:"+blockID,
			5*time.Second,
			fmt.Sprintf("ANCHORS: can't fetch anchor block %s from Anchors-PoD, falling back to anchors HTTP", blockID),
			utils.YELLOW_COLOR,
		)

		if b := getAnchorBlockFromAnchorsNetworkById(blockID); b != nil {
			resetAnchorsPodMisses(blockID)

			resp := websocket_pack.WsAnchorBlockWithAfpResponse{Block: b}

			// Keep the existing AFP fallback behavior, even when the block came from anchors directly.
			if resp.Afp == nil && epochHandler != nil {
				if nextID := nextBlockId(blockID); nextID != "" {
					if afp := utils.GetVerifiedAnchorsAggregatedFinalizationProofByBlockId(nextID, epochHandler); afp != nil {
						resp.Afp = afp
					}
				}
			}

			return &resp
		}

		utils.LogWithTimeThrottled(
			"anchors:pod_block_fallback_fail:"+blockID,
			5*time.Second,
			fmt.Sprintf("ANCHORS: anchors HTTP fallback failed for anchor block %s", blockID),
			utils.YELLOW_COLOR,
		)
	}

	return nil
}

func parseBlockId(blockId string) (epochIndex int, creator string, index int, ok bool) {
	parts := strings.Split(blockId, ":")
	if len(parts) != 3 {
		return 0, "", 0, false
	}
	ei, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", 0, false
	}
	idx, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, "", 0, false
	}
	return ei, parts[1], idx, true
}

func getBlockFromNetworkById(blockID string, epochHandler *structures.EpochDataHandler) *block_pack.Block {
	epochIndex, creator, index, ok := parseBlockId(blockID)
	if !ok || epochHandler == nil {
		return nil
	}
	if index < 0 {
		return nil
	}

	b := block_pack.GetBlock(epochIndex, creator, uint(index), epochHandler)
	if b == nil {
		return nil
	}
	// Basic sanity checks: ensure the fetched block matches the requested ID and is signed by its creator.
	if b.Creator != creator || b.Index != index || !b.VerifySignature() {
		return nil
	}
	return b
}

func shouldFallbackToAnchorsNetwork(blockID string) bool {
	now := time.Now()

	ANCHORS_POD_MISSES_MUTEX.Lock()
	defer ANCHORS_POD_MISSES_MUTEX.Unlock()

	// TTL cleanup to avoid unbounded growth if blockIDs become highly dynamic.
	if len(ANCHORS_POD_MISSES) > 5000 {
		for k, v := range ANCHORS_POD_MISSES {
			if v == nil {
				delete(ANCHORS_POD_MISSES, k)
				continue
			}
			if !v.LastSeen.IsZero() && now.Sub(v.LastSeen) > 2*time.Minute {
				delete(ANCHORS_POD_MISSES, k)
			}
		}
		// Absolute safety valve.
		if len(ANCHORS_POD_MISSES) > 10000 {
			ANCHORS_POD_MISSES = make(map[string]*AnchorsPodMissState)
		}
	}

	st, ok := ANCHORS_POD_MISSES[blockID]
	if !ok {
		st = &AnchorsPodMissState{}
		ANCHORS_POD_MISSES[blockID] = st
	}

	st.LastSeen = now
	st.Misses++
	if st.Misses < POD_MISSES_BEFORE_NETWORK_FALLBACK {
		return false
	}

	if !st.LastFallback.IsZero() && now.Sub(st.LastFallback) < ANCHORS_FALLBACK_MIN_INTERVAL {
		return false
	}

	st.LastFallback = now
	return true
}

func resetAnchorsPodMisses(blockID string) {
	ANCHORS_POD_MISSES_MUTEX.Lock()
	delete(ANCHORS_POD_MISSES, blockID)
	ANCHORS_POD_MISSES_MUTEX.Unlock()
}

func getAnchorBlockFromAnchorsNetworkById(blockID string) *anchors_pack.AnchorBlock {
	_, creator, index, ok := parseBlockId(blockID)
	if !ok || index < 0 {
		return nil
	}

	client := &http.Client{Timeout: 2 * time.Second}

	// Query a randomized list of anchors. The block may be replicated on peers even if its creator is down.
	anchors := make([]structures.Anchor, 0, len(globals.ANCHORS))
	for _, a := range globals.ANCHORS {
		if a.AnchorUrl == "" {
			continue
		}
		anchors = append(anchors, a)
	}
	if len(anchors) == 0 {
		return nil
	}
	ANCHORS_POD_MISSES_MUTEX.Lock()
	ANCHORS_HTTP_FALLBACK_RNG.Shuffle(len(anchors), func(i, j int) { anchors[i], anchors[j] = anchors[j], anchors[i] })
	ANCHORS_POD_MISSES_MUTEX.Unlock()

	resultChan := make(chan *anchors_pack.AnchorBlock, len(anchors))
	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, anchor := range anchors {
		wg.Add(1)
		go func(endpoint string) {
			defer wg.Done()

			endpoint = strings.TrimRight(endpoint, "/")
			req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/block/"+blockID, nil)
			if err != nil {
				return
			}

			resp, err := client.Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				return
			}
			defer resp.Body.Close()

			var b anchors_pack.AnchorBlock
			if json.NewDecoder(resp.Body).Decode(&b) != nil {
				return
			}

			// Sanity check: match requested ID and signature.
			if b.Creator != creator || b.Index != index || !b.VerifySignature() {
				return
			}

			select {
			case resultChan <- &b:
				cancel()
			default:
			}
		}(anchor.AnchorUrl)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	for res := range resultChan {
		if res != nil {
			return res
		}
	}

	return nil
}

func nextBlockId(blockId string) string {
	parts := strings.Split(blockId, ":")
	if len(parts) != 3 {
		return ""
	}
	idx, err := strconv.Atoi(parts[2])
	if err != nil {
		return ""
	}
	parts[2] = strconv.Itoa(idx + 1)
	return strings.Join(parts, ":")
}

func executeBlock(block *block_pack.Block) {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()
	stateBatch, logMsg, ok := buildExecutionBatch(block)
	handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

	if !ok {
		return
	}

	if err := databases.STATE.Write(stateBatch, nil); err == nil {
		utils.LogWithTimeAlt(logMsg, utils.CYAN_COLOR)
	} else {
		panic("Impossible to commit changes in atomic batch to permanent state")
	}
}

// buildExecutionBatch mutates in-memory state and builds a LevelDB batch.
// Caller MUST hold handlers.EXECUTION_THREAD_METADATA.RWMutex in write mode.
func buildExecutionBatch(block *block_pack.Block) (*leveldb.Batch, string, bool) {
	epochHandlerRef := &handlers.EXECUTION_THREAD_METADATA.Handler

	if epochHandlerRef.Statistics == nil {
		epochHandlerRef.Statistics = &structures.Statistics{LastHeight: -1}
	}
	if epochHandlerRef.EpochStatistics == nil {
		epochHandlerRef.EpochStatistics = &structures.Statistics{LastHeight: -1}
	}

	currentEpochIndex := epochHandlerRef.EpochDataHandler.Id
	currentBlockId := strconv.Itoa(currentEpochIndex) + ":" + block.Creator + ":" + strconv.Itoa(block.Index)

	expectedPrevHash := epochHandlerRef.ExecutionData[block.Creator].Hash
	if expectedPrevHash != block.PrevHash {
		utils.LogWithTimeThrottled(
			"exec:prev_hash_mismatch:"+currentBlockId,
			2*time.Second,
			fmt.Sprintf("EXECUTION: prevHash mismatch for %s (expected %s..., got %s...)", currentBlockId, utils.ShortHash(expectedPrevHash), utils.ShortHash(block.PrevHash)),
			utils.YELLOW_COLOR,
		)
		return nil, "", false
	}

	// Reset per-block write-back sets. We only persist accounts/validators touched during this block,
	// while keeping the read caches bounded via LRU.
	utils.ResetExecTouchedSets()

	// To change the state atomically - prepare the atomic batch
	stateBatch := new(leveldb.Batch)

	// 1. Process all transactions in the block
	blockFees := applyTransactions(block, currentBlockId, stateBatch, epochHandlerRef)

	// 2. Distribute fees
	sendFeesToValidatorAccount(block.Creator, blockFees)

	// 3. Persist touched state (accounts and validators)
	persistTouchedState(stateBatch)

	// 4. Update execution statistics and save ET metadata
	logMsg := updateExecutionStatistics(block, currentBlockId, blockFees, stateBatch, epochHandlerRef)

	return stateBatch, logMsg, true
}

func applyTransactions(block *block_pack.Block, currentBlockId string, stateBatch *leveldb.Batch, epochHandlerRef *structures.ExecutionThreadMetadataHandler) uint64 {
	blockFees := uint64(0)
	delayedTxPayloadsForBatch := make([]map[string]string, 0)

	for index, transaction := range block.Transactions {
		success, reason, fee, delayedPayload, isDelayed := executeTransaction(&transaction)

		if isDelayed {
			delayedTxPayloadsForBatch = append(delayedTxPayloadsForBatch, delayedPayload)
		}

		epochHandlerRef.Statistics.TotalTransactions++
		epochHandlerRef.EpochStatistics.TotalTransactions++

		if success {
			epochHandlerRef.Statistics.SuccessfulTransactions++
			epochHandlerRef.EpochStatistics.SuccessfulTransactions++
		}

		blockFees += fee

		receiptReason := ""
		if !success {
			receiptReason = reason
		}

		if locationBytes, err := json.Marshal(structures.TransactionReceipt{Block: currentBlockId, Position: index, Success: success, Reason: receiptReason}); err == nil {
			stateBatch.Put([]byte(constants.DBKeyPrefixTxReceipt+transaction.Hash()), locationBytes)
		} else {
			panic("Impossible to add transaction location data to atomic batch")
		}
	}

	if len(delayedTxPayloadsForBatch) > 0 {
		if err := addDelayedTransactionsToBatch(delayedTxPayloadsForBatch, epochHandlerRef.EpochDataHandler.Id, stateBatch); err != nil {
			panic("Impossible to add delayed transactions to atomic batch")
		}
	}

	return blockFees
}

func persistTouchedState(stateBatch *leveldb.Batch) {
	for accountID, accountData := range handlers.EXECUTION_THREAD_METADATA.AccountsTouched {
		if accountDataBytes, err := json.Marshal(accountData); err == nil {
			stateBatch.Put([]byte(accountID), accountDataBytes)
		} else {
			panic("Impossible to add new account data to atomic batch")
		}
	}

	for storageKey, validatorStorage := range handlers.EXECUTION_THREAD_METADATA.ValidatorsTouched {
		if dataBytes, err := json.Marshal(validatorStorage); err == nil {
			// storageKey already includes DBKeyPrefixValidatorStorage.
			stateBatch.Put([]byte(storageKey), dataBytes)
		} else {
			panic("Impossible to add validator storage to atomic batch")
		}
	}
}

func updateExecutionStatistics(block *block_pack.Block, currentBlockId string, blockFees uint64, stateBatch *leveldb.Batch, epochHandlerRef *structures.ExecutionThreadMetadataHandler) string {
	blockHash := block.GetHash()

	blockCreatorData := epochHandlerRef.ExecutionData[block.Creator]
	blockCreatorData.Index = block.Index
	blockCreatorData.Hash = blockHash
	epochHandlerRef.ExecutionData[block.Creator] = blockCreatorData

	epochHandlerRef.Statistics.LastHeight++
	epochHandlerRef.Statistics.LastBlockHash = blockHash
	epochHandlerRef.Statistics.TotalFees += blockFees
	epochHandlerRef.Statistics.BlocksGenerated++

	epochHandlerRef.EpochStatistics.TotalFees += blockFees
	epochHandlerRef.EpochStatistics.BlocksGenerated++

	epochHandlerRef.EpochStatistics.LastHeight = epochHandlerRef.Statistics.LastHeight
	epochHandlerRef.EpochStatistics.LastBlockHash = blockHash

	stateBatch.Put([]byte(fmt.Sprintf(constants.DBKeyPrefixBlockIndex+"%d", epochHandlerRef.Statistics.LastHeight)), []byte(currentBlockId))

	if execThreadRawBytes, err := json.Marshal(epochHandlerRef); err == nil {
		stateBatch.Put([]byte(constants.DBKeyExecutionThreadMetadata), execThreadRawBytes)
	} else {
		panic("Impossible to store updated execution thread version to atomic batch")
	}

	return fmt.Sprintf("Executed block %s ✅ [%d]", currentBlockId, epochHandlerRef.Statistics.LastHeight)
}

func sendFeesToValidatorAccount(blockCreatorPubkey string, feeFromBlock uint64) {
	blockCreatorAccount := utils.GetAccountFromExecThreadState(blockCreatorPubkey)

	// Transfer fees to account with pubkey associated with block creator

	blockCreatorAccount.Balance += feeFromBlock
}

func executeTransaction(tx *structures.Transaction) (bool, string, uint64, map[string]string, bool) {
	// Prevent overwriting system keys in STATE via crafted tx.To/tx.From.
	// Account IDs must be canonical pubkeys.
	if !cryptography.IsValidPubKey(tx.From) || !cryptography.IsValidPubKey(tx.To) {
		return false, "invalid pubkey", 0, nil, false
	}

	if cryptography.VerifySignature(tx.Hash(), tx.From, tx.Sig) {
		accountFrom := utils.GetAccountFromExecThreadState(tx.From)
		accountFrom.InitiatedTransactions++

		if delayedTxPayload, delayedTxType, isDelayed := getDelayedTransactionPayload(tx); isDelayed {
			if ok, reason := validateDelayedTransaction(delayedTxType, tx, delayedTxPayload, accountFrom); !ok {
				return false, reason, 0, nil, false
			}

			accountFrom.Balance -= tx.Fee

			if delayedTxType == "stake" {
				stakeAmount, _ := strconv.ParseUint(delayedTxPayload["amount"], 10, 64)
				accountFrom.Balance -= stakeAmount
			}

			accountFrom.Nonce++

			accountFrom.SuccessfulInitiatedTransactions++

			return true, "", tx.Fee, delayedTxPayload, true
		}

		accountTo := utils.GetAccountFromExecThreadState(tx.To)

		totalSpend := tx.Fee + tx.Amount

		nonceOk := tx.Nonce == accountFrom.Nonce+1
		if constants.ShouldBypassNonceCheck(tx.From) {
			nonceOk = true
		}

		if !nonceOk {
			return false, "wrong nonce", 0, nil, false
		}

		if accountFrom.Balance >= totalSpend {
			accountFrom.Balance -= totalSpend

			accountTo.Balance += tx.Amount

			accountFrom.Nonce++

			accountFrom.SuccessfulInitiatedTransactions++

			return true, "", tx.Fee, nil, false
		}

		return false, "insufficient balance", 0, nil, false
	}

	return false, "invalid signature", 0, nil, false
}

func getDelayedTransactionPayload(tx *structures.Transaction) (map[string]string, string, bool) {
	if tx.Payload == nil {
		return nil, "", false
	}

	payloadType, ok := tx.Payload["type"]

	if !ok || payloadType == "" {
		return nil, "", false
	}

	if _, exists := system_contracts.DELAYED_TRANSACTIONS_MAP[payloadType]; !exists {
		return nil, "", false
	}

	return tx.Payload, payloadType, true
}

func validateDelayedTransaction(delayedTxType string, tx *structures.Transaction, payload map[string]string, accountFrom *structures.Account) (bool, string) {
	if accountFrom == nil {
		return false, "missing sender account"
	}

	if !constants.ShouldBypassNonceCheck(tx.From) && tx.Nonce != accountFrom.Nonce+1 {
		return false, "wrong nonce"
	}

	if accountFrom.Balance < tx.Fee {
		return false, "insufficient balance for fee"
	}

	switch delayedTxType {
	case "createValidator", "updateValidator":

		if tx.From != payload["creator"] {
			return false, "invalid delayed transaction creator"
		}
		return true, ""

	case "stake":

		amount, err := strconv.ParseUint(payload["amount"], 10, 64)

		if err != nil {
			return false, "invalid delayed transaction amount"
		}

		if accountFrom.Balance < amount+tx.Fee {
			return false, "insufficient balance"
		}

		return true, ""

	default:

		return true, ""
	}
}

func addDelayedTransactionsToBatch(delayedTxPayloads []map[string]string, epochIndex int, batch *leveldb.Batch) error {
	delayedTxKey := fmt.Sprintf("DELAYED_TRANSACTIONS:%d", epochIndex+2)

	cachedPayloads := make([]map[string]string, 0)

	rawCachedPayloads, err := databases.STATE.Get([]byte(delayedTxKey), nil)

	if err == nil {
		if jsonErr := json.Unmarshal(rawCachedPayloads, &cachedPayloads); jsonErr != nil {
			cachedPayloads = make([]map[string]string, 0)
		}
	} else if err != leveldb.ErrNotFound {
		return err
	}

	cachedPayloads = append(cachedPayloads, delayedTxPayloads...)

	serializedPayloads, err := json.Marshal(cachedPayloads)

	if err != nil {
		return err
	}

	batch.Put([]byte(delayedTxKey), serializedPayloads)

	return nil
}

func setupNextEpochFromAttestation(epochHandler *structures.EpochDataHandler, nextEpochData *structures.NextEpochDataHandler) {
	currentEpochIndex := epochHandler.Id
	nextEpochIndex := currentEpochIndex + 1

	if nextEpochData != nil {
		// Reset touched sets before executing delayed txs for next epoch so we only persist what they touch.
		utils.ResetExecTouchedSets()

		dbBatch := new(leveldb.Batch)

		// Persist per-epoch statistics snapshot for the finishing epoch into STATE.
		// This allows HTTP API to query historical epoch statistics without bloating the ET payload.
		finishingEpochStats := handlers.EXECUTION_THREAD_METADATA.Handler.EpochStatistics
		if finishingEpochStats == nil {
			finishingEpochStats = &structures.Statistics{LastHeight: -1}
		}
		if rawStats, err := json.Marshal(finishingEpochStats); err == nil {
			dbBatch.Put([]byte(fmt.Sprintf("EPOCH_STATS:%d", currentEpochIndex)), rawStats)
		}

		// Exec delayed txs here

		for _, delayedTx := range nextEpochData.DelayedTransactions {
			executeDelayedTransaction(delayedTx, constants.ContextExecutionThread)
		}

		// Prepare epoch handler for next epoch

		templateForNextEpoch := &structures.EpochDataHandler{
			Id:                 nextEpochIndex,
			Hash:               nextEpochData.NextEpochHash,
			ValidatorsRegistry: nextEpochData.NextEpochValidatorsRegistry,
			StartTimestamp:     epochHandler.StartTimestamp + uint64(handlers.EXECUTION_THREAD_METADATA.Handler.NetworkParameters.EpochDuration),
			Quorum:             nextEpochData.NextEpochQuorum,
			LeadersSequence:    nextEpochData.NextEpochLeadersSequence,
		}

		handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler = *templateForNextEpoch

		// Nullify values for the upcoming epoch

		handlers.EXECUTION_THREAD_METADATA.Handler.EpochStatistics = &structures.Statistics{LastHeight: -1}

		handlers.EXECUTION_THREAD_METADATA.Handler.ExecutionData = make(map[string]structures.ExecutionStats)

		for _, validatorPubkey := range handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.LeadersSequence {
			handlers.EXECUTION_THREAD_METADATA.Handler.ExecutionData[validatorPubkey] = structures.NewExecutionStatsTemplate()
		}

		// Finally, clean & nullify sequence data

		handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData = structures.AlignmentDataHandler{

			CurrentAnchorAssumption:         0,
			CurrentAnchorBlockIndexObserved: -1,
			CurrentLeaderToExecBlocksFrom:   0,

			LastBlocksByLeaders: make(map[string]structures.ExecutionStats),
			LastBlocksByAnchors: make(map[int]structures.ExecutionStats),
		}

		// Commit the changes of state using atomic batch. Because we modified state via delayed transactions when epoch finished

		for accountID, accountData := range handlers.EXECUTION_THREAD_METADATA.AccountsTouched {
			if accountDataBytes, err := json.Marshal(accountData); err == nil {
				dbBatch.Put([]byte(accountID), accountDataBytes)
			} else {
				panic("Impossible to add new account data to atomic batch")
			}
		}

		for storageKey, validatorStorage := range handlers.EXECUTION_THREAD_METADATA.ValidatorsTouched {
			if dataBytes, err := json.Marshal(validatorStorage); err == nil {
				// storageKey already includes DBKeyPrefixValidatorStorage.
				dbBatch.Put([]byte(storageKey), dataBytes)
			} else {
				panic("Impossible to add validator storage to atomic batch")
			}
		}

		if execThreadRawBytes, err := json.Marshal(&handlers.EXECUTION_THREAD_METADATA.Handler); err == nil {
			dbBatch.Put([]byte(constants.DBKeyExecutionThreadMetadata), execThreadRawBytes)
		} else {
			panic("Impossible to store updated execution thread version to atomic batch")
		}

		if err := databases.STATE.Write(dbBatch, nil); err != nil {
			panic("Impossible to modify the state when epoch finished")
		}

		// Version check once new epoch started

		if utils.IsMyCoreVersionOld(&handlers.EXECUTION_THREAD_METADATA.Handler) {
			utils.LogWithTime("New version detected on EXECUTION_THREAD. Please, upgrade your node software", utils.YELLOW_COLOR)

			utils.GracefulShutdown()
		}
	} else {
		utils.LogWithTimeThrottled(
			fmt.Sprintf("execution:next_epoch_missing:%d", nextEpochIndex),
			5*time.Second,
			fmt.Sprintf("EXECUTION: can't setup next epoch %d (missing %s%d in APPROVEMENT_THREAD_METADATA)", nextEpochIndex, constants.DBKeyPrefixEpochData, nextEpochIndex),
			utils.YELLOW_COLOR,
		)
	}
}
