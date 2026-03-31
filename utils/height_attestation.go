package utils

import (
	"encoding/json"
	"fmt"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/structures"
)

type HeightAttestationInfo struct {
	AbsoluteHeight int    `json:"absoluteHeight"`
	BlockId        string `json:"blockId"`
	BlockHash      string `json:"blockHash"`
	EpochId        int    `json:"epochId"`
}

// LoadHeightAttestationInfo reads a HeightAttestation from FINALIZATION_VOTING_STATS
// and returns a summary suitable for the recovery API.
func LoadHeightAttestationInfo(absoluteHeight int) *HeightAttestationInfo {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixHeightAttestation, absoluteHeight))

	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	if err != nil {
		return nil
	}

	var proof structures.HeightAttestation
	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}

	return &HeightAttestationInfo{
		AbsoluteHeight: proof.AbsoluteHeight,
		BlockId:        proof.BlockId,
		BlockHash:      proof.BlockHash,
		EpochId:        proof.EpochId,
	}
}
