package threads

import (
	"math/big"

	"github.com/ethereum/go-ethereum/core/types"
)

var evmChainID = big.NewInt(1) // should come from config/genesis later

func init() {
	// Modulr EVM chainId
	evmChainID = big.NewInt(7337)
}

func EVMParseSignedTx0x(raw0x string) (*types.Transaction, error) {
	return parseSignedTx0x(raw0x)
}

func EVMSandboxSignedTx(tx *types.Transaction) error {
	r, err := ensureEVMRunner()
	if err != nil {
		return err
	}
	_, _, _, err = r.SimulateSignedTx(tx, evmChainID)
	return err
}

