package evmrpc

import "encoding/json"

func ResultResponse(id any, result any) []byte {
	resp := Response{
		JSONRPC: "2.0",
		Result:  result,
		ID:      id,
	}
	b, _ := json.Marshal(resp)
	return b
}

func ErrorResponse(id any, code int, message string) []byte {
	resp := Response{
		JSONRPC: "2.0",
		Error: &RPCError{
			Code:    code,
			Message: message,
		},
		ID: id,
	}
	b, _ := json.Marshal(resp)
	return b
}

