package evmvm

import (
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/trie"
)

// CompactTo rewrites the currently committed state root into a fresh database directory.
// This is the practical equivalent of "pruning history": the new DB will contain only
// trie nodes and contract codes reachable from the current root.
//
// Notes:
// - Works for the legacy hash-based state scheme (default).
// - The resulting root is the SAME root; only the backing DB contents are compacted.
func (r *Runner) CompactTo(newDBPath string, newEngine string) (common.Hash, error) {
	if r.root == (common.Hash{}) {
		return common.Hash{}, fmt.Errorf("runner has no root (did you commit?)")
	}
	if tdb := r.cdb.TrieDB(); tdb == nil {
		return common.Hash{}, fmt.Errorf("no trie database configured")
	} else if tdb.Scheme() != rawdb.HashScheme {
		return common.Hash{}, fmt.Errorf("CompactTo only supports hash scheme; got %q", tdb.Scheme())
	}

	// Ensure root is fully on disk in the source DB.
	if err := r.cdb.TrieDB().Commit(r.root, false); err != nil {
		return common.Hash{}, err
	}

	if err := os.MkdirAll(newDBPath, 0o755); err != nil {
		return common.Hash{}, err
	}

	// Special-case: empty root has no reachable nodes/codes. Some geth versions reject syncing it
	// because it's not "available" in DB. For pruning purposes, an empty DB + root marker is enough.
	if r.root == types.EmptyRootHash {
		dst, err := openKV(newDBPath, newEngine, 0, 0, false)
		if err != nil {
			return common.Hash{}, err
		}
		_ = dst.Close()
		if err := writeRootFile(newDBPath, r.root); err != nil {
			return common.Hash{}, err
		}
		return r.root, nil
	}

	// Create destination DB.
	dst, err := openKV(newDBPath, newEngine, 0, 0, false)
	if err != nil {
		return common.Hash{}, err
	}
	defer dst.Close()

	// State sync scheduler: pulls all trie nodes + contract codes reachable from root.
	sync := state.NewStateSync(r.root, dst, nil, r.cdb.TrieDB().Scheme())

	// Source readers.
	nodeReader, err := r.cdb.TrieDB().NodeReader(r.root)
	if err != nil {
		return common.Hash{}, err
	}
	codeReader, err := r.cdb.Reader(r.root)
	if err != nil {
		return common.Hash{}, err
	}

	const batchSize = 10_000
	for {
		paths, nodes, codes := sync.Missing(batchSize)
		if len(paths) == 0 && len(codes) == 0 {
			break
		}
		nodeResults := make([]trie.NodeSyncResult, len(paths))
		codeResults := make([]trie.CodeSyncResult, len(codes))

		for i, h := range codes {
			data, err := codeReader.Code(common.Address{}, h)
			if err != nil {
				return common.Hash{}, err
			}
			if len(data) == 0 {
				return common.Hash{}, fmt.Errorf("missing contract code %x", h)
			}
			codeResults[i] = trie.CodeSyncResult{Hash: h, Data: data}
		}
		for i := 0; i < len(paths); i++ {
			owner, inner := trie.ResolvePath([]byte(paths[i]))
			data, err := nodeReader.Node(owner, inner, nodes[i])
			if err != nil {
				return common.Hash{}, err
			}
			nodeResults[i] = trie.NodeSyncResult{Path: paths[i], Data: data}
		}

		for _, r := range codeResults {
			if err := sync.ProcessCode(r); err != nil {
				return common.Hash{}, err
			}
		}
		for _, r := range nodeResults {
			if err := sync.ProcessNode(r); err != nil {
				return common.Hash{}, err
			}
		}

		batch := dst.NewBatch()
		if err := sync.Commit(batch); err != nil {
			return common.Hash{}, err
		}
		if err := batch.Write(); err != nil {
			return common.Hash{}, err
		}
	}

	// Store root file so a Runner can re-open it easily.
	if err := writeRootFile(newDBPath, r.root); err != nil {
		return common.Hash{}, err
	}

	return r.root, nil
}
