package evmrpc

import (
	"errors"
	"encoding/json"
	"encoding/hex"
	"math/big"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/trie"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/evmvm"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/threads"
)

var (
	filterIDCounter atomic.Uint64
)

const (
	maxRawTxHexLen     = 512_000 // 256KB payload (hex chars, excluding 0x)
	maxCallDataBytes   = 128 << 10
	maxLogsRangeBlocks = 5_000
	maxGasCap          = 30_000_000
)

func Handle(req Request) []byte {
	if req.JSONRPC != "2.0" || req.Method == "" {
		return ErrorResponse(req.ID, -32600, "Invalid Request")
	}
	switch req.Method {
	case "web3_clientVersion":
		return ResultResponse(req.ID, "modulr-evm/0.1")
	case "web3_sha3":
		return handleWeb3Sha3(req)
	case "net_version":
		// Network id (decimal string).
		return ResultResponse(req.ID, "7337")
	case "eth_chainId":
		// Chain id in hex.
		return ResultResponse(req.ID, "0x1ca9")
	case "eth_protocolVersion":
		return ResultResponse(req.ID, "0x0")
	case "eth_syncing":
		return ResultResponse(req.ID, false)
	case "eth_gasPrice":
		return ResultResponse(req.ID, "0x0")
	case "eth_accounts":
		return ResultResponse(req.ID, []any{})
	case "eth_coinbase":
		return ResultResponse(req.ID, "0x0000000000000000000000000000000000000000")
	case "eth_mining":
		return ResultResponse(req.ID, false)
	case "eth_hashrate":
		return ResultResponse(req.ID, "0x0")
	case "eth_blockNumber":
		h := threads.GetLastExecutedHeight()
		if h < 0 {
			return ResultResponse(req.ID, "0x0")
		}
		return ResultResponse(req.ID, "0x"+strconv.FormatInt(h, 16))
	case "eth_getTransactionReceipt":
		return handleGetTransactionReceipt(req)
	case "eth_getTransactionByHash":
		return handleGetTransactionByHash(req)
	case "eth_getTransactionByBlockNumberAndIndex":
		return handleGetTransactionByBlockNumberAndIndex(req)
	case "eth_getBlockByNumber":
		return handleGetBlockByNumber(req)
	case "eth_getBlockByHash":
		return handleGetBlockByHash(req)
	case "eth_getBlockReceipts":
		return handleGetBlockReceipts(req)
	case "eth_getBlockTransactionCountByNumber":
		return handleGetBlockTxCountByNumber(req)
	case "eth_getBlockTransactionCountByHash":
		return handleGetBlockTxCountByHash(req)
	case "eth_getLogs":
		return handleGetLogs(req)
	case "eth_getBalance":
		return handleGetBalance(req)
	case "eth_getTransactionCount":
		return handleGetTransactionCount(req)
	case "eth_getCode":
		return handleGetCode(req)
	case "eth_getStorageAt":
		return handleGetStorageAt(req)
	case "eth_call":
		return handleEthCall(req)
	case "eth_estimateGas":
		return handleEstimateGas(req)
	case "eth_newFilter", "eth_newBlockFilter", "eth_newPendingTransactionFilter":
		return handleNewFilter(req)
	case "eth_uninstallFilter":
		return handleUninstallFilter(req)
	case "eth_getFilterChanges", "eth_getFilterLogs":
		return ResultResponse(req.ID, []any{})
	case "eth_maxPriorityFeePerGas":
		return ResultResponse(req.ID, "0x0")
	case "eth_feeHistory":
		return handleFeeHistory(req)
	case "eth_getProof":
		return handleGetProof(req)
	case "eth_sendRawTransaction":
		return handleSendRawTransaction(req)
	default:
		return ErrorResponse(req.ID, -32601, "Method not found")
	}
}

func handleWeb3Sha3(req Request) []byte {
	var params []string
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	input := params[0]
	sum := crypto.Keccak256([]byte(input))
	return ResultResponse(req.ID, "0x"+common.Bytes2Hex(sum))
}

func handleSendRawTransaction(req Request) []byte {
	var params []string
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	raw := strings.TrimSpace(params[0])
	if strings.HasPrefix(raw, "0x") && len(raw) > 2+maxRawTxHexLen {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	tx, err := threads.EVMParseSignedTx0x(raw)
	if err != nil {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	hash := tx.Hash()

	// Sandbox execution to reject obviously failing txs before mempool inclusion.
	if err := threads.EVMSandboxSignedTx(tx); err != nil {
		return ErrorResponse(req.ID, -32000, "EVM sandbox failed: "+err.Error())
	}

	// Enqueue to Modulr mempool as a normal transaction with payload.type=evmRaw.
	if err := threads.EnqueueEVMSignedTx(raw); err != nil {
		return ErrorResponse(req.ID, -32000, "EVM enqueue failed: "+err.Error())
	}

	// Store minimal tx record for lookups (optional but useful).
	_ = databasesPutJSON("EVM_TX_RAW:"+hash.Hex(), map[string]any{"raw": raw})

	return ResultResponse(req.ID, hash.Hex())
}

func databasesPutJSON(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// Best-effort write into STATE; failures are non-fatal for RPC call.
	if databases.STATE != nil {
		_ = databases.STATE.Put([]byte(key), b, nil)
	}
	return nil
}

func handleGetTransactionReceipt(req Request) []byte {
	var params []string
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	txHash := strings.TrimSpace(params[0])
	b, err := databases.STATE.Get([]byte("TX:"+txHash), nil)
	if err != nil {
		return ResultResponse(req.ID, nil)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return ResultResponse(req.ID, nil)
	}
	if receipt, ok := doc["receipt"]; ok {
		return ResultResponse(req.ID, receipt)
	}
	return ResultResponse(req.ID, nil)
}

func handleGetTransactionByHash(req Request) []byte {
	var params []string
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	txHash := strings.TrimSpace(params[0])
	b, err := databases.STATE.Get([]byte("TX:"+txHash), nil)
	if err != nil {
		return ResultResponse(req.ID, nil)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return ResultResponse(req.ID, nil)
	}
	if tx, ok := doc["tx"]; ok {
		return ResultResponse(req.ID, tx)
	}
	return ResultResponse(req.ID, nil)
}

func handleGetTransactionByBlockNumberAndIndex(req Request) []byte {
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 2 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	blockTag, _ := params[0].(string)
	indexTag, _ := params[1].(string)
	heightHex, err := resolveBlockNumberTag(blockTag)
	if err != nil {
		return ResultResponse(req.ID, nil)
	}
	idx, err := parseQuantityIndex(indexTag)
	if err != nil {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	blk, err := loadEVMBlock(heightHex)
	if err != nil {
		return ResultResponse(req.ID, nil)
	}
	hashes, _ := blk["transactions"].([]any)
	if idx < 0 || idx >= len(hashes) {
		return ResultResponse(req.ID, nil)
	}
	txHash, _ := hashes[idx].(string)
	if txHash == "" {
		return ResultResponse(req.ID, nil)
	}
	tb, err := databases.STATE.Get([]byte("TX:"+txHash), nil)
	if err != nil {
		return ResultResponse(req.ID, nil)
	}
	var doc map[string]any
	if err := json.Unmarshal(tb, &doc); err != nil {
		return ResultResponse(req.ID, nil)
	}
	if tx, ok := doc["tx"]; ok {
		return ResultResponse(req.ID, tx)
	}
	return ResultResponse(req.ID, nil)
}

func handleGetBlockByNumber(req Request) []byte {
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	tag, _ := params[0].(string)
	full := false
	if len(params) > 1 {
		if b, ok := params[1].(bool); ok {
			full = b
		}
	}
	var heightHex string
	if tag == "latest" || tag == "" {
		h := threads.GetLastExecutedHeight()
		if h < 0 {
			heightHex = "0x0"
		} else {
			heightHex = "0x" + strconv.FormatInt(h, 16)
		}
	} else {
		heightHex = tag
	}
	b, err := databases.STATE.Get([]byte("EVM_BLOCK:"+heightHex), nil)
	if err != nil {
		return ResultResponse(req.ID, nil)
	}
	if !full {
		return ResultResponse(req.ID, json.RawMessage(b))
	}
	var blk map[string]any
	if err := json.Unmarshal(b, &blk); err != nil {
		return ResultResponse(req.ID, nil)
	}
	// Ensure baseFeePerGas is present (EIP-1559 UX). We run baseFee=0 for now.
	if _, ok := blk["baseFeePerGas"]; !ok {
		blk["baseFeePerGas"] = "0x0"
	}
	hashes, _ := blk["transactions"].([]any)
	fullTxs := make([]any, 0, len(hashes))
	for _, h := range hashes {
		hs, _ := h.(string)
		if hs == "" {
			continue
		}
		tb, err := databases.STATE.Get([]byte("TX:"+hs), nil)
		if err != nil {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(tb, &doc); err == nil {
			if tx, ok := doc["tx"]; ok {
				fullTxs = append(fullTxs, tx)
			}
		}
	}
	blk["transactions"] = fullTxs
	return ResultResponse(req.ID, blk)
}

func handleGetBlockByHash(req Request) []byte {
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	blockHash, _ := params[0].(string)
	full := false
	if len(params) > 1 {
		if b, ok := params[1].(bool); ok {
			full = b
		}
	}
	heightHexB, err := databases.STATE.Get([]byte("EVM_INDEX:"+blockHash), nil)
	if err != nil {
		return ResultResponse(req.ID, nil)
	}
	heightHex := strings.TrimSpace(string(heightHexB))
	req2 := Request{JSONRPC: "2.0", Method: "eth_getBlockByNumber", Params: mustJSON([]any{heightHex, full}), ID: req.ID}
	return handleGetBlockByNumber(req2)
}

func handleGetBlockReceipts(req Request) []byte {
	// Accept single param: blockNrOrHash as string (like gethclient does).
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	tag, _ := params[0].(string)
	heightHex, err := resolveBlockNrOrHash(tag)
	if err != nil {
		return ResultResponse(req.ID, nil)
	}
	blk, err := loadEVMBlock(heightHex)
	if err != nil {
		return ResultResponse(req.ID, nil)
	}
	hashes, _ := blk["transactions"].([]any)
	receipts := make([]any, 0, len(hashes))
	for _, hh := range hashes {
		hs, _ := hh.(string)
		if hs == "" {
			continue
		}
		b, err := databases.STATE.Get([]byte("TX:"+hs), nil)
		if err != nil {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			continue
		}
		if receipt, ok := doc["receipt"]; ok {
			receipts = append(receipts, receipt)
		}
	}
	return ResultResponse(req.ID, receipts)
}

func handleGetBlockTxCountByNumber(req Request) []byte {
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	tag, _ := params[0].(string)
	if tag == "latest" || tag == "" {
		h := threads.GetLastExecutedHeight()
		if h < 0 {
			return ResultResponse(req.ID, "0x0")
		}
		tag = "0x" + strconv.FormatInt(h, 16)
	}
	b, err := databases.STATE.Get([]byte("EVM_BLOCK:"+tag), nil)
	if err != nil {
		return ResultResponse(req.ID, "0x0")
	}
	var blk map[string]any
	if err := json.Unmarshal(b, &blk); err != nil {
		return ResultResponse(req.ID, "0x0")
	}
	txs, _ := blk["transactions"].([]any)
	return ResultResponse(req.ID, "0x"+strconv.FormatUint(uint64(len(txs)), 16))
}

func handleGetBlockTxCountByHash(req Request) []byte {
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	blockHash, _ := params[0].(string)
	heightHexB, err := databases.STATE.Get([]byte("EVM_INDEX:"+blockHash), nil)
	if err != nil {
		return ResultResponse(req.ID, "0x0")
	}
	heightHex := strings.TrimSpace(string(heightHexB))
	req2 := Request{JSONRPC: "2.0", Method: "eth_getBlockTransactionCountByNumber", Params: mustJSON([]any{heightHex}), ID: req.ID}
	return handleGetBlockTxCountByNumber(req2)
}

func handleGetLogs(req Request) []byte {
	var params []map[string]any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	filter := params[0]
	fromTag, _ := filter["fromBlock"].(string)
	toTag, _ := filter["toBlock"].(string)
	addressFilter := filter["address"]
	var topicsFilter []any
	if t, ok := filter["topics"].([]any); ok {
		topicsFilter = t
	}
	from := uint64(0)
	to := uint64(0)
	latest := threads.GetLastExecutedHeight()
	if latest < 0 {
		return ResultResponse(req.ID, []any{})
	}
	to = uint64(latest)
	if fromTag != "" && fromTag != "latest" {
		if strings.HasPrefix(fromTag, "0x") {
			if v, err := strconv.ParseUint(strings.TrimPrefix(fromTag, "0x"), 16, 64); err == nil {
				from = v
			}
		}
	}
	if toTag != "" && toTag != "latest" {
		if strings.HasPrefix(toTag, "0x") {
			if v, err := strconv.ParseUint(strings.TrimPrefix(toTag, "0x"), 16, 64); err == nil {
				to = v
			}
		}
	}
	if to < from {
		return ResultResponse(req.ID, []any{})
	}
	if to-from > maxLogsRangeBlocks {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}

	// Normalize address filter into a list (supports DATA|Array per spec).
	addrList := normalizeAddressFilter(addressFilter)
	// Normalize topic0 into OR-list (if provided).
	topic0List := normalizeTopic0(topicsFilter)

	out := make([]any, 0)
	for h := from; h <= to; h++ {
		heightHex := "0x" + strconv.FormatUint(h, 16)
		var logs []any

		// Fast paths using indexes:
		// - if address list is present -> union EVM_LOGS_ADDR:* for each address
		// - else if topic0 list is present -> union EVM_LOGS_TOPIC0:* for each topic0
		// - else -> fall back to full block logs EVM_LOGS:<heightHex>
		if len(addrList) > 0 {
			for _, addr := range addrList {
				lst := loadIndexedLogs("EVM_LOGS_ADDR:"+addr+":"+heightHex)
				if len(lst) > 0 {
					logs = append(logs, lst...)
				}
			}
		} else if len(topic0List) > 0 {
			for _, t0 := range topic0List {
				lst := loadIndexedLogs("EVM_LOGS_TOPIC0:"+t0+":"+heightHex)
				if len(lst) > 0 {
					logs = append(logs, lst...)
				}
			}
		} else {
			logs = loadIndexedLogs("EVM_LOGS:" + heightHex)
		}

		if len(logs) == 0 {
			continue
		}

		for _, l := range logs {
			lm, ok := l.(map[string]any)
			if !ok {
				continue
			}
			if len(addrList) > 0 {
				if addr, _ := lm["address"].(string); !containsFold(addrList, addr) {
					continue
				}
			}
			if len(topicsFilter) > 0 {
				if topics, ok := lm["topics"].([]any); ok {
					match := true
					for i := 0; i < len(topicsFilter) && i < len(topics); i++ {
						want := topicsFilter[i]
						if want == nil {
							continue
						}
						// Support topic OR-list: [t1, [t2,t3], null ...]
						switch w := want.(type) {
						case string:
							if w != "" {
								ts, _ := topics[i].(string)
								if !strings.EqualFold(w, ts) {
									match = false
									break
								}
							}
						case []any:
							ts, _ := topics[i].(string)
							okAny := false
							for _, cand := range w {
								cs, _ := cand.(string)
								if cs != "" && strings.EqualFold(cs, ts) {
									okAny = true
									break
								}
							}
							if !okAny {
								match = false
								break
							}
						default:
							// ignore unknown types
						}
					}
					if !match {
						continue
					}
				}
			}
			out = append(out, lm)
		}
	}
	return ResultResponse(req.ID, out)
}

func loadIndexedLogs(key string) []any {
	if databases.STATE == nil {
		return nil
	}
	b, err := databases.STATE.Get([]byte(key), nil)
	if err != nil || len(b) == 0 {
		return nil
	}
	var logs []any
	if err := json.Unmarshal(b, &logs); err != nil {
		return nil
	}
	return logs
}

func normalizeAddressFilter(v any) []string {
	switch a := v.(type) {
	case string:
		s := strings.ToLower(strings.TrimSpace(a))
		if s == "" {
			return nil
		}
		return []string{s}
	case []any:
		out := make([]string, 0, len(a))
		for _, e := range a {
			if s, ok := e.(string); ok {
				s = strings.ToLower(strings.TrimSpace(s))
				if s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func normalizeTopic0(topics []any) []string {
	if len(topics) == 0 || topics[0] == nil {
		return nil
	}
	switch t0 := topics[0].(type) {
	case string:
		s := strings.ToLower(strings.TrimSpace(t0))
		if s == "" {
			return nil
		}
		return []string{s}
	case []any:
		out := make([]string, 0, len(t0))
		for _, e := range t0 {
			if s, ok := e.(string); ok {
				s = strings.ToLower(strings.TrimSpace(s))
				if s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func containsFold(list []string, s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func handleGetBalance(req Request) []byte {
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	addrStr, _ := params[0].(string)
	addr := common.HexToAddress(addrStr)
	r, err := openReadOnlyRunner()
	if err != nil {
		return ErrorResponse(req.ID, -32000, err.Error())
	}
	defer r.Close()
	bal := r.StateDB().GetBalance(addr)
	return ResultResponse(req.ID, "0x"+bal.ToBig().Text(16))
}

func handleGetTransactionCount(req Request) []byte {
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	addrStr, _ := params[0].(string)
	addr := common.HexToAddress(addrStr)
	r, err := openReadOnlyRunner()
	if err != nil {
		return ErrorResponse(req.ID, -32000, err.Error())
	}
	defer r.Close()
	nonce := r.StateDB().GetNonce(addr)
	return ResultResponse(req.ID, "0x"+strconv.FormatUint(nonce, 16))
}

func handleGetCode(req Request) []byte {
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	addrStr, _ := params[0].(string)
	addr := common.HexToAddress(addrStr)
	r, err := openReadOnlyRunner()
	if err != nil {
		return ErrorResponse(req.ID, -32000, err.Error())
	}
	defer r.Close()
	code := r.StateDB().GetCode(addr)
	return ResultResponse(req.ID, "0x"+common.Bytes2Hex(code))
}

func handleGetStorageAt(req Request) []byte {
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 2 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	addrStr, _ := params[0].(string)
	posStr, _ := params[1].(string)
	addr := common.HexToAddress(addrStr)
	slot, err := parseStorageSlot(posStr)
	if err != nil {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	r, err := openReadOnlyRunner()
	if err != nil {
		return ErrorResponse(req.ID, -32000, err.Error())
	}
	defer r.Close()
	val := r.StateDB().GetState(addr, slot)
	return ResultResponse(req.ID, val.Hex())
}

func handleEthCall(req Request) []byte {
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	callObj, ok := params[0].(map[string]any)
	if !ok {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	from := common.Address{}
	if s, ok := callObj["from"].(string); ok && s != "" {
		from = common.HexToAddress(s)
	}
	toStr, _ := callObj["to"].(string)
	to := common.HexToAddress(toStr)
	dataStr, _ := callObj["data"].(string)
	dataBytes, _ := hex.DecodeString(strings.TrimPrefix(dataStr, "0x"))
	if len(dataBytes) > maxCallDataBytes {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	gas := uint64(3_000_000)
	if g, ok := callObj["gas"].(string); ok && strings.HasPrefix(g, "0x") {
		if v, err := strconv.ParseUint(strings.TrimPrefix(g, "0x"), 16, 64); err == nil && v > 0 {
			gas = v
		}
	}
	if gas > maxGasCap {
		gas = maxGasCap
	}
	r, err := openReadOnlyRunner()
	if err != nil {
		return ErrorResponse(req.ID, -32000, err.Error())
	}
	defer r.Close()
	ret, _, err := r.Simulate(from, to, dataBytes, gas)
	if err != nil {
		return ErrorResponse(req.ID, -32000, err.Error())
	}
	return ResultResponse(req.ID, "0x"+common.Bytes2Hex(ret))
}

func handleEstimateGas(req Request) []byte {
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 1 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	callObj, ok := params[0].(map[string]any)
	if !ok {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	from := common.Address{}
	if s, ok := callObj["from"].(string); ok && s != "" {
		from = common.HexToAddress(s)
	}
	toStr, _ := callObj["to"].(string)
	to := common.HexToAddress(toStr)
	dataStr, _ := callObj["data"].(string)
	dataBytes, _ := hex.DecodeString(strings.TrimPrefix(dataStr, "0x"))
	if len(dataBytes) > maxCallDataBytes {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}

	// Upper bound.
	hi := uint64(maxGasCap)
	if g, ok := callObj["gas"].(string); ok && strings.HasPrefix(g, "0x") {
		if v, err := strconv.ParseUint(strings.TrimPrefix(g, "0x"), 16, 64); err == nil && v > 0 {
			hi = v
		}
	}
	if hi > maxGasCap {
		hi = maxGasCap
	}

	// Lower bound (very rough intrinsic minimum).
	lo := uint64(21_000)
	if hi < lo {
		hi = lo
	}

	r, err := openReadOnlyRunner()
	if err != nil {
		return ErrorResponse(req.ID, -32000, err.Error())
	}
	defer r.Close()

	// First, ensure the call succeeds with the upper bound.
	if err := simulateWithGas(r, from, to, dataBytes, hi); err != nil {
		if isOutOfGas(err) {
			return ErrorResponse(req.ID, -32000, "out of gas (increase gas limit)")
		}
		// Logical revert or other error: estimateGas should return error.
		return ErrorResponse(req.ID, -32000, err.Error())
	}

	// Binary search for minimal gas that succeeds.
	for i := 0; i < 64 && lo+1 < hi; i++ {
		mid := lo + (hi-lo)/2
		err := simulateWithGas(r, from, to, dataBytes, mid)
		if err == nil {
			hi = mid
			continue
		}
		if isOutOfGas(err) {
			lo = mid
			continue
		}
		// Not gas-related => stop and return error (same semantics as geth).
		return ErrorResponse(req.ID, -32000, err.Error())
	}

	return ResultResponse(req.ID, "0x"+strconv.FormatUint(hi, 16))
}

func handleFeeHistory(req Request) []byte {
	// Params: [blockCount, newestBlock, rewardPercentiles]
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 2 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	blockCountHex, _ := params[0].(string)
	newest, _ := params[1].(string)
	blockCount := uint64(0)
	if strings.HasPrefix(blockCountHex, "0x") {
		blockCount, _ = strconv.ParseUint(strings.TrimPrefix(blockCountHex, "0x"), 16, 64)
	}
	if blockCount == 0 {
		blockCount = 1
	}
	if blockCount > 1024 {
		blockCount = 1024
	}
	var newestHeight uint64
	if newest == "latest" || newest == "" {
		h := threads.GetLastExecutedHeight()
		if h < 0 {
			newestHeight = 0
		} else {
			newestHeight = uint64(h)
		}
	} else if strings.HasPrefix(newest, "0x") {
		newestHeight, _ = strconv.ParseUint(strings.TrimPrefix(newest, "0x"), 16, 64)
	}
	oldest := uint64(0)
	if newestHeight > blockCount {
		oldest = newestHeight - blockCount + 1
	}
	// baseFeePerGas length is blockCount+1, gasUsedRatio length is blockCount.
	baseFees := make([]string, 0, blockCount+1)
	ratios := make([]float64, 0, blockCount)
	for i := uint64(0); i < blockCount+1; i++ {
		baseFees = append(baseFees, "0x0")
		if i < blockCount {
			ratios = append(ratios, 0)
		}
	}
	resp := map[string]any{
		"oldestBlock":  "0x" + strconv.FormatUint(oldest, 16),
		"baseFeePerGas": baseFees,
		"gasUsedRatio":  ratios,
		"reward":        [][]string{},
	}
	return ResultResponse(req.ID, resp)
}

func handleNewFilter(req Request) []byte {
	id := filterIDCounter.Add(1)
	return ResultResponse(req.ID, "0x"+strconv.FormatUint(id, 16))
}

func handleUninstallFilter(req Request) []byte {
	return ResultResponse(req.ID, true)
}

func openReadOnlyRunner() (*evmvm.Runner, error) {
	root := threadsLoadEVMRoot()
	dbPath := globals.CHAINDATA_PATH + "/DATABASES/EVM"
	return evmvm.NewRunner(evmvm.Options{
		DBPath:    dbPath,
		DBEngine:  "leveldb",
		ReadOnly:  true,
		StateRoot: &root,
		BaseFee:   big.NewInt(0),
		GasPrice:  big.NewInt(0),
	})
}

func threadsLoadEVMRoot() common.Hash {
	if databases.STATE == nil {
		return types.EmptyRootHash
	}
	b, err := databases.STATE.Get([]byte("EVM_ROOT"), nil)
	if err != nil {
		return types.EmptyRootHash
	}
	s := strings.TrimSpace(string(b))
	if len(s) == 66 && strings.HasPrefix(s, "0x") {
		return common.HexToHash(s)
	}
	return types.EmptyRootHash
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func simulateWithGas(r *evmvm.Runner, from common.Address, to common.Address, data []byte, gas uint64) error {
	_, _, err := r.Simulate(from, to, data, gas)
	return err
}

func isOutOfGas(err error) bool {
	if err == nil {
		return false
	}
	// vm.ErrOutOfGas is the canonical one, but sometimes wrapped.
	if errors.Is(err, vm.ErrOutOfGas) || errors.Is(err, vm.ErrCodeStoreOutOfGas) {
		return true
	}
	// Catch wrapped errors from interpreter: "%w: %v" etc.
	msg := err.Error()
	return strings.Contains(msg, "out of gas") || strings.Contains(msg, "gas too low")
}

func parseStorageSlot(s string) (common.Hash, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") {
		hexStr := strings.TrimPrefix(s, "0x")
		if len(hexStr) > 64 {
			return common.Hash{}, strconv.ErrSyntax
		}
		// If already full 32 bytes, use as-is.
		if len(hexStr) == 64 {
			return common.HexToHash(s), nil
		}
		// Quantity form: parse as big.Int and left-pad.
		if hexStr == "" {
			return common.Hash{}, strconv.ErrSyntax
		}
		bi := new(big.Int)
		if _, ok := bi.SetString(hexStr, 16); !ok {
			return common.Hash{}, strconv.ErrSyntax
		}
		return common.BigToHash(bi), nil
	}
	// decimal quantity (big.Int)
	bi := new(big.Int)
	if _, ok := bi.SetString(s, 10); !ok {
		return common.Hash{}, strconv.ErrSyntax
	}
	return common.BigToHash(bi), nil
}

// ---- shared helpers for block/tx lookups ----

func resolveBlockNumberTag(tag string) (string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" || tag == "latest" {
		h := threads.GetLastExecutedHeight()
		if h < 0 {
			return "0x0", nil
		}
		return "0x" + strconv.FormatInt(h, 16), nil
	}
	if tag == "earliest" {
		return "0x0", nil
	}
	if strings.HasPrefix(tag, "0x") {
		return tag, nil
	}
	return "", errors.New("invalid block tag")
}

func resolveBlockNrOrHash(tag string) (string, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" || tag == "latest" || tag == "earliest" || strings.HasPrefix(tag, "0x") && len(tag) <= 66 {
		// Could be number tag; try as number first.
		if tag == "" || tag == "latest" || tag == "earliest" || strings.HasPrefix(tag, "0x") && len(tag) <= 18 {
			return resolveBlockNumberTag(tag)
		}
		// If looks like hash, map via EVM_INDEX.
		if len(tag) == 66 {
			if b, err := databases.STATE.Get([]byte("EVM_INDEX:"+tag), nil); err == nil {
				return strings.TrimSpace(string(b)), nil
			}
			// If not found, fall back to treating as number.
			return resolveBlockNumberTag(tag)
		}
		return resolveBlockNumberTag(tag)
	}
	return "", errors.New("invalid block param")
}

func loadEVMBlock(heightHex string) (map[string]any, error) {
	b, err := databases.STATE.Get([]byte("EVM_BLOCK:"+heightHex), nil)
	if err != nil {
		return nil, err
	}
	var blk map[string]any
	if err := json.Unmarshal(b, &blk); err != nil {
		return nil, err
	}
	return blk, nil
}

func parseQuantityIndex(s string) (int, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") {
		u, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
		return int(u), err
	}
	u, err := strconv.ParseUint(s, 10, 64)
	return int(u), err
}

// ---- eth_getProof (EIP-1186) ----

type accountResult struct {
	Address      common.Address   `json:"address"`
	AccountProof []string         `json:"accountProof"`
	Balance      *hexutil.Big     `json:"balance"`
	CodeHash     common.Hash      `json:"codeHash"`
	Nonce        hexutil.Uint64   `json:"nonce"`
	StorageHash  common.Hash      `json:"storageHash"`
	StorageProof []storageResult  `json:"storageProof"`
}

type storageResult struct {
	Key   string       `json:"key"`
	Value *hexutil.Big `json:"value"`
	Proof []string     `json:"proof"`
}

type proofList []string

func (n *proofList) Put(_key []byte, value []byte) error {
	*n = append(*n, hexutil.Encode(value))
	return nil
}

func (n *proofList) Delete(_key []byte) error { panic("not supported") }

func handleGetProof(req Request) []byte {
	// Params: [address, storageKeys[], blockTag?]
	var params []any
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) < 2 {
		return ErrorResponse(req.ID, -32602, "Invalid params")
	}
	addrStr, _ := params[0].(string)
	address := common.HexToAddress(addrStr)

	var storageKeys []string
	if raw, ok := params[1].([]any); ok {
		for _, e := range raw {
			if s, ok := e.(string); ok {
				storageKeys = append(storageKeys, s)
			}
		}
	}
	// We currently ignore the optional block tag and always use latest embedded EVM root.

	r, err := openReadOnlyRunner()
	if err != nil {
		return ErrorResponse(req.ID, -32000, err.Error())
	}
	defer r.Close()
	statedb := r.StateDB()
	root := r.Root()

	// Parse keys leniently, like geth (0x optional, <=32 bytes).
	keys := make([]common.Hash, len(storageKeys))
	keyLengths := make([]int, len(storageKeys))
	for i, hexKey := range storageKeys {
		h, l, err := decodeStorageKey(hexKey)
		if err != nil {
			return ErrorResponse(req.ID, -32602, "Invalid params")
		}
		keys[i], keyLengths[i] = h, l
	}

	codeHash := statedb.GetCodeHash(address)
	storageRoot := statedb.GetStorageRoot(address)
	storageProof := make([]storageResult, len(keys))

	if len(keys) > 0 {
		var storageTrie state.Trie
		if storageRoot != types.EmptyRootHash && storageRoot != (common.Hash{}) {
			id := trie.StorageTrieID(root, crypto.Keccak256Hash(address.Bytes()), storageRoot)
			st, err := trie.NewStateTrie(id, statedb.Database().TrieDB())
			if err != nil {
				return ErrorResponse(req.ID, -32000, err.Error())
			}
			storageTrie = st
		}
		for i, key := range keys {
			var outputKey string
			if keyLengths[i] != 32 {
				outputKey = hexutil.EncodeBig(key.Big())
			} else {
				outputKey = hexutil.Encode(key[:])
			}
			if storageTrie == nil {
				storageProof[i] = storageResult{outputKey, &hexutil.Big{}, []string{}}
				continue
			}
			var proof proofList
			if err := storageTrie.Prove(crypto.Keccak256(key.Bytes()), &proof); err != nil {
				return ErrorResponse(req.ID, -32000, err.Error())
			}
			value := (*hexutil.Big)(statedb.GetState(address, key).Big())
			storageProof[i] = storageResult{outputKey, value, proof}
		}
	}

	tr, err := trie.NewStateTrie(trie.StateTrieID(root), statedb.Database().TrieDB())
	if err != nil {
		return ErrorResponse(req.ID, -32000, err.Error())
	}
	var accountProof proofList
	if err := tr.Prove(crypto.Keccak256(address.Bytes()), &accountProof); err != nil {
		return ErrorResponse(req.ID, -32000, err.Error())
	}
	balance := statedb.GetBalance(address).ToBig()
	resp := &accountResult{
		Address:      address,
		AccountProof: accountProof,
		Balance:      (*hexutil.Big)(balance),
		CodeHash:     codeHash,
		Nonce:        hexutil.Uint64(statedb.GetNonce(address)),
		StorageHash:  storageRoot,
		StorageProof: storageProof,
	}
	return ResultResponse(req.ID, resp)
}

func decodeStorageKey(s string) (h common.Hash, inputLength int, err error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s = s[2:]
	}
	if (len(s) & 1) > 0 {
		s = "0" + s
	}
	if len(s) > 64 {
		return common.Hash{}, len(s) / 2, errors.New("storage key too long (want at most 32 bytes)")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return common.Hash{}, 0, errors.New("invalid hex in storage key")
	}
	return common.BytesToHash(b), len(b), nil
}

