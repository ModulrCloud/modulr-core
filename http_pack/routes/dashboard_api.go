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

type dashboardOverviewResponse struct {
	PublicKey   string              `json:"publicKey"`
	NetworkId   string              `json:"networkId"`
	CoreVersion int                 `json:"coreVersion"`
	Uptime      string              `json:"uptime"`
	Anchors     []structures.Anchor `json:"anchors"`
	NodeConfig  nodeConfigSafe      `json:"nodeConfig"`
}

type nodeConfigSafe struct {
	Interface   string `json:"interface"`
	Port        int    `json:"port"`
	WsInterface string `json:"wsInterface"`
	WsPort      int    `json:"wsPort"`
	MyHostname  string `json:"myHostname"`
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
	resp := dashboardOverviewResponse{
		PublicKey:   globals.CONFIGURATION.PublicKey,
		NetworkId:   globals.GENESIS.NetworkId,
		CoreVersion: globals.CORE_MAJOR_VERSION,
		Uptime:      time.Since(dashboardStartTime).Truncate(time.Second).String(),
		Anchors:     globals.ANCHORS,
		NodeConfig: nodeConfigSafe{
			Interface:   globals.CONFIGURATION.Interface,
			Port:        globals.CONFIGURATION.Port,
			WsInterface: globals.CONFIGURATION.WebSocketInterface,
			WsPort:      globals.CONFIGURATION.WebSocketPort,
			MyHostname:  globals.CONFIGURATION.MyHostname,
		},
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, resp)
}

type dashboardExecutionThreadResponse struct {
	Epoch             structures.EpochDataHandler          `json:"epoch"`
	Statistics        *structures.Statistics                `json:"statistics"`
	EpochStatistics   *structures.Statistics                `json:"epochStatistics"`
	ExecutionData     map[string]structures.ExecutionStats  `json:"executionData"`
	SequenceAlignment structures.AlignmentDataHandler       `json:"sequenceAlignment"`
	NetworkParameters structures.NetworkParameters          `json:"networkParameters"`
}

func ServeDashboardExecutionThread(ctx *fasthttp.RequestCtx) {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	h := handlers.EXECUTION_THREAD_METADATA.Handler
	resp := dashboardExecutionThreadResponse{
		Epoch:             h.EpochDataHandler,
		Statistics:        h.Statistics,
		EpochStatistics:   h.EpochStatistics,
		ExecutionData:     h.ExecutionData,
		SequenceAlignment: h.SequenceAlignmentData,
		NetworkParameters: h.NetworkParameters,
	}
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	helpers.WriteJSON(ctx, fasthttp.StatusOK, resp)
}

type dashboardApprovementThreadResponse struct {
	Epoch             structures.EpochDataHandler  `json:"epoch"`
	NetworkParameters structures.NetworkParameters `json:"networkParameters"`
	CoreVersion       int                         `json:"coreVersion"`
}

func ServeDashboardApprovementThread(ctx *fasthttp.RequestCtx) {
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	h := handlers.APPROVEMENT_THREAD_METADATA.Handler
	resp := dashboardApprovementThreadResponse{
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

