package evmvm

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/ethdb/leveldb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
)

type Options struct {
	ChainConfig *params.ChainConfig
	VMConfig    vm.Config

	DBPath    string // directory path
	DBEngine  string // "leveldb" (default). Pebble is intentionally not used in modulr-core to keep deps smaller.
	DBCacheMB int
	DBHandles int
	ReadOnly  bool
	StateRoot *common.Hash

	// Block context
	BlockNumber uint64
	Time        uint64
	GasLimit    uint64
	Coinbase    common.Address
	BaseFee     *big.Int
	BlobBaseFee *big.Int
	Difficulty  *big.Int
	Random      *common.Hash

	// Tx context
	Origin     common.Address
	GasPrice   *big.Int
	BlobFeeCap *big.Int
	BlobHashes []common.Hash
}

type Runner struct {
	chainConfig *params.ChainConfig
	vmConfig    vm.Config

	blockCtx vm.BlockContext
	txCtx    vm.TxContext
	rules    params.Rules

	dbPath  string
	kvdb    ethdb.Database
	root    common.Hash
	cdb     *state.CachingDB
	statedb *state.StateDB
	evm     *vm.EVM
}

func NewRunner(opts Options) (*Runner, error) {
	chainConfig := opts.ChainConfig
	if chainConfig == nil {
		chainConfig = params.AllDevChainProtocolChanges
	}

	blockNumber := opts.BlockNumber
	if blockNumber == 0 {
		blockNumber = 1
	}
	time := opts.Time
	if time == 0 {
		time = 1
	}
	gasLimit := opts.GasLimit
	if gasLimit == 0 {
		gasLimit = 30_000_000
	}
	baseFee := opts.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(0)
	}
	blobBaseFee := opts.BlobBaseFee
	if blobBaseFee == nil {
		blobBaseFee = big.NewInt(0)
	}
	diff := opts.Difficulty
	if diff == nil {
		diff = big.NewInt(0)
	}

	origin := opts.Origin
	if origin == (common.Address{}) {
		origin = common.HexToAddress("0x000000000000000000000000000000000000c0de")
	}
	gasPrice := opts.GasPrice
	if gasPrice == nil {
		gasPrice = big.NewInt(0)
	}
	blobFeeCap := opts.BlobFeeCap
	if blobFeeCap == nil {
		blobFeeCap = big.NewInt(0)
	}

	blockCtx := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash: func(uint64) common.Hash {
			return common.Hash{}
		},
		Coinbase:    opts.Coinbase,
		GasLimit:    gasLimit,
		BlockNumber: new(big.Int).SetUint64(blockNumber),
		Time:        time,
		Difficulty:  diff,
		BaseFee:     baseFee,
		BlobBaseFee: blobBaseFee,
		Random:      opts.Random,
	}
	rules := chainConfig.Rules(blockCtx.BlockNumber, blockCtx.Random != nil, blockCtx.Time)

	kvdb, root, cdb, statedb, err := openStateDB(opts)
	if err != nil {
		return nil, err
	}

	evm := vm.NewEVM(blockCtx, statedb, chainConfig, opts.VMConfig)
	txCtx := vm.TxContext{
		Origin:     origin,
		GasPrice:   gasPrice,
		BlobFeeCap: blobFeeCap,
		BlobHashes: opts.BlobHashes,
	}
	evm.SetTxContext(txCtx)

	return &Runner{
		chainConfig: chainConfig,
		vmConfig:    opts.VMConfig,
		blockCtx:    blockCtx,
		txCtx:       txCtx,
		rules:       rules,
		dbPath:      opts.DBPath,
		kvdb:        kvdb,
		root:        root,
		cdb:         cdb,
		statedb:     statedb,
		evm:         evm,
	}, nil
}

func (r *Runner) StateDB() *state.StateDB { return r.statedb }
func (r *Runner) Root() common.Hash       { return r.root }
func (r *Runner) ChainConfig() *params.ChainConfig {
	return r.chainConfig
}

// SetBlockContext updates the EVM block environment (NUMBER, TIMESTAMP, BASEFEE, GASLIMIT).
// Caller is responsible for external synchronization if Runner is shared.
func (r *Runner) SetBlockContext(blockNumber uint64, time uint64, gasLimit uint64, baseFee *big.Int) {
	if blockNumber == 0 {
		blockNumber = 1
	}
	if time == 0 {
		time = 1
	}
	if gasLimit == 0 {
		gasLimit = 30_000_000
	}
	if baseFee == nil {
		baseFee = big.NewInt(0)
	}
	r.blockCtx.BlockNumber = new(big.Int).SetUint64(blockNumber)
	r.blockCtx.Time = time
	r.blockCtx.GasLimit = gasLimit
	r.blockCtx.BaseFee = new(big.Int).Set(baseFee)
	// Update fork rules and EVM context copy.
	r.rules = r.chainConfig.Rules(r.blockCtx.BlockNumber, r.blockCtx.Random != nil, r.blockCtx.Time)
	r.evm.Context = r.blockCtx
}

func (r *Runner) Close() error {
	if r.kvdb != nil {
		return r.kvdb.Close()
	}
	return nil
}

func (r *Runner) InstallCode(addr common.Address, runtimeCode []byte) {
	if !r.statedb.Exist(addr) {
		r.statedb.CreateAccount(addr)
	}
	r.statedb.SetCode(addr, runtimeCode, tracing.CodeChangeGenesis)
}

func (r *Runner) Fund(addr common.Address, amount *uint256.Int) {
	if !r.statedb.Exist(addr) {
		r.statedb.CreateAccount(addr)
	}
	r.statedb.AddBalance(addr, amount, tracing.BalanceIncreaseGenesisBalance)
}

func (r *Runner) Execute(caller common.Address, contract common.Address, calldata []byte, gas uint64) (ret []byte, gasLeft uint64, err error) {
	if gas == 0 {
		return nil, 0, errors.New("gas must be > 0")
	}
	if !r.statedb.Exist(caller) {
		r.statedb.CreateAccount(caller)
	}
	precompiles := vm.ActivePrecompiles(r.rules)
	dst := contract
	r.statedb.Prepare(r.rules, r.txCtx.Origin, r.blockCtx.Coinbase, &dst, precompiles, types.AccessList{})
	return r.evm.Call(caller, contract, calldata, gas, uint256.NewInt(0))
}

func (r *Runner) Create(caller common.Address, initCode []byte, gas uint64) (ret []byte, created common.Address, gasLeft uint64, err error) {
	if gas == 0 {
		return nil, common.Address{}, 0, errors.New("gas must be > 0")
	}
	if !r.statedb.Exist(caller) {
		r.statedb.CreateAccount(caller)
	}
	precompiles := vm.ActivePrecompiles(r.rules)
	r.statedb.Prepare(r.rules, r.txCtx.Origin, r.blockCtx.Coinbase, nil, precompiles, types.AccessList{})
	return r.evm.Create(caller, initCode, gas, uint256.NewInt(0))
}

func (r *Runner) Simulate(caller common.Address, contract common.Address, calldata []byte, gas uint64) (ret []byte, gasLeft uint64, err error) {
	snap := r.statedb.Snapshot()
	ret, gasLeft, err = r.Execute(caller, contract, calldata, gas)
	r.statedb.RevertToSnapshot(snap)
	return ret, gasLeft, err
}

func (r *Runner) SimulateCreate(caller common.Address, initCode []byte, gas uint64) (ret []byte, created common.Address, gasLeft uint64, err error) {
	snap := r.statedb.Snapshot()
	ret, created, gasLeft, err = r.Create(caller, initCode, gas)
	r.statedb.RevertToSnapshot(snap)
	return ret, created, gasLeft, err
}

// SimulateSignedTx executes a signed Ethereum transaction against the current state and reverts changes.
// It still performs nonce/balance/gas checks as per the EVM rules (core.ApplyMessage).
func (r *Runner) SimulateSignedTx(tx *types.Transaction, chainID *big.Int) (res *core.ExecutionResult, logs []*types.Log, sender common.Address, err error) {
	snap := r.statedb.Snapshot()
	defer r.statedb.RevertToSnapshot(snap)
	return r.ApplySignedTx(tx, chainID, 0, 0, common.Hash{}, 0, true)
}

// ApplySignedTx executes a signed Ethereum transaction against the current state.
// If readOnly is true, state changes are reverted (but callers should prefer SimulateSignedTx).
func (r *Runner) ApplySignedTx(tx *types.Transaction, chainID *big.Int, txIndex int, blockNumber uint64, blockHash common.Hash, blockTime uint64, readOnly bool) (res *core.ExecutionResult, logs []*types.Log, sender common.Address, err error) {
	if chainID == nil {
		chainID = big.NewInt(1)
	}
	signer := types.LatestSignerForChainID(chainID)
	sender, err = types.Sender(signer, tx)
	if err != nil {
		return nil, nil, common.Address{}, err
	}

	snap := r.statedb.Snapshot()
	if readOnly {
		defer r.statedb.RevertToSnapshot(snap)
	}

	r.statedb.SetTxContext(tx.Hash(), txIndex)
	msg, err := core.TransactionToMessage(tx, signer, r.blockCtx.BaseFee)
	if err != nil {
		return nil, nil, common.Address{}, err
	}
	// Ensure ORIGIN/GASPRICE opcodes match this tx (EVM keeps tx context separately from msg).
	r.evm.SetTxContext(vm.TxContext{
		Origin:   sender,
		GasPrice: msg.GasPrice,
	})
	res, err = core.ApplyMessage(r.evm, msg, new(core.GasPool).AddGas(msg.GasLimit))
	if err != nil {
		return res, nil, sender, err
	}
	logs = r.statedb.GetLogs(tx.Hash(), blockNumber, blockHash, blockTime)
	return res, logs, sender, nil
}

func (r *Runner) Commit() (common.Hash, error) {
	root, err := r.statedb.Commit(r.blockCtx.BlockNumber.Uint64(), true, true)
	if err != nil {
		return common.Hash{}, err
	}
	// Flush trie nodes to disk so this root is available after restart.
	if tdb := r.cdb.TrieDB(); tdb != nil {
		if err := tdb.Commit(root, false); err != nil {
			return common.Hash{}, err
		}
	}
	newState, err := state.New(root, r.cdb)
	if err != nil {
		return common.Hash{}, err
	}
	r.statedb = newState
	r.evm.StateDB = newState
	r.root = root
	if r.dbPath != "" {
		_ = writeRootFile(r.dbPath, root) // best-effort convenience; consensus should store root elsewhere
	}
	return root, nil
}

func openStateDB(opts Options) (ethdb.Database, common.Hash, *state.CachingDB, *state.StateDB, error) {
	if strings.TrimSpace(opts.DBPath) != "" {
		path := opts.DBPath
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, common.Hash{}, nil, nil, err
		}
		kvdb, err := openKV(path, opts.DBEngine, opts.DBCacheMB, opts.DBHandles, opts.ReadOnly)
		if err != nil {
			return nil, common.Hash{}, nil, nil, err
		}
		tdb := triedb.NewDatabase(kvdb, nil)
		cdb := state.NewDatabase(tdb, nil)

		root := types.EmptyRootHash
		if opts.StateRoot != nil {
			root = *opts.StateRoot
		} else if loaded, ok, err := readRootFile(path); err != nil {
			kvdb.Close()
			return nil, common.Hash{}, nil, nil, err
		} else if ok {
			root = loaded
		}
		statedb, err := state.New(root, cdb)
		if err != nil {
			kvdb.Close()
			return nil, common.Hash{}, nil, nil, err
		}
		return kvdb, root, cdb, statedb, nil
	}

	cdb := state.NewDatabaseForTesting()
	statedb, err := state.New(types.EmptyRootHash, cdb)
	if err != nil {
		return nil, common.Hash{}, nil, nil, err
	}
	return nil, types.EmptyRootHash, cdb, statedb, nil
}

func openKV(path string, engine string, cacheMB int, handles int, readonly bool) (ethdb.Database, error) {
	if cacheMB == 0 {
		cacheMB = 128
	}
	if handles == 0 {
		handles = 64
	}
	// modulr uses leveldb; default to it.
	// Note: even if a preexisting DB was created as pebble, modulr-core currently doesn't support it.
	if engine == "" {
		engine = rawdb.DBLeveldb
	}
	switch engine {
	case rawdb.DBLeveldb:
		kv, err := leveldb.New(path, cacheMB, handles, "modulr-evm", readonly)
		if err != nil {
			return nil, err
		}
		return rawdb.NewDatabase(kv), nil
	default:
		return nil, fmt.Errorf("unknown DBEngine %q (expected %q)", engine, rawdb.DBLeveldb)
	}
}

const rootFileName = "evmbox-state-root.txt"

func readRootFile(dbPath string) (common.Hash, bool, error) {
	b, err := os.ReadFile(filepath.Join(dbPath, rootFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return common.Hash{}, false, nil
		}
		return common.Hash{}, false, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return common.Hash{}, false, nil
	}
	// accept 0x + 64 hex
	if len(s) != 66 || !(strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
		return common.Hash{}, false, fmt.Errorf("invalid state root in %s: %q", rootFileName, s)
	}
	return common.HexToHash(s), true, nil
}

func writeRootFile(dbPath string, root common.Hash) error {
	tmp := filepath.Join(dbPath, rootFileName+".tmp")
	final := filepath.Join(dbPath, rootFileName)
	if err := os.WriteFile(tmp, []byte(root.Hex()+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}
