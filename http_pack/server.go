package http_pack

import (
	"fmt"
	"strconv"

	"github.com/modulrcloud/modulr-core/dashboard"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/http_pack/routes"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

func createRouter() fasthttp.RequestHandler {

	r := router.New()

	r.GET("/block/{id}", routes.GetBlockById)
	r.GET("/height/{absoluteHeightIndex}", routes.GetBlockByHeight)
	r.GET("/last_height", routes.GetLastHeight)
	r.GET("/live_stats", routes.GetLiveStats)
	r.GET("/epoch_stats", routes.GetCurrentEpochStats)
	r.GET("/epoch_stats/{epochIndex}", routes.GetEpochStatsByEpochIndex)

	r.GET("/account/{accountId}", routes.GetAccountById)
	r.GET("/validator/{validatorPubkey}", routes.GetValidatorByPubkey)

	r.GET("/epoch_data/{epochIndex}", routes.GetEpochData)

	r.GET("/aggregated_finalization_proof/{blockId}", routes.GetAggregatedFinalizationProof)

	r.GET("/transaction/{hash}", routes.GetTransactionByHash)
	r.POST("/transaction", routes.AcceptTransaction)
	r.POST("/delayed_transactions_signature", routes.SignDelayedTransactions)

	// Dashboard
	r.GET("/dashboard", dashboard.ServeDashboard)
	r.GET("/dashboard/api/overview", dashboard.ServeOverview)
	r.GET("/dashboard/api/execution", dashboard.ServeExecutionThread)
	r.GET("/dashboard/api/approvement", dashboard.ServeApprovementThread)
	r.GET("/dashboard/api/generation", dashboard.ServeBlockGeneration)
	r.GET("/dashboard/api/leader_finalization", dashboard.ServeLeaderFinalization)
	r.GET("/dashboard/api/alfp_watcher", dashboard.ServeAlfpWatcher)

	return r.Handler
}

func CreateHTTPServer() {

	serverAddr := globals.CONFIGURATION.Interface + ":" + strconv.Itoa(globals.CONFIGURATION.Port)

	utils.LogWithTime(fmt.Sprintf("Server is starting at http://%s ...✅", serverAddr), utils.CYAN_COLOR)

	if err := fasthttp.ListenAndServe(serverAddr, createRouter()); err != nil {
		utils.LogWithTime(fmt.Sprintf("Error in server: %s", err), utils.RED_COLOR)
	}
}
