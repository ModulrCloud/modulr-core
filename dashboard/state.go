package dashboard

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/threads"
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
	ProcessingEpochId int                `json:"processingEpochId"`
	InQuorum          bool               `json:"inQuorum"`
	Leaders           []LeaderAlfpStatus `json:"leaders"`
	WsConnectionCount int                `json:"wsConnectionCount"`
	RecentEpochs      []RecentEpochEntry `json:"recentEpochs"`
}

func GetLeaderFinalizationState() LeaderFinalizationState {
	progress := loadFinalizationProgress()

	result := LeaderFinalizationState{
		ProcessingEpochId: progress,
		Leaders:           make([]LeaderAlfpStatus, 0),
		RecentEpochs:      make([]RecentEpochEntry, 0),
	}

	snapshot := loadEpochSnapshot(progress)
	if snapshot == nil {
		return result
	}

	epochHandler := &snapshot.EpochDataHandler
	result.InQuorum = weAreInEpochQuorum(epochHandler)
	result.WsConnectionCount = threads.GetLeaderFinalizationWsConnectionCount(progress)

	leaders := make([]LeaderAlfpStatus, 0, len(epochHandler.LeadersSequence))
	for idx, leader := range epochHandler.LeadersSequence {
		status := LeaderAlfpStatus{
			Pubkey:               leader,
			LeaderIndex:          idx,
			HasAlfp:              leaderHasAlfp(progress, leader),
			Included:             utils.HasAnyAlfpIncluded(progress, leader),
			ConfirmedByAlignment: leaderFinalizationConfirmedByAlignment(progress, leader),
			SkipDataIndex:        -1,
			SkipDataHash:         "",
			ProofsCollected:      0,
		}

		if status.HasAlfp {
			if agg := loadAggregatedLeaderFinalizationProof(progress, leader); agg != nil {
				status.Aggregated = agg
				status.SkipDataIndex = agg.VotingStat.Index
				status.SkipDataHash = agg.VotingStat.Hash
				status.ProofsCollected = len(agg.Signatures)
			}
		} else {
			if skipIdx, skipHash, proofs, ok := threads.GetLeaderFinalizationLiveCache(progress, leader); ok {
				status.SkipDataIndex = skipIdx
				status.SkipDataHash = skipHash
				status.ProofsCollected = proofs
			}
		}

		leaders = append(leaders, status)
	}
	result.Leaders = leaders

	// Recent epoch history (completed epochs behind ALFP progress)
	startFrom := progress - 1
	if startFrom < 0 {
		startFrom = 0
	}
	endAt := progress - 10
	if endAt < 0 {
		endAt = 0
	}

	for eid := startFrom; eid >= endAt; eid-- {
		snap := loadEpochSnapshot(eid)
		if snap == nil {
			continue
		}

		epochCompleted := progress > eid
		entry := RecentEpochEntry{
			EpochId:     eid,
			AllResolved: epochCompleted,
			Leaders:     make([]RecentEpochLeader, 0, len(snap.EpochDataHandler.LeadersSequence)),
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

		result.RecentEpochs = append(result.RecentEpochs, entry)
	}

	return result
}

func loadFinalizationProgress() int {
	if raw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte("ALFP_PROGRESS"), nil); err == nil {
		if idx, convErr := strconv.Atoi(string(raw)); convErr == nil {
			return idx
		}
	}
	return 0
}

func loadEpochSnapshot(epochId int) *structures.EpochDataSnapshot {
	key := []byte("EPOCH_HANDLER:" + strconv.Itoa(epochId))
	raw, err := databases.APPROVEMENT_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return nil
	}
	var snap structures.EpochDataSnapshot
	if json.Unmarshal(raw, &snap) != nil {
		return nil
	}
	return &snap
}

func weAreInEpochQuorum(epochHandler *structures.EpochDataHandler) bool {
	if epochHandler == nil {
		return false
	}
	me := strings.ToLower(globals.CONFIGURATION.PublicKey)
	for _, q := range epochHandler.Quorum {
		if strings.ToLower(q) == me {
			return true
		}
	}
	return false
}

func leaderHasAlfp(epochId int, leader string) bool {
	key := []byte(fmt.Sprintf("ALFP:%d:%s", epochId, leader))
	_, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	return err == nil
}

func loadAggregatedLeaderFinalizationProof(epochId int, leader string) *structures.AggregatedLeaderFinalizationProof {
	key := []byte(fmt.Sprintf("ALFP:%d:%s", epochId, leader))
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	if err != nil {
		return nil
	}
	var proof structures.AggregatedLeaderFinalizationProof
	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}
	return &proof
}

func leaderFinalizationConfirmedByAlignment(epochId int, leader string) bool {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id != epochId {
		return false
	}
	_, exists := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders[leader]
	return exists
}

