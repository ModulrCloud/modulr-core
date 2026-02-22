package helpers

import (
	"encoding/json"

	"github.com/valyala/fasthttp"
)

type errResponse struct {
	Err string `json:"err"`
}

func setJSONHeaders(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")
	ctx.SetContentType("application/json")
}

func WriteErr(ctx *fasthttp.RequestCtx, status int, msg string) {
	setJSONHeaders(ctx)
	ctx.SetStatusCode(status)
	if payload, err := json.Marshal(errResponse{Err: msg}); err == nil {
		ctx.Write(payload)
		return
	}
	ctx.Write([]byte(`{"err":"marshal failed"}`))
}

func WriteJSON(ctx *fasthttp.RequestCtx, status int, v any) {
	setJSONHeaders(ctx)
	data, err := json.Marshal(v)
	if err != nil {
		WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to marshal response")
		return
	}
	ctx.SetStatusCode(status)
	ctx.Write(data)
}

func WriteJSONBytes(ctx *fasthttp.RequestCtx, status int, raw []byte) {
	setJSONHeaders(ctx)
	ctx.SetStatusCode(status)
	ctx.Write(raw)
}

