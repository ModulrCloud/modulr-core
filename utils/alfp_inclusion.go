package utils

import (
	"encoding/json"
	"fmt"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb/util"
)

// AlfpInclusionMarker is a durable record that a valid ALFP was observed inside an accepted anchor block.
// Stored under FINALIZATION_VOTING_STATS with key prefix "ALFP_INCLUDED:".
type AlfpInclusionMarker struct {
	Hash    string `json:"hash"`
	BlockId string `json:"blockId"`
	Anchor  string `json:"anchor"`
}

func alfpIncludedKey(epochId int, leader string, index int) []byte {
	return []byte(fmt.Sprintf("ALFP_INCLUDED:%d:%s:%d", epochId, leader, index))
}

func StoreAlfpIncluded(marker AlfpInclusionMarker, epochId int, leader string, index int) {
	if leader == "" || index < 0 || marker.Hash == "" {
		return
	}
	if payload, err := json.Marshal(marker); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put(alfpIncludedKey(epochId, leader, index), payload, nil)
	}
}

func IsAlfpIncluded(epochId int, leader string, index int, hash string) bool {
	if leader == "" || index < 0 || hash == "" {
		return false
	}
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(alfpIncludedKey(epochId, leader, index), nil)
	if err != nil || len(raw) == 0 {
		return false
	}
	var marker AlfpInclusionMarker
	if json.Unmarshal(raw, &marker) != nil {
		return false
	}
	return marker.Hash == hash
}

func IsAggregatedAlfpIncluded(alfp *structures.AggregatedLeaderFinalizationProof) bool {
	if alfp == nil {
		return false
	}
	return IsAlfpIncluded(alfp.EpochIndex, alfp.Leader, alfp.VotingStat.Index, alfp.VotingStat.Hash)
}

// HasAnyAlfpIncluded returns true if there exists at least one ALFP_INCLUDED marker
// for the (epochId, leader) pair, regardless of index/hash.
func HasAnyAlfpIncluded(epochId int, leader string) bool {
	if leader == "" {
		return false
	}
	prefix := []byte(fmt.Sprintf("ALFP_INCLUDED:%d:%s:", epochId, leader))
	it := databases.FINALIZATION_VOTING_STATS.NewIterator(util.BytesPrefix(prefix), nil)
	defer it.Release()
	return it.Next()
}
