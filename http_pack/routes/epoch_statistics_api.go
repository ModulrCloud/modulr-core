package routes

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/valyala/fasthttp"
)

type epochStatsResponse struct {
	EpochId    int                    `json:"epochId"`
	Statistics *structures.Statistics `json:"statistics"`
}

func GetCurrentEpochStats(ctx *fasthttp.RequestCtx) {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	epochId := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
	stats := handlers.EXECUTION_THREAD_METADATA.Handler.EpochStatistics
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if stats == nil {
		stats = &structures.Statistics{LastHeight: -1}
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, epochStatsResponse{EpochId: epochId, Statistics: stats})
}

func GetEpochStatsByEpochIndex(ctx *fasthttp.RequestCtx) {
	epochIndexVal := ctx.UserValue("epochIndex")
	epochIndexStr, ok := epochIndexVal.(string)
	if !ok || epochIndexStr == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid epoch index")
		return
	}

	epochIndex, err := strconv.Atoi(epochIndexStr)
	if err != nil || epochIndex < 0 {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid epoch index")
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
		helpers.WriteJSON(ctx, fasthttp.StatusOK, epochStatsResponse{EpochId: epochIndex, Statistics: currentStats})
		return
	}

	key := []byte(fmt.Sprintf("EPOCH_STATS:%d", epochIndex))
	value, derr := databases.STATE.Get(key, nil)
	if derr != nil {
		if derr == leveldb.ErrNotFound {
			helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
			return
		}
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to load epoch statistics")
		return
	}

	// Value is already JSON for structures.Statistics. Wrap it with epochId for UX.
	var st structures.Statistics
	if uerr := json.Unmarshal(value, &st); uerr != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to parse epoch statistics")
		return
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, epochStatsResponse{EpochId: epochIndex, Statistics: &st})
}
