package routes

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/syndtr/goleveldb/leveldb"

	"github.com/valyala/fasthttp"
)

type EpochStatsResponse struct {
	EpochId    int                    `json:"epochId"`
	Statistics *structures.Statistics `json:"statistics"`
}

func GetEpochData(ctx *fasthttp.RequestCtx) {
	epochIndexVal := ctx.UserValue("epochIndex")
	epochIndex, ok := epochIndexVal.(string)

	if !ok {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid epoch index")
		return
	}

	epochId, err := strconv.Atoi(epochIndex)
	if err == nil {
		if snapshot := utils.GetEpochSnapshot(epochId); snapshot != nil {
			helpers.WriteJSON(ctx, fasthttp.StatusOK, snapshot)
			return
		}
	}

	helpers.WriteErr(ctx, fasthttp.StatusNotFound, "No epoch data found")
}

func GetAggregatedEpochRotationProof(ctx *fasthttp.RequestCtx) {
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

	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedEpochRotationProof, epochId))
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	if err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
		return
	}

	var proof structures.AggregatedEpochRotationProof
	if json.Unmarshal(raw, &proof) != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to parse proof")
		return
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, proof)
}

func GetCurrentEpochStats(ctx *fasthttp.RequestCtx) {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	epochId := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
	stats := handlers.EXECUTION_THREAD_METADATA.Handler.EpochStatistics
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if stats == nil {
		stats = &structures.Statistics{LastHeight: -1}
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, EpochStatsResponse{EpochId: epochId, Statistics: stats})
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
		helpers.WriteJSON(ctx, fasthttp.StatusOK, EpochStatsResponse{EpochId: epochIndex, Statistics: currentStats})
		return
	}

	key := []byte(fmt.Sprintf(constants.DBKeyPrefixEpochStats+"%d", epochIndex))
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

	helpers.WriteJSON(ctx, fasthttp.StatusOK, EpochStatsResponse{EpochId: epochIndex, Statistics: &st})
}
