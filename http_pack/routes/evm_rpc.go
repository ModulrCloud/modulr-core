package routes

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/evmrpc"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/valyala/fasthttp"
)

// EVMRPC handles a minimal Ethereum JSON-RPC 2.0 subset for wallet compatibility.
// Endpoint: POST /evm_rpc
func EVMRPC(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")
	ctx.Response.Header.SetContentType("application/json")

	// Basic DoS protection: cap request size.
	if len(ctx.PostBody()) > 1<<20 {
		ctx.SetStatusCode(fasthttp.StatusRequestEntityTooLarge)
		ctx.Write(evmrpc.ErrorResponse(nil, -32602, "Request too large"))
		return
	}

	var req evmrpc.Request
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.Write(evmrpc.ErrorResponse(nil, -32700, "Parse error"))
		return
	}

	// For eth_sendRawTransaction, forward to the current leader if we are not the leader,
	// mirroring Modulr's /transaction behavior.
	if req.Method == "eth_sendRawTransaction" {
		leader := utils.GetCurrentLeader()
		if !leader.IsMeLeader && leader.Url != "" {
			fwdReq := fasthttp.AcquireRequest()
			defer fasthttp.ReleaseRequest(fwdReq)
			fwdResp := fasthttp.AcquireResponse()
			defer fasthttp.ReleaseResponse(fwdResp)

			fwdReq.SetRequestURI(leader.Url + "/evm_rpc")
			fwdReq.Header.SetMethod(fasthttp.MethodPost)
			fwdReq.SetBody(ctx.PostBody())

			if err := fasthttp.Do(fwdReq, fwdResp); err == nil {
				ctx.SetStatusCode(fasthttp.StatusOK)
				ctx.Write(fwdResp.Body())
				return
			}
			// If forwarding fails, fall back to local handling.
		}
	}

	resp := evmrpc.Handle(req)
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.Write(resp)
}
