package threads

import (
	"fmt"

	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
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

type LeaderFinalizationState struct {
	ProcessingEpochId int                `json:"processingEpochId"`
	InQuorum          bool               `json:"inQuorum"`
	Leaders           []LeaderAlfpStatus `json:"leaders"`
	WsConnectionCount int                `json:"wsConnectionCount"`
}

type AlfpWatcherDashboardState struct {
	EpochId                         int                               `json:"epochId"`
	CurrentAnchorAssumption         int                               `json:"currentAnchorAssumption"`
	CurrentAnchorBlockIndexObserved int                               `json:"currentAnchorBlockIndexObserved"`
	LastBlocksByAnchors             map[int]structures.ExecutionStats `json:"lastBlocksByAnchors"`
	AllIncluded                     bool                              `json:"allIncluded"`
	LeaderInclusionStatus           map[string]bool                   `json:"leaderInclusionStatus"`
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
	return result
}

func GetAlfpWatcherState() AlfpWatcherDashboardState {
	epochId := loadFinalizationProgress()
	st := loadAlfpWatcherState(epochId)

	result := AlfpWatcherDashboardState{
		EpochId:                         st.EpochId,
		CurrentAnchorAssumption:         st.CurrentAnchorAssumption,
		CurrentAnchorBlockIndexObserved: st.CurrentAnchorBlockIndexObserved,
		LastBlocksByAnchors:             st.LastBlocksByAnchors,
		LeaderInclusionStatus:           make(map[string]bool),
	}

	snapshot := loadEpochSnapshotForWatcher(epochId)
	if snapshot != nil {
		epochHandler := &snapshot.EpochDataHandler
		result.AllIncluded = allLocalAlfpsIncluded(epochHandler)
		for _, leader := range epochHandler.LeadersSequence {
			result.LeaderInclusionStatus[leader] = utils.HasAnyAlfpIncluded(epochHandler.Id, leader)
		}
	}

	return result
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
