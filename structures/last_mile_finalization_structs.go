package structures

import "encoding/json"

type AggregatedHeightProof struct {
	AbsoluteHeight int               `json:"absoluteHeight"`
	BlockId        string            `json:"blockId"`
	BlockHash      string            `json:"blockHash"`
	EpochId        int               `json:"epochId"`
	HeightInEpoch  int               `json:"heightInEpoch"`
	Proofs         map[string]string `json:"proofs"`
}

type AggregatedHeightProofInfo struct {
	AbsoluteHeight int    `json:"absoluteHeight"`
	BlockId        string `json:"blockId"`
	BlockHash      string `json:"blockHash"`
	EpochId        int    `json:"epochId"`
}

func (ha *AggregatedHeightProof) UnmarshalJSON(data []byte) error {
	type alias AggregatedHeightProof

	var aux alias

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	*ha = AggregatedHeightProof(aux)

	return nil
}

func (ha AggregatedHeightProof) MarshalJSON() ([]byte, error) {
	type alias AggregatedHeightProof

	aux := alias(ha)

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	return json.Marshal(aux)
}

type AggregatedAnchorEpochAckProof struct {
	EpochId       int               `json:"epochId"`
	NextEpochId   int               `json:"nextEpochId"`
	EpochDataHash string            `json:"epochDataHash"`
	Proofs        map[string]string `json:"proofs"`
}

func (a *AggregatedAnchorEpochAckProof) UnmarshalJSON(data []byte) error {
	type alias AggregatedAnchorEpochAckProof

	var aux alias

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	*a = AggregatedAnchorEpochAckProof(aux)

	return nil
}

func (a AggregatedAnchorEpochAckProof) MarshalJSON() ([]byte, error) {
	type alias AggregatedAnchorEpochAckProof

	aux := alias(a)

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	return json.Marshal(aux)
}

// LastMileEpochBoundary is a durable local checkpoint for a fully sequenced epoch.
// For empty epochs FinishedOnHeight may point to the previous confirmed height.
type LastMileEpochBoundary struct {
	EpochId           int    `json:"epochId"`
	FinishedOnHeight  int64  `json:"finishedOnHeight"`
	FinishedOnBlockId string `json:"finishedOnBlockId"`
	FinishedOnHash    string `json:"finishedOnHash"`
}

type AggregatedEpochRotationProof struct {
	EpochId           int                  `json:"epochId"`
	NextEpochId       int                  `json:"nextEpochId"`
	EpochData         NextEpochDataHandler `json:"epochData"`
	EpochDataHash     string               `json:"epochDataHash"`
	FinishedOnHeight  int64                `json:"finishedOnHeight"`
	FinishedOnBlockId string               `json:"finishedOnBlockId"`
	FinishedOnHash    string               `json:"finishedOnHash"`
	Proofs            map[string]string    `json:"proofs"`
}

func (eda *AggregatedEpochRotationProof) UnmarshalJSON(data []byte) error {
	type alias AggregatedEpochRotationProof

	var aux alias

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	*eda = AggregatedEpochRotationProof(aux)

	return nil
}

func (eda AggregatedEpochRotationProof) MarshalJSON() ([]byte, error) {
	type alias AggregatedEpochRotationProof

	aux := alias(eda)

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	return json.Marshal(aux)
}
