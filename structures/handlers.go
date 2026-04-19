package structures

type ApprovementThreadMetadataHandler struct {
	CoreMajorVersion  int               `json:"coreMajorVersion"`
	NetworkParameters NetworkParameters `json:"networkParameters"`
	EpochDataHandler  EpochDataHandler  `json:"epoch"`
}

type GenerationThreadMetadataHandler struct {
	EpochFullId string `json:"epochFullId"`
	PrevHash    string `json:"prevHash"`
	NextIndex   int    `json:"nextIndex"`
}

type AlignmentDataHandler struct {
	CurrentAnchorAssumption         int                       `json:"currentAnchorAssumption"`
	CurrentAnchorBlockIndexObserved int                       `json:"currentAnchorBlockIndexObserved"`
	CurrentLeaderToExecBlocksFrom   int                       `json:"currentToExecute"`
	LastBlocksByLeaders             map[string]ExecutionStats `json:"lastBlocksByLeaders"` // PUBKEY => {index:int, hash:""}
	LastBlocksByAnchors             map[int]ExecutionStats    `json:"lastBlocksByAnchors"`
}

type EpochDataHandler struct {
	Id                 int      `json:"id"`
	Hash               string   `json:"hash"`
	ValidatorsRegistry []string `json:"validatorsRegistry"`
	Quorum             []string `json:"quorum"`
	LeadersSequence    []string `json:"leadersSequence"`
	StartTimestamp     uint64   `json:"startTimestamp"`
	CurrentLeaderIndex int      `json:"currentLeaderIndex"`
}

type EpochDataSnapshot struct {
	EpochDataHandler
	NetworkParameters NetworkParameters `json:"networkParameters"`
}

type NextEpochDataHandler struct {
	NextEpochHash               string              `json:"nextEpochHash"`
	NextEpochValidatorsRegistry []string            `json:"nextEpochValidatorsRegistry"`
	NextEpochQuorum             []string            `json:"nextEpochQuorum"`
	NextEpochLeadersSequence    []string            `json:"nextEpochLeadersSequence"`
	DelayedTransactions         []map[string]string `json:"delayedTransactions"`
}

// FinalizerThreadMetadataHandler holds state used by the consensus/sequencing threads
// (last_mile_finalizer, sequence_alignment, anchor_rotation_monitor, alfp_inclusion_watcher,
// leader_finalization). It is intentionally decoupled from the execution thread (ChainCursor)
// so that block sequencing/finalization is not bound to block execution speed.
//
// Persisted as a single key (constants.DBKeyFinalizerThreadMetadata) in
// databases.FINALIZATION_THREAD_METADATA.
type FinalizerThreadMetadataHandler struct {
	EpochDataHandler      EpochDataHandler     `json:"epoch"`
	SequenceAlignmentData AlignmentDataHandler `json:"currentEpochAlignmentData"`
}
