package handlers

import (
	"container/list"
	"sync"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/structures"
)

var GENERATION_THREAD_METADATA structures.GenerationThreadMetadataHandler

// STATE_MUTEX guards all mutable state related to block execution:
// both EXECUTION_THREAD_METADATA and CHAIN_CURSOR.
var STATE_MUTEX sync.RWMutex

// CHAIN_CURSOR is the permanent state descriptor stored in STATE DB.
// It holds offset mapping for network restarts (HeightOffset, EpochOffset) and
// permanent world-state metadata (CoreMajorVersion, Statistics, NetworkParameters).
// Loaded on startup; default zero-value means fresh chain with no offsets.
// Protected by STATE_MUTEX.
var CHAIN_CURSOR = structures.ChainCursor{
	CoreMajorVersion: -1,
	Statistics:       &structures.Statistics{LastHeight: -1},
}

var APPROVEMENT_THREAD_METADATA = struct {
	RWMutex sync.RWMutex
	Handler structures.ApprovementThreadMetadataHandler

	// Bounded ValidatorsStoragesCache (keys are DB keys) and its LRU bookkeeping.
	// Kept here (not in Handler) because caches are runtime-only accelerators,
	// not part of the persisted thread state.
	ValidatorsStoragesCache map[string]*structures.ValidatorStorage
	ValidatorsCacheMax      int
	ValidatorsLRU           *list.List
	ValidatorsLRUIndex      map[string]*list.Element
	ValidatorsTouched       map[string]*structures.ValidatorStorage
}{
	Handler: structures.ApprovementThreadMetadataHandler{
		CoreMajorVersion: -1,
	},
	ValidatorsStoragesCache: make(map[string]*structures.ValidatorStorage),
	ValidatorsCacheMax:      constants.DefaultValidatorsCacheMax,
	ValidatorsLRU:           list.New(),
	ValidatorsLRUIndex:      make(map[string]*list.Element),
	ValidatorsTouched:       make(map[string]*structures.ValidatorStorage),
}

// EXECUTION_THREAD_METADATA holds ephemeral consensus state.
// Protected by STATE_MUTEX (not an embedded mutex).
var EXECUTION_THREAD_METADATA = struct {
	Handler structures.ExecutionThreadMetadataHandler

	// Bounded AccountsCache + ValidatorsStoragesCache and their LRU bookkeeping.
	// Kept here (not in Handler) because caches are runtime-only accelerators,
	// not part of the persisted thread state.
	AccountsCache           map[string]*structures.Account
	AccountsCacheMax        int
	AccountsLRU             *list.List
	AccountsLRUIndex        map[string]*list.Element
	AccountsTouched         map[string]*structures.Account
	ValidatorsStoragesCache map[string]*structures.ValidatorStorage
	ValidatorsCacheMax      int
	ValidatorsLRU           *list.List
	ValidatorsLRUIndex      map[string]*list.Element
	ValidatorsTouched       map[string]*structures.ValidatorStorage
}{
	Handler: structures.ExecutionThreadMetadataHandler{
		EpochStatistics: &structures.Statistics{LastHeight: -1},
	},
	AccountsCache:           make(map[string]*structures.Account),
	AccountsCacheMax:        constants.DefaultAccountsCacheMax,
	AccountsLRU:             list.New(),
	AccountsLRUIndex:        make(map[string]*list.Element),
	AccountsTouched:         make(map[string]*structures.Account),
	ValidatorsStoragesCache: make(map[string]*structures.ValidatorStorage),
	ValidatorsCacheMax:      constants.DefaultValidatorsCacheMax,
	ValidatorsLRU:           list.New(),
	ValidatorsLRUIndex:      make(map[string]*list.Element),
	ValidatorsTouched:       make(map[string]*structures.ValidatorStorage),
}

// FINALIZER_THREAD_METADATA holds state for the consensus/sequencing threads
// (last_mile_finalizer, sequence_alignment, anchor_rotation_monitor, leader_finalization,
// alfp_inclusion_watcher). Decoupled from EXECUTION_THREAD_METADATA so that the
// fast sequencing/finalization layer is independent of the execution layer's speed.
//
// Persisted in databases.FINALIZATION_THREAD_METADATA under DBKeyFinalizerThreadMetadata.
var FINALIZER_THREAD_METADATA = struct {
	RWMutex sync.RWMutex
	Handler structures.FinalizerThreadMetadataHandler
}{
	Handler: structures.FinalizerThreadMetadataHandler{
		SequenceAlignmentData: structures.AlignmentDataHandler{
			CurrentAnchorAssumption:         0,
			CurrentAnchorBlockIndexObserved: -1,
			CurrentLeaderToExecBlocksFrom:   0,
			LastBlocksByLeaders:             make(map[string]structures.ExecutionStats),
			LastBlocksByAnchors:             make(map[int]structures.ExecutionStats),
		},
	},
}
