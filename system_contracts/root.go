package system_contracts

type SystemContractTxExecutorFunction = func(map[string]string, string) bool

var SYSTEM_CONTRACTS_MAP = map[string]map[string]SystemContractTxExecutorFunction{
	"delayedTransactions": DELAYED_TRANSACTIONS_MAP,
	"bridge":              BRIDGE_MAP,
}

var DELAYED_TRANSACTIONS_MAP = map[string]SystemContractTxExecutorFunction{
	"createValidator": CreateValidator,
	"updateValidator": UpdateValidator,
	"stake":           Stake,
	"unstake":         Unstake,
}

var BRIDGE_MAP = map[string]SystemContractTxExecutorFunction{
	"import": BridgeImport,
	"export": BridgeExport,
}
