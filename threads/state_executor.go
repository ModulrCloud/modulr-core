// State Executor: manages the permanent world-state layer in STATE DB.
//
// block_execution.go operates in local coordinates (0-based heights and epoch IDs
// that match the consensus layer's HEIGHT_PROOF and EPOCH_ROTATION_PROOF numbering).
//
// When writing to STATE (BLOCK_INDEX, EPOCH_STATS, EPOCH_DATA), the executor applies
// ChainCursor offsets to produce absolute keys that survive network restarts.
// With default cursor {0, 0} the absolute values equal the local ones.
//
// ChainCursor is the single source of truth for permanent fields (CoreMajorVersion,
// Statistics, NetworkParameters). It is persisted atomically in the same LevelDB batch
// as other STATE writes.
package threads

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/syndtr/goleveldb/leveldb"
)

func toAbsoluteHeight(localHeight int64) int64 {
	return localHeight + handlers.CHAIN_CURSOR.HeightOffset
}

func toAbsoluteEpochId(localEpochId int) int {
	return localEpochId + handlers.CHAIN_CURSOR.EpochOffset
}

// persistChainCursor serializes the current ChainCursor and appends it to the batch.
func persistChainCursor(batch *leveldb.Batch) {
	if data, err := json.Marshal(handlers.CHAIN_CURSOR); err == nil {
		batch.Put([]byte(constants.DBKeyChainCursor), data)
	}
}
