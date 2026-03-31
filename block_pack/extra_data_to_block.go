package block_pack

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/structures"
)

type ExtraDataToBlock struct {
	DelayedTransactionsBatch structures.DelayedTransactionsBatch `json:"delayedTxsBatch"`
	Rest                     map[string]string                   `json:"rest"`
}

func (ed ExtraDataToBlock) MarshalJSON() ([]byte, error) {
	type alias ExtraDataToBlock

	aux := alias(ed)

	// Normalize empty maps to nil so JSON uses `null` instead of {}
	if aux.Rest != nil && len(aux.Rest) == 0 {
		aux.Rest = nil
	}

	return json.Marshal(aux)
}

func (ed *ExtraDataToBlock) UnmarshalJSON(data []byte) error {
	type alias ExtraDataToBlock

	var aux alias

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.Rest == nil {
		aux.Rest = make(map[string]string)
	}

	*ed = ExtraDataToBlock(aux)

	return nil
}
