package websocket_pack

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/lxzan/gws"
)

// Only one block creator can request proof for block at a choosen period of time T
var BLOCK_CREATOR_REQUEST_MUTEX = sync.Mutex{}

func getEpochHandlerForLeaderFinalization(epochIndex int) *structures.EpochDataHandler {

	if epochIndex < 0 {
		return nil
	}

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	if handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.Id == epochIndex {
		handlerCopy := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
		return &handlerCopy
	}
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	key := []byte("EPOCH_HANDLER:" + strconv.Itoa(epochIndex))
	// EPOCH_HANDLER snapshots are stored in APPROVEMENT_THREAD_METADATA DB
	if raw, err := databases.APPROVEMENT_THREAD_METADATA.Get(key, nil); err == nil {
		var snapshot structures.EpochDataSnapshot
		if json.Unmarshal(raw, &snapshot) == nil {
			return &snapshot.EpochDataHandler
		}
	}

	return nil
}

func GetFinalizationProof(parsedRequest WsFinalizationProofRequest, connection *gws.Conn) {

	if !globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Load() {
		return
	}

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()

	defer handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	epochHandler := &handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler

	epochIndex := epochHandler.Id

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochIndex)

	itsLeader := epochHandler.LeadersSequence[epochHandler.CurrentLeaderIndex] == parsedRequest.Block.Creator

	if itsLeader {

		localVotingDataForLeader := structures.NewLeaderVotingStatTemplate()

		localVotingDataRaw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte(strconv.Itoa(epochIndex)+":"+parsedRequest.Block.Creator), nil)

		if err == nil {

			json.Unmarshal(localVotingDataRaw, &localVotingDataForLeader)

		}

		proposedBlockHash := parsedRequest.Block.GetHash()

		itsSameChainSegment := localVotingDataForLeader.Index < int(parsedRequest.Block.Index) || localVotingDataForLeader.Index == int(parsedRequest.Block.Index) && proposedBlockHash == localVotingDataForLeader.Hash && parsedRequest.Block.Epoch == epochFullID

		if itsSameChainSegment {

			proposedBlockId := strconv.Itoa(epochIndex) + ":" + parsedRequest.Block.Creator + ":" + strconv.Itoa(int(parsedRequest.Block.Index))

			previousBlockIndex := int(parsedRequest.Block.Index - 1)

			var futureVotingDataToStore structures.VotingStat

			if parsedRequest.Block.VerifySignature() && !utils.SignalAboutEpochRotationExists(epochIndex) {

				BLOCK_CREATOR_REQUEST_MUTEX.Lock()

				defer BLOCK_CREATOR_REQUEST_MUTEX.Unlock()

				if localVotingDataForLeader.Index == int(parsedRequest.Block.Index) {

					futureVotingDataToStore = localVotingDataForLeader

				} else if parsedRequest.Block.Index == 0 {

					futureVotingDataToStore = structures.NewLeaderVotingStatTemplate()

				} else {

					futureVotingDataToStore = structures.VotingStat{

						Index: previousBlockIndex,

						Hash: parsedRequest.PreviousBlockAfp.BlockHash,

						Afp: parsedRequest.PreviousBlockAfp,
					}

				}

				// This branch related to case when block index is > 0 (so it's not the first block by leader)

				previousBlockId := strconv.Itoa(epochIndex) + ":" + parsedRequest.Block.Creator + ":" + strconv.Itoa(previousBlockIndex)

				// Check if AFP inside related to previous block AFP

				if parsedRequest.Block.Index == 0 || previousBlockId == parsedRequest.PreviousBlockAfp.BlockId && utils.VerifyAggregatedFinalizationProof(&parsedRequest.PreviousBlockAfp, epochHandler) {

					// Store the block and return finalization proof

					blockBytes, err := json.Marshal(parsedRequest.Block)

					if err == nil {

						// 1. Store the block

						err = databases.BLOCKS.Put([]byte(proposedBlockId), blockBytes, nil)

						if err == nil {

							var errStore error

							if parsedRequest.Block.Index == 0 {
								// No previous block; skip AFP storage.
								errStore = nil
							} else {
								afpBytes, err := json.Marshal(parsedRequest.PreviousBlockAfp)
								if err == nil {
									// 2. Store the AFP for previous block
									errStore = databases.EPOCH_DATA.Put([]byte("AFP:"+parsedRequest.PreviousBlockAfp.BlockId), afpBytes, nil)
								} else {
									errStore = err
								}
							}

							votingStatBytes, errParse := json.Marshal(futureVotingDataToStore)

							if errStore == nil && errParse == nil {

								// 3. Store the voting stats

								err := databases.FINALIZATION_VOTING_STATS.Put([]byte(strconv.Itoa(epochIndex)+":"+parsedRequest.Block.Creator), votingStatBytes, nil)

								if err == nil {

									// Only after we stored the these 3 components = generate signature (finalization proof)

									dataToSign, prevBlockHash := "", ""

									if parsedRequest.Block.Index == 0 {

										prevBlockHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

									} else {

										prevBlockHash = parsedRequest.PreviousBlockAfp.BlockHash

									}

									dataToSign += strings.Join([]string{prevBlockHash, proposedBlockId, proposedBlockHash, epochFullID}, ":")

									response := WsFinalizationProofResponse{
										Voter:             globals.CONFIGURATION.PublicKey,
										FinalizationProof: cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, dataToSign),
										VotedForHash:      proposedBlockHash,
									}

									jsonResponse, err := json.Marshal(response)

									if err == nil {

										go SendBlockAndAfpToPoD(parsedRequest.Block, parsedRequest.PreviousBlockAfp)

										connection.WriteMessage(gws.OpcodeText, jsonResponse)

									}

								}

							}

						}

					}

				}

			}

		}

	}

}

func GetLeaderFinalizationProof(parsedRequest WsLeaderFinalizationProofRequest, connection *gws.Conn) {

	if !globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Load() {
		return
	}

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	activeEpochID := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.Id
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	epochHandler := getEpochHandlerForLeaderFinalization(parsedRequest.EpochIndex)
	if epochHandler == nil {
		return
	}

	isRequestForPastEpoch := parsedRequest.EpochIndex < activeEpochID

	if parsedRequest.IndexOfLeaderToFinalize < 0 || parsedRequest.IndexOfLeaderToFinalize >= len(epochHandler.LeadersSequence) {
		return
	}

	if !isRequestForPastEpoch && epochHandler.CurrentLeaderIndex <= parsedRequest.IndexOfLeaderToFinalize {
		return
	}

	epochIndex := epochHandler.Id
	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochIndex)

	leaderToFinalize := epochHandler.LeadersSequence[parsedRequest.IndexOfLeaderToFinalize]

	if isRequestForPastEpoch || epochHandler.CurrentLeaderIndex > parsedRequest.IndexOfLeaderToFinalize {

		localVotingData := structures.NewLeaderVotingStatTemplate()

		localVotingDataRaw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte(strconv.Itoa(epochIndex)+":"+leaderToFinalize), nil)

		if err == nil {

			json.Unmarshal(localVotingDataRaw, &localVotingData)

		}

		propSkipData := parsedRequest.SkipData

		if localVotingData.Index > propSkipData.Index {

			responseData := WsLeaderFinalizationProofResponseUpgrade{
				Voter:           globals.CONFIGURATION.PublicKey,
				ForLeaderPubkey: leaderToFinalize,
				Status:          "UPGRADE",
				SkipData:        localVotingData,
			}

			jsonResponse, err := json.Marshal(responseData)

			if err == nil {

				connection.WriteMessage(gws.OpcodeText, jsonResponse)

			}

		} else {

			//________________________________________________ Verify the proposed AFP __________________________________________________________________

			afpIsOk := false

			if propSkipData.Index > -1 {

				parts := strings.Split(propSkipData.Afp.BlockId, ":")
				if len(parts) != 3 {
					return
				}

				indexOfBlockInAfp, err := strconv.Atoi(parts[2])
				if err != nil {
					return
				}

				if propSkipData.Hash == propSkipData.Afp.BlockHash && propSkipData.Index == indexOfBlockInAfp {

					afpIsOk = utils.VerifyAggregatedFinalizationProof(&propSkipData.Afp, epochHandler)

				}

			} else {

				afpIsOk = true

			}

			if afpIsOk {

				dataToSignForLeaderFinalization := ""

				if parsedRequest.SkipData.Index == -1 {

					dataToSignForLeaderFinalization = "LEADER_FINALIZATION_PROOF:" + leaderToFinalize

					dataToSignForLeaderFinalization += ":-1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef:"

					dataToSignForLeaderFinalization += epochFullID

				} else if parsedRequest.SkipData.Index >= 0 {

					dataToSignForLeaderFinalization = "LEADER_FINALIZATION_PROOF:" + leaderToFinalize +
						":" + strconv.Itoa(propSkipData.Index) +
						":" + propSkipData.Hash +
						":" + epochFullID

				}

				// Finally - generate LFP(leader finalization proof)

				leaderFinalizationProofMessage := WsLeaderFinalizationProofResponseOk{

					Voter:           globals.CONFIGURATION.PublicKey,
					ForLeaderPubkey: leaderToFinalize,
					Status:          "OK",
					Sig:             cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, dataToSignForLeaderFinalization),
				}

				jsonResponse, err := json.Marshal(leaderFinalizationProofMessage)

				if err == nil {

					connection.WriteMessage(gws.OpcodeText, jsonResponse)

				}

			}

		}

	}

}

func GetBlockWithProof(parsedRequest WsBlockWithAfpRequest, connection *gws.Conn) {

	if blockBytes, err := databases.BLOCKS.Get([]byte(parsedRequest.BlockId), nil); err == nil {

		var block block_pack.Block

		if err := json.Unmarshal(blockBytes, &block); err == nil {

			resp := WsBlockWithAfpResponse{&block, nil}

			// Now try to get AFP for block

			parts := strings.Split(parsedRequest.BlockId, ":")

			if len(parts) > 0 {

				last := parts[len(parts)-1]

				if idx, err := strconv.ParseUint(last, 10, 64); err == nil {

					parts[len(parts)-1] = strconv.FormatUint(idx+1, 10)

					nextBlockId := strings.Join(parts, ":")

					// Remark: To make sure block with index X is 100% approved we need to get the AFP for next block

					if afpBytes, err := databases.EPOCH_DATA.Get([]byte("AFP:"+nextBlockId), nil); err == nil {

						var afp structures.AggregatedFinalizationProof

						if err := json.Unmarshal(afpBytes, &afp); err == nil {

							resp.Afp = &afp

						}

					}

				}

			}

			jsonResponse, err := json.Marshal(resp)

			if err == nil {

				connection.WriteMessage(gws.OpcodeText, jsonResponse)

			}

		}

	}

}
