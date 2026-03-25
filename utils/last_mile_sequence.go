package utils

import (
	"encoding/json"
	"fmt"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
)

type LastMileSequenceState struct {
	EpochId     int   `json:"epochId"`
	LeaderIndex int   `json:"leaderIndex"`
	BlockIndex  int   `json:"blockIndex"`
	NextHeight  int64 `json:"nextHeight"`
}

// CurrentBlockId returns the blockId at the current tracker position.
// It auto-skips leaders whose blocks are fully processed or who produced no blocks.
// Returns empty string if the tracker can't advance (data not yet available or all leaders done).
func (s *LastMileSequenceState) CurrentBlockId(leadersSequence []string, lastBlocksByLeaders map[string]structures.ExecutionStats) string {

	for s.LeaderIndex < len(leadersSequence) {

		leader := leadersSequence[s.LeaderIndex]
		lastBlock, known := lastBlocksByLeaders[leader]

		if !known {
			return ""
		}

		if lastBlock.Index < 0 || s.BlockIndex > lastBlock.Index {
			s.LeaderIndex++
			s.BlockIndex = 0
			continue
		}

		return fmt.Sprintf("%d:%s:%d", s.EpochId, leader, s.BlockIndex)
	}

	return ""
}

// AllLeadersDone returns true when the tracker has moved past all leaders in the sequence.
func (s *LastMileSequenceState) AllLeadersDone(leadersSequence []string) bool {
	return s.LeaderIndex >= len(leadersSequence)
}

// Advance moves to the next block position after the current one is processed.
func (s *LastMileSequenceState) Advance() {
	s.BlockIndex++
	s.NextHeight++
}

// AdvanceToNextEpoch resets the tracker for the next epoch.
func (s *LastMileSequenceState) AdvanceToNextEpoch() {
	s.EpochId++
	s.LeaderIndex = 0
	s.BlockIndex = 0
}

func LoadLastMileSequenceState(dbKey string) *LastMileSequenceState {

	raw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte(dbKey), nil)

	if err != nil || len(raw) == 0 {
		return &LastMileSequenceState{}
	}

	var state LastMileSequenceState

	if json.Unmarshal(raw, &state) != nil {
		return &LastMileSequenceState{}
	}

	return &state
}

func PersistLastMileSequenceState(dbKey string, state *LastMileSequenceState) {

	if raw, err := json.Marshal(state); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put([]byte(dbKey), raw, nil)
	}
}

func StoreHeightBlockIdMapping(height int64, blockId string) {
	key := fmt.Sprintf("LAST_MILE_HEIGHT_MAP:%d", height)
	_ = databases.FINALIZATION_VOTING_STATS.Put([]byte(key), []byte(blockId), nil)
}

func LoadHeightBlockIdMapping(height int64) string {
	key := fmt.Sprintf("LAST_MILE_HEIGHT_MAP:%d", height)
	raw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte(key), nil)
	if err != nil {
		return ""
	}
	return string(raw)
}
