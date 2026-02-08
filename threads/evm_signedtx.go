package threads

import (
	"encoding/hex"
	"errors"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func parseSignedTx0x(raw0x string) (*types.Transaction, error) {
	raw0x = strings.TrimSpace(raw0x)
	if !strings.HasPrefix(raw0x, "0x") {
		return nil, errors.New("missing 0x prefix")
	}
	b, err := hex.DecodeString(strings.TrimPrefix(raw0x, "0x"))
	if err != nil {
		return nil, err
	}
	var tx types.Transaction
	if err := tx.UnmarshalBinary(b); err != nil {
		return nil, err
	}
	return &tx, nil
}

func deriveEVMBlockHash(modulrBlockHashHex string) common.Hash {
	modulrBlockHashHex = strings.TrimSpace(modulrBlockHashHex)
	b, err := hex.DecodeString(modulrBlockHashHex)
	if err != nil || len(b) == 0 {
		b = []byte(modulrBlockHashHex)
	}
	prefix := []byte("modulr-evm:")
	sum := crypto.Keccak256(append(prefix, b...))
	return common.BytesToHash(sum)
}

