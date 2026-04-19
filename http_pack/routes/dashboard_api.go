package routes

import (
	"time"

	"github.com/modulrcloud/modulr-core/dashboard"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/valyala/fasthttp"
)

var dashboardStartTime = time.Now()

type DashboardOverviewResponse struct {
	PublicKey   string              `json:"publicKey"`
	NetworkId   string              `json:"networkId"`
	CoreVersion int                 `json:"coreVersion"`
	Uptime      string              `json:"uptime"`
	Anchors     []structures.Anchor `json:"anchors"`
	NodeConfig  NodeConfigSafe      `json:"nodeConfig"`
}

type NodeConfigSafe struct {
	Interface   string `json:"interface"`
	Port        int    `json:"port"`
	WsInterface string `json:"wsInterface"`
	WsPort      int    `json:"wsPort"`
	MyHostname  string `json:"myHostname"`
}

type DashboardExecutionThreadResponse struct {
	Epoch             structures.EpochDataHandler     `json:"epoch"`
	Statistics        *structures.Statistics          `json:"statistics"`
	EpochStatistics   *structures.Statistics          `json:"epochStatistics"`
	SequenceAlignment structures.AlignmentDataHandler `json:"sequenceAlignment"`
	NetworkParameters structures.NetworkParameters    `json:"networkParameters"`
}

type DashboardApprovementThreadResponse struct {
	Epoch             structures.EpochDataHandler  `json:"epoch"`
	NetworkParameters structures.NetworkParameters `json:"networkParameters"`
	CoreVersion       int                          `json:"coreVersion"`
}

func ServeDashboard(ctx *fasthttp.RequestCtx) {
	data, err := dashboard.ReadDashboardHTML()
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.WriteString("failed to load dashboard")
		return
	}
	ctx.SetContentType("text/html; charset=utf-8")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.Write(data)
}

func ServeDashboardOverview(ctx *fasthttp.RequestCtx) {
	resp := DashboardOverviewResponse{
		PublicKey:   globals.CONFIGURATION.PublicKey,
		NetworkId:   globals.GENESIS.NetworkId,
		CoreVersion: globals.CORE_MAJOR_VERSION,
		Uptime:      time.Since(dashboardStartTime).Truncate(time.Second).String(),
		Anchors:     globals.ANCHORS,
		NodeConfig: NodeConfigSafe{
			Interface:   globals.CONFIGURATION.Interface,
			Port:        globals.CONFIGURATION.Port,
			WsInterface: globals.CONFIGURATION.WebSocketInterface,
			WsPort:      globals.CONFIGURATION.WebSocketPort,
			MyHostname:  globals.CONFIGURATION.MyHostname,
		},
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, resp)
}

func ServeDashboardExecutionThread(ctx *fasthttp.RequestCtx) {
	handlers.STATE_MUTEX.RLock()
	h := handlers.EXECUTION_THREAD_METADATA.Handler
	statistics := handlers.CHAIN_CURSOR.Statistics
	networkParameters := handlers.CHAIN_CURSOR.NetworkParameters
	handlers.STATE_MUTEX.RUnlock()

	handlers.FINALIZER_THREAD_METADATA.RWMutex.RLock()
	sequenceAlignment := handlers.FINALIZER_THREAD_METADATA.Handler.SequenceAlignmentData
	handlers.FINALIZER_THREAD_METADATA.RWMutex.RUnlock()

	resp := DashboardExecutionThreadResponse{
		Epoch:             h.EpochDataHandler,
		Statistics:        statistics,
		EpochStatistics:   h.EpochStatistics,
		SequenceAlignment: sequenceAlignment,
		NetworkParameters: networkParameters,
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, resp)
}

func ServeDashboardApprovementThread(ctx *fasthttp.RequestCtx) {
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	h := handlers.APPROVEMENT_THREAD_METADATA.Handler
	resp := DashboardApprovementThreadResponse{
		Epoch:             h.EpochDataHandler,
		NetworkParameters: h.NetworkParameters,
		CoreVersion:       h.CoreMajorVersion,
	}
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	helpers.WriteJSON(ctx, fasthttp.StatusOK, resp)
}

func ServeDashboardLeaderFinalization(ctx *fasthttp.RequestCtx) {
	helpers.WriteJSON(ctx, fasthttp.StatusOK, dashboard.GetLeaderFinalizationState())
}
