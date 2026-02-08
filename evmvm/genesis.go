package evmvm

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/holiman/uint256"
)

// ApplyGenesisAlloc seeds accounts/contracts into the given StateDB (genesis-like).
// Caller should Commit() the runner afterwards to persist and obtain the new root.
func ApplyGenesisAlloc(st *state.StateDB, alloc types.GenesisAlloc) error {
	for addr, acct := range alloc {
		if !st.Exist(addr) {
			st.CreateAccount(addr)
		}
		if acct.Balance == nil {
			acct.Balance = new(big.Int)
		}
		if acct.Balance.Sign() < 0 {
			return fmt.Errorf("negative balance for %s", addr.Hex())
		}
		bal, overflow := uint256.FromBig(acct.Balance)
		if overflow {
			return fmt.Errorf("balance overflows uint256 for %s", addr.Hex())
		}
		st.SetBalance(addr, bal, tracing.BalanceIncreaseGenesisBalance)
		st.SetNonce(addr, acct.Nonce, tracing.NonceChangeGenesis)
		if len(acct.Code) > 0 {
			st.SetCode(addr, acct.Code, tracing.CodeChangeGenesis)
		}
		for k, v := range acct.Storage {
			st.SetState(addr, k, v)
		}
	}
	return nil
}
