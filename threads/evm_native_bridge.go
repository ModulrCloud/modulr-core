package threads

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/holiman/uint256"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/units"
	"github.com/modulrcloud/modulr-core/utils"
)

var errUint256Overflow = errors.New("amount overflows uint256")

func transferNativeToEVMAddress(evmAddress string, nativeAmount uint64) error {
	r, err := ensureEVMRunner()
	if err != nil {
		return err
	}
	addr := common.HexToAddress(evmAddress)
	wei := units.NativeUnitsToWeiBig(nativeAmount)
	amt, overflow := uint256.FromBig(wei)
	if overflow {
		return errUint256Overflow
	}
	r.Fund(addr, amt)
	execEVMDirtied = true
	return nil
}

type evmConnectorPayload struct {
	To string `json:"to"`
}

func processEVMToNativeConnectorTransfer(tx *types.Transaction) (bool, string) {
	if tx == nil || tx.To() == nil {
		return false, ""
	}
	if !strings.EqualFold(tx.To().Hex(), constants.EVMBridgeConnectorAddress) {
		return false, ""
	}

	var payload evmConnectorPayload
	if err := json.Unmarshal(tx.Data(), &payload); err != nil {
		return false, "invalid connector payload"
	}
	if !cryptography.IsValidPubKey(payload.To) {
		return false, "invalid connector recipient"
	}

	nativeAmount, ok := units.WeiToNativeUnitsExact(tx.Value())
	if !ok {
		return false, "connector transfer value must be divisible by 1e9 wei"
	}

	accountTo := utils.GetAccountFromExecThreadState(payload.To)
	accountTo.Balance += nativeAmount

	return true, ""
}
