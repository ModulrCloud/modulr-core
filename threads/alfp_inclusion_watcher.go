package threads

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

type AlfpWatcherState struct {
	EpochId                         int                               `json:"epochId"`
	CurrentAnchorAssumption         int                               `json:"currentAnchorAssumption"`
	CurrentAnchorBlockIndexObserved int                               `json:"currentAnchorBlockIndexObserved"`
	LastBlocksByAnchors             map[int]structures.ExecutionStats `json:"lastBlocksByAnchors"`
}

func newAlfpWatcherState(epochId int) *AlfpWatcherState {
	return &AlfpWatcherState{
		EpochId:                         epochId,
		CurrentAnchorAssumption:         0,
		CurrentAnchorBlockIndexObserved: 0,
		LastBlocksByAnchors:             make(map[int]structures.ExecutionStats),
	}
}

func alfpWatcherStateKey(epochId int) []byte {
	return []byte(fmt.Sprintf("ALFP_WATCHER_STATE:%d", epochId))
}

func loadAlfpWatcherState(epochId int) *AlfpWatcherState {
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(alfpWatcherStateKey(epochId), nil)
	if err != nil || len(raw) == 0 {
		return newAlfpWatcherState(epochId)
	}
	var st AlfpWatcherState
	if json.Unmarshal(raw, &st) != nil {
		return newAlfpWatcherState(epochId)
	}
	if st.LastBlocksByAnchors == nil {
		st.LastBlocksByAnchors = make(map[int]structures.ExecutionStats)
	}
	if st.EpochId != epochId {
		return newAlfpWatcherState(epochId)
	}
	if st.CurrentAnchorAssumption < 0 {
		st.CurrentAnchorAssumption = 0
	}
	if st.CurrentAnchorBlockIndexObserved < 0 {
		st.CurrentAnchorBlockIndexObserved = 0
	}
	return &st
}

func persistAlfpWatcherState(st *AlfpWatcherState) {
	if st == nil {
		return
	}
	if st.LastBlocksByAnchors == nil {
		st.LastBlocksByAnchors = make(map[int]structures.ExecutionStats)
	}
	if payload, err := json.Marshal(st); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put(alfpWatcherStateKey(st.EpochId), payload, nil)
	}
}

func loadEpochSnapshotForWatcher(epochId int) *structures.EpochDataSnapshot {
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

func allLocalAlfpsIncluded(epochHandler *structures.EpochDataHandler) bool {
	if epochHandler == nil {
		return false
	}
	for _, leader := range epochHandler.LeadersSequence {
		if !utils.HasAnyAlfpIncluded(epochHandler.Id, leader) {
			return false
		}
	}
	return true
}

func fetchCatchUpTargetForAnchor(epochHandler *structures.EpochDataHandler, anchorIndex int, client *http.Client, rng *rand.Rand) (structures.ExecutionStats, bool) {
	if epochHandler == nil || client == nil || rng == nil {
		return structures.ExecutionStats{}, false
	}
	if anchorIndex < 0 || anchorIndex >= len(globals.ANCHORS) {
		return structures.ExecutionStats{}, false
	}
	targetAnchor := globals.ANCHORS[rng.Intn(len(globals.ANCHORS))]
	url := fmt.Sprintf("%s/sequence_alignment_data/%d/%d", targetAnchor.AnchorUrl, epochHandler.Id, anchorIndex)
	resp, err := client.Get(url)
	if err != nil {
		return structures.ExecutionStats{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return structures.ExecutionStats{}, false
	}

	dec := json.NewDecoder(io.LimitReader(resp.Body, 10<<20))
	var alignmentData SequenceAlignmentDataResponse
	if err := dec.Decode(&alignmentData); err != nil {
		return structures.ExecutionStats{}, false
	}

	earliestRotationStats, ok := processSequenceAlignmentDataResponse(&alignmentData, anchorIndex, epochHandler)
	if !ok {
		return structures.ExecutionStats{}, false
	}
	return earliestRotationStats, true
}

// AlfpInclusionWatcherThread scans anchor blocks linearly (per epoch) and persists ALFP_INCLUDED markers.
// It follows ALFP_PROGRESS (same as LeaderFinalizationThread) so it focuses on the epoch currently being finalized.
func AlfpInclusionWatcherThread() {
	client := &http.Client{Timeout: 5 * time.Second}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	var lastEpochSeen = -1
	var state *AlfpWatcherState

	for {
		epochId := loadFinalizationProgress()

		if epochId != lastEpochSeen || state == nil {
			state = loadAlfpWatcherState(epochId)
			lastEpochSeen = epochId
		}

		snapshot := loadEpochSnapshotForWatcher(epochId)
		if snapshot == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		epochHandler := &snapshot.EpochDataHandler

		// If everything for this epoch is already included, back off a bit.
		if allLocalAlfpsIncluded(epochHandler) {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		anchorIndex := state.CurrentAnchorAssumption
		if anchorIndex < 0 {
			anchorIndex = 0
			state.CurrentAnchorAssumption = 0
		}
		if anchorIndex >= len(globals.ANCHORS) {
			// Nothing else to scan for this epoch.
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Ensure we know the last block target for this anchor (catch-up limit).
		target, ok := state.LastBlocksByAnchors[anchorIndex]
		if !ok {
			if stats, ok := fetchCatchUpTargetForAnchor(epochHandler, anchorIndex, client, rng); ok {
				state.LastBlocksByAnchors[anchorIndex] = stats
				persistAlfpWatcherState(state)
			} else {
				time.Sleep(200 * time.Millisecond)
			}
			continue
		}

		currentExec := state.CurrentAnchorBlockIndexObserved
		if currentExec < 0 {
			currentExec = 0
			state.CurrentAnchorBlockIndexObserved = 0
		}

		anchor := globals.ANCHORS[anchorIndex]
		blockID := fmt.Sprintf("%d:%s:%d", epochId, anchor.Pubkey, currentExec)

		response := getAnchorBlockAndAfpFromAnchorsPoD(blockID, epochHandler)
		if response == nil || response.Block == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		block := response.Block

		if block.Creator != anchor.Pubkey || block.Index != currentExec || !block.VerifySignature() {
			// Don't advance on invalid data; wait and retry.
			time.Sleep(200 * time.Millisecond)
			continue
		}

		// Determine if this block is accepted in the linear chain.
		afpOk := response.Afp != nil && utils.VerifyAggregatedFinalizationProofForAnchorBlock(response.Afp, epochHandler)

		accepted := false
		switch {
		case currentExec < target.Index:
			accepted = afpOk
		case currentExec == target.Index:
			if target.Hash == "" {
				accepted = true
			} else {
				accepted = block.GetHash() == target.Hash
			}
		default:
			accepted = false
		}

		if !accepted {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		// Record any included ALFPs inside this accepted anchor block.
		for _, proof := range block.ExtraData.AggregatedLeaderFinalizationProofs {
			if !utils.VerifyAggregatedLeaderFinalizationProof(&proof, epochHandler) {
				continue
			}
			marker := utils.AlfpInclusionMarker{
				Hash:    proof.VotingStat.Hash,
				BlockId: blockID,
				Anchor:  anchor.Pubkey,
			}
			utils.StoreAlfpIncluded(marker, epochId, proof.Leader, proof.VotingStat.Index)
		}

		// Advance scan cursor.
		if currentExec < target.Index {
			state.CurrentAnchorBlockIndexObserved = currentExec + 1
			persistAlfpWatcherState(state)
			continue
		}

		// Reached the end of this anchor chain segment - move to next anchor.
		state.CurrentAnchorAssumption = anchorIndex + 1
		state.CurrentAnchorBlockIndexObserved = 0
		persistAlfpWatcherState(state)
	}
}
