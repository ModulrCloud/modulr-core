package handlers

import (
	"container/list"
	"sync"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/structures"
)

var GENERATION_THREAD_METADATA structures.GenerationThreadMetadataHandler

// CHAIN_CURSOR maps local chain coordinates (used by consensus/proofs) to absolute
// positions in STATE. Loaded once on startup from STATE DB; default {0,0} means no offset.
var CHAIN_CURSOR structures.ChainCursor

var APPROVEMENT_THREAD_METADATA = struct {
	RWMutex sync.RWMutex
	Handler structures.ApprovementThreadMetadataHandler

	// Bounded cache bookkeeping for ValidatorsStoragesCache (keys are DB keys).
	ValidatorsCacheMax int
	ValidatorsLRU      *list.List
	ValidatorsLRUIndex map[string]*list.Element
	ValidatorsTouched  map[string]*structures.ValidatorStorage
}{
	Handler: structures.ApprovementThreadMetadataHandler{
		CoreMajorVersion:        -1,
		ValidatorsStoragesCache: make(map[string]*structures.ValidatorStorage),
	},
	ValidatorsCacheMax: constants.DefaultValidatorsCacheMax,
	ValidatorsLRU:      list.New(),
	ValidatorsLRUIndex: make(map[string]*list.Element),
	ValidatorsTouched:  make(map[string]*structures.ValidatorStorage),
}

var EXECUTION_THREAD_METADATA = struct {
	RWMutex sync.RWMutex
	Handler structures.ExecutionThreadMetadataHandler

	// Bounded cache bookkeeping for AccountsCache + ValidatorsStoragesCache.
	AccountsCacheMax   int
	AccountsLRU        *list.List
	AccountsLRUIndex   map[string]*list.Element
	AccountsTouched    map[string]*structures.Account
	ValidatorsCacheMax int
	ValidatorsLRU      *list.List
	ValidatorsLRUIndex map[string]*list.Element
	ValidatorsTouched  map[string]*structures.ValidatorStorage
}{
	Handler: structures.ExecutionThreadMetadataHandler{
		CoreMajorVersion:        -1,
		AccountsCache:           make(map[string]*structures.Account),
		ValidatorsStoragesCache: make(map[string]*structures.ValidatorStorage),
		SequenceAlignmentData: structures.AlignmentDataHandler{
			CurrentAnchorAssumption:         0,
			CurrentAnchorBlockIndexObserved: -1,
			CurrentLeaderToExecBlocksFrom:   0,
			LastBlocksByLeaders:             make(map[string]structures.ExecutionStats),
			LastBlocksByAnchors:             make(map[int]structures.ExecutionStats),
		},
		Statistics:      &structures.Statistics{LastHeight: -1},
		EpochStatistics: &structures.Statistics{LastHeight: -1},
	},
	AccountsCacheMax:   constants.DefaultAccountsCacheMax,
	AccountsLRU:        list.New(),
	AccountsLRUIndex:   make(map[string]*list.Element),
	AccountsTouched:    make(map[string]*structures.Account),
	ValidatorsCacheMax: constants.DefaultValidatorsCacheMax,
	ValidatorsLRU:      list.New(),
	ValidatorsLRUIndex: make(map[string]*list.Element),
	ValidatorsTouched:  make(map[string]*structures.ValidatorStorage),
}
