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
