package routes

import (
	"math/big"
	"strings"
)

var (
	weiPerEther = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil) // 1e18
)

func parseHexQuantityToBig(s string) *big.Int {
	s = strings.TrimSpace(s)
	if s == "" {
		return big.NewInt(0)
	}
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return big.NewInt(0)
	}
	n := new(big.Int)
	if _, ok := n.SetString(s, 16); ok {
		return n
	}
	return big.NewInt(0)
}

// formatWeiToEtherString converts wei to a decimal string with up to 18 fractional digits (trimmed).
// Example: 1000000000000000000 -> "1", 1500000000000000000 -> "1.5"
func formatWeiToEtherString(wei *big.Int) string {
	if wei == nil || wei.Sign() == 0 {
		return "0"
	}
	sign := ""
	if wei.Sign() < 0 {
		sign = "-"
		wei = new(big.Int).Abs(wei)
	}
	intPart := new(big.Int).Quo(wei, weiPerEther)
	fracPart := new(big.Int).Mod(wei, weiPerEther)
	if fracPart.Sign() == 0 {
		return sign + intPart.String()
	}
	// left-pad to 18 digits, then trim right zeros
	fracStr := fracPart.Text(10)
	if len(fracStr) < 18 {
		fracStr = strings.Repeat("0", 18-len(fracStr)) + fracStr
	}
	fracStr = strings.TrimRight(fracStr, "0")
	return sign + intPart.String() + "." + fracStr
}

// etherFloorUint64 returns floor(wei / 1e18) clamped to uint64 max.
// Useful for legacy modulr-core APIs that represent balances as uint64.
func etherFloorUint64(wei *big.Int) uint64 {
	if wei == nil || wei.Sign() <= 0 {
		return 0
	}
	q := new(big.Int).Quo(wei, weiPerEther)
	if q.Sign() <= 0 {
		return 0
	}
	if q.BitLen() > 64 {
		return ^uint64(0)
	}
	return q.Uint64()
}

func is0xHexLen(s string, hexLen int) bool {
	s = strings.TrimSpace(s)
	if !(strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
		return false
	}
	h := s[2:]
	if len(h) != hexLen {
		return false
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}
