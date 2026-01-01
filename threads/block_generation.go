package threads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/syndtr/goleveldb/leveldb"
)

func BlockGenerationThread() {

	for {

		// Don't hold AT lock while generating blocks (generation may include network I/O for delayed tx quorum signatures).
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
		blockTime := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.BlockTime
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		generateBlock()

		time.Sleep(time.Duration(blockTime) * time.Millisecond)

	}

}

func getTransactionsFromMempool(limit int) []structures.Transaction {

	globals.MEMPOOL.Mutex.Lock()

	defer globals.MEMPOOL.Mutex.Unlock()

	mempoolSize := len(globals.MEMPOOL.Slice)

	if limit > mempoolSize {

		limit = mempoolSize

	}

	transactions := make([]structures.Transaction, limit)

	copy(transactions, globals.MEMPOOL.Slice[:limit])

	globals.MEMPOOL.Slice = globals.MEMPOOL.Slice[limit:]

	return transactions
}

func getBatchOfApprovedDelayedTxsByQuorum(epochHandler structures.EpochDataHandler, indexOfLeader int) structures.DelayedTransactionsBatch {

	prevEpochIndex := epochHandler.Id - 2
	majority := utils.GetQuorumMajority(&epochHandler)

	batch := structures.DelayedTransactionsBatch{
		EpochIndex:          prevEpochIndex,
		DelayedTransactions: []map[string]string{},
		Proofs:              map[string]string{},
	}

	if indexOfLeader != 0 || handlers.GENERATION_THREAD_METADATA.NextIndex != 0 {
		return batch
	}

	delayedTxKey := fmt.Sprintf("DELAYED_TRANSACTIONS:%d", prevEpochIndex)
	rawDelayedTxs, err := databases.STATE.Get([]byte(delayedTxKey), nil)
	if err != nil {
		return batch
	}

	var delayedTransactions []map[string]string
	if err := json.Unmarshal(rawDelayedTxs, &delayedTransactions); err != nil {
		return batch
	}

	if len(delayedTransactions) == 0 {
		return batch
	}

	delayedTxHash := utils.Blake3(string(rawDelayedTxs))

	proofs := map[string]string{
		globals.CONFIGURATION.PublicKey: cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, delayedTxHash),
	}

	quorumMembers := utils.GetQuorumUrlsAndPubkeys(&epochHandler)
	reqBody, err := json.Marshal(map[string]int{"epochIndex": prevEpochIndex})
	if err != nil {
		return batch
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type signatureResult struct {
		pubKey    string
		signature string
	}

	httpClient := &http.Client{Timeout: 2 * time.Second}
	signaturesChan := make(chan signatureResult, len(quorumMembers))
	var wg sync.WaitGroup

	for _, member := range quorumMembers {
		if member.PubKey == globals.CONFIGURATION.PublicKey {
			continue
		}

		wg.Add(1)
		go func(member structures.QuorumMemberData) {
			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, http.MethodPost, member.Url+"/delayed_transactions_signature", bytes.NewBuffer(reqBody))
			if err != nil {
				return
			}

			req.Header.Set("Content-Type", "application/json")

			resp, err := httpClient.Do(req)
			if err != nil {
				return
			}

			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return
			}

			var signResponse struct {
				Signature string `json:"signature"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&signResponse); err != nil {
				return
			}

			if signResponse.Signature == "" {
				return
			}

			select {
			case signaturesChan <- signatureResult{pubKey: member.PubKey, signature: signResponse.Signature}:
			case <-ctx.Done():
			}
		}(member)
	}

	go func() {
		wg.Wait()
		close(signaturesChan)
	}()

	for signResult := range signaturesChan {
		if _, alreadyAdded := proofs[signResult.pubKey]; alreadyAdded {
			continue
		}

		proofs[signResult.pubKey] = signResult.signature
		if len(proofs) >= majority {
			cancel()
		}
	}

	if len(proofs) < majority {
		return batch
	}

	batch.DelayedTransactions = delayedTransactions
	batch.Proofs = proofs

	return batch

}

func generateBlock() {

	// Snapshot AT quickly; do heavy work without holding AT lock.
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	atSnapshot := handlers.APPROVEMENT_THREAD_METADATA.Handler
	epochSnapshot := atSnapshot.EpochDataHandler
	txLimit := atSnapshot.NetworkParameters.TxLimitPerBlock
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	if !utils.EpochStillFresh(&atSnapshot) {

		return

	}

	epochFullID := epochSnapshot.Hash + "#" + strconv.Itoa(epochSnapshot.Id)

	epochIndex := epochSnapshot.Id

	currentLeaderPubKey := epochSnapshot.LeadersSequence[epochSnapshot.CurrentLeaderIndex]

	PROOFS_GRABBER_MUTEX.RLock()

	// Safe "if" branch to prevent unnecessary blocks generation

	shouldGenerateBlocks := currentLeaderPubKey == globals.CONFIGURATION.PublicKey && handlers.GENERATION_THREAD_METADATA.NextIndex <= PROOFS_GRABBER.AcceptedIndex+1

	shouldRotateEpochOnGenerationThread := handlers.GENERATION_THREAD_METADATA.EpochFullId != epochFullID

	if shouldGenerateBlocks || shouldRotateEpochOnGenerationThread {

		PROOFS_GRABBER_MUTEX.RUnlock()

		// Check if <epochFullID> is the same in APPROVEMENT_THREAD and in GENERATION_THREAD

		if shouldRotateEpochOnGenerationThread {

			// Update the index & hash of epoch (by assigning new epoch full ID)

			handlers.GENERATION_THREAD_METADATA.EpochFullId = epochFullID

			// Nullish the index & hash in generation thread for new epoch

			handlers.GENERATION_THREAD_METADATA.PrevHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

			handlers.GENERATION_THREAD_METADATA.NextIndex = 0

		}

		// Safe "if" branch to prevent unnecessary blocks generation
		if !shouldGenerateBlocks {
			return
		}

		extraData := block_pack.ExtraDataToBlock{}

		extraData.DelayedTransactionsBatch = getBatchOfApprovedDelayedTxsByQuorum(epochSnapshot, epochSnapshot.CurrentLeaderIndex)

		extraData.Rest = globals.CONFIGURATION.ExtraDataToBlock

		blockDbAtomicBatch := new(leveldb.Batch)

		blockCandidate := block_pack.NewBlock(getTransactionsFromMempool(txLimit), extraData, epochFullID)

		blockHash := blockCandidate.GetHash()

		blockCandidate.SignBlock()

		// BlockID has the following format => epochID(epochIndex):Ed25519_Pubkey:IndexOfBlockInCurrentEpoch

		blockID := strconv.Itoa(epochIndex) + ":" + globals.CONFIGURATION.PublicKey + ":" + strconv.Itoa(blockCandidate.Index)

		utils.LogWithTime("New block generated "+blockID+" (txs: "+strconv.Itoa(len(blockCandidate.Transactions))+", hash: "+blockHash[:8]+"...)", utils.CYAN_COLOR)

		if blockBytes, serializeErr := json.Marshal(blockCandidate); serializeErr == nil {

			handlers.GENERATION_THREAD_METADATA.PrevHash = blockHash

			handlers.GENERATION_THREAD_METADATA.NextIndex++

			if gtBytes, serializeErr2 := json.Marshal(handlers.GENERATION_THREAD_METADATA); serializeErr2 == nil {

				// Store block locally

				blockDbAtomicBatch.Put([]byte(blockID), blockBytes)

				// Update the GENERATION_THREAD after all

				blockDbAtomicBatch.Put([]byte("GT"), gtBytes)

				if err := databases.BLOCKS.Write(blockDbAtomicBatch, nil); err != nil {

					panic("Can't store GT and block candidate")

				}

			}

		}

	} else {

		PROOFS_GRABBER_MUTEX.RUnlock()

	}

}
