package threads

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/system_contracts"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/modulrcloud/modulr-core/websocket_pack"

	"github.com/syndtr/goleveldb/leveldb"
)

func BlockExecutionThread() {

	for {

		// NOTE: Don't hold EXECUTION_THREAD_METADATA lock during network I/O to PoD.
		// On high-latency links this blocks alignment/monitor threads and can cause huge execution lag.

		progressed := false

		handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()

		epochHandlerRef := &handlers.EXECUTION_THREAD_METADATA.Handler
		currentEpochAlignmentData := &epochHandlerRef.SequenceAlignmentData

		epochSnapshot := epochHandlerRef.EpochDataHandler

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
				break
			}

			// Decide whether we can execute this block.
			shouldExecWithoutAfp := infoAboutLastBlockExists &&
				execStatsOfLeader.Index+1 == infoAboutLastBlockByThisLeader.Index &&
				response.Block.GetHash() == infoAboutLastBlockByThisLeader.Hash

			shouldExecWithAfp := response.Afp != nil && utils.VerifyAggregatedFinalizationProof(response.Afp, &epochSnapshot)

			if !shouldExecWithoutAfp && !shouldExecWithAfp {
				break
			}

			// Apply block under lock (mutates handler caches/state and writes to DB).
			handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()
			executeBlock(response.Block)
			execStatsOfLeader = handlers.EXECUTION_THREAD_METADATA.Handler.ExecutionData[leaderPubkeyToExecBlocks]
			handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

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

func getAnchorBlockAndAfpFromAnchorsPoD(blockID string) *websocket_pack.WsAnchorBlockWithAfpResponse {

	req := websocket_pack.WsAnchorBlockWithAfpRequest{
		Route:   constants.WsRouteGetAnchorBlockWithAfp,
		BlockId: blockID,
	}

	if reqBytes, err := json.Marshal(req); err == nil {

		if respBytes, err := utils.SendWebsocketMessageToAnchorsPoD(reqBytes); err == nil {

			var resp websocket_pack.WsAnchorBlockWithAfpResponse

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

func executeBlock(block *block_pack.Block) {

	epochHandlerRef := &handlers.EXECUTION_THREAD_METADATA.Handler

	if epochHandlerRef.Statistics == nil {

		epochHandlerRef.Statistics = &structures.Statistics{LastHeight: -1}

	}

	if epochHandlerRef.ExecutionData[block.Creator].Hash == block.PrevHash {

		currentEpochIndex := epochHandlerRef.EpochDataHandler.Id

		currentBlockId := strconv.Itoa(currentEpochIndex) + ":" + block.Creator + ":" + strconv.Itoa(block.Index)

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

			if success {

				epochHandlerRef.Statistics.SuccessfulTransactions++

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

		for accountID, accountData := range epochHandlerRef.AccountsCache {

			if accountDataBytes, err := json.Marshal(accountData); err == nil {

				stateBatch.Put([]byte(accountID), accountDataBytes)

			} else {

				panic("Impossible to add new account data to atomic batch")

			}

		}

		for validatorPubkey, validatorStorage := range epochHandlerRef.ValidatorsStoragesCache {

			if dataBytes, err := json.Marshal(validatorStorage); err == nil {

				stateBatch.Put([]byte(constants.DBKeyPrefixValidatorStorage+validatorPubkey), dataBytes)

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

		stateBatch.Put([]byte(fmt.Sprintf("BLOCK_INDEX:%d", epochHandlerRef.Statistics.LastHeight)), []byte(currentBlockId))

		if execThreadRawBytes, err := json.Marshal(epochHandlerRef); err == nil {

			stateBatch.Put([]byte("ET"), execThreadRawBytes)

		} else {

			panic("Impossible to store updated execution thread version to atomic batch")

		}

		if err := databases.STATE.Write(stateBatch, nil); err == nil {

			utils.LogWithTime2(fmt.Sprintf("Executed block %s ✅ [%d]", currentBlockId, epochHandlerRef.Statistics.LastHeight), utils.CYAN_COLOR)

		} else {

			panic("Impossible to commit changes in atomic batch to permanent state")

		}

	}

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

		dbBatch := new(leveldb.Batch)

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

		for accountID, accountData := range handlers.EXECUTION_THREAD_METADATA.Handler.AccountsCache {

			if accountDataBytes, err := json.Marshal(accountData); err == nil {

				dbBatch.Put([]byte(accountID), accountDataBytes)

			} else {

				panic("Impossible to add new account data to atomic batch")

			}

		}

		for validatorPubkey, validatorStorage := range handlers.EXECUTION_THREAD_METADATA.Handler.ValidatorsStoragesCache {

			if dataBytes, err := json.Marshal(validatorStorage); err == nil {

				dbBatch.Put([]byte(constants.DBKeyPrefixValidatorStorage+validatorPubkey), dataBytes)

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

}
