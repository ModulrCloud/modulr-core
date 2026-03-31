package routes

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
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

	attestation := loadLatestVerifiedHeightAttestation(lastHeight)
	if attestation == nil {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "No height attestation found")
		return
	}

	payload := RecoveryLastFinalizedHeightPayload{
		LastHeight: attestation.AbsoluteHeight,
		BlockId:    attestation.BlockId,
		BlockHash:  attestation.BlockHash,
		EpochId:    attestation.EpochId,
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

func loadLatestVerifiedHeightAttestation(startHeight int) *utils.HeightAttestationInfo {
	for h := startHeight; h >= 0 && h > startHeight-10; h-- {
		info := utils.LoadHeightAttestationInfo(h)
		if info != nil {
			return info
		}
	}
	return nil
}
