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
	// How many consecutive PoD misses for the same blockID before we start querying the network (quorum/anchors) directly.
	POD_MISSES_BEFORE_NETWORK_FALLBACK = 3

	// Prevent spamming anchors with HTTP fallbacks if multiple threads call into anchors-PoD getter concurrently.
	ANCHORS_FALLBACK_MIN_INTERVAL = 500 * time.Millisecond
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

	// Track PoD misses per blockID so we only fall back to querying the network after several failures.
	podMisses := make(map[string]int)
	lastEpochId := -1

	for {

		// NOTE: Don't hold EXECUTION_THREAD_METADATA lock during network I/O to PoD.
		// On high-latency links this blocks alignment/monitor threads and can cause huge execution lag.

		progressed := false

		handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()

		epochHandlerRef := &handlers.EXECUTION_THREAD_METADATA.Handler
		currentEpochAlignmentData := &epochHandlerRef.SequenceAlignmentData

		epochSnapshot := epochHandlerRef.EpochDataHandler
		if epochSnapshot.Id != lastEpochId {
			// New epoch: drop stale miss counters (safety against growth across epochs).
			podMisses = make(map[string]int)
			lastEpochId = epochSnapshot.Id
		}

		leaderIndexToExec := currentEpochAlignmentData.CurrentLeaderToExecBlocksFrom
		if leaderIndexToExec < 0 || leaderIndexToExec >= len(epochSnapshot.LeadersSequence) {
			handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()
			time.Sleep(200 * time.Millisecond)
			continue
		}

		leaderPubkeyToExecBlocks := epochSnapshot.LeadersSequence[leaderIndexToExec]
		execStatsOfLeader := epochHandlerRef.ExecutionData[leaderPubkeyToExecBlocks] // {index,hash}
		infoAboutLastBlockByThisLeader, infoAboutLastBlockExists := currentEpochAlignmentData.LastBlocksByLeaders[leaderPubkeyToExecBlocks]

		// If we already executed everything we know for this leader, advance leader/epoch.
		if infoAboutLastBlockExists && execStatsOfLeader.Index == infoAboutLastBlockByThisLeader.Index {
			allBlocksInEpochWereExecuted := len(epochSnapshot.LeadersSequence) == currentEpochAlignmentData.CurrentLeaderToExecBlocksFrom+1
			if allBlocksInEpochWereExecuted {
				setupNextEpoch(&epochHandlerRef.EpochDataHandler)
			} else {
				epochHandlerRef.SequenceAlignmentData.CurrentLeaderToExecBlocksFrom++
			}
			handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()
			continue
		}

		handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

		// ___________ Now start a cycle to fetch blocks and exec ___________
		for {
			blockId := strconv.Itoa(epochSnapshot.Id) + ":" + leaderPubkeyToExecBlocks + ":" + strconv.Itoa(execStatsOfLeader.Index+1)

			// Network I/O (PoD) - no locks held.
			response := getBlockAndAfpFromPoD(blockId)
			if response == nil || response.Block == nil {
				podMisses[blockId]++

				// Safety valve: avoid unbounded growth if keys become highly dynamic.
				if len(podMisses) > 5000 {
					podMisses = make(map[string]int)
				}

				utils.LogWithTimeThrottled(
					"exec:pod_block_miss:"+blockId,
					2*time.Second,
					fmt.Sprintf("EXECUTION: can't fetch block %s from PoD (miss %d/%d)", blockId, podMisses[blockId], POD_MISSES_BEFORE_NETWORK_FALLBACK),
					utils.YELLOW_COLOR,
				)

				// After a few PoD misses, try to fetch the block from quorum/network directly (HTTP).
				if podMisses[blockId] >= POD_MISSES_BEFORE_NETWORK_FALLBACK {
					utils.LogWithTimeThrottled(
						"exec:pod_block_fallback:"+blockId,
						5*time.Second,
						fmt.Sprintf("EXECUTION: falling back to quorum HTTP for block %s", blockId),
						utils.DEEP_GRAY,
					)

					if fallbackBlock := getBlockFromNetworkById(blockId, &epochSnapshot); fallbackBlock != nil {
						response = &websocket_pack.WsBlockWithAfpResponse{Block: fallbackBlock}
						// success via fallback - reset counter
						delete(podMisses, blockId)

						utils.LogWithTimeThrottled(
							"exec:pod_block_fallback_ok:"+blockId,
							5*time.Second,
							fmt.Sprintf("EXECUTION: fetched block %s via quorum HTTP fallback", blockId),
							utils.CYAN_COLOR,
						)
					}
				}

				// Still nothing - stop this execution cycle and retry later.
				if response == nil || response.Block == nil {
					break
				}
			} else {
				// Got data from PoD - reset counter.
				delete(podMisses, blockId)
			}

			// Decide whether we can execute this block.
			canExecWithoutAfp := infoAboutLastBlockExists &&
				execStatsOfLeader.Index+1 == infoAboutLastBlockByThisLeader.Index &&
				response.Block.GetHash() == infoAboutLastBlockByThisLeader.Hash

			// Only fetch/verify AFP if we can't safely execute without it.
			if !canExecWithoutAfp && response.Afp == nil {
				if nextID := nextBlockId(blockId); nextID != "" {
					if afp := utils.GetVerifiedAggregatedFinalizationProofByBlockId(nextID, &epochSnapshot); afp != nil {
						response.Afp = afp
					} else {
						utils.LogWithTimeThrottled(
							"exec:afp_missing:"+blockId,
							2*time.Second,
							fmt.Sprintf("EXECUTION: missing AFP for block %s (attempted quorum AFP for %s)", blockId, nextID),
							utils.YELLOW_COLOR,
						)
					}
				}
			}

			// Verify AFP only if we can't safely execute without it (AFP verification is relatively expensive).
			mustExecWithAfp := false
			if !canExecWithoutAfp {
				mustExecWithAfp = response.Afp != nil && utils.VerifyAggregatedFinalizationProof(response.Afp, &epochSnapshot)
				if response.Afp != nil && !mustExecWithAfp {
					utils.LogWithTimeThrottled(
						"exec:afp_invalid:"+blockId,
						2*time.Second,
						fmt.Sprintf("EXECUTION: AFP verification failed for block %s", blockId),
						utils.YELLOW_COLOR,
					)
				}
			}

			if !canExecWithoutAfp && !mustExecWithAfp {
				break
			}

			// Apply block (executes with internal lock; DB write happens outside lock).
			executeBlock(response.Block)
			handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
			execStatsOfLeader = handlers.EXECUTION_THREAD_METADATA.Handler.ExecutionData[leaderPubkeyToExecBlocks]
			handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

			progressed = true
		}

		// Avoid tight loop when PoD doesn't have the next block yet (especially on high RTT links).
		if !progressed {
			time.Sleep(100 * time.Millisecond)
		}

	}

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

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func executeBlock(block *block_pack.Block) {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()
	stateBatch, logMsg, ok := buildExecutionBatch(block)
	handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

	if !ok {
		return
	}

	if err := databases.STATE.Write(stateBatch, nil); err == nil {
		utils.LogWithTime2(logMsg, utils.CYAN_COLOR)
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
			fmt.Sprintf("EXECUTION: prevHash mismatch for %s (expected %s..., got %s...)", currentBlockId, shortHash(expectedPrevHash), shortHash(block.PrevHash)),
			utils.YELLOW_COLOR,
		)
		return nil, "", false
	}

	// Reset per-block write-back sets. We only persist accounts/validators touched during this block,
	// while keeping the read caches bounded via LRU.
	utils.ResetExecTouchedSets()

	// To change the state atomically - prepare the atomic batch
	stateBatch := new(leveldb.Batch)

	blockFees := uint64(0)

	delayedTxPayloadsForBatch := make([]map[string]string, 0)

	for index, transaction := range block.Transactions {

		success, fee, delayedPayload, isDelayed := executeTransaction(&transaction)

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

		if locationBytes, err := json.Marshal(structures.TransactionReceipt{Block: currentBlockId, Position: index, Success: success}); err == nil {
			stateBatch.Put([]byte(constants.DBKeyPrefixTxReceipt+transaction.Hash()), locationBytes)
		} else {
			panic("Impossible to add transaction location data to atomic batch")
		}

	}

	if len(delayedTxPayloadsForBatch) > 0 {
		if err := addDelayedTransactionsToBatch(delayedTxPayloadsForBatch, currentEpochIndex, stateBatch); err != nil {
			panic("Impossible to add delayed transactions to atomic batch")
		}
	}

	// distributeFeesAmongValidatorAndStakers(block.Creator, blockFees)
	sendFeesToValidatorAccount(block.Creator, blockFees)

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

	// Update the execution data for progress
	blockHash := block.GetHash()

	blockCreatorData := epochHandlerRef.ExecutionData[block.Creator]
	blockCreatorData.Index = block.Index
	blockCreatorData.Hash = blockHash
	epochHandlerRef.ExecutionData[block.Creator] = blockCreatorData

	// Finally set the updated execution thread handler to atomic batch
	epochHandlerRef.Statistics.LastHeight++
	epochHandlerRef.Statistics.LastBlockHash = blockHash
	epochHandlerRef.Statistics.TotalFees += blockFees
	epochHandlerRef.Statistics.BlocksGenerated++

	epochHandlerRef.EpochStatistics.TotalFees += blockFees
	epochHandlerRef.EpochStatistics.BlocksGenerated++
	// For per-epoch stats we still expose the absolute last height / last block hash (useful to know
	// which exact block finished the epoch).
	epochHandlerRef.EpochStatistics.LastHeight = epochHandlerRef.Statistics.LastHeight
	epochHandlerRef.EpochStatistics.LastBlockHash = blockHash

	stateBatch.Put([]byte(fmt.Sprintf("BLOCK_INDEX:%d", epochHandlerRef.Statistics.LastHeight)), []byte(currentBlockId))

	if execThreadRawBytes, err := json.Marshal(epochHandlerRef); err == nil {
		stateBatch.Put([]byte("ET"), execThreadRawBytes)
	} else {
		panic("Impossible to store updated execution thread version to atomic batch")
	}

	logMsg := fmt.Sprintf("Executed block %s ✅ [%d]", currentBlockId, epochHandlerRef.Statistics.LastHeight)
	return stateBatch, logMsg, true
}

func sendFeesToValidatorAccount(blockCreatorPubkey string, feeFromBlock uint64) {

	blockCreatorAccount := utils.GetAccountFromExecThreadState(blockCreatorPubkey)

	// Transfer fees to account with pubkey associated with block creator

	blockCreatorAccount.Balance += feeFromBlock

}

func executeTransaction(tx *structures.Transaction) (bool, uint64, map[string]string, bool) {

	// Prevent overwriting system keys in STATE via crafted tx.To/tx.From.
	// Account IDs must be canonical pubkeys.
	if !cryptography.IsValidPubKey(tx.From) || !cryptography.IsValidPubKey(tx.To) {
		return false, 0, nil, false
	}

	if cryptography.VerifySignature(tx.Hash(), tx.From, tx.Sig) {

		accountFrom := utils.GetAccountFromExecThreadState(tx.From)
		accountFrom.InitiatedTransactions++

		if delayedTxPayload, delayedTxType, isDelayed := getDelayedTransactionPayload(tx); isDelayed {

			if !validateDelayedTransaction(delayedTxType, tx, delayedTxPayload, accountFrom) {

				return false, 0, nil, false

			}

			accountFrom.Balance -= tx.Fee

			accountFrom.Nonce++

			accountFrom.SuccessfulInitiatedTransactions++

			return true, tx.Fee, delayedTxPayload, true

		}

		accountTo := utils.GetAccountFromExecThreadState(tx.To)

		totalSpend := tx.Fee + tx.Amount

		if accountFrom.Balance >= totalSpend && tx.Nonce == accountFrom.Nonce+1 {

			accountFrom.Balance -= totalSpend

			accountTo.Balance += tx.Amount

			accountFrom.Nonce++

			accountFrom.SuccessfulInitiatedTransactions++

			return true, tx.Fee, nil, false

		}

		return false, 0, nil, false

	}

	return false, 0, nil, false

}

func getDelayedTransactionPayload(tx *structures.Transaction) (map[string]string, string, bool) {

	if tx.Payload == nil {

		return nil, "", false

	}

	payloadType, ok := tx.Payload["type"]

	if !ok {

		return nil, "", false

	}

	payloadTypeStr, ok := payloadType.(string)

	if !ok {

		return nil, "", false

	}

	if _, exists := system_contracts.DELAYED_TRANSACTIONS_MAP[payloadTypeStr]; !exists {

		return nil, "", false

	}

	payload := make(map[string]string)

	for key, value := range tx.Payload {

		payload[key] = fmt.Sprint(value)

	}

	return payload, payloadTypeStr, true

}

func validateDelayedTransaction(delayedTxType string, tx *structures.Transaction, payload map[string]string, accountFrom *structures.Account) bool {

	if accountFrom == nil {

		return false

	}

	if tx.Nonce != accountFrom.Nonce+1 {

		return false

	}

	if accountFrom.Balance < tx.Fee {

		return false

	}

	switch delayedTxType {

	case "createValidator", "updateValidator":

		return tx.From == payload["creator"]

	case "stake":

		amount, err := strconv.ParseUint(payload["amount"], 10, 64)

		if err != nil {

			return false

		}

		return accountFrom.Balance >= amount+tx.Fee

	default:

		return true

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

func setupNextEpoch(epochHandler *structures.EpochDataHandler) {

	currentEpochIndex := epochHandler.Id

	nextEpochIndex := currentEpochIndex + 1

	var nextEpochData *structures.NextEpochDataHandler

	// Take from DB

	rawHandler, dbErr := databases.APPROVEMENT_THREAD_METADATA.Get([]byte("EPOCH_DATA:"+strconv.Itoa(nextEpochIndex)), nil)

	if dbErr == nil {

		json.Unmarshal(rawHandler, &nextEpochData)

	}

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

			dbBatch.Put([]byte("ET"), execThreadRawBytes)

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

	}
	// If we can't start the next epoch, execution can look "stuck" even if other threads progress.
	// Log it (throttled) to surface missing/late epoch data.
	utils.LogWithTimeThrottled(
		fmt.Sprintf("execution:next_epoch_missing:%d", nextEpochIndex),
		5*time.Second,
		fmt.Sprintf("EXECUTION: can't setup next epoch %d (missing EPOCH_DATA:%d in APPROVEMENT_THREAD_METADATA)", nextEpochIndex, nextEpochIndex),
		utils.YELLOW_COLOR,
	)

}
