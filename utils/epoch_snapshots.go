package utils

import (
	"encoding/json"
	"strconv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
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
