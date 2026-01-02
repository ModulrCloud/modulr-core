package handlers

import (
	"sync"

	"github.com/modulrcloud/modulr-core/structures"
)

var GENERATION_THREAD_METADATA structures.GenerationThreadMetadataHandler

var APPROVEMENT_THREAD_METADATA = struct {
	RWMutex sync.RWMutex
	Handler structures.ApprovementThreadMetadataHandler
}{
	Handler: structures.ApprovementThreadMetadataHandler{
		CoreMajorVersion:        -1,
		ValidatorsStoragesCache: make(map[string]*structures.ValidatorStorage),
	},
}

var EXECUTION_THREAD_METADATA = struct {
	RWMutex sync.RWMutex
	Handler structures.ExecutionThreadMetadataHandler
}{
	Handler: structures.ExecutionThreadMetadataHandler{
		CoreMajorVersion:        -1,
		AccountsCache:           make(map[string]*structures.Account),
		ValidatorsStoragesCache: make(map[string]*structures.ValidatorStorage),
		ExecutionData:           make(map[string]structures.ExecutionStats),
		SequenceAlignmentData: structures.AlignmentDataHandler{
			CurrentAnchorAssumption:         0,
			CurrentAnchorBlockIndexObserved: -1,
			CurrentLeaderToExecBlocksFrom:   0,
			LastBlocksByLeaders:             make(map[string]structures.ExecutionStats),
			LastBlocksByAnchors:             make(map[int]structures.ExecutionStats),
		},
		Statistics: &structures.Statistics{LastHeight: -1},
	},
}
