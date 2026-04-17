package structures

// ChainCursor maps local chain coordinates to absolute positions in STATE.
// After a network restart, HeightOffset and EpochOffset let the execution layer
// continue writing BLOCK_INDEX, EPOCH_STATS, EPOCH_DATA under absolute keys
// while the consensus layer (height proofs, epoch rotation proofs) starts from zero.
type ChainCursor struct {
	HeightOffset int64 `json:"heightOffset"`
	EpochOffset  int   `json:"epochOffset"`
}
