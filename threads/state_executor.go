// State Executor: maps local chain coordinates to absolute positions in STATE.
//
// block_execution.go operates in local coordinates (0-based heights and epoch IDs
// that match the consensus layer's HEIGHT_PROOF and EPOCH_ROTATION_PROOF numbering).
//
// When writing to STATE (BLOCK_INDEX, EPOCH_STATS, EPOCH_DATA), the executor applies
// ChainCursor offsets to produce absolute keys that survive network restarts.
// With default cursor {0, 0} the absolute values equal the local ones.
package threads

import "github.com/modulrcloud/modulr-core/handlers"

func toAbsoluteHeight(localHeight int64) int64 {
	return localHeight + handlers.CHAIN_CURSOR.HeightOffset
}

func toAbsoluteEpochId(localEpochId int) int {
	return localEpochId + handlers.CHAIN_CURSOR.EpochOffset
}
