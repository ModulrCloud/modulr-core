package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/holiman/uint256"
	"github.com/modulrcloud/modulr-core/evmvm"
)

func TestEVMCompactToPrunesOldRoot(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	r, err := evmvm.NewRunner(evmvm.Options{
		DBPath:   srcDir,
		DBEngine: "leveldb",
	})
	if err != nil {
		t.Fatalf("NewRunner(src): %v", err)
	}

	addrA := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	codeA := []byte{0x60, 0x00, 0x60, 0x00, 0xf3} // RETURN(0,0)

	r.Fund(addrA, uint256.NewInt(123))
	r.InstallCode(addrA, codeA)

	root1, err := r.Commit()
	if err != nil {
		_ = r.Close()
		t.Fatalf("Commit(root1): %v", err)
	}

	// Create a second root that does not include addrA, so compacting root2 should prune root1.
	r.StateDB().SelfDestruct(addrA)
	// Ensure root2 is non-empty (some geth versions don't allow syncing EmptyRootHash).
	addrB := common.HexToAddress("0x00000000000000000000000000000000000000bb")
	r.Fund(addrB, uint256.NewInt(1))

	root2, err := r.Commit()
	if err != nil {
		_ = r.Close()
		t.Fatalf("Commit(root2): %v", err)
	}

	compactedRoot, err := r.CompactTo(dstDir, "leveldb")
	if err != nil {
		_ = r.Close()
		t.Fatalf("CompactTo: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close(src): %v", err)
	}
	if compactedRoot != root2 {
		t.Fatalf("unexpected compacted root: got %s want %s", compactedRoot.Hex(), root2.Hex())
	}

	// Verify root marker file was written.
	b, err := os.ReadFile(filepath.Join(dstDir, "evmbox-state-root.txt"))
	if err != nil {
		t.Fatalf("read root file: %v", err)
	}
	if got := string(b); got != root2.Hex()+"\n" {
		t.Fatalf("unexpected root file content: %q", got)
	}

	// New runner on compacted DB at root2 should open and show addrA pruned.
	r2, err := evmvm.NewRunner(evmvm.Options{
		DBPath:    dstDir,
		DBEngine:  "leveldb",
		StateRoot: &root2,
	})
	if err != nil {
		t.Fatalf("NewRunner(dst, root2): %v", err)
	}
	if bal := r2.StateDB().GetBalance(addrA); bal != nil && !bal.IsZero() {
		_ = r2.Close()
		t.Fatalf("addrA balance not pruned: %s", bal.String())
	}
	if code := r2.StateDB().GetCode(addrA); len(code) != 0 {
		_ = r2.Close()
		t.Fatalf("addrA code not pruned: len=%d", len(code))
	}
	if err := r2.Close(); err != nil {
		t.Fatalf("Close(dst): %v", err)
	}

	// Old root should not be openable from the compacted DB.
	if _, err := evmvm.NewRunner(evmvm.Options{
		DBPath:    dstDir,
		DBEngine:  "leveldb",
		StateRoot: &root1,
	}); err == nil {
		t.Fatalf("expected NewRunner(dst, root1) to fail after pruning, but got nil error")
	}
}

func TestEVMCompactToEmptyRoot(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	r, err := evmvm.NewRunner(evmvm.Options{
		DBPath:   srcDir,
		DBEngine: "leveldb",
	})
	if err != nil {
		t.Fatalf("NewRunner(src): %v", err)
	}
	// Commit empty state so runner has a root.
	root, err := r.Commit()
	if err != nil {
		_ = r.Close()
		t.Fatalf("Commit(empty): %v", err)
	}

	compactedRoot, err := r.CompactTo(dstDir, "leveldb")
	if err != nil {
		_ = r.Close()
		t.Fatalf("CompactTo(empty): %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close(src): %v", err)
	}
	if compactedRoot != root {
		t.Fatalf("unexpected compacted root: got %s want %s", compactedRoot.Hex(), root.Hex())
	}

	b, err := os.ReadFile(filepath.Join(dstDir, "evmbox-state-root.txt"))
	if err != nil {
		t.Fatalf("read root file: %v", err)
	}
	if got := string(b); got != root.Hex()+"\n" {
		t.Fatalf("unexpected root file content: %q", got)
	}
}
