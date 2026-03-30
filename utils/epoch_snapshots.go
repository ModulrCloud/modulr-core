package utils

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/syndtr/goleveldb/leveldb"
)

// GetEpochSnapshot retrieves the epoch snapshot (EpochDataSnapshot) from the APPROVEMENT_THREAD_METADATA DB.
// It returns nil if the snapshot is not found or cannot be unmarshaled.
func GetEpochSnapshot(epochId int) *structures.EpochDataSnapshot {
	key := []byte(constants.DBKeyPrefixEpochHandler + strconv.Itoa(epochId))

	raw, err := databases.APPROVEMENT_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return nil
	}

	var snapshot structures.EpochDataSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil
	}

	return &snapshot
}

// AddEpochSnapshotToBatch marshals the epoch snapshot and adds it to the provided LevelDB batch.
// This is useful for atomicity during epoch transitions or genesis initialization.
func AddEpochSnapshotToBatch(batch *leveldb.Batch, epochId int, snapshot *structures.EpochDataSnapshot) error {
	valBytes, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal epoch snapshot: %w", err)
	}

	key := []byte(constants.DBKeyPrefixEpochHandler + strconv.Itoa(epochId))
	batch.Put(key, valBytes)

	return nil
}
