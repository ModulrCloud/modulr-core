package structures

type LogicalThread interface {
	GetCoreMajorVersion() int
	GetNetworkParams() NetworkParameters
	GetEpochHandler() EpochDataHandler
}

type ApprovementThreadMetadataHandler struct {
	CoreMajorVersion        int                          `json:"coreMajorVersion"`
	NetworkParameters       NetworkParameters            `json:"networkParameters"`
	EpochDataHandler        EpochDataHandler             `json:"epoch"`
	ValidatorsStoragesCache map[string]*ValidatorStorage `json:"-"`
}

type ExecutionThreadMetadataHandler struct {
	CoreMajorVersion  int               `json:"coreMajorVersion"`
	NetworkParameters NetworkParameters `json:"networkParameters"`
	EpochDataHandler  EpochDataHandler  `json:"epoch"`

	Statistics *Statistics `json:"statistics,omitempty"`
	// EpochStatistics tracks metrics within the current epoch (reset on epoch rotation).
	EpochStatistics *Statistics `json:"epochStatistics,omitempty"`

	ExecutionData         map[string]ExecutionStats `json:"executionData"` // PUBKEY => {index:int, hash:""}
	SequenceAlignmentData AlignmentDataHandler      `json:"currentEpochAlignmentData"`

	AccountsCache           map[string]*Account          `json:"-"`
	ValidatorsStoragesCache map[string]*ValidatorStorage `json:"-"`
}

func (handler *ApprovementThreadMetadataHandler) GetCoreMajorVersion() int {
	return handler.CoreMajorVersion
}

func (handler *ExecutionThreadMetadataHandler) GetCoreMajorVersion() int {
	return handler.CoreMajorVersion
}

func (handler *ApprovementThreadMetadataHandler) GetNetworkParams() NetworkParameters {
	return handler.NetworkParameters
}

func (handler *ApprovementThreadMetadataHandler) GetEpochHandler() EpochDataHandler {
	return handler.EpochDataHandler
}

func (handler *ExecutionThreadMetadataHandler) GetNetworkParams() NetworkParameters {
	return handler.NetworkParameters
}

func (handler *ExecutionThreadMetadataHandler) GetEpochHandler() EpochDataHandler {
	return handler.EpochDataHandler
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
