package structures

// ChainCursor is the permanent state descriptor stored in STATE DB under CHAIN_CURSOR key.
//
// Offset fields (HeightOffset, EpochOffset) map local chain coordinates to
// absolute positions in STATE so that BLOCK_INDEX, EPOCH_STATS, EPOCH_DATA keys
// remain continuous across network restarts (eras).
//
// Permanent fields (CoreMajorVersion, Statistics, NetworkParameters) are the
// single source of truth for world-state metadata. They are read and written
// directly — not duplicated in ExecutionThreadMetadataHandler.
type ChainCursor struct {
	HeightOffset int64 `json:"heightOffset"`
	EpochOffset  int   `json:"epochOffset"`

	CoreMajorVersion  int               `json:"coreMajorVersion"`
	Statistics        *Statistics       `json:"statistics,omitempty"`
	NetworkParameters NetworkParameters `json:"networkParameters"`
}
