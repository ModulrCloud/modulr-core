package routes

import (
	"encoding/json"
	"strconv"

	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/valyala/fasthttp"
)

func GetEpochData(ctx *fasthttp.RequestCtx) {

	epochIndexVal := ctx.UserValue("epochIndex")
	epochIndex, ok := epochIndexVal.(string)

	if !ok {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid epoch index")
		return
	}

	// EPOCH_HANDLER snapshots are stored in APPROVEMENT_THREAD_METADATA DB
	// to be committed atomically with AT updates.
	epochId, err := strconv.Atoi(epochIndex)
	if err == nil {
		if snapshot := utils.GetEpochSnapshot(epochId); snapshot != nil {
			if value, err := json.Marshal(snapshot); err == nil {
				helpers.WriteJSONBytes(ctx, fasthttp.StatusOK, value)
				return
			}
		}
	}

	helpers.WriteErr(ctx, fasthttp.StatusNotFound, "No epoch data found")
}
