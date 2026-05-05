package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"
)

type recoveryData struct {
	LastEpochIndex              int    `json:"lastEpochIndex"`
	LastFinalizedAbsoluteHeight int64  `json:"lastFinalizedAbsoluteHeight"`
	TeamSig                     string `json:"teamSig"`
}

func main() {
	chaindataPath := flag.String("chaindata", "", "absolute path to modulr-core chaindata directory")
	teamPubkey := flag.String("team-pubkey", "", "team public key used to verify recovery.json")
	recoveryJSONPath := flag.String("recovery-json", "scripts/recovery/recovery.json", "path to recovery.json")
	flag.Parse()

	if err := run(*chaindataPath, *teamPubkey, *recoveryJSONPath); err != nil {
		fmt.Fprintln(os.Stderr, "recovery failed:", err)
		os.Exit(1)
	}
}

func run(chaindataPath, teamPubkey, recoveryJSONPath string) error {
	if chaindataPath == "" {
		return fmt.Errorf("missing -chaindata")
	}
	if teamPubkey == "" {
		return fmt.Errorf("missing -team-pubkey")
	}
	if !cryptography.IsValidPubKey(teamPubkey) {
		return fmt.Errorf("invalid team pubkey")
	}

	data, err := loadRecoveryData(recoveryJSONPath)
	if err != nil {
		return err
	}
	if err := validateRecoveryData(data, teamPubkey); err != nil {
		return err
	}

	statePath := filepath.Join(chaindataPath, "STATE")
	stateDB, err := leveldb.OpenFile(statePath, nil)
	if err != nil {
		return fmt.Errorf("open STATE db at %s: %w", statePath, err)
	}
	defer stateDB.Close()

	cursor, err := loadChainCursor(stateDB)
	if err != nil {
		return err
	}
	if err := validateLocalCursor(cursor, data); err != nil {
		return err
	}

	deletedDelayedTxs, err := patchStateForRecovery(stateDB, cursor, data)
	if err != nil {
		return err
	}

	fmt.Println("Recovery STATE patch applied successfully")
	fmt.Printf("  lastEpochIndex: %d\n", data.LastEpochIndex)
	fmt.Printf("  lastFinalizedAbsoluteHeight: %d\n", data.LastFinalizedAbsoluteHeight)
	fmt.Printf("  deleted delayed transaction batches: %d\n", deletedDelayedTxs)
	fmt.Printf("  next epoch offset: %d\n", data.LastEpochIndex+1)

	return nil
}

func loadRecoveryData(path string) (recoveryData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return recoveryData{}, fmt.Errorf("read recovery json: %w", err)
	}

	var data recoveryData
	if err := json.Unmarshal(raw, &data); err != nil {
		return recoveryData{}, fmt.Errorf("parse recovery json: %w", err)
	}

	return data, nil
}

func validateRecoveryData(data recoveryData, teamPubkey string) error {
	if data.LastEpochIndex < 0 {
		return fmt.Errorf("lastEpochIndex must be non-negative")
	}
	if data.LastFinalizedAbsoluteHeight < 0 {
		return fmt.Errorf("lastFinalizedAbsoluteHeight must be non-negative")
	}
	if data.TeamSig == "" {
		return fmt.Errorf("teamSig is empty")
	}

	payload := buildRecoveryPayload(data.LastEpochIndex, data.LastFinalizedAbsoluteHeight)
	if !cryptography.VerifySignature(payload, teamPubkey, data.TeamSig) {
		return fmt.Errorf("invalid team signature for payload %q", payload)
	}

	return nil
}

func buildRecoveryPayload(lastEpochIndex int, lastFinalizedAbsoluteHeight int64) string {
	return fmt.Sprintf("RECOVERY_RESTART:%d:%d", lastEpochIndex, lastFinalizedAbsoluteHeight)
}

func loadChainCursor(stateDB *leveldb.DB) (structures.ChainCursor, error) {
	raw, err := stateDB.Get([]byte(constants.DBKeyChainCursor), nil)
	if err != nil {
		return structures.ChainCursor{}, fmt.Errorf("load %s: %w", constants.DBKeyChainCursor, err)
	}

	var cursor structures.ChainCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return structures.ChainCursor{}, fmt.Errorf("parse %s: %w", constants.DBKeyChainCursor, err)
	}

	return cursor, nil
}

func validateLocalCursor(cursor structures.ChainCursor, data recoveryData) error {
	if cursor.Statistics == nil {
		return fmt.Errorf("CHAIN_CURSOR.statistics is nil")
	}

	if cursor.Statistics.LastHeight < data.LastFinalizedAbsoluteHeight {
		return fmt.Errorf(
			"local node is below recovery height: local=%d required=%d; start core again and wait for synchronization",
			cursor.Statistics.LastHeight,
			data.LastFinalizedAbsoluteHeight,
		)
	}
	if cursor.Statistics.LastHeight > data.LastFinalizedAbsoluteHeight {
		return fmt.Errorf(
			"local node is above recovery height: local=%d required=%d; restore/rollback chaindata to the recovery point first",
			cursor.Statistics.LastHeight,
			data.LastFinalizedAbsoluteHeight,
		)
	}

	localAbsoluteEpoch := cursor.EpochOffset + cursor.EpochDataHandler.Id
	if localAbsoluteEpoch < data.LastEpochIndex {
		return fmt.Errorf(
			"local node is below recovery epoch: local=%d required=%d; start core again and wait for synchronization",
			localAbsoluteEpoch,
			data.LastEpochIndex,
		)
	}
	if localAbsoluteEpoch > data.LastEpochIndex {
		return fmt.Errorf(
			"local node is above recovery epoch: local=%d required=%d; restore/rollback chaindata to the recovery point first",
			localAbsoluteEpoch,
			data.LastEpochIndex,
		)
	}

	if cursor.EpochStatistics == nil {
		return fmt.Errorf("CHAIN_CURSOR.epochStatistics is nil; cannot persist %s%d", constants.DBKeyPrefixEpochStats, data.LastEpochIndex)
	}

	return nil
}

func patchStateForRecovery(stateDB *leveldb.DB, cursor structures.ChainCursor, data recoveryData) (int, error) {
	batch := new(leveldb.Batch)

	deletedDelayedTxs, err := deleteDelayedTransactionsFromEpoch(stateDB, batch, data.LastEpochIndex+1)
	if err != nil {
		return 0, err
	}

	statsBytes, err := json.Marshal(cursor.EpochStatistics)
	if err != nil {
		return 0, fmt.Errorf("marshal epoch statistics: %w", err)
	}
	batch.Put([]byte(constants.DBKeyPrefixEpochStats+strconv.Itoa(data.LastEpochIndex)), statsBytes)

	cursor.EpochDataHandler = structures.EpochDataHandler{}
	cursor.EpochOffset = data.LastEpochIndex + 1
	cursor.Statistics.LastHeight = data.LastFinalizedAbsoluteHeight
	cursor.EpochStatistics = nil

	cursorBytes, err := json.Marshal(cursor)
	if err != nil {
		return 0, fmt.Errorf("marshal chain cursor: %w", err)
	}
	batch.Put([]byte(constants.DBKeyChainCursor), cursorBytes)

	if err := stateDB.Write(batch, &opt.WriteOptions{Sync: true}); err != nil {
		return 0, fmt.Errorf("write recovery batch: %w", err)
	}

	return deletedDelayedTxs, nil
}

func deleteDelayedTransactionsFromEpoch(stateDB *leveldb.DB, batch *leveldb.Batch, fromEpoch int) (int, error) {
	prefix := []byte(constants.DBKeyPrefixDelayedTransactions)
	it := stateDB.NewIterator(util.BytesPrefix(prefix), nil)
	defer it.Release()

	deleted := 0
	for it.Next() {
		key := string(it.Key())
		rawEpoch := strings.TrimPrefix(key, constants.DBKeyPrefixDelayedTransactions)

		epoch, err := strconv.Atoi(rawEpoch)
		if err != nil {
			continue
		}
		if epoch < fromEpoch {
			continue
		}

		keyCopy := append([]byte(nil), it.Key()...)
		batch.Delete(keyCopy)
		deleted++
	}
	if err := it.Error(); err != nil {
		return 0, fmt.Errorf("iterate delayed transactions: %w", err)
	}

	return deleted, nil
}
