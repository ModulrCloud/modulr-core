package utils

import (
	"encoding/json"
	"strconv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
)

// GetEpochSnapshot retrieves the epoch snapshot (EpochDataSnapshot).
// It first tries to read from the durable STATE DB, then falls back to APPROVEMENT_THREAD_METADATA.
func GetEpochSnapshot(epochId int) *structures.EpochDataSnapshot {
	epochIdStr := strconv.Itoa(epochId)

	// 1. Try durable STATE DB first (source of truth for historical epochs)
	stateKey := []byte(constants.DBKeyPrefixEpochData + epochIdStr)
	if raw, err := databases.STATE.Get(stateKey, nil); err == nil {
		var snapshot structures.EpochDataSnapshot
		if json.Unmarshal(raw, &snapshot) == nil {
			return &snapshot
		}
	}

	// 2. Fallback to APPROVEMENT_THREAD_METADATA (source of truth for current/future epochs)
	atKey := []byte(constants.DBKeyPrefixEpochHandler + epochIdStr)
	if raw, err := databases.APPROVEMENT_THREAD_METADATA.Get(atKey, nil); err == nil {
		var snapshot structures.EpochDataSnapshot
		if json.Unmarshal(raw, &snapshot) == nil {
			return &snapshot
		}
	}

	return nil
}

// LoadNextEpochData retrieves EPOCH_DATA:{N} from APPROVEMENT_THREAD_METADATA.
// It returns nil if the data is not found or cannot be unmarshaled.
func LoadNextEpochData(nextEpochId int) *structures.NextEpochDataHandler {
	key := []byte(constants.DBKeyPrefixEpochData + strconv.Itoa(nextEpochId))

	raw, err := databases.APPROVEMENT_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return nil
	}

	var data structures.NextEpochDataHandler
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil
	}

	return &data
}
