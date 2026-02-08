package evmvm

import (
	"fmt"

	"github.com/btcsuite/btcutil/base58"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// ModulrPubKeyToEVMAddress deterministically maps a Modulr ed25519 pubkey (base58 of 32 bytes)
// to an EVM address: keccak256(pubkeyBytes)[12:].
func ModulrPubKeyToEVMAddress(modulrBase58PubKey string) (common.Address, error) {
	pub := base58.Decode(modulrBase58PubKey)
	if len(pub) != 32 {
		return common.Address{}, fmt.Errorf("invalid modulr pubkey (expected 32 bytes after base58 decode)")
	}
	sum := crypto.Keccak256(pub)
	return common.BytesToAddress(sum[12:]), nil
}
