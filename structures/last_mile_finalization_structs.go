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

type EpochDataAttestation struct {
	EpochId       int                  `json:"epochId"`
	NextEpochId   int                  `json:"nextEpochId"`
	EpochData     NextEpochDataHandler `json:"epochData"`
	EpochDataHash string               `json:"epochDataHash"`
	Proofs        map[string]string    `json:"proofs"`
}

func (eda *EpochDataAttestation) UnmarshalJSON(data []byte) error {
	type alias EpochDataAttestation

	var aux alias

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	*eda = EpochDataAttestation(aux)

	return nil
}

func (eda EpochDataAttestation) MarshalJSON() ([]byte, error) {
	type alias EpochDataAttestation

	aux := alias(eda)

	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}

	return json.Marshal(aux)
}
