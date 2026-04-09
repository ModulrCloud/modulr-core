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

	// Information about accounts and validators
	r.GET("/account/{accountId}", routes.GetAccountById)
	r.GET("/validator/{validatorPubkey}", routes.GetValidatorByPubkey)

	// Transactions related API
	r.GET("/transaction/{hash}", routes.GetTransactionByHash)
	r.POST("/transaction", routes.AcceptTransaction)
	r.POST("/delayed_transactions_signature", routes.SignDelayedTransactions)

	// Information to help with recovery
	r.GET("/recovery/last_finalized_height", routes.GetRecoveryLastFinalizedHeight)

	// Dashboard
	r.GET("/dashboard", routes.ServeDashboard)
	r.GET("/dashboard/api/overview", routes.ServeDashboardOverview)
	r.GET("/dashboard/api/execution", routes.ServeDashboardExecutionThread)
	r.GET("/dashboard/api/approvement", routes.ServeDashboardApprovementThread)
	r.GET("/dashboard/api/leader_finalization", routes.ServeDashboardLeaderFinalization)

	// Other
	r.GET("/aggregated_anchor_epoch_ack_proof/{epochId}", routes.GetAggregatedAnchorEpochAckProof)
	r.GET("/live_stats", routes.GetLiveStats)

	return r.Handler
}

func CreateHTTPServer() {
	serverAddr := globals.CONFIGURATION.Interface + ":" + strconv.Itoa(globals.CONFIGURATION.Port)

	utils.LogWithTime(fmt.Sprintf("Server is starting at http://%s ...✅", serverAddr), utils.CYAN_COLOR)

	if err := fasthttp.ListenAndServe(serverAddr, createRouter()); err != nil {
		utils.LogWithTime(fmt.Sprintf("Error in server: %s", err), utils.RED_COLOR)
	}
}
