package routes

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/syndtr/goleveldb/leveldb"

	"github.com/valyala/fasthttp"
)

type lastHeightResponse struct {
	LastHeight int64 `json:"lastHeight"`
}

type liveStatsResponse struct {
	Statistics        *structures.Statistics       `json:"statistics"`
	EpochStatistics   *structures.Statistics       `json:"epochStatistics"`
	NetworkParameters structures.NetworkParameters `json:"networkParameters"`
	Epoch             structures.EpochDataHandler  `json:"epoch"`
}

func GetLastHeight(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	statistics := handlers.EXECUTION_THREAD_METADATA.Handler.Statistics
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if statistics == nil || statistics.LastHeight < 0 {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Not found"}`))
		return
	}

	response, err := json.Marshal(lastHeightResponse{LastHeight: statistics.LastHeight})
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to marshal response"}`))
		return
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Write(response)
}

func GetLiveStats(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	statistics := handlers.EXECUTION_THREAD_METADATA.Handler.Statistics
	epochStatistics := handlers.EXECUTION_THREAD_METADATA.Handler.EpochStatistics
	networkParameters := handlers.EXECUTION_THREAD_METADATA.Handler.NetworkParameters
	epoch := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if statistics == nil {
		statistics = &structures.Statistics{LastHeight: -1}
	}
	if epochStatistics == nil {
		epochStatistics = &structures.Statistics{LastHeight: -1}
	}

	response, err := json.Marshal(liveStatsResponse{
		Statistics:        statistics,
		EpochStatistics:   epochStatistics,
		NetworkParameters: networkParameters,
		Epoch:             epoch,
	})
	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to marshal response"}`))
		return
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Write(response)
}

func GetBlockById(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	blockIdRaw := ctx.UserValue("id")
	blockId, ok := blockIdRaw.(string)

	if !ok {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid value"}`))
		return
	}

	block, err := databases.BLOCKS.Get([]byte(blockId), nil)

	if err == nil && block != nil {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json")
		ctx.Write(block)
		return
	}

	ctx.SetStatusCode(fasthttp.StatusNotFound)
	ctx.SetContentType("application/json")
	ctx.Write([]byte(`{"err": "Not found"}`))
}

func GetBlockByHeight(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	absoluteHeightRaw := ctx.UserValue("absoluteHeightIndex")
	absoluteHeight, ok := absoluteHeightRaw.(string)

	if !ok || absoluteHeight == "" {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid height"}`))
		return
	}

	if _, err := strconv.ParseInt(absoluteHeight, 10, 64); err != nil {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid height"}`))
		return
	}

	blockIndexKey := "BLOCK_INDEX:" + absoluteHeight
	blockID, err := databases.STATE.Get([]byte(blockIndexKey), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err": "Not found"}`))
			return
		}

		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to load block id"}`))
		return
	}

	if len(blockID) == 0 {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Not found"}`))
		return
	}

	block, err := databases.BLOCKS.Get(blockID, nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err": "Not found"}`))
			return
		}

		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to load block"}`))
		return
	}

	if len(block) == 0 {
		ctx.SetStatusCode(fasthttp.StatusNotFound)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Not found"}`))
		return
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Write(block)
}

func GetAggregatedFinalizationProof(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	blockIdRaw := ctx.UserValue("blockId")
	blockId, ok := blockIdRaw.(string)

	if !ok {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid value"}`))
		return
	}

	afp, err := databases.EPOCH_DATA.Get([]byte("AFP:"+blockId), nil)

	if err == nil && afp != nil {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json")
		ctx.Write(afp)
		return
	}

	ctx.SetStatusCode(fasthttp.StatusNotFound)
	ctx.SetContentType("application/json")
	ctx.Write([]byte(`{"err": "Not found"}`))
}

func GetTransactionByHash(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	hashRaw := ctx.UserValue("hash")
	hash, ok := hashRaw.(string)

	if !ok || hash == "" {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Invalid value"}`))
		return
	}

	// EVM lookup path: transaction hashes are 0x + 64 hex chars and stored as TX:<hash> in STATE.
	if is0xHexLen(hash, 64) {
		if b, err := databases.STATE.Get([]byte("TX:"+hash), nil); err == nil && len(b) > 0 {
			var doc map[string]any
			if err := json.Unmarshal(b, &doc); err == nil {
				// Parse the stored ethereum-formatted tx/receipt and convert into the legacy
				// modulr-core API shape: { tx: structures.Transaction, receipt: structures.TransactionReceipt }.
				//
				// The full Ethereum objects are still returned under "evm".
				evmTx, _ := doc["tx"].(map[string]any)
				evmRcpt, _ := doc["receipt"].(map[string]any)
				errStr, _ := doc["error"].(string)

				fromStr, _ := evmTx["from"].(string)
				toStr := ""
				if toAny, ok := evmTx["to"]; ok {
					if ts, ok := toAny.(string); ok {
						toStr = ts
					}
				}
				nonceU64 := uint64(0)
				if nhex, ok := evmTx["nonce"].(string); ok {
					nonceU64 = parseHexQuantityToBig(nhex).Uint64()
				}

				valueWei := big.NewInt(0)
				if vhex, ok := evmTx["value"].(string); ok {
					valueWei = parseHexQuantityToBig(vhex)
				}
				valueEthStr := formatWeiToEtherString(valueWei)

				feeWei := big.NewInt(0)
				if evmRcpt != nil {
					gasUsed := uint64(0)
					if gu, ok := evmRcpt["gasUsed"].(string); ok {
						gasUsed = parseHexQuantityToBig(gu).Uint64()
					}
					egp := big.NewInt(0)
					if egpHex, ok := evmRcpt["effectiveGasPrice"].(string); ok {
						egp = parseHexQuantityToBig(egpHex)
					}
					feeWei = new(big.Int).Mul(egp, new(big.Int).SetUint64(gasUsed))
				}
				feeEthStr := formatWeiToEtherString(feeWei)

				// Legacy-compatible tx object.
				legacyTx := structures.Transaction{
					V:      1,
					From:   fromStr,
					To:     toStr,
					Amount: etherFloorUint64(valueWei), // legacy uint64; exact value is in payload.valueWei
					// Keep the legacy modulr-style unit conversion (same approach as Amount).
					// Exact fee is still exposed via payload.feeWei/fee.
					Fee:   etherFloorUint64(feeWei),
					Sig:   "",
					Nonce: nonceU64,
					Payload: map[string]any{
						"type":     "evm",
						"txHash":   hash,
						"error":    errStr,
						"valueWei": valueWei.String(),
						"value":    valueEthStr,
						"feeWei":   feeWei.String(),
						"fee":      feeEthStr,
						"evm": map[string]any{
							"tx":      doc["tx"],
							"receipt": doc["receipt"],
						},
					},
				}

				// Legacy-compatible receipt object.
				legacyRcpt := structures.TransactionReceipt{
					Block:    "",
					Position: 0,
					Success:  errStr == "",
					Reason:   errStr,
				}
				if evmRcpt != nil {
					// status: "0x1" / "0x0"
					if st, ok := evmRcpt["status"].(string); ok {
						legacyRcpt.Success = strings.TrimSpace(st) == "0x1" && errStr == ""
					}
					if ti, ok := evmRcpt["transactionIndex"].(string); ok {
						legacyRcpt.Position = int(parseHexQuantityToBig(ti).Int64())
					}
					// blockNumber can be used as a stable "block" identifier for the explorer.
					if bn, ok := evmRcpt["blockNumber"].(string); ok {
						heightDec := parseHexQuantityToBig(bn).Text(10)
						// Prefer the native block id using the existing index STATE["BLOCK_INDEX:<heightDec>"].
						if bid, err := databases.STATE.Get([]byte("BLOCK_INDEX:"+heightDec), nil); err == nil && len(bid) > 0 {
							legacyRcpt.Block = string(bid)
						} else {
							legacyRcpt.Block = "evm:" + heightDec
						}
					}
				}

				// Return strictly the legacy API shape: { tx, receipt }.
				// EVM-specific data is stored inside tx.payload.* (see payload["evm"], valueWei/feeWei, etc).
				out, _ := json.Marshal(structures.TxWithReceipt{Tx: legacyTx, Receipt: legacyRcpt})
				ctx.SetStatusCode(fasthttp.StatusOK)
				ctx.SetContentType("application/json")
				ctx.Write(out)
				return
			}
		}
	}

	txReceiptRawBytes, err := databases.STATE.Get([]byte(constants.DBKeyPrefixTxReceipt+hash), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err": "Not found"}`))
			return
		}

		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to load transaction location"}`))
		return
	}

	var txReceipt structures.TransactionReceipt

	if err := json.Unmarshal(txReceiptRawBytes, &txReceipt); err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to parse transaction location"}`))
		return
	}

	blockBytes, err := databases.BLOCKS.Get([]byte(txReceipt.Block), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetContentType("application/json")
			ctx.Write([]byte(`{"err": "Not found"}`))
			return
		}

		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to load block"}`))
		return
	}

	var block block_pack.Block

	if err := json.Unmarshal(blockBytes, &block); err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to parse block"}`))
		return
	}

	response := structures.TxWithReceipt{
		Tx:      block.Transactions[txReceipt.Position],
		Receipt: txReceipt,
	}

	// Return strictly the legacy API shape: { tx, receipt }.
	transactionBytes, err := json.Marshal(response)

	if err != nil {
		ctx.SetStatusCode(fasthttp.StatusInternalServerError)
		ctx.SetContentType("application/json")
		ctx.Write([]byte(`{"err": "Failed to serialize transaction"}`))
		return
	}

	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Write(transactionBytes)
}

func AcceptTransaction(ctx *fasthttp.RequestCtx) {

	ctx.Response.Header.Set("Access-Control-Allow-Origin", "*")

	var transaction structures.Transaction

	if err := json.Unmarshal(ctx.PostBody(), &transaction); err != nil {

		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.Write([]byte(`{"err":"Invalid JSON"}`))
		return

	}

	if transaction.From == "" || transaction.Nonce == 0 || transaction.Sig == "" {

		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.Write([]byte(`{"err":"Event structure is wrong"}`))
		return

	}

	currentLeader := utils.GetCurrentLeader()

	if !currentLeader.IsMeLeader {

		// Redirect tx to leader

		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		resp := fasthttp.AcquireResponse()
		defer fasthttp.ReleaseResponse(resp)

		req.SetRequestURI(currentLeader.Url + "/transaction")
		req.Header.SetMethod(fasthttp.MethodPost)
		req.SetBody(ctx.PostBody())

		if err := fasthttp.Do(req, resp); err != nil {
			ctx.SetStatusCode(fasthttp.StatusInternalServerError)
			ctx.Write([]byte(`{"err":"Impossible to redirect to current leader"}`))
			return
		}

		ctx.Write([]byte(`{"status":"Ok, tx redirected to current leader"}`))
		return

	}

	// Check mempool size

	globals.MEMPOOL.Mutex.Lock()
	defer globals.MEMPOOL.Mutex.Unlock()

	if len(globals.MEMPOOL.Slice) >= globals.CONFIGURATION.TxsMempoolSize {
		ctx.SetStatusCode(fasthttp.StatusTooManyRequests)
		ctx.Write([]byte(`{"err":"Mempool is fullfilled"}`))
		return
	}

	// Add to mempool
	globals.MEMPOOL.Slice = append(globals.MEMPOOL.Slice, transaction)

	ctx.Write([]byte(`{"status":"OK"}`))

}
