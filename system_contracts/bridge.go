package system_contracts

import (
	"strconv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/utils"
)

func BridgeImport(bridgeTxPayload map[string]string, context string) bool {
	return true
}

func BridgeExport(bridgeTxPayload map[string]string, context string) bool {

	sender := bridgeTxPayload["from"]
	amount, err := strconv.ParseUint(bridgeTxPayload["amount"], 10, 64)

	if err != nil || amount == 0 {
		return false
	}

	if context == constants.ContextExecutionThread {

		account := utils.GetAccountFromExecThreadState(sender)

		if account == nil {
			return false
		}

		if account.Balance < amount {
			return false
		}

		account.Balance -= amount

		return true

	}

	return false

}
