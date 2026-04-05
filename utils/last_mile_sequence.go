package utils

import (
	"encoding/json"
	"fmt"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
)

type LastMileSequenceState struct {
	EpochId       int   `json:"epochId"`
	LeaderIndex   int   `json:"leaderIndex"`
	BlockIndex    int   `json:"blockIndex"`
	NextHeight    int64 `json:"nextHeight"`
	HeightInEpoch int   `json:"heightInEpoch"`
}

// CurrentBlockId returns the blockId at the current tracker position.
// It skips leaders that are known (via LastBlocksByLeaders) to have produced no blocks
// or whose blocks are fully processed. For leaders whose ALFP data is not yet available,
// it still returns a blockId so the caller can confirm the block via AFP.
func (s *LastMileSequenceState) CurrentBlockId(leadersSequence []string, lastBlocksByLeaders map[string]structures.ExecutionStats) string {
	for s.LeaderIndex < len(leadersSequence) {
		leader := leadersSequence[s.LeaderIndex]
		lastBlock, known := lastBlocksByLeaders[leader]

		if known && (lastBlock.Index < 0 || s.BlockIndex > lastBlock.Index) {
			s.LeaderIndex++
			s.BlockIndex = 0
			continue
		}

		return fmt.Sprintf("%d:%s:%d", s.EpochId, leader, s.BlockIndex)
	}

	return ""
}

// IsBlockConfirmed checks whether the block at the current position is confirmed.
// A block is confirmed if:
//  1. The next block by the same leader has a verified AFP in local DB (chain continuity), OR
//  2. This is the last block for the leader according to LastBlocksByLeaders (ALFP data).
//
// Returns (confirmed bool, isLastBlock bool).
func (s *LastMileSequenceState) IsBlockConfirmed(
	leadersSequence []string,
	lastBlocksByLeaders map[string]structures.ExecutionStats,
	blockHash string,
	epochHandler *structures.EpochDataHandler,
) (bool, bool) {
	if s.LeaderIndex >= len(leadersSequence) {
		return false, false
	}

	leader := leadersSequence[s.LeaderIndex]
	lastBlock, known := lastBlocksByLeaders[leader]

	if known && lastBlock.Index == s.BlockIndex && lastBlock.Hash == blockHash {
		return true, true
	}

	nextBlockId := fmt.Sprintf("%d:%s:%d", s.EpochId, leader, s.BlockIndex+1)
	if HasLocalVerifiedAfp(nextBlockId, epochHandler) {
		return true, false
	}

	return false, false
}

// AllLeadersDone returns true when the tracker has moved past all leaders in the sequence.
func (s *LastMileSequenceState) AllLeadersDone(leadersSequence []string) bool {
	return s.LeaderIndex >= len(leadersSequence)
}

// Advance moves to the next block position after the current one is processed.
func (s *LastMileSequenceState) Advance() {
	s.BlockIndex++
	s.NextHeight++
	s.HeightInEpoch++
}

// AdvanceToNextEpoch resets the tracker for the next epoch.
func (s *LastMileSequenceState) AdvanceToNextEpoch() {
	s.EpochId++
	s.LeaderIndex = 0
	s.BlockIndex = 0
	s.HeightInEpoch = 0
}

// HasLocalVerifiedAfp checks if a verified AFP for the given blockId exists in local EPOCH_DATA DB.
func HasLocalVerifiedAfp(blockId string, epochHandler *structures.EpochDataHandler) bool {
	if epochHandler == nil {
		return false
	}

	raw, err := databases.EPOCH_DATA.Get([]byte(constants.DBKeyPrefixAfp+blockId), nil)
	if err != nil {
		return false
	}

	var afp structures.AggregatedFinalizationProof
	if json.Unmarshal(raw, &afp) != nil {
		return false
	}

	return VerifyAggregatedFinalizationProof(&afp, epochHandler)
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
	key := fmt.Sprintf(constants.DBKeyPrefixLastMileHeightMap+"%d", height)
	_ = databases.FINALIZATION_VOTING_STATS.Put([]byte(key), []byte(blockId), nil)
}

func LoadHeightBlockIdMapping(height int64) string {
	key := fmt.Sprintf(constants.DBKeyPrefixLastMileHeightMap+"%d", height)
	raw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte(key), nil)
	if err != nil {
		return ""
	}
	return string(raw)
}

const dbKeyPrefixHeightInEpochMap = "HEIGHT_IN_EPOCH_MAP:"

func StoreHeightInEpochMapping(height int64, heightInEpoch int) {
	key := fmt.Sprintf("%s%d", dbKeyPrefixHeightInEpochMap, height)
	_ = databases.FINALIZATION_VOTING_STATS.Put([]byte(key), []byte(fmt.Sprintf("%d", heightInEpoch)), nil)
}

func LoadHeightInEpochMapping(height int64) (int, bool) {
	key := fmt.Sprintf("%s%d", dbKeyPrefixHeightInEpochMap, height)
	raw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte(key), nil)
	if err != nil {
		return 0, false
	}
	var val int
	if _, err := fmt.Sscanf(string(raw), "%d", &val); err != nil {
		return 0, false
	}
	return val, true
}
