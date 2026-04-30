package websocket_pack

import (
	"encoding/json"
	"fmt"
	"net/http"
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

	"github.com/lxzan/gws"
)

var httpClient = &http.Client{Timeout: 2 * time.Second}

// Only one block creator can request proof for block at a choosen period of time T
var BLOCK_CREATOR_REQUEST_MUTEX = sync.Mutex{}

var (
	anchorEpochAckFetchInFlight sync.Map
	anchorEpochAckLastAttempt   sync.Map
)

// anchorEpochAckThrottleWindow is how long a recorded attempt in
// anchorEpochAckLastAttempt is still considered "recent" for throttling purposes.
// Anything older is pure dead-weight and is swept opportunistically on each
// scheduleAnchorEpochAckFetch call.
const anchorEpochAckThrottleWindow = 2 * time.Second

func sendNotReady(connection *gws.Conn) {
	connection.WriteMessage(gws.OpcodeText, []byte(`{"status":"NOT_READY"}`))
}

func logHeightProofReturn(reason string, request WsHeightProofRequest, details string) {
	utils.LogWithTimeThrottled(
		fmt.Sprintf("sign_height_proof:return:%s:%d:%s", reason, request.AbsoluteHeight, request.BlockId),
		2*time.Second,
		fmt.Sprintf(
			"SignHeightProof return [%s] height=%d epoch=%d heightInEpoch=%d blockId=%s hash=%s details=%s",
			reason,
			request.AbsoluteHeight,
			request.EpochId,
			request.HeightInEpoch,
			request.BlockId,
			utils.ShortHash(request.BlockHash),
			details,
		),
		utils.YELLOW_COLOR,
	)
}

func logEpochRotationProofReturn(reason string, request WsEpochRotationProofRequest, details string) {
	utils.LogWithTimeThrottled(
		fmt.Sprintf("sign_epoch_rotation_proof:return:%s:%d:%d", reason, request.EpochId, request.NextEpochId),
		2*time.Second,
		fmt.Sprintf(
			"SignEpochRotationProof return [%s] epoch=%d nextEpoch=%d epochDataHash=%s finishedOnHeight=%d finishedOnBlockId=%s finishedOnHash=%s details=%s",
			reason,
			request.EpochId,
			request.NextEpochId,
			utils.ShortHash(request.EpochDataHash),
			request.FinishedOnHeight,
			request.FinishedOnBlockId,
			utils.ShortHash(request.FinishedOnHash),
			details,
		),
		utils.YELLOW_COLOR,
	)
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

		localVotingDataRaw, err := databases.FINALIZATION_THREAD_METADATA.Get([]byte(strconv.Itoa(epochIndex)+":"+parsedRequest.Block.Creator), nil)

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

								err := databases.FINALIZATION_THREAD_METADATA.Put([]byte(strconv.Itoa(epochIndex)+":"+parsedRequest.Block.Creator), votingStatBytes, nil)

								if err == nil {
									// Only after we stored the these 3 components = generate signature (finalization proof)

									dataToSign, prevBlockHash := "", ""

									if parsedRequest.Block.Index == 0 {
										prevBlockHash = constants.ZeroHash
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

	// Defense-in-depth for the last leader of an active epoch.
	//
	// For non-last leaders we already know they're done because CurrentLeaderIndex
	// has moved past them (the leaderTimeIsOut watchdog rotated within the epoch).
	// The last leader has no such intra-epoch indicator — CurrentLeaderIndex stays
	// at lastLeaderIdx until the whole epoch rotates. Without this gate, a misbehaving
	// or buggy peer (e.g. an anchor scheduling proactive ALFP collection too early)
	// could ask us to sign a finalization for the last leader while it is still
	// actively producing blocks, locking in a low VotingStat.Index and stalling
	// the anchor's last-mile sequencing.
	//
	// Refuse unless one of these "last leader is genuinely done" signals holds:
	//   1. Time-based: epoch is no longer fresh, i.e. now >= StartTimestamp + EpochDuration.
	//      Past this boundary the last leader has no right to produce more blocks.
	//   2. Event-based: a local EPOCH_FINISH:N=TRUE signal has been raised, meaning
	//      this node has already accepted that the epoch is rotating.
	if !isRequestForPastEpoch && isLastLeader {
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
		stillFresh := utils.EpochStillFresh(&handlers.APPROVEMENT_THREAD_METADATA.Handler)
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		if stillFresh && !utils.SignalAboutEpochRotationExists(epochHandler.Id) {
			sendNotReady(connection)
			return
		}
	}

	if !isRequestForPastEpoch && epochHandler.CurrentLeaderIndex <= parsedRequest.IndexOfLeaderToFinalize && !isLastLeader {
		sendNotReady(connection)
		return
	}

	epochIndex := epochHandler.Id
	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochIndex)

	leaderToFinalize := epochHandler.LeadersSequence[parsedRequest.IndexOfLeaderToFinalize]

	if isRequestForPastEpoch || epochHandler.CurrentLeaderIndex > parsedRequest.IndexOfLeaderToFinalize || isLastLeader {
		localVotingData := structures.NewLeaderVotingStatTemplate()

		localVotingDataRaw, err := databases.FINALIZATION_THREAD_METADATA.Get([]byte(strconv.Itoa(epochIndex)+":"+leaderToFinalize), nil)

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

				dataToSignForLeaderFinalization := strings.Join([]string{
					constants.SigningPrefixLeaderFinalization,
					leaderToFinalize,
					strconv.Itoa(propSkipData.Index),
					propSkipData.Hash,
					epochFullID,
				}, ":")

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

func AcceptAggregatedAnchorEpochAckProof(parsedRequest WsAcceptAggregatedAnchorEpochAckProofRequest, connection *gws.Conn) {
	proof := &parsedRequest.Proof
	if !utils.VerifyAggregatedAnchorEpochAckProof(proof) {
		sendNotReady(connection)
		return
	}

	utils.StoreAggregatedAnchorEpochAckProof(proof)
	connection.WriteMessage(gws.OpcodeText, []byte(`{"status":"OK"}`))
}

func isAnchorEpochAckAvailable(epochId int) bool {
	key := []byte(constants.DBKeyPrefixAggregatedAnchorEpochAckProof + strconv.Itoa(epochId))
	raw, err := databases.FINALIZATION_THREAD_METADATA.Get(key, nil)
	if err == nil && len(raw) > 0 {
		var proof structures.AggregatedAnchorEpochAckProof
		if json.Unmarshal(raw, &proof) == nil && utils.VerifyAggregatedAnchorEpochAckProof(&proof) {
			return true
		}
	}

	return false
}

func scheduleAnchorEpochAckFetch(epochId int) {
	if _, inFlight := anchorEpochAckFetchInFlight.LoadOrStore(epochId, struct{}{}); inFlight {
		return
	}

	now := time.Now()

	// Opportunistic sweep: any lastAttempt entry older than the throttle window
	// is no longer throttling anything and would otherwise live forever (one
	// leaked entry per epoch ID). This keeps the map bounded to "recently
	// attempted" epochs without a separate cleanup goroutine.
	anchorEpochAckLastAttempt.Range(func(key, value any) bool {
		if ts, ok := value.(time.Time); ok && now.Sub(ts) > anchorEpochAckThrottleWindow {
			anchorEpochAckLastAttempt.Delete(key)
		}
		return true
	})

	if lastAttempt, ok := anchorEpochAckLastAttempt.Load(epochId); ok {
		if now.Sub(lastAttempt.(time.Time)) < anchorEpochAckThrottleWindow {
			anchorEpochAckFetchInFlight.Delete(epochId)
			return
		}
	}

	anchorEpochAckLastAttempt.Store(epochId, now)

	go func() {
		defer anchorEpochAckFetchInFlight.Delete(epochId)

		if proof := tryPullAnchorEpochAck(epochId); proof != nil {
			utils.StoreAggregatedAnchorEpochAckProof(proof)
		}
	}()
}

func tryPullAnchorEpochAck(epochId int) *structures.AggregatedAnchorEpochAckProof {
	if proof := GetAggregatedAnchorEpochAckProofFromPoD(epochId); proof != nil && utils.VerifyAggregatedAnchorEpochAckProof(proof) {
		return proof
	}

	if proof := fetchAnchorEpochAckFromHTTP(epochId); proof != nil && utils.VerifyAggregatedAnchorEpochAckProof(proof) {
		return proof
	}

	return nil
}

func fetchAnchorEpochAckFromHTTP(epochId int) *structures.AggregatedAnchorEpochAckProof {
	epochHandler := getEpochHandlerForLeaderFinalization(epochId)

	var urls []string
	if epochHandler != nil {
		for _, member := range utils.GetQuorumUrlsAndPubkeys(epochHandler) {
			if member.Url != globals.CONFIGURATION.MyHostname {
				urls = append(urls, member.Url)
			}
		}
	}
	urls = append(urls, globals.CONFIGURATION.BootstrapNodes...)

	for _, endpoint := range urls {
		if endpoint == globals.CONFIGURATION.MyHostname {
			continue
		}
		resp, err := httpClient.Get(endpoint + "/aggregated_anchor_epoch_ack_proof/" + strconv.Itoa(epochId))
		if err != nil {
			continue
		}
		var proof structures.AggregatedAnchorEpochAckProof
		if json.NewDecoder(resp.Body).Decode(&proof) == nil {
			resp.Body.Close()
			return &proof
		}
		resp.Body.Close()
	}
	return nil
}

func GetHeightProof(parsedRequest WsHeightProofRequest, connection *gws.Conn) {
	if !globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Load() {
		logHeightProofReturn("flood_prevention_disabled", parsedRequest, "route is temporarily blocked")
		sendNotReady(connection)
		return
	}

	if parsedRequest.EpochId > 0 && !isAnchorEpochAckAvailable(parsedRequest.EpochId) {
		scheduleAnchorEpochAckFetch(parsedRequest.EpochId)
		logHeightProofReturn("anchor_epoch_ack_missing", parsedRequest, fmt.Sprintf("ack for epoch %d is not available yet", parsedRequest.EpochId))
		sendNotReady(connection)
		return
	}

	requestedHeight := int64(parsedRequest.AbsoluteHeight)

	// Look up the pre-computed mapping written by LastMileFinalizerThread
	expectedBlockId := utils.LoadHeightBlockIdMapping(requestedHeight)
	if expectedBlockId == "" || expectedBlockId != parsedRequest.BlockId {
		logHeightProofReturn(
			"expected_block_id_mismatch",
			parsedRequest,
			fmt.Sprintf("expectedBlockId=%s requestedBlockId=%s", expectedBlockId, parsedRequest.BlockId),
		)
		sendNotReady(connection)
		return
	}

	expectedHeightInEpoch, ok := utils.LoadHeightInEpochMapping(requestedHeight)
	if !ok || expectedHeightInEpoch != parsedRequest.HeightInEpoch {
		logHeightProofReturn(
			"height_in_epoch_mismatch",
			parsedRequest,
			fmt.Sprintf("mappingFound=%t expectedHeightInEpoch=%d requestedHeightInEpoch=%d", ok, expectedHeightInEpoch, parsedRequest.HeightInEpoch),
		)
		sendNotReady(connection)
		return
	}

	blockHash := getBlockHashForHeightVoter(parsedRequest.BlockId)
	if blockHash == "" || blockHash != parsedRequest.BlockHash {
		logHeightProofReturn(
			"block_hash_mismatch",
			parsedRequest,
			fmt.Sprintf("localBlockHash=%s requestedBlockHash=%s", utils.ShortHash(blockHash), utils.ShortHash(parsedRequest.BlockHash)),
		)
		sendNotReady(connection)
		return
	}

	dataToSign := strings.Join([]string{
		constants.SigningPrefixHeightProof,
		strconv.Itoa(parsedRequest.AbsoluteHeight),
		parsedRequest.BlockId,
		parsedRequest.BlockHash,
		strconv.Itoa(parsedRequest.EpochId),
		strconv.Itoa(parsedRequest.HeightInEpoch),
	}, ":")

	response := WsHeightProofResponse{
		Voter: globals.CONFIGURATION.PublicKey,
		Sig:   cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, dataToSign),
	}

	jsonResponse, err := json.Marshal(response)
	if err == nil {
		connection.WriteMessage(gws.OpcodeText, jsonResponse)
		return
	}

	logHeightProofReturn("marshal_response_failed", parsedRequest, err.Error())
}

func GetEpochRotationProof(parsedRequest WsEpochRotationProofRequest, connection *gws.Conn) {
	if !globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Load() {
		logEpochRotationProofReturn("flood_prevention_disabled", parsedRequest, "route is temporarily blocked")
		sendNotReady(connection)
		return
	}

	epochHandler := getEpochHandlerForLeaderFinalization(parsedRequest.EpochId)
	if epochHandler == nil {
		logEpochRotationProofReturn("epoch_handler_missing", parsedRequest, fmt.Sprintf("no epoch handler snapshot for epoch %d", parsedRequest.EpochId))
		sendNotReady(connection)
		return
	}

	if !utils.HasLocallySequencedFullEpoch(parsedRequest.EpochId) {
		logEpochRotationProofReturn(
			"local_last_mile_epoch_not_completed",
			parsedRequest,
			fmt.Sprintf("local absolute height mapping for epoch %d is not finished yet", parsedRequest.EpochId),
		)
		sendNotReady(connection)
		return
	}

	localBoundary := utils.LoadLastMileEpochBoundary(parsedRequest.EpochId)
	if localBoundary == nil {
		logEpochRotationProofReturn(
			"local_epoch_boundary_missing",
			parsedRequest,
			fmt.Sprintf("missing durable last-mile boundary for epoch %d", parsedRequest.EpochId),
		)
		sendNotReady(connection)
		return
	}

	if localBoundary.FinishedOnHeight != parsedRequest.FinishedOnHeight ||
		localBoundary.FinishedOnBlockId != parsedRequest.FinishedOnBlockId ||
		localBoundary.FinishedOnHash != parsedRequest.FinishedOnHash {
		logEpochRotationProofReturn(
			"epoch_boundary_mismatch",
			parsedRequest,
			fmt.Sprintf(
				"localHeight=%d requestedHeight=%d localBlockId=%s requestedBlockId=%s localHash=%s requestedHash=%s",
				localBoundary.FinishedOnHeight,
				parsedRequest.FinishedOnHeight,
				localBoundary.FinishedOnBlockId,
				parsedRequest.FinishedOnBlockId,
				utils.ShortHash(localBoundary.FinishedOnHash),
				utils.ShortHash(parsedRequest.FinishedOnHash),
			),
		)
		sendNotReady(connection)
		return
	}

	localEpochData := utils.LoadNextEpochData(parsedRequest.NextEpochId)
	if localEpochData == nil {
		logEpochRotationProofReturn("next_epoch_data_missing", parsedRequest, fmt.Sprintf("local next epoch data for epoch %d not found", parsedRequest.NextEpochId))
		sendNotReady(connection)
		return
	}

	localHash := utils.ComputeEpochDataHash(localEpochData)
	if localHash != parsedRequest.EpochDataHash {
		logEpochRotationProofReturn(
			"epoch_data_hash_mismatch",
			parsedRequest,
			fmt.Sprintf("localHash=%s requestedHash=%s", utils.ShortHash(localHash), utils.ShortHash(parsedRequest.EpochDataHash)),
		)
		sendNotReady(connection)
		return
	}

	dataToSign := utils.BuildEpochRotationProofSigningPayload(
		parsedRequest.EpochId,
		parsedRequest.NextEpochId,
		parsedRequest.EpochDataHash,
		parsedRequest.FinishedOnHeight,
		parsedRequest.FinishedOnBlockId,
		parsedRequest.FinishedOnHash,
	)

	response := WsEpochRotationProofResponse{
		Voter: globals.CONFIGURATION.PublicKey,
		Sig:   cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, dataToSign),
	}

	jsonResponse, err := json.Marshal(response)
	if err == nil {
		connection.WriteMessage(gws.OpcodeText, jsonResponse)
		return
	}

	logEpochRotationProofReturn("marshal_response_failed", parsedRequest, err.Error())
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

func GetBlockWithAfp(parsedRequest WsBlockWithAfpRequest, connection *gws.Conn) {
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
