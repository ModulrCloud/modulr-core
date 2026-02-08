package evmrpc

import "encoding/json"

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      any             `json:"id"`
}

type Response struct {
	JSONRPC string       `json:"jsonrpc"`
	Result  any          `json:"result,omitempty"`
	Error   *RPCError    `json:"error,omitempty"`
	ID      any          `json:"id"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

