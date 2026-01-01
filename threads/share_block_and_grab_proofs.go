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
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/modulrcloud/modulr-core/websocket_pack"

	"github.com/gorilla/websocket"
)

type ProofsGrabber struct {
	EpochId             int
	AcceptedIndex       int
	AcceptedHash        string
	AfpForPrevious      structures.AggregatedFinalizationProof
	HuntingForBlockId   string
	HuntingForBlockHash string
}

var (
	PROOFS_GRABBER       = ProofsGrabber{EpochId: -1}
	PROOFS_GRABBER_MUTEX = sync.RWMutex{}
)

var (
	WEBSOCKET_CONNECTIONS       = make(map[string]*websocket.Conn) // quorumMember => websocket handler
	WEBSOCKET_GUARDS_FOR_PROOFS = utils.NewWebsocketGuards()
)

var FINALIZATION_PROOFS_CACHE = make(map[string]string) // quorumMember => finalization proof signa

var BLOCK_TO_SHARE *block_pack.Block = &block_pack.Block{Index: -1}

var QUORUM_WAITER_FOR_FINALIZATION_PROOFS *utils.QuorumWaiter

func BlocksSharingAndProofsGrabingThread() {

	for {

		// Take a snapshot of AT state quickly, then do any heavy work without holding AT lock.
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
		atSnapshot := handlers.APPROVEMENT_THREAD_METADATA.Handler
		epochSnapshot := atSnapshot.EpochDataHandler
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		currentLeaderPubKey := epochSnapshot.LeadersSequence[epochSnapshot.CurrentLeaderIndex]

		if currentLeaderPubKey != globals.CONFIGURATION.PublicKey || !utils.EpochStillFresh(&atSnapshot) {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		PROOFS_GRABBER_MUTEX.RLock()

		if PROOFS_GRABBER.EpochId != epochSnapshot.Id {

			PROOFS_GRABBER_MUTEX.RUnlock()

			PROOFS_GRABBER_MUTEX.Lock()

			// Try to get stored proofs grabber from db

			dbKey := []byte(strconv.Itoa(epochSnapshot.Id) + ":PROOFS_GRABBER")

			if rawGrabber, err := databases.FINALIZATION_VOTING_STATS.Get(dbKey, nil); err == nil {

				json.Unmarshal(rawGrabber, &PROOFS_GRABBER)

			} else {

				// Assign initial value of proofs grabber for each new epoch

				PROOFS_GRABBER = ProofsGrabber{

					EpochId: epochSnapshot.Id,

					AcceptedIndex: -1,

					AcceptedHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				}

				// Also - clean the mapping with the signatures for AFP

				FINALIZATION_PROOFS_CACHE = make(map[string]string)

			}

			// And store new descriptor

			if serialized, err := json.Marshal(PROOFS_GRABBER); err == nil {

				databases.FINALIZATION_VOTING_STATS.Put(dbKey, serialized, nil)

			}

			PROOFS_GRABBER_MUTEX.Unlock()

			// Also, open connections with quorum here. Create QuorumWaiter etc.
			// This is network I/O, so do it without holding AT lock.
			utils.OpenWebsocketConnectionsWithQuorum(epochSnapshot.Quorum, WEBSOCKET_CONNECTIONS, WEBSOCKET_GUARDS_FOR_PROOFS)
			QUORUM_WAITER_FOR_FINALIZATION_PROOFS = utils.NewQuorumWaiter(len(epochSnapshot.Quorum), WEBSOCKET_GUARDS_FOR_PROOFS)

		} else {

			PROOFS_GRABBER_MUTEX.RUnlock()

		}

		runFinalizationProofsGrabbing(&epochSnapshot)

	}

}

func runFinalizationProofsGrabbing(epochHandler *structures.EpochDataHandler) {

	// Call SendAndWait here
	// Once received 2/3 votes for block - continue

	// Snapshot what we need under lock, then do network work without holding PROOFS_GRABBER_MUTEX.
	PROOFS_GRABBER_MUTEX.Lock()

	epochFullId := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)
	majority := utils.GetQuorumMajority(epochHandler)

	blockIndexToHunt := PROOFS_GRABBER.AcceptedIndex + 1
	blockIdForHunting := strconv.Itoa(epochHandler.Id) + ":" + globals.CONFIGURATION.PublicKey + ":" + strconv.Itoa(blockIndexToHunt)

	// Copy current cache and previous proof snapshot
	proofsCopy := make(map[string]string, len(FINALIZATION_PROOFS_CACHE))
	for k, v := range FINALIZATION_PROOFS_CACHE {
		proofsCopy[k] = v
	}
	acceptedHash := PROOFS_GRABBER.AcceptedHash
	afpPrev := PROOFS_GRABBER.AfpForPrevious

	// Try to reuse in-memory block if it matches; otherwise we'll load it outside lock.
	reuseBlock := BLOCK_TO_SHARE != nil && strconv.Itoa(epochHandler.Id)+":"+globals.CONFIGURATION.PublicKey+":"+strconv.Itoa(BLOCK_TO_SHARE.Index) == blockIdForHunting
	var blockToShare block_pack.Block
	if reuseBlock {
		blockToShare = *BLOCK_TO_SHARE
	}

	PROOFS_GRABBER.HuntingForBlockId = blockIdForHunting
	PROOFS_GRABBER_MUTEX.Unlock()

	// Load block from DB if not in memory (DB I/O outside lock).
	if !reuseBlock {
		blockDataRaw, errDB := databases.BLOCKS.Get([]byte(blockIdForHunting), nil)
		if errDB != nil {
			return
		}
		if err := json.Unmarshal(blockDataRaw, &blockToShare); err != nil {
			return
		}
	}

	blockHash := blockToShare.GetHash()

	// If we already have enough proofs, we can skip network step.
	if len(proofsCopy) < majority {

		// Build message - then parse to JSON

		message := websocket_pack.WsFinalizationProofRequest{
			Route:            "get_finalization_proof",
			Block:            blockToShare,
			PreviousBlockAfp: afpPrev,
		}

		if messageJsoned, err := json.Marshal(message); err == nil {

			// Create max delay

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			responses, ok := QUORUM_WAITER_FOR_FINALIZATION_PROOFS.SendAndWait(ctx, messageJsoned, epochHandler.Quorum, WEBSOCKET_CONNECTIONS, majority)

			if !ok {
				return
			}

			for _, raw := range responses {

				var parsedFinalizationProof websocket_pack.WsFinalizationProofResponse

				if err := json.Unmarshal(raw, &parsedFinalizationProof); err == nil {

					// Now verify proof and parse requests

					if parsedFinalizationProof.VotedForHash == blockHash {

						// Verify the finalization proof

						dataThatShouldBeSigned := strings.Join(

							[]string{acceptedHash, blockIdForHunting, blockHash, epochFullId}, ":",
						)

						finalizationProofIsOk := slices.Contains(epochHandler.Quorum, parsedFinalizationProof.Voter) && cryptography.VerifySignature(

							dataThatShouldBeSigned, parsedFinalizationProof.Voter, parsedFinalizationProof.FinalizationProof,
						)

						if finalizationProofIsOk {

							proofsCopy[parsedFinalizationProof.Voter] = parsedFinalizationProof.FinalizationProof

						}

					}

				}

			}

		}

	}

	// Apply results under lock and, if reached majority, persist AFP/progress.
	PROOFS_GRABBER_MUTEX.Lock()
	defer PROOFS_GRABBER_MUTEX.Unlock()

	// Ensure we are still hunting the same block for the same epoch.
	if PROOFS_GRABBER.EpochId != epochHandler.Id || PROOFS_GRABBER.HuntingForBlockId != blockIdForHunting {
		return
	}

	// Merge proofs (in case some were added while we were unlocked).
	for k, v := range proofsCopy {
		FINALIZATION_PROOFS_CACHE[k] = v
	}

	PROOFS_GRABBER.HuntingForBlockHash = blockHash

	// Keep BLOCK_TO_SHARE up to date for compatibility with existing logic/debugging.
	if BLOCK_TO_SHARE == nil {
		BLOCK_TO_SHARE = &block_pack.Block{}
	}
	*BLOCK_TO_SHARE = blockToShare

	if len(FINALIZATION_PROOFS_CACHE) >= majority {

		aggregatedFinalizationProof := structures.AggregatedFinalizationProof{

			PrevBlockHash: PROOFS_GRABBER.AcceptedHash,

			BlockId: blockIdForHunting,

			BlockHash: blockHash,

			Proofs: FINALIZATION_PROOFS_CACHE,
		}

		keyBytes := []byte("AFP:" + blockIdForHunting)

		valueBytes, _ := json.Marshal(aggregatedFinalizationProof)

		// Store AFP locally

		databases.EPOCH_DATA.Put(keyBytes, valueBytes, nil)

		// Repeat procedure for the next block and store the progress

		proofGrabberKeyBytes := []byte(strconv.Itoa(epochHandler.Id) + ":PROOFS_GRABBER")

		proofGrabberValueBytes, marshalErr := json.Marshal(PROOFS_GRABBER)

		if marshalErr == nil {

			proofsGrabberStoreErr := databases.FINALIZATION_VOTING_STATS.Put(proofGrabberKeyBytes, proofGrabberValueBytes, nil)

			if proofsGrabberStoreErr == nil {

				PROOFS_GRABBER.AfpForPrevious = aggregatedFinalizationProof

				PROOFS_GRABBER.AcceptedIndex++

				PROOFS_GRABBER.AcceptedHash = PROOFS_GRABBER.HuntingForBlockHash

				if PROOFS_GRABBER.AcceptedIndex > 0 {

					msg := fmt.Sprintf(
						"%sApproved height for epoch %s%d %sis %s%d %s(hash:%s...) %s(%.3f%% agreements)",
						utils.RED_COLOR,
						utils.CYAN_COLOR,
						epochHandler.Id,
						utils.RED_COLOR,
						utils.CYAN_COLOR,
						PROOFS_GRABBER.AcceptedIndex-1,
						utils.CYAN_COLOR,
						PROOFS_GRABBER.AfpForPrevious.PrevBlockHash[:8],
						utils.GREEN_COLOR,
						float64(len(FINALIZATION_PROOFS_CACHE))/float64(len(epochHandler.Quorum))*100,
					)

					utils.LogWithTime(msg, utils.WHITE_COLOR)

				}

				// Delete finalization proofs that we don't need more

				FINALIZATION_PROOFS_CACHE = make(map[string]string)

			} else {
				return
			}

		} else {
			return
		}

	}

}
