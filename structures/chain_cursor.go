package structures

// ChainCursor is the permanent state descriptor stored in STATE DB under CHAIN_CURSOR key.
// It is the single source of truth for everything related to block execution
// (consensus position + permanent world-state metadata). There is no separate
// ExecutionThreadMetadataHandler — all execution-thread state lives here.
//
// Offset fields (HeightOffset, EpochOffset) map local chain coordinates to
// absolute positions in STATE so that BLOCK_INDEX, EPOCH_STATS, EPOCH_DATA keys
// remain continuous across network restarts (eras).
//
// NetworkId identifies the network iteration the saved state belongs to. It is
// captured from globals.GENESIS.NetworkId on first launch and rewritten by
// setGenesisToState() on a network restart. Mismatch with globals.GENESIS.NetworkId
// on startup or before block execution is a hard error (wrong genesis / wrong
// chaindata directory).
//
// EpochDataHandler.Hash == "" is the sentinel that triggers genesis initialization
// in prepareBlockchain(). It covers both first-ever launch (no cursor in STATE)
// and network restart (operator wiped EpochDataHandler to bootstrap a new genesis
// while preserving accounts/validators/offsets).
type ChainCursor struct {
	HeightOffset int64 `json:"heightOffset"`
	EpochOffset  int   `json:"epochOffset"`

	NetworkId string `json:"networkId"`

	CoreMajorVersion  int               `json:"coreMajorVersion"`
	NetworkParameters NetworkParameters `json:"networkParameters"`

	EpochDataHandler EpochDataHandler `json:"epoch"`

	// Statistics tracks lifetime metrics across the whole chain.
	Statistics *Statistics `json:"statistics,omitempty"`

	// EpochStatistics tracks metrics within the current epoch (reset on epoch rotation).
	EpochStatistics *Statistics `json:"epochStatistics,omitempty"`
}
