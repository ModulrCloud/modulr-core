package threads

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/evmvm"
	"github.com/modulrcloud/modulr-core/globals"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/syndtr/goleveldb/leveldb"
)

var (
	execEVMRunner   *evmvm.Runner
	execEVMDirtied  bool
	execEVMRunnerMu sync.Mutex
)

// EVMLock/EVMUnlock guard access to the embedded EVM runner/state.
// The EVM runner is not safe for concurrent use: block execution and RPC may run in parallel.
func EVMLock()   { execEVMRunnerMu.Lock() }
func EVMUnlock() { execEVMRunnerMu.Unlock() }

// EVMRunnerUnsafe returns the singleton embedded EVM runner.
// Callers MUST hold EVMLock() while using the returned runner.
func EVMRunnerUnsafe() (*evmvm.Runner, error) { return ensureEVMRunner() }

func ensureEVMRunner() (*evmvm.Runner, error) {
	if execEVMRunner != nil {
		return execEVMRunner, nil
	}
	root := loadEVMRootFromState(databases.STATE)
	evmdbPath := globals.CHAINDATA_PATH + "/DATABASES/EVM"
	r, err := evmvm.NewRunner(evmvm.Options{
		DBPath:    evmdbPath,
		DBEngine:  "leveldb",
		StateRoot: &root,
		// Fee hints & London semantics for wallet UX.
		BaseFee:  EVMBaseFee(),
		GasPrice: EVMBaseFee(),
	})
	if err != nil {
		return nil, err
	}
	execEVMRunner = r
	execEVMDirtied = false
	return r, nil
}

func loadEVMRootFromState(db *leveldb.DB) common.Hash {
	if db == nil {
		return types.EmptyRootHash
	}
	b, err := db.Get([]byte(constants.DBKeyEVMRoot), nil)
	if err != nil {
		return types.EmptyRootHash
	}
	s := strings.TrimSpace(string(b))
	if len(s) == 66 && (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
		return common.HexToHash(s)
	}
	return types.EmptyRootHash
}

func storeEVMRootToBatch(batch *leveldb.Batch, root common.Hash) {
	if batch == nil {
		return
	}
	batch.Put([]byte(constants.DBKeyEVMRoot), []byte(root.Hex()))
}

// executeEVMTransaction executes an EVM call against the embedded EVM state.
//
// Expected payload (minimal):
//
//	{
//	  "type": "evm",
//	  "to": "0x....",        // required, 20-byte hex address
//	  "data": "0x....",      // optional, calldata hex
//	  "gas": 3000000         // optional, defaults
//	}
//
// Returns: success, reason, feeSpent
func executeEVMTransaction(senderPubKey string, payload map[string]any, fee uint64) (bool, string, uint64) {
	r, err := ensureEVMRunner()
	if err != nil {
		return false, "evm init failed: " + err.Error(), 0
	}
	caller, err := evmvm.ModulrPubKeyToEVMAddress(senderPubKey)
	if err != nil {
		return false, "evm caller mapping failed: " + err.Error(), 0
	}

	toStr, ok := payload["to"].(string)
	if !ok || !strings.HasPrefix(toStr, "0x") || len(toStr) != 42 {
		return false, "evm payload: invalid or missing to", 0
	}
	to := common.HexToAddress(toStr)

	data := []byte{}
	if raw, ok := payload["data"]; ok {
		if s, ok := raw.(string); ok && s != "" {
			s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
			if s != "" {
				b, err := hex.DecodeString(s)
				if err != nil {
					return false, "evm payload: invalid data hex", 0
				}
				data = b
			}
		}
	}

	gas := uint64(3_000_000)
	if raw, ok := payload["gas"]; ok {
		switch v := raw.(type) {
		case float64:
			if v > 0 {
				gas = uint64(v)
			}
		case string:
			if parsed, err := strconv.ParseUint(v, 10, 64); err == nil && parsed > 0 {
				gas = parsed
			}
		}
	}

	_, _, execErr := r.Execute(caller, to, data, gas)
	if execErr != nil {
		// Encode a short reason; keep it deterministic.
		j, _ := json.Marshal(map[string]string{"evmError": execErr.Error()})
		return false, string(j), 0
	}
	execEVMDirtied = true
	return true, "", fee
}
