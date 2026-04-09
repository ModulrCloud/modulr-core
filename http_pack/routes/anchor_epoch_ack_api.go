package routes

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/valyala/fasthttp"
)

func GetAggregatedAnchorEpochAckProof(ctx *fasthttp.RequestCtx) {
	epochIdRaw := ctx.UserValue("epochId")
	epochIdStr, ok := epochIdRaw.(string)

	if !ok || epochIdStr == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid epochId")
		return
	}

	epochId, err := strconv.Atoi(epochIdStr)
	if err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid epochId")
		return
	}

	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedAnchorEpochAckProof, epochId))
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	if err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
		return
	}

	var proof structures.AggregatedAnchorEpochAckProof
	if json.Unmarshal(raw, &proof) != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to parse proof")
		return
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, proof)
}
