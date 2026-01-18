package structures

// Statistics aggregates blockchain-level insights that can be surfaced in UIs.
// The struct can grow with additional counters or gauges over time.
type Statistics struct {
	LastHeight int64 `json:"lastHeight"`
	// BlocksGenerated is a simple counter of how many blocks were generated/executed in the tracked scope.
	// For global chain statistics it represents total blocks executed; for per-epoch statistics it represents
	// blocks executed within that epoch.
	BlocksGenerated        uint64 `json:"blocksGenerated"`
	LastBlockHash          string `json:"lastBlockHash"`
	TotalTransactions      uint64 `json:"totalTransactions"`
	SuccessfulTransactions uint64 `json:"successfulTransactions"`
	TotalFees              uint64 `json:"totalFees"`
}
