package structures

import "github.com/modulrcloud/modulr-core/constants"

type VotingStat struct {
	Index int                         `json:"index"`
	Hash  string                      `json:"hash"`
	Afp   AggregatedFinalizationProof `json:"afp"`
}

func NewLeaderVotingStatTemplate() VotingStat {
	return VotingStat{
		Index: -1,
		Hash:  constants.ZeroBlockHash,
		Afp:   AggregatedFinalizationProof{},
	}
}
