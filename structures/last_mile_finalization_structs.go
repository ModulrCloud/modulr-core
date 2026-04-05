package structures

import "encoding/json"

type HeightAttestation struct {
	AbsoluteHeight int               `json:"absoluteHeight"`
	BlockId        string            `json:"blockId"`
	BlockHash      string            `json:"blockHash"`
	EpochId        int               `json:"epochId"`
	HeightInEpoch  int               `json:"heightInEpoch"`
	Proofs         map[string]string `json:"proofs"`
}

func (ha *HeightAttestation) UnmarshalJSON(data []byte) error {
	type alias HeightAttestation

	var aux alias

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	*ha = HeightAttestation(aux)

	return nil
}

func (ha HeightAttestation) MarshalJSON() ([]byte, error) {
	type alias HeightAttestation

	aux := alias(ha)

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	return json.Marshal(aux)
}

type QuorumRotationAttestation struct {
	EpochId       int               `json:"epochId"`
	NextEpochId   int               `json:"nextEpochId"`
	NextEpochHash string            `json:"nextEpochHash"`
	NextQuorum    []string          `json:"nextQuorum"`
	Proofs        map[string]string `json:"proofs"`
}

func (qra *QuorumRotationAttestation) UnmarshalJSON(data []byte) error {
	type alias QuorumRotationAttestation

	var aux alias

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	*qra = QuorumRotationAttestation(aux)

	return nil
}

func (qra QuorumRotationAttestation) MarshalJSON() ([]byte, error) {
	type alias QuorumRotationAttestation

	aux := alias(qra)

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	return json.Marshal(aux)
}
