package structures

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/constants"
)

type QuorumMemberData struct {
	PubKey, Url string
}

type DelayedTransactionsBatch struct {
	EpochIndex          int                 `json:"epochIndex"`
	DelayedTransactions []map[string]string `json:"delayedTransactions"`
	Proofs              map[string]string   `json:"proofs"`
}

func (dtb DelayedTransactionsBatch) MarshalJSON() ([]byte, error) {
	type alias DelayedTransactionsBatch

	if dtb.DelayedTransactions == nil {
		dtb.DelayedTransactions = make([]map[string]string, 0)
	}

	if dtb.Proofs == nil {
		dtb.Proofs = make(map[string]string)
	}

	return json.Marshal(alias(dtb))
}

func (dtb *DelayedTransactionsBatch) UnmarshalJSON(data []byte) error {
	type alias DelayedTransactionsBatch
	var aux alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if aux.DelayedTransactions == nil {
		aux.DelayedTransactions = make([]map[string]string, 0)
	}
	if aux.Proofs == nil {
		aux.Proofs = make(map[string]string)
	}
	*dtb = DelayedTransactionsBatch(aux)
	return nil
}

type ExecutionStats struct {
	Index int
	Hash  string
}

func NewExecutionStatsTemplate() ExecutionStats {
	return ExecutionStats{
		Index: -1,
		Hash:  constants.ZeroHash,
	}
}
