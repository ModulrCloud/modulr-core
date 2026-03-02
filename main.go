package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/evmvm"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/syndtr/goleveldb/leveldb"
)

func main() {
	var (
		evmCompactTo    = flag.String("evm-compact-to", "", "rewrite current embedded EVM state into a fresh DB directory (prune history)")
		evmPruneInPlace = flag.Bool("evm-prune-in-place", false, "compact embedded EVM state and replace the existing EVM DB directory (creates a backup)")
		evmBackupSuffix = flag.String("evm-backup-suffix", "", "optional suffix for EVM DB backup directory (default: timestamp)")
		evmEngine       = flag.String("evm-engine", "leveldb", "EVM db engine (currently only leveldb supported)")
	)
	flag.Parse()

	// Maintenance mode: compact/prune embedded EVM state DB without starting the node.
	if strings.TrimSpace(*evmCompactTo) != "" || *evmPruneInPlace {
		if err := runEVMPruneMode(strings.TrimSpace(*evmCompactTo), *evmPruneInPlace, strings.TrimSpace(*evmBackupSuffix), strings.TrimSpace(*evmEngine)); err != nil {
			fmt.Fprintln(os.Stderr, "EVM prune/compact failed:", err)
			os.Exit(1)
		}
		return
	}

	configsRawJson, readError := os.ReadFile(globals.CHAINDATA_PATH + "/configs.json")

	if readError != nil {

		panic("Error while reading configs: " + readError.Error())

	}

	if err := json.Unmarshal(configsRawJson, &globals.CONFIGURATION); err != nil {

		panic("Error with configs parsing: " + err.Error())

	}

	anchorsRawJson, readError := os.ReadFile(globals.CHAINDATA_PATH + "/anchors.json")

	if readError != nil {

		panic("Error while reading anchors: " + readError.Error())

	}

	var anchorsList []structures.Anchor

	if err := json.Unmarshal(anchorsRawJson, &anchorsList); err != nil {

		panic("Error with anchors parsing: " + err.Error())

	}

	globals.ANCHORS = anchorsList

	globals.ANCHORS_PUBKEYS = make([]string, len(anchorsList))

	for i, anchor := range anchorsList {
		globals.ANCHORS_PUBKEYS[i] = anchor.Pubkey
	}

	genesisRawJson, readError := os.ReadFile(globals.CHAINDATA_PATH + "/genesis.json")

	if readError != nil {

		panic("Error while reading genesis: " + readError.Error())

	}

	if err := json.Unmarshal(genesisRawJson, &globals.GENESIS); err != nil {

		panic("Error with genesis parsing: " + err.Error())

	}

	currentUser, _ := user.Current()

	statsStringToPrint := fmt.Sprintf("System info \x1b[31mgolang:%s \033[36;1m/\x1b[31m os info:%s # %s # cpu:%d \033[36;1m/\x1b[31m runned as:%s\x1b[0m", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), currentUser.Username)

	utils.LogWithTime(statsStringToPrint, utils.CYAN_COLOR)

	go signalHandler()

	// Function that runs the main logic

	RunBlockchain()

}

func runEVMPruneMode(compactTo string, pruneInPlace bool, backupSuffix string, engine string) error {
	// STATE DB is used only to read EVM_ROOT marker so we can open EVM state at the correct root.
	stateDBPath := globals.CHAINDATA_PATH + "/DATABASES/STATE"
	st, err := leveldb.OpenFile(stateDBPath, nil)
	if err != nil {
		return fmt.Errorf("open STATE db at %s: %w", stateDBPath, err)
	}
	defer st.Close()

	root := loadEVMRootFromStateDB(st)
	evmdbPath := globals.CHAINDATA_PATH + "/DATABASES/EVM"

	r, err := evmvm.NewRunner(evmvm.Options{
		DBPath:    evmdbPath,
		DBEngine:  engine,
		StateRoot: &root,
	})
	if err != nil {
		return fmt.Errorf("open embedded EVM db at %s: %w", evmdbPath, err)
	}

	if pruneInPlace {
		ts := time.Now().UTC().Format("20060102T150405Z")
		// Important: tmp dir MUST be on the same filesystem to allow atomic rename swap.
		tmpPath := evmdbPath + ".prune_tmp." + ts

		newRoot, err := r.CompactTo(tmpPath, engine)
		if err != nil {
			_ = r.Close()
			return fmt.Errorf("compact to %s: %w", tmpPath, err)
		}

		// Close DB so directory renames work reliably (especially on Windows).
		if err := r.Close(); err != nil {
			return fmt.Errorf("close embedded EVM db: %w", err)
		}

		suf := backupSuffix
		if suf == "" {
			suf = ts
		}
		backupPath := evmdbPath + ".bak." + suf

		// Rename old EVM DB aside, then atomically swap compacted DB in place.
		if err := os.Rename(evmdbPath, backupPath); err != nil {
			return fmt.Errorf("rename old EVM db to backup (%s -> %s): %w", evmdbPath, backupPath, err)
		}
		if err := os.Rename(tmpPath, evmdbPath); err != nil {
			return fmt.Errorf("activate compacted EVM db (%s -> %s): %w", tmpPath, evmdbPath, err)
		}

		fmt.Printf("evmRoot=%s\n", newRoot.Hex())
		fmt.Printf("evmDB=%s\n", evmdbPath)
		fmt.Printf("backupDB=%s\n", backupPath)
		fmt.Printf("rootFile=%s\n", filepath.Join(evmdbPath, "evmbox-state-root.txt"))
		return nil
	}

	if compactTo == "" {
		_ = r.Close()
		return fmt.Errorf("missing --evm-compact-to (or provide --evm-prune-in-place)")
	}
	newRoot, err := r.CompactTo(compactTo, engine)
	if err != nil {
		_ = r.Close()
		return err
	}
	if err := r.Close(); err != nil {
		return fmt.Errorf("close embedded EVM db: %w", err)
	}
	fmt.Printf("evmRoot=%s\n", newRoot.Hex())
	fmt.Printf("compactedDB=%s\n", compactTo)
	fmt.Printf("rootFile=%s\n", filepath.Join(compactTo, "evmbox-state-root.txt"))
	return nil
}

func loadEVMRootFromStateDB(db *leveldb.DB) common.Hash {
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

// Function to handle Ctrl+C interruptions
func signalHandler() {

	sig := make(chan os.Signal, 1)

	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	<-sig

	utils.GracefulShutdown()

}
