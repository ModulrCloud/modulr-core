package threads

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/modulrcloud/modulr-core/evmvm"
	"github.com/modulrcloud/modulr-core/globals"
)

// SeedEVMGenesisAlloc applies the given alloc into the embedded EVM state DB and returns the new root.
// Intended to be used exactly once during genesis initialization.
func SeedEVMGenesisAlloc(alloc types.GenesisAlloc) (common.Hash, error) {
	if len(alloc) == 0 {
		return types.EmptyRootHash, nil
	}
	evmdbPath := globals.CHAINDATA_PATH + "/DATABASES/EVM"
	root := types.EmptyRootHash

	r, err := evmvm.NewRunner(evmvm.Options{
		DBPath:    evmdbPath,
		DBEngine:  "leveldb",
		StateRoot: &root,
	})
	if err != nil {
		return common.Hash{}, fmt.Errorf("open evm db: %w", err)
	}
	defer r.Close()

	if err := evmvm.ApplyGenesisAlloc(r.StateDB(), alloc); err != nil {
		return common.Hash{}, fmt.Errorf("apply alloc: %w", err)
	}
	newRoot, err := r.Commit()
	if err != nil {
		return common.Hash{}, fmt.Errorf("commit: %w", err)
	}
	return newRoot, nil
}
