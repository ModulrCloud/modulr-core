package structures

type ApprovementThreadMetadataHandler struct {
	CoreMajorVersion        int                          `json:"coreMajorVersion"`
	NetworkParameters       NetworkParameters            `json:"networkParameters"`
	EpochDataHandler        EpochDataHandler             `json:"epoch"`
	ValidatorsStoragesCache map[string]*ValidatorStorage `json:"-"`
}

type ExecutionThreadMetadataHandler struct {
	EpochDataHandler EpochDataHandler `json:"epoch"`

	// EpochStatistics tracks metrics within the current epoch (reset on epoch rotation).
	EpochStatistics *Statistics `json:"epochStatistics,omitempty"`

	SequenceAlignmentData AlignmentDataHandler `json:"currentEpochAlignmentData"`

	AccountsCache           map[string]*Account          `json:"-"`
	ValidatorsStoragesCache map[string]*ValidatorStorage `json:"-"`
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
