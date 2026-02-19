package threads

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"

	"github.com/syndtr/goleveldb/leveldb/util"
)

type LeaderAlfpStatus struct {
	Pubkey               string                                        `json:"pubkey"`
	LeaderIndex          int                                           `json:"leaderIndex"`
	HasAlfp              bool                                          `json:"hasAlfp"`
	Included             bool                                          `json:"included"`
	ConfirmedByAlignment bool                                          `json:"confirmedByAlignment"`
	SkipDataIndex        int                                           `json:"skipDataIndex"`
	SkipDataHash         string                                        `json:"skipDataHash"`
	ProofsCollected      int                                           `json:"proofsCollected"`
	Aggregated           *structures.AggregatedLeaderFinalizationProof `json:"aggregated,omitempty"`
}

type RecentEpochLeader struct {
	Pubkey               string `json:"pubkey"`
	Included             bool   `json:"included"`
	ConfirmedByAlignment bool   `json:"confirmedByAlignment"`
	HasAlfp              bool   `json:"hasAlfp"`
}

type RecentEpochEntry struct {
	EpochId     int                 `json:"epochId"`
	Leaders     []RecentEpochLeader `json:"leaders"`
	AllResolved bool                `json:"allResolved"`
}

type LeaderFinalizationState struct {
	ProcessingEpochId int                  `json:"processingEpochId"`
	InQuorum          bool                 `json:"inQuorum"`
	Leaders           []LeaderAlfpStatus   `json:"leaders"`
	WsConnectionCount int                  `json:"wsConnectionCount"`
	RecentEpochs      []RecentEpochEntry   `json:"recentEpochs"`
}

type AlfpInclusionEvent struct {
	EpochId    int    `json:"epochId"`
	Leader     string `json:"leader"`
	BlockIndex int    `json:"blockIndex"`
	Hash       string `json:"hash"`
	AnchorBlockId string `json:"anchorBlockId"`
	Anchor     string `json:"anchor"`
}

type AlfpWatcherDashboardState struct {
	AlfpProgressEpoch               int                               `json:"alfpProgressEpoch"`
	CurrentAnchorAssumption         int                               `json:"currentAnchorAssumption"`
	CurrentAnchorBlockIndexObserved int                               `json:"currentAnchorBlockIndexObserved"`
	LastBlocksByAnchors             map[int]structures.ExecutionStats `json:"lastBlocksByAnchors"`
	AllIncludedForCurrentEpoch      bool                              `json:"allIncludedForCurrentEpoch"`
	InclusionEvents                 []AlfpInclusionEvent              `json:"inclusionEvents"`
	EpochSummaries                  []AlfpEpochSummary                `json:"epochSummaries"`
}

type AlfpEpochSummary struct {
	EpochId      int                    `json:"epochId"`
	Leaders      []AlfpLeaderSummary    `json:"leaders"`
	AllIncluded  bool                   `json:"allIncluded"`
}

type AlfpLeaderSummary struct {
	Pubkey   string `json:"pubkey"`
	Included bool   `json:"included"`
	HasAlfp  bool   `json:"hasAlfp"`
}

type ProofsGrabberState struct {
	EpochId             int    `json:"epochId"`
	AcceptedIndex       int    `json:"acceptedIndex"`
	AcceptedHash        string `json:"acceptedHash"`
	HuntingForBlockId   string `json:"huntingForBlockId"`
	HuntingForBlockHash string `json:"huntingForBlockHash"`
}

type GenerationThreadState struct {
	EpochFullId string `json:"epochFullId"`
	PrevHash    string `json:"prevHash"`
	NextIndex   int    `json:"nextIndex"`
}

func GetLeaderFinalizationState() LeaderFinalizationState {
	progress := loadFinalizationProgress()

	snapshot := getOrLoadEpochSnapshot(progress)

	result := LeaderFinalizationState{
		ProcessingEpochId: progress,
	}

	if snapshot == nil {
		return result
	}

	epochHandler := &snapshot.EpochDataHandler
	result.InQuorum = weAreInEpochQuorum(epochHandler)

	ALFP_GRABBING_MUTEX.Lock()
	meta := ALFP_PROCESS_METADATA
	ALFP_GRABBING_MUTEX.Unlock()

	wsCount := 0
	if meta != nil && meta.EpochId == progress {
		for _, conn := range meta.WsConns {
			if conn != nil {
				wsCount++
			}
		}
	}
	result.WsConnectionCount = wsCount

	leaders := make([]LeaderAlfpStatus, 0, len(epochHandler.LeadersSequence))

	for idx, leader := range epochHandler.LeadersSequence {
		status := LeaderAlfpStatus{
			Pubkey:               leader,
			LeaderIndex:          idx,
			HasAlfp:              leaderHasAlfp(epochHandler.Id, leader),
			Included:             utils.HasAnyAlfpIncluded(epochHandler.Id, leader),
			ConfirmedByAlignment: leaderFinalizationConfirmedByAlignment(epochHandler.Id, leader),
			SkipDataIndex:        -1,
			SkipDataHash:         "",
		}

		if status.HasAlfp {
			if agg := loadAggregatedLeaderFinalizationProof(epochHandler.Id, leader); agg != nil {
				status.Aggregated = agg
				status.SkipDataIndex = agg.VotingStat.Index
				status.SkipDataHash = agg.VotingStat.Hash
				status.ProofsCollected = len(agg.Signatures)
			}
		}

		if meta != nil && meta.EpochId == progress {
			ALFP_GRABBING_MUTEX.Lock()
			key := fmt.Sprintf("%d:%s", epochHandler.Id, leader)
			if cache, ok := meta.Caches[key]; ok {
				if !status.HasAlfp {
					status.SkipDataIndex = cache.SkipData.Index
					status.SkipDataHash = cache.SkipData.Hash
					status.ProofsCollected = len(cache.Proofs)
				}
			}
			ALFP_GRABBING_MUTEX.Unlock()
		}

		leaders = append(leaders, status)
	}

	result.Leaders = leaders

	recentEpochs := make([]RecentEpochEntry, 0)
	startFrom := progress - 1
	if startFrom < 0 {
		startFrom = 0
	}
	endAt := progress - 10
	if endAt < 0 {
		endAt = 0
	}
	for eid := startFrom; eid >= endAt; eid-- {
		snap := loadEpochSnapshotForWatcher(eid)
		if snap == nil {
			continue
		}
		epochCompleted := progress > eid
		entry := RecentEpochEntry{
			EpochId:     eid,
			AllResolved: epochCompleted,
		}
		for _, leader := range snap.EpochDataHandler.LeadersSequence {
			included := utils.HasAnyAlfpIncluded(eid, leader)
			hasAlfp := leaderHasAlfp(eid, leader)
			confirmedByAlignment := false
			if epochCompleted && !included {
				confirmedByAlignment = true
			}
			entry.Leaders = append(entry.Leaders, RecentEpochLeader{
				Pubkey:               leader,
				Included:             included,
				ConfirmedByAlignment: confirmedByAlignment,
				HasAlfp:              hasAlfp,
			})
		}
		recentEpochs = append(recentEpochs, entry)
	}
	result.RecentEpochs = recentEpochs

	return result
}

func GetAlfpWatcherState() AlfpWatcherDashboardState {
	alfpProgress := loadFinalizationProgress()
	st := loadAlfpWatcherState(alfpProgress)

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	etEpoch := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	result := AlfpWatcherDashboardState{
		AlfpProgressEpoch:               alfpProgress,
		CurrentAnchorAssumption:         st.CurrentAnchorAssumption,
		CurrentAnchorBlockIndexObserved: st.CurrentAnchorBlockIndexObserved,
		LastBlocksByAnchors:             st.LastBlocksByAnchors,
		InclusionEvents:                 make([]AlfpInclusionEvent, 0),
		EpochSummaries:                  make([]AlfpEpochSummary, 0),
	}

	// Scan ALFP_INCLUDED markers from DB for recent epochs (back to max(0, etEpoch-10))
	scanFrom := etEpoch - 10
	if scanFrom < 0 {
		scanFrom = 0
	}
	scanTo := alfpProgress
	if etEpoch > scanTo {
		scanTo = etEpoch
	}

	for eid := scanTo; eid >= scanFrom; eid-- {
		events := scanAlfpIncludedForEpoch(eid)
		result.InclusionEvents = append(result.InclusionEvents, events...)

		snap := loadEpochSnapshotForWatcher(eid)
		if snap == nil {
			continue
		}

		summary := AlfpEpochSummary{
			EpochId:     eid,
			AllIncluded: true,
			Leaders:     make([]AlfpLeaderSummary, 0, len(snap.EpochDataHandler.LeadersSequence)),
		}

		for _, leader := range snap.EpochDataHandler.LeadersSequence {
			included := utils.HasAnyAlfpIncluded(eid, leader)
			hasAlfp := leaderHasAlfp(eid, leader)
			if !included {
				summary.AllIncluded = false
			}
			summary.Leaders = append(summary.Leaders, AlfpLeaderSummary{
				Pubkey:   leader,
				Included: included,
				HasAlfp:  hasAlfp,
			})
		}

		result.EpochSummaries = append(result.EpochSummaries, summary)
	}

	// Check current epoch inclusion
	snapshot := loadEpochSnapshotForWatcher(alfpProgress)
	if snapshot != nil {
		result.AllIncludedForCurrentEpoch = allLocalAlfpsIncluded(&snapshot.EpochDataHandler)
	}

	return result
}

// scanAlfpIncludedForEpoch reads all ALFP_INCLUDED:<epochId>:* keys from LevelDB.
func scanAlfpIncludedForEpoch(epochId int) []AlfpInclusionEvent {
	prefix := []byte(fmt.Sprintf("ALFP_INCLUDED:%d:", epochId))
	it := databases.FINALIZATION_VOTING_STATS.NewIterator(util.BytesPrefix(prefix), nil)
	defer it.Release()

	events := make([]AlfpInclusionEvent, 0)

	for it.Next() {
		key := string(it.Key())
		// Key format: ALFP_INCLUDED:<epochId>:<leader>:<index>
		parts := strings.SplitN(key, ":", 4)
		if len(parts) != 4 {
			continue
		}

		blockIndex, err := strconv.Atoi(parts[3])
		if err != nil {
			continue
		}

		var marker utils.AlfpInclusionMarker
		if json.Unmarshal(it.Value(), &marker) != nil {
			continue
		}

		events = append(events, AlfpInclusionEvent{
			EpochId:       epochId,
			Leader:        parts[2],
			BlockIndex:    blockIndex,
			Hash:          marker.Hash,
			AnchorBlockId: marker.BlockId,
			Anchor:        marker.Anchor,
		})
	}

	return events
}

func GetProofsGrabberState() ProofsGrabberState {
	PROOFS_GRABBER_MUTEX.RLock()
	defer PROOFS_GRABBER_MUTEX.RUnlock()

	return ProofsGrabberState{
		EpochId:             PROOFS_GRABBER.EpochId,
		AcceptedIndex:       PROOFS_GRABBER.AcceptedIndex,
		AcceptedHash:        PROOFS_GRABBER.AcceptedHash,
		HuntingForBlockId:   PROOFS_GRABBER.HuntingForBlockId,
		HuntingForBlockHash: PROOFS_GRABBER.HuntingForBlockHash,
	}
}

func GetGenerationThreadState() GenerationThreadState {
	return GenerationThreadState{
		EpochFullId: handlers.GENERATION_THREAD_METADATA.EpochFullId,
		PrevHash:    handlers.GENERATION_THREAD_METADATA.PrevHash,
		NextIndex:   handlers.GENERATION_THREAD_METADATA.NextIndex,
	}
}
