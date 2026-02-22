package routes

import (
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"

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
	value, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte("EPOCH_HANDLER:"+epochIndex), nil)

	if err == nil && value != nil {
		helpers.WriteJSONBytes(ctx, fasthttp.StatusOK, value)
		return
	}

	helpers.WriteErr(ctx, fasthttp.StatusNotFound, "No epoch data found")
}
