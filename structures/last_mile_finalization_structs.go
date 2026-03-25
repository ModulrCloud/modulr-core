package structures

import "encoding/json"

type LastMileFinalizationProof struct {
	AbsoluteHeight int               `json:"absoluteHeight"`
	BlockId        string            `json:"blockId"`
	BlockHash      string            `json:"blockHash"`
	Proofs         map[string]string `json:"proofs"`
}

func (lmfp *LastMileFinalizationProof) UnmarshalJSON(data []byte) error {

	type alias LastMileFinalizationProof

	var aux alias

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	*lmfp = LastMileFinalizationProof(aux)

	return nil
}

func (lmfp LastMileFinalizationProof) MarshalJSON() ([]byte, error) {

	type alias LastMileFinalizationProof

	aux := alias(lmfp)

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	return json.Marshal(aux)
}
