package threads

import (
	"math/big"

	"github.com/ethereum/go-ethereum/core/types"
)

var evmChainID = big.NewInt(1) // should come from config/genesis later
var evmBaseFee = big.NewInt(0) // fixed baseFeePerGas (wei) for now; used for wallet UX and EffectiveGasPrice

func init() {
	// Modulr EVM chainId
	evmChainID = big.NewInt(7338)
	// Default baseFeePerGas = 1 gwei (wallet UX).
	evmBaseFee = big.NewInt(1_000_000_000)
}

func EVMChainID() *big.Int { return new(big.Int).Set(evmChainID) }
func EVMBaseFee() *big.Int { return new(big.Int).Set(evmBaseFee) }

func EVMParseSignedTx0x(raw0x string) (*types.Transaction, error) {
	return parseSignedTx0x(raw0x)
}

func EVMSandboxSignedTx(tx *types.Transaction) error {
	EVMLock()
	defer EVMUnlock()
	r, err := ensureEVMRunner()
	if err != nil {
		return err
	}
	_, _, _, err = r.SimulateSignedTx(tx, evmChainID)
	return err
}
