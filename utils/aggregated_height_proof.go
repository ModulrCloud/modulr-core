package utils

import (
	"encoding/json"
	"fmt"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
)

type AggregatedHeightProofInfo struct {
	AbsoluteHeight int    `json:"absoluteHeight"`
	BlockId        string `json:"blockId"`
	BlockHash      string `json:"blockHash"`
	EpochId        int    `json:"epochId"`
}

// LoadAggregatedHeightProofInfo reads an AggregatedHeightProof from FINALIZATION_THREAD_METADATA
// and returns a summary suitable for the recovery API.
func LoadAggregatedHeightProofInfo(absoluteHeight int) *AggregatedHeightProofInfo {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedHeightProof, absoluteHeight))

	raw, err := databases.FINALIZATION_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return nil
	}

	var proof structures.AggregatedHeightProof
	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}

	return &AggregatedHeightProofInfo{
		AbsoluteHeight: proof.AbsoluteHeight,
		BlockId:        proof.BlockId,
		BlockHash:      proof.BlockHash,
		EpochId:        proof.EpochId,
	}
}
