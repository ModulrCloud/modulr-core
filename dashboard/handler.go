package dashboard

import (
	"embed"
	"encoding/json"
	"time"

	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/threads"

	"github.com/valyala/fasthttp"
)

//go:embed dashboard.html
var dashboardFS embed.FS

var startTime = time.Now()

func ServeDashboard(ctx *fasthttp.RequestCtx) {
	data, err := dashboardFS.ReadFile("dashboard.html")
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.WriteString("failed to load dashboard")
		return
	}
	ctx.SetContentType("text/html; charset=utf-8")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.Write(data)
}

type OverviewResponse struct {
	PublicKey    string               `json:"publicKey"`
	NetworkId   string               `json:"networkId"`
	CoreVersion int                  `json:"coreVersion"`
	Uptime      string               `json:"uptime"`
	Anchors     []structures.Anchor  `json:"anchors"`
	NodeConfig  NodeConfigSafe       `json:"nodeConfig"`
}

type NodeConfigSafe struct {
	Interface     string `json:"interface"`
	Port          int    `json:"port"`
	WsInterface   string `json:"wsInterface"`
	WsPort        int    `json:"wsPort"`
	MyHostname    string `json:"myHostname"`
}

func ServeOverview(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")
	ctx.SetContentType("application/json")

	resp := OverviewResponse{
		PublicKey:    globals.CONFIGURATION.PublicKey,
		NetworkId:    globals.GENESIS.NetworkId,
		CoreVersion:  globals.CORE_MAJOR_VERSION,
		Uptime:       time.Since(startTime).Truncate(time.Second).String(),
		Anchors:      globals.ANCHORS,
		NodeConfig: NodeConfigSafe{
			Interface:   globals.CONFIGURATION.Interface,
			Port:        globals.CONFIGURATION.Port,
			WsInterface: globals.CONFIGURATION.WebSocketInterface,
			WsPort:      globals.CONFIGURATION.WebSocketPort,
			MyHostname:  globals.CONFIGURATION.MyHostname,
		},
	}

	writeJSON(ctx, resp)
}

type ExecutionThreadResponse struct {
	Epoch              structures.EpochDataHandler          `json:"epoch"`
	Statistics         *structures.Statistics                `json:"statistics"`
	EpochStatistics    *structures.Statistics                `json:"epochStatistics"`
	ExecutionData      map[string]structures.ExecutionStats  `json:"executionData"`
	SequenceAlignment  structures.AlignmentDataHandler       `json:"sequenceAlignment"`
	NetworkParameters  structures.NetworkParameters          `json:"networkParameters"`
}

func ServeExecutionThread(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")
	ctx.SetContentType("application/json")

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	h := handlers.EXECUTION_THREAD_METADATA.Handler
	resp := ExecutionThreadResponse{
		Epoch:             h.EpochDataHandler,
		Statistics:        h.Statistics,
		EpochStatistics:   h.EpochStatistics,
		ExecutionData:     h.ExecutionData,
		SequenceAlignment: h.SequenceAlignmentData,
		NetworkParameters: h.NetworkParameters,
	}
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	writeJSON(ctx, resp)
}

type ApprovementThreadResponse struct {
	Epoch             structures.EpochDataHandler `json:"epoch"`
	NetworkParameters structures.NetworkParameters `json:"networkParameters"`
	CoreVersion       int                          `json:"coreVersion"`
}

func ServeApprovementThread(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")
	ctx.SetContentType("application/json")

	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	h := handlers.APPROVEMENT_THREAD_METADATA.Handler
	resp := ApprovementThreadResponse{
		Epoch:             h.EpochDataHandler,
		NetworkParameters: h.NetworkParameters,
		CoreVersion:       h.CoreMajorVersion,
	}
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	writeJSON(ctx, resp)
}

func ServeLeaderFinalization(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")
	ctx.SetContentType("application/json")
	writeJSON(ctx, threads.GetLeaderFinalizationState())
}


func writeJSON(ctx *fasthttp.RequestCtx, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.WriteString(`{"err":"marshal failed"}`)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.Write(data)
}
