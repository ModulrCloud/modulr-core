package http_pack

import (
	"fmt"
	"strconv"

	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/http_pack/routes"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

func createRouter() fasthttp.RequestHandler {
	r := router.New()

	// Information about blocks, heights, etc.
	r.GET("/block/{id}", routes.GetBlockById)
	r.GET("/height/{absoluteHeightIndex}", routes.GetBlockByHeight)
	r.GET("/last_height", routes.GetLastHeight)
	r.GET("/aggregated_finalization_proof/{blockId}", routes.GetAggregatedFinalizationProof)
	r.GET("/aggregated_height_proof/{height}", routes.GetAggregatedHeightProof)
	r.GET("/first_block_in_epoch/{epochId}", routes.GetFirstBlockInEpoch)

	// Information about epoch
	r.GET("/epoch_data/{epochIndex}", routes.GetEpochData)
	r.GET("/epoch_stats", routes.GetCurrentEpochStats)
	r.GET("/epoch_stats/{epochIndex}", routes.GetEpochStatsByEpochIndex)
	r.GET("/aggregated_epoch_rotation_proof/{epochId}", routes.GetAggregatedEpochRotationProof)

	// Information about statistics
	r.GET("/live_stats", routes.GetLiveStats)

	// Information about accounts and validators
	r.GET("/account/{accountId}", routes.GetAccountById)
	r.GET("/validator/{validatorPubkey}", routes.GetValidatorByPubkey)
	r.GET("/get_validator_ws_endpoints", routes.GetValidatorWsEndpoints)
	r.GET("/get_validator_endpoints", routes.GetValidatorEndpoints)

	// Transactions related API
	r.GET("/transaction/{hash}", routes.GetTransactionByHash)
	r.POST("/transaction", routes.AcceptTransaction)
	r.POST("/delayed_transactions_signature", routes.SignDelayedTransactions)

	// Information to help with recovery
	r.GET("/recovery/last_finalized_height", routes.GetRecoveryLastFinalizedHeight)

	// Other
	r.GET("/aggregated_anchor_epoch_ack_proof/{epochId}", routes.GetAggregatedAnchorEpochAckProof)

	return r.Handler
}

func createRecoveryRouter() fasthttp.RequestHandler {
	r := router.New()

	// Recovery/read-only routes. No transaction acceptance, delayed-tx signing,
	// websocket consensus routes, or background consensus threads are available
	// when RECOVERY_MODE is enabled.
	r.GET("/recovery/last_finalized_height", routes.GetRecoveryLastFinalizedHeight)
	r.GET("/get_validator_endpoints", routes.GetValidatorEndpoints)
	r.GET("/get_validator_ws_endpoints", routes.GetValidatorWsEndpoints)

	return r.Handler
}

func listen(handler fasthttp.RequestHandler, mode string) {
	serverAddr := globals.CONFIGURATION.Interface + ":" + strconv.Itoa(globals.CONFIGURATION.Port)

	utils.LogWithTime(fmt.Sprintf("%s server is starting at http://%s ...✅", mode, serverAddr), utils.CYAN_COLOR)

	if err := fasthttp.ListenAndServe(serverAddr, handler); err != nil {
		utils.LogWithTime(fmt.Sprintf("Error in server: %s", err), utils.RED_COLOR)
	}
}

func CreateHTTPServer() {
	listen(createRouter(), "Server")
}

func CreateRecoveryHTTPServer() {
	listen(createRecoveryRouter(), "Recovery")
}
