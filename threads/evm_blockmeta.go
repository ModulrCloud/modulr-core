package threads

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/syndtr/goleveldb/leveldb"
)

func getEVMRawPayload(tx *structures.Transaction) (string, bool) {
	if tx == nil || tx.Payload == nil {
		return "", false
	}
	typ, _ := tx.Payload["type"].(string)
	if typ != "evmRaw" {
		return "", false
	}
	raw, ok := tx.Payload["raw"].(string)
	if !ok || raw == "" {
		return "", false
	}
	return raw, true
}

func executeEVMSignedTxInBlock(raw0x string, txIndex int, blockNumber uint64, blockHash common.Hash, blockTimeSec uint64, batch *leveldb.Batch, logsOut *[]any) (bool, string, uint64, string, uint64, types.Bloom) {
	if execEVMRunner == nil {
		return false, "evm is not initialized", 0, "", 0, types.Bloom{}
	}
	tx, err := parseSignedTx0x(raw0x)
	if err != nil {
		return false, "invalid raw tx", 0, "", 0, types.Bloom{}
	}
	hashHex := tx.Hash().Hex()

	res, logs, sender, err := execEVMRunner.ApplySignedTx(tx, evmChainID, txIndex, blockNumber, blockHash, blockTimeSec, false)
	if err != nil {
		// Core error: invalid tx for this state.
		storeEVMErrorTxToBatch(batch, hashHex, err.Error(), raw0x)
		return false, "evm core error: " + err.Error(), 0, hashHex, 0, types.Bloom{}
	}
	if res == nil {
		storeEVMErrorTxToBatch(batch, hashHex, "missing execution result", raw0x)
		return false, "evm: missing execution result", 0, hashHex, 0, types.Bloom{}
	}

	status := types.ReceiptStatusSuccessful
	if res.Failed() {
		status = types.ReceiptStatusFailed
	}

	receipt := &types.Receipt{
		Type:              tx.Type(),
		Status:            status,
		CumulativeGasUsed: res.UsedGas,
		GasUsed:           res.UsedGas,
		EffectiveGasPrice: effectiveGasPrice(tx, EVMBaseFee()),
		TxHash:            tx.Hash(),
		BlockHash:         blockHash,
		BlockNumber:       new(big.Int).SetUint64(blockNumber),
		TransactionIndex:  uint(txIndex),
		Logs:              logs,
	}
	if receipt.Logs == nil {
		receipt.Logs = []*types.Log{}
	}
	// Derive contract address if needed.
	if tx.To() == nil {
		receipt.ContractAddress = crypto.CreateAddress(sender, tx.Nonce())
	}
	receipt.Bloom = types.CreateBloom(receipt)

	if logsOut != nil {
		for _, lg := range logs {
			b, err := json.Marshal(lg)
			if err != nil {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				continue
			}
			m["removed"] = false
			*logsOut = append(*logsOut, m)
		}
	}

	effGasPrice := effectiveGasPrice(tx, EVMBaseFee())
	feeCap := tx.GasFeeCap()
	tipCap := tx.GasTipCap()
	if feeCap == nil {
		feeCap = big.NewInt(0)
	}
	if tipCap == nil {
		tipCap = big.NewInt(0)
	}
	v, r, s := tx.RawSignatureValues()
	if v == nil {
		v = big.NewInt(0)
	}
	if r == nil {
		r = big.NewInt(0)
	}
	if s == nil {
		s = big.NewInt(0)
	}

	txJSON := map[string]any{
		"type":    "0x" + strconv.FormatUint(uint64(tx.Type()), 16),
		"chainId": "0x" + evmChainID.Text(16),
		"hash":    hashHex,
		"from":    sender.Hex(),
		"to": func() any {
			if tx.To() == nil {
				return nil
			}
			return tx.To().Hex()
		}(),
		"nonce": "0x" + strconv.FormatUint(tx.Nonce(), 16),
		"gas":   "0x" + strconv.FormatUint(tx.Gas(), 16),
		// For EIP-1559 txs, many clients return the effective gasPrice here.
		"gasPrice":             "0x" + effGasPrice.Text(16),
		"maxPriorityFeePerGas": "0x" + tipCap.Text(16),
		"maxFeePerGas":         "0x" + feeCap.Text(16),
		"input":                "0x" + common.Bytes2Hex(tx.Data()),
		"value":                "0x" + tx.Value().Text(16),
		"blockHash":            blockHash.Hex(),
		"blockNumber":          "0x" + strconv.FormatUint(blockNumber, 16),
		"transactionIndex":     "0x" + strconv.FormatUint(uint64(txIndex), 16),
		"v":                    "0x" + v.Text(16),
		"r":                    "0x" + r.Text(16),
		"s":                    "0x" + s.Text(16),
	}

	putJSON(batch, "TX:"+hashHex, map[string]any{
		"tx":      txJSON,
		"receipt": receipt,
	})

	execEVMDirtied = true
	return !res.Failed(), "evm", 0, hashHex, res.UsedGas, receipt.Bloom
}

func effectiveGasPrice(tx *types.Transaction, baseFee *big.Int) *big.Int {
	if tx == nil {
		return big.NewInt(0)
	}
	if baseFee == nil {
		baseFee = big.NewInt(0)
	}
	// Legacy (0) and access list (1) have explicit gasPrice.
	if tx.Type() != types.DynamicFeeTxType {
		gp := tx.GasPrice()
		if gp == nil {
			return big.NewInt(0)
		}
		return new(big.Int).Set(gp)
	}
	feeCap := tx.GasFeeCap()
	tipCap := tx.GasTipCap()
	if feeCap == nil {
		feeCap = big.NewInt(0)
	}
	if tipCap == nil {
		tipCap = big.NewInt(0)
	}
	// effective = min(feeCap, baseFee+tipCap)
	sum := new(big.Int).Add(baseFee, tipCap)
	if sum.Cmp(feeCap) > 0 {
		return new(big.Int).Set(feeCap)
	}
	return sum
}

func storeEVMErrorTxToBatch(batch *leveldb.Batch, hashHex string, errMsg string, raw0x string) {
	putJSON(batch, "TX:"+hashHex, map[string]any{
		"error": errMsg,
		"raw":   raw0x,
	})
}

func storeEVMBlockToBatch(batch *leveldb.Batch, height uint64, blockHash common.Hash, blockTimeSec uint64, stateRootHex string, gasUsed uint64, txHashes []string, logsBloom types.Bloom, logs []any) {
	heightHex := "0x" + strconv.FormatUint(height, 16)
	// JSON-RPC clients expect arrays, not null. Keep these non-nil.
	if txHashes == nil {
		txHashes = []string{}
	}
	if logs == nil {
		logs = []any{}
	}

	parentHash := common.Hash{}
	if height > 0 && databases.STATE != nil {
		prevHex := "0x" + strconv.FormatUint(height-1, 16)
		if b, err := databases.STATE.Get([]byte("EVM_BLOCKHASH:"+prevHex), nil); err == nil {
			s := strings.TrimSpace(string(b))
			if len(s) == 66 && (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
				parentHash = common.HexToHash(s)
			}
		}
	}

	emptyTrie := "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"
	bloomHex := "0x" + common.Bytes2Hex(logsBloom[:])
	blockObj := map[string]any{
		"number":           heightHex,
		"hash":             blockHash.Hex(),
		"parentHash":       parentHash.Hex(),
		"nonce":            "0x0000000000000000",
		"sha3Uncles":       "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
		"logsBloom":        bloomHex,
		"transactionsRoot": emptyTrie,
		"receiptsRoot":     emptyTrie,
		"stateRoot":        stateRootHex,
		"miner":            "0x0000000000000000000000000000000000000000",
		"difficulty":       "0x0",
		"totalDifficulty":  "0x0",
		"extraData":        "0x",
		"size":             "0x0",
		"gasLimit":         "0x1c9c380", // 30_000_000
		"gasUsed":          "0x" + strconv.FormatUint(gasUsed, 16),
		"timestamp":        "0x" + strconv.FormatUint(blockTimeSec, 16),
		"baseFeePerGas":    "0x" + EVMBaseFee().Text(16),
		"transactions":     txHashes,
		"uncles":           []string{},
	}

	putJSON(batch, "EVM_BLOCK:"+heightHex, blockObj)
	if logs != nil {
		putJSON(batch, "EVM_LOGS:"+heightHex, logs)
		// Per-block log indexes for faster eth_getLogs:
		// - EVM_LOGS_ADDR:<address>:<heightHex>
		// - EVM_LOGS_TOPIC0:<topic0>:<heightHex>
		//
		// We index only by address and topic0. Further topic filtering is done in-memory.
		addrIdx := make(map[string][]any)
		topic0Idx := make(map[string][]any)
		for _, entry := range logs {
			m, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			addr, _ := m["address"].(string)
			addr = strings.ToLower(strings.TrimSpace(addr))
			if addr != "" {
				addrIdx[addr] = append(addrIdx[addr], m)
			}
			if topics, ok := m["topics"].([]any); ok && len(topics) > 0 {
				t0, _ := topics[0].(string)
				t0 = strings.ToLower(strings.TrimSpace(t0))
				if t0 != "" {
					topic0Idx[t0] = append(topic0Idx[t0], m)
				}
			}
		}
		for addr, lst := range addrIdx {
			putJSON(batch, "EVM_LOGS_ADDR:"+addr+":"+heightHex, lst)
		}
		for t0, lst := range topic0Idx {
			putJSON(batch, "EVM_LOGS_TOPIC0:"+t0+":"+heightHex, lst)
		}
	}
	batch.Put([]byte("EVM_INDEX:"+blockHash.Hex()), []byte(heightHex))
	batch.Put([]byte("EVM_BLOCKHASH:"+heightHex), []byte(blockHash.Hex()))
	// Convenience: persist root per height too.
	if stateRootHex != "" {
		batch.Put([]byte("EVM_ROOT:"+heightHex), []byte(stateRootHex))
	}
}

func putJSON(batch *leveldb.Batch, key string, v any) {
	if batch == nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	batch.Put([]byte(key), b)
}
