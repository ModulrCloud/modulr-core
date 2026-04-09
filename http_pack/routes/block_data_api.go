package routes

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/http_pack/helpers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/syndtr/goleveldb/leveldb"

	"github.com/valyala/fasthttp"
)

type LastHeightResponse struct {
	LastHeight int64 `json:"lastHeight"`
}

type LiveStatsResponse struct {
	Statistics        *structures.Statistics       `json:"statistics"`
	EpochStatistics   *structures.Statistics       `json:"epochStatistics"`
	NetworkParameters structures.NetworkParameters `json:"networkParameters"`
	Epoch             structures.EpochDataHandler  `json:"epoch"`
}

func GetLastHeight(ctx *fasthttp.RequestCtx) {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	statistics := handlers.EXECUTION_THREAD_METADATA.Handler.Statistics
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if statistics == nil || statistics.LastHeight < 0 {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
		return
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, LastHeightResponse{LastHeight: statistics.LastHeight})
}

func GetLiveStats(ctx *fasthttp.RequestCtx) {
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

	helpers.WriteJSON(ctx, fasthttp.StatusOK, LiveStatsResponse{
		Statistics:        statistics,
		EpochStatistics:   epochStatistics,
		NetworkParameters: networkParameters,
		Epoch:             epoch,
	})
}

func GetBlockById(ctx *fasthttp.RequestCtx) {
	blockIdRaw := ctx.UserValue("id")
	blockId, ok := blockIdRaw.(string)

	if !ok {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid value")
		return
	}

	block, err := databases.BLOCKS.Get([]byte(blockId), nil)

	if err == nil && block != nil {
		helpers.WriteJSONBytes(ctx, fasthttp.StatusOK, block)
		return
	}

	helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
}

func GetBlockByHeight(ctx *fasthttp.RequestCtx) {
	absoluteHeightRaw := ctx.UserValue("absoluteHeightIndex")
	absoluteHeight, ok := absoluteHeightRaw.(string)

	if !ok || absoluteHeight == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid height")
		return
	}

	if _, err := strconv.ParseInt(absoluteHeight, 10, 64); err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid height")
		return
	}

	blockIndexKey := constants.DBKeyPrefixBlockIndex + absoluteHeight
	blockID, err := databases.STATE.Get([]byte(blockIndexKey), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
			return
		}

		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to load block id")
		return
	}

	if len(blockID) == 0 {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
		return
	}

	block, err := databases.BLOCKS.Get(blockID, nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
			return
		}

		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to load block")
		return
	}

	if len(block) == 0 {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
		return
	}

	helpers.WriteJSONBytes(ctx, fasthttp.StatusOK, block)
}

func GetHeightAttestation(ctx *fasthttp.RequestCtx) {
	heightRaw := ctx.UserValue("height")
	heightStr, ok := heightRaw.(string)

	if !ok || heightStr == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid height")
		return
	}

	height, err := strconv.Atoi(heightStr)
	if err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid height")
		return
	}

	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixHeightAttestation, height))
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	if err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
		return
	}

	var proof structures.HeightAttestation
	if json.Unmarshal(raw, &proof) != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to parse attestation")
		return
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, proof)
}

func GetFirstBlockInEpoch(ctx *fasthttp.RequestCtx) {
	epochIdRaw := ctx.UserValue("epochId")
	epochIdStr, ok := epochIdRaw.(string)

	if !ok || epochIdStr == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid epochId")
		return
	}

	epochId, err := strconv.Atoi(epochIdStr)
	if err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid epochId")
		return
	}

	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixFirstBlockAttestation, epochId))
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	if err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
		return
	}

	var proof structures.HeightAttestation
	if json.Unmarshal(raw, &proof) != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to parse attestation")
		return
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, proof)
}

func GetAggregatedFinalizationProof(ctx *fasthttp.RequestCtx) {
	blockIdRaw := ctx.UserValue("blockId")
	blockId, ok := blockIdRaw.(string)

	if !ok {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid value")
		return
	}

	afp, err := databases.EPOCH_DATA.Get([]byte(constants.DBKeyPrefixAfp+blockId), nil)

	if err == nil && afp != nil {
		helpers.WriteJSONBytes(ctx, fasthttp.StatusOK, afp)
		return
	}

	helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
}

func GetTransactionByHash(ctx *fasthttp.RequestCtx) {
	hashRaw := ctx.UserValue("hash")
	hash, ok := hashRaw.(string)

	if !ok || hash == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid value")
		return
	}

	txReceiptRawBytes, err := databases.STATE.Get([]byte(constants.DBKeyPrefixTxReceipt+hash), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
			return
		}

		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to load transaction location")
		return
	}

	var txReceipt structures.TransactionReceipt

	if err := json.Unmarshal(txReceiptRawBytes, &txReceipt); err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to parse transaction location")
		return
	}

	blockBytes, err := databases.BLOCKS.Get([]byte(txReceipt.Block), nil)

	if err != nil {
		if err == leveldb.ErrNotFound {
			helpers.WriteErr(ctx, fasthttp.StatusNotFound, "Not found")
			return
		}

		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to load block")
		return
	}

	var block block_pack.Block

	if err := json.Unmarshal(blockBytes, &block); err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Failed to parse block")
		return
	}

	response := structures.TxWithReceipt{
		Tx:      block.Transactions[txReceipt.Position],
		Receipt: txReceipt,
	}

	helpers.WriteJSON(ctx, fasthttp.StatusOK, response)
}

func AcceptTransaction(ctx *fasthttp.RequestCtx) {
	var transaction structures.Transaction

	if err := json.Unmarshal(ctx.PostBody(), &transaction); err != nil {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid JSON")
		return
	}

	if transaction.From == "" || transaction.To == "" || transaction.Nonce == 0 || transaction.Sig == "" {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Event structure is wrong")
		return
	}

	if !cryptography.IsValidPubKey(transaction.From) || !cryptography.IsValidPubKey(transaction.To) {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid pubkey")
		return
	}

	if !cryptography.VerifySignature(transaction.Hash(), transaction.From, transaction.Sig) {
		helpers.WriteErr(ctx, fasthttp.StatusBadRequest, "Invalid signature")
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
			helpers.WriteErr(ctx, fasthttp.StatusInternalServerError, "Impossible to redirect to current leader")
			return
		}

		ctx.SetStatusCode(resp.StatusCode())
		ctx.Response.Header.SetContentTypeBytes(resp.Header.ContentType())
		ctx.SetBody(resp.Body())
		return
	}

	// Check mempool size

	globals.MEMPOOL.Mutex.Lock()
	defer globals.MEMPOOL.Mutex.Unlock()

	if len(globals.MEMPOOL.Slice) >= globals.CONFIGURATION.TxsMempoolSize {
		helpers.WriteErr(ctx, fasthttp.StatusTooManyRequests, "Mempool is fullfilled")
		return
	}

	// Add to mempool
	globals.MEMPOOL.Slice = append(globals.MEMPOOL.Slice, transaction)

	helpers.WriteJSON(ctx, fasthttp.StatusOK, map[string]string{"status": "OK"})
}
