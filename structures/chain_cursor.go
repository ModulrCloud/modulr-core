package structures

// ChainCursor is the permanent state descriptor stored in STATE DB under CHAIN_CURSOR key.
//
// Offset fields (HeightOffset, EpochOffset) map local chain coordinates to
// absolute positions in STATE so that BLOCK_INDEX, EPOCH_STATS, EPOCH_DATA keys
// remain continuous across network restarts (eras).
//
// NetworkId identifies the network iteration the saved state belongs to. It is
// captured from globals.GENESIS.NetworkId on first launch and rewritten by
// setGenesisToState() on a network restart (when ET is absent from STATE).
// Mismatch with globals.GENESIS.NetworkId on startup or before block execution
// is a hard error (wrong genesis / wrong chaindata directory).
//
// Permanent fields (CoreMajorVersion, Statistics, NetworkParameters) are the
// single source of truth for world-state metadata. They are read and written
// directly — not duplicated in ExecutionThreadMetadataHandler.
type ChainCursor struct {
	HeightOffset int64 `json:"heightOffset"`
	EpochOffset  int   `json:"epochOffset"`

	NetworkId string `json:"networkId"`

	CoreMajorVersion  int               `json:"coreMajorVersion"`
	Statistics        *Statistics       `json:"statistics,omitempty"`
	NetworkParameters NetworkParameters `json:"networkParameters"`
}
