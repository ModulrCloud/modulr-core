package utils

import (
	"encoding/json"
	"fmt"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/syndtr/goleveldb/leveldb"
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

func appendLastMileSequenceState(batch *leveldb.Batch, dbKey string, state *LastMileSequenceState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}

	batch.Put([]byte(dbKey), raw)

	return nil
}

func appendHeightMappings(batch *leveldb.Batch, height int64, blockId string, heightInEpoch int) {
	heightKey := fmt.Sprintf(constants.DBKeyPrefixLastMileHeightMap+"%d", height)
	batch.Put([]byte(heightKey), []byte(blockId))

	heightInEpochKey := fmt.Sprintf(constants.DBKeyPrefixHeightInEpochMap+"%d", height)
	batch.Put([]byte(heightInEpochKey), []byte(fmt.Sprintf("%d", heightInEpoch)))
}

func appendEpochSequencedMarker(batch *leveldb.Batch, epochId int) {
	key := fmt.Sprintf(constants.DBKeyPrefixLastMileEpochComplete+"%d", epochId)
	batch.Put([]byte(key), []byte{1})
}

func appendLastMileEpochBoundary(batch *leveldb.Batch, boundary *structures.LastMileEpochBoundary) error {
	if boundary == nil {
		return nil
	}

	raw, err := json.Marshal(boundary)
	if err != nil {
		return err
	}

	key := fmt.Sprintf(constants.DBKeyPrefixLastMileEpochBoundary+"%d", boundary.EpochId)
	batch.Put([]byte(key), raw)
	appendEpochSequencedMarker(batch, boundary.EpochId)

	return nil
}

func PersistLastMileMappingsAndState(dbKey string, height int64, blockId string, heightInEpoch int, state *LastMileSequenceState) error {
	batch := new(leveldb.Batch)

	appendHeightMappings(batch, height, blockId, heightInEpoch)

	if err := appendLastMileSequenceState(batch, dbKey, state); err != nil {
		return err
	}

	return databases.FINALIZATION_VOTING_STATS.Write(batch, nil)
}

func PersistLastMileMappingsAndStateTransition(
	dbKey string,
	height int64,
	blockId string,
	heightInEpoch int,
	state *LastMileSequenceState,
	completedBoundary *structures.LastMileEpochBoundary,
) error {
	batch := new(leveldb.Batch)

	appendHeightMappings(batch, height, blockId, heightInEpoch)

	if err := appendLastMileSequenceState(batch, dbKey, state); err != nil {
		return err
	}

	if err := appendLastMileEpochBoundary(batch, completedBoundary); err != nil {
		return err
	}

	return databases.FINALIZATION_VOTING_STATS.Write(batch, nil)
}

func PersistLastMileStateTransition(dbKey string, state *LastMileSequenceState, completedBoundary *structures.LastMileEpochBoundary) error {
	batch := new(leveldb.Batch)

	if err := appendLastMileSequenceState(batch, dbKey, state); err != nil {
		return err
	}

	if err := appendLastMileEpochBoundary(batch, completedBoundary); err != nil {
		return err
	}

	return databases.FINALIZATION_VOTING_STATS.Write(batch, nil)
}

func HasLocallySequencedFullEpoch(epochId int) bool {
	return LoadLastMileEpochBoundary(epochId) != nil
}

func LoadLastMileEpochBoundary(epochId int) *structures.LastMileEpochBoundary {
	key := fmt.Sprintf(constants.DBKeyPrefixLastMileEpochBoundary+"%d", epochId)
	raw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte(key), nil)
	if err != nil || len(raw) == 0 {
		return nil
	}

	var boundary structures.LastMileEpochBoundary
	if json.Unmarshal(raw, &boundary) != nil {
		return nil
	}

	return &boundary
}

func LoadHeightBlockIdMapping(height int64) string {
	key := fmt.Sprintf(constants.DBKeyPrefixLastMileHeightMap+"%d", height)
	raw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte(key), nil)
	if err != nil {
		return ""
	}
	return string(raw)
}

func LoadHeightInEpochMapping(height int64) (int, bool) {
	key := fmt.Sprintf("%s%d", constants.DBKeyPrefixHeightInEpochMap, height)
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
