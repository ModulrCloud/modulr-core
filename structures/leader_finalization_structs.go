package structures

import "encoding/json"

type AcceptLeaderFinalizationProofRequest struct {
	LeaderFinalizations []AggregatedLeaderFinalizationProof `json:"leaderFinalizations"`
}

type AggregatedLeaderFinalizationProof struct {
	EpochIndex int               `json:"epochIndex"`
	Leader     string            `json:"leader"`
	VotingStat VotingStat        `json:"votingStat"`
	Signatures map[string]string `json:"signatures"`
}

func (alfp *AggregatedLeaderFinalizationProof) UnmarshalJSON(data []byte) error {
	type alias AggregatedLeaderFinalizationProof

	var aux alias

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Signatures == nil {
		aux.Signatures = make(map[string]string)
	}

	*alfp = AggregatedLeaderFinalizationProof(aux)

	return nil
}

func (alfp AggregatedLeaderFinalizationProof) MarshalJSON() ([]byte, error) {
	type alias AggregatedLeaderFinalizationProof

	aux := alias(alfp)

	if aux.Signatures == nil {
		aux.Signatures = make(map[string]string)
	}

	return json.Marshal(aux)
}
