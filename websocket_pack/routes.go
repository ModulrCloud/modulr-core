package websocket_pack

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/constants"
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

func sendNotReady(connection *gws.Conn) {
	connection.WriteMessage(gws.OpcodeText, []byte(`{"status":"NOT_READY"}`))
}

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

	// EPOCH_HANDLER snapshots are stored in APPROVEMENT_THREAD_METADATA DB
	if snapshot := utils.GetEpochSnapshot(epochIndex); snapshot != nil {
		return &snapshot.EpochDataHandler
	}

	return nil
}

func GetFinalizationProof(parsedRequest WsFinalizationProofRequest, connection *gws.Conn) {
	if !globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Load() {
		sendNotReady(connection)
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
									errStore = databases.EPOCH_DATA.Put([]byte(constants.DBKeyPrefixAfp+parsedRequest.PreviousBlockAfp.BlockId), afpBytes, nil)
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
										prevBlockHash = constants.ZeroBlockHash
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
		sendNotReady(connection)
		return
	}

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	activeEpochID := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.Id
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	epochHandler := getEpochHandlerForLeaderFinalization(parsedRequest.EpochIndex)
	if epochHandler == nil {
		sendNotReady(connection)
		return
	}

	isRequestForPastEpoch := parsedRequest.EpochIndex < activeEpochID

	if parsedRequest.IndexOfLeaderToFinalize < 0 || parsedRequest.IndexOfLeaderToFinalize >= len(epochHandler.LeadersSequence) {
		sendNotReady(connection)
		return
	}

	if !isRequestForPastEpoch && epochHandler.CurrentLeaderIndex < parsedRequest.IndexOfLeaderToFinalize {
		sendNotReady(connection)
		return
	}

	lastLeaderIdx := len(epochHandler.LeadersSequence) - 1
	isLastLeader := parsedRequest.IndexOfLeaderToFinalize == lastLeaderIdx && epochHandler.CurrentLeaderIndex == lastLeaderIdx

	if !isRequestForPastEpoch && epochHandler.CurrentLeaderIndex <= parsedRequest.IndexOfLeaderToFinalize && !isLastLeader {
		sendNotReady(connection)
		return
	}

	epochIndex := epochHandler.Id
	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochIndex)

	leaderToFinalize := epochHandler.LeadersSequence[parsedRequest.IndexOfLeaderToFinalize]

	if isRequestForPastEpoch || epochHandler.CurrentLeaderIndex > parsedRequest.IndexOfLeaderToFinalize || isLastLeader {
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

					dataToSignForLeaderFinalization += ":-1:" + constants.ZeroBlockHash + ":"

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

var heightAttestationVoterMutex sync.Mutex

func SignHeightAttestation(parsedRequest WsHeightAttestationRequest, connection *gws.Conn) {
	if !globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Load() {
		sendNotReady(connection)
		return
	}

	heightAttestationVoterMutex.Lock()
	defer heightAttestationVoterMutex.Unlock()

	state := utils.LoadLastMileSequenceState(constants.DBKeyHeightAttestationVoterState)
	requestedHeight := int64(parsedRequest.AbsoluteHeight)

	expectedBlockId := ""
	expectedHeightInEpoch := -1

	if requestedHeight < state.NextHeight {
		expectedBlockId = utils.LoadHeightBlockIdMapping(requestedHeight)
		if h, ok := utils.LoadHeightInEpochMapping(requestedHeight); ok {
			expectedHeightInEpoch = h
		}
	} else {
		maxIterations := 1000

		for i := 0; i < maxIterations && state.NextHeight <= requestedHeight; i++ {
			epochHandler := getEpochHandlerForLeaderFinalization(state.EpochId)

			if epochHandler == nil {
				break
			}

			lastBlocksByLeaders := snapshotAlignmentDataForHeightVoter()
			if lastBlocksByLeaders == nil {
				lastBlocksByLeaders = make(map[string]structures.ExecutionStats)
			}

			blockId := state.CurrentBlockId(epochHandler.LeadersSequence, lastBlocksByLeaders)

			if blockId == "" {
				if state.AllLeadersDone(epochHandler.LeadersSequence) {
					state.AdvanceToNextEpoch()
					continue
				}
				break
			}

			blockHash := getBlockHashForHeightVoter(blockId)
			if blockHash == "" {
				break
			}

			confirmed, _ := state.IsBlockConfirmed(epochHandler.LeadersSequence, lastBlocksByLeaders, blockHash, epochHandler)
			if !confirmed {
				break
			}

			utils.StoreHeightBlockIdMapping(state.NextHeight, blockId)
			utils.StoreHeightInEpochMapping(state.NextHeight, state.HeightInEpoch)

			if state.NextHeight == requestedHeight {
				expectedBlockId = blockId
				expectedHeightInEpoch = state.HeightInEpoch
			}

			state.Advance()
		}

		utils.PersistLastMileSequenceState(constants.DBKeyHeightAttestationVoterState, state)
	}

	if expectedBlockId == "" || expectedBlockId != parsedRequest.BlockId {
		sendNotReady(connection)
		return
	}

	if expectedHeightInEpoch < 0 || expectedHeightInEpoch != parsedRequest.HeightInEpoch {
		sendNotReady(connection)
		return
	}

	storedBlockHash := getBlockHashForHeightVoter(parsedRequest.BlockId)

	if storedBlockHash == "" || storedBlockHash != parsedRequest.BlockHash {
		sendNotReady(connection)
		return
	}

	dataToSign := strings.Join([]string{
		constants.SigningPrefixHeightAttestation,
		strconv.Itoa(parsedRequest.AbsoluteHeight),
		parsedRequest.BlockId,
		parsedRequest.BlockHash,
		strconv.Itoa(parsedRequest.EpochId),
		strconv.Itoa(parsedRequest.HeightInEpoch),
	}, ":")

	response := WsHeightAttestationResponse{
		Voter: globals.CONFIGURATION.PublicKey,
		Sig:   cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, dataToSign),
	}

	jsonResponse, err := json.Marshal(response)

	if err == nil {
		connection.WriteMessage(gws.OpcodeText, jsonResponse)
	}
}

func SignQuorumRotation(parsedRequest WsQuorumRotationRequest, connection *gws.Conn) {
	if !globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Load() {
		sendNotReady(connection)
		return
	}

	epochHandler := getEpochHandlerForLeaderFinalization(parsedRequest.EpochId)

	if epochHandler == nil {
		return
	}

	nextEpochHandler := getEpochHandlerForLeaderFinalization(parsedRequest.NextEpochId)

	if nextEpochHandler == nil {
		return
	}

	if nextEpochHandler.Hash != parsedRequest.NextEpochHash {
		return
	}

	sortedQuorum := make([]string, len(nextEpochHandler.Quorum))
	copy(sortedQuorum, nextEpochHandler.Quorum)
	sort.Strings(sortedQuorum)

	expectedSorted := make([]string, len(parsedRequest.NextQuorum))
	copy(expectedSorted, parsedRequest.NextQuorum)
	sort.Strings(expectedSorted)

	if len(sortedQuorum) != len(expectedSorted) {
		return
	}
	for i := range sortedQuorum {
		if sortedQuorum[i] != expectedSorted[i] {
			return
		}
	}

	dataToSign := constants.SigningPrefixQuorumRotation + strconv.Itoa(parsedRequest.EpochId) + ":" + strconv.Itoa(parsedRequest.NextEpochId) + ":" + parsedRequest.NextEpochHash + ":" + strings.Join(sortedQuorum, ",")

	response := WsQuorumRotationResponse{
		Voter: globals.CONFIGURATION.PublicKey,
		Sig:   cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, dataToSign),
	}

	jsonResponse, err := json.Marshal(response)

	if err == nil {
		connection.WriteMessage(gws.OpcodeText, jsonResponse)
	}
}

func snapshotAlignmentDataForHeightVoter() map[string]structures.ExecutionStats {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	data := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders

	if data == nil {
		return nil
	}

	snapshot := make(map[string]structures.ExecutionStats, len(data))

	for k, v := range data {
		snapshot[k] = v
	}

	return snapshot
}

func getBlockHashForHeightVoter(blockId string) string {
	blockRaw, err := databases.BLOCKS.Get([]byte(blockId), nil)

	if err != nil {
		return ""
	}

	var block block_pack.Block

	if err := json.Unmarshal(blockRaw, &block); err != nil {
		return ""
	}

	return block.GetHash()
}

func GetBlockWithProof(parsedRequest WsBlockWithAfpRequest, connection *gws.Conn) {
	if blockBytes, err := databases.BLOCKS.Get([]byte(parsedRequest.BlockId), nil); err == nil {
		var block block_pack.Block

		if err := json.Unmarshal(blockBytes, &block); err == nil {
			resp := WsBlockWithAfpResponse{Block: &block}

			// Now try to get AFP for block

			parts := strings.Split(parsedRequest.BlockId, ":")

			if len(parts) > 0 {
				last := parts[len(parts)-1]

				if idx, err := strconv.ParseUint(last, 10, 64); err == nil {
					parts[len(parts)-1] = strconv.FormatUint(idx+1, 10)

					nextBlockId := strings.Join(parts, ":")

					// Remark: To make sure block with index X is 100% approved we need to get the AFP for next block

					if afpBytes, err := databases.EPOCH_DATA.Get([]byte(constants.DBKeyPrefixAfp+nextBlockId), nil); err == nil {
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
