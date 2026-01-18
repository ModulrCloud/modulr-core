package routes

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/valyala/fasthttp"
)

type epochStatsResponse struct {
	EpochId    int                    `json:"epochId"`
	Statistics *structures.Statistics `json:"statistics"`
}

func GetCurrentEpochStats(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	epochId := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
	stats := handlers.EXECUTION_THREAD_METADATA.Handler.EpochStatistics
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if stats == nil {
		stats = &structures.Statistics{LastHeight: -1}
	}

	raw, err := json.Marshal(epochStatsResponse{EpochId: epochId, Statistics: stats})
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err":"Failed to marshal response"}`))
		return
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Write(raw)
}

func GetEpochStatsByEpochIndex(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	epochIndexVal := ctx.UserValue("epochIndex")
	epochIndexStr, ok := epochIndexVal.(string)
	if !ok || epochIndexStr == "" {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err":"Invalid epoch index"}`))
		return
	}

	epochIndex, err := strconv.Atoi(epochIndexStr)
	if err != nil || epochIndex < 0 {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err":"Invalid epoch index"}`))
		return
	}

	// If requested epoch is the current one, serve from memory (live, not yet snapshotted).
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	currentEpochId := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
	currentStats := handlers.EXECUTION_THREAD_METADATA.Handler.EpochStatistics
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if epochIndex == currentEpochId {
		if currentStats == nil {
			currentStats = &structures.Statistics{LastHeight: -1}
		}
		out, merr := json.Marshal(epochStatsResponse{EpochId: epochIndex, Statistics: currentStats})
		if merr != nil {
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err":"Failed to marshal response"}`))
			return
		}
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json")
		ctx.Write(out)
		return
	}

	key := []byte(fmt.Sprintf("EPOCH_STATS:%d", epochIndex))
	value, derr := databases.STATE.Get(key, nil)
	if derr != nil {
		if derr == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err":"Not found"}`))
			return
		}
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err":"Failed to load epoch statistics"}`))
		return
	}

	// Value is already JSON for structures.Statistics. Wrap it with epochId for UX.
	var st structures.Statistics
	if uerr := json.Unmarshal(value, &st); uerr != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err":"Failed to parse epoch statistics"}`))
		return
	}

	out, merr := json.Marshal(epochStatsResponse{EpochId: epochIndex, Statistics: &st})
	if merr != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err":"Failed to marshal response"}`))
		return
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Write(out)
}
