package routes

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/valyala/fasthttp"
)

type RecoveryLastFinalizedHeightPayload struct {
	LastHeight int    `json:"lastHeight"`
	BlockId    string `json:"blockId"`
	BlockHash  string `json:"blockHash"`
	EpochId    int    `json:"epochId"`
}

type RecoverySignedResponse struct {
	PubKey    string          `json:"pubKey"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

func GetRecoveryLastFinalizedHeight(ctx *fasthttp.RequestCtx) {
	tracker := utils.LoadLastMileSequenceState(constants.DBKeyLastMileFinalizerTracker)
	if tracker == nil || tracker.NextHeight <= 0 {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "No finalized height data available")
		return
	}

	lastHeight := int(tracker.NextHeight - 1)

	var proofInfo *structures.AggregatedHeightProofInfo
	for h := lastHeight; h >= 0 && h > lastHeight-10; h-- {
		if info := utils.LoadAggregatedHeightProofInfo(h); info != nil {
			proofInfo = info
			break
		}
	}

	if proofInfo == nil {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "No aggregated height proof found")
		return
	}

	payload := RecoveryLastFinalizedHeightPayload{
		LastHeight: proofInfo.AbsoluteHeight,
		BlockId:    proofInfo.BlockId,
		BlockHash:  proofInfo.BlockHash,
		EpochId:    proofInfo.EpochId,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to marshal payload")
		return
	}

	sig := cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, string(payloadBytes))

	resp := RecoverySignedResponse{
		PubKey:    globals.CONFIGURATION.PublicKey,
		Payload:   payloadBytes,
		Signature: sig,
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, resp)
}
