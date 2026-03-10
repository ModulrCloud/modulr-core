package units

import (
	"errors"
	"math/big"
	"strings"

	"github.com/modulrcloud/modulr-core/constants"
)

var weiPerNativeUnitBig = new(big.Int).SetUint64(constants.WeiPerNativeUnit)

func NativeUnitsToWeiBig(amount uint64) *big.Int {
	return new(big.Int).Mul(new(big.Int).SetUint64(amount), weiPerNativeUnitBig)
}

// WeiToNativeUnitsFloor converts wei into native smallest units by flooring the remainder.
func WeiToNativeUnitsFloor(wei *big.Int) uint64 {
	if wei == nil || wei.Sign() <= 0 {
		return 0
	}
	q := new(big.Int).Quo(wei, weiPerNativeUnitBig)
	if q.Sign() <= 0 {
		return 0
	}
	if q.BitLen() > 64 {
		return ^uint64(0)
	}
	return q.Uint64()
}

// WeiToNativeUnitsExact converts wei into native smallest units only if it is exactly divisible by 1e9.
func WeiToNativeUnitsExact(wei *big.Int) (uint64, bool) {
	if wei == nil || wei.Sign() < 0 {
		return 0, false
	}
	if wei.Sign() == 0 {
		return 0, true
	}
	rem := new(big.Int).Mod(wei, weiPerNativeUnitBig)
	if rem.Sign() != 0 {
		return 0, false
	}
	q := new(big.Int).Quo(wei, weiPerNativeUnitBig)
	if q.BitLen() > 64 {
		return 0, false
	}
	return q.Uint64(), true
}

func FormatNativeUnits(amount uint64) string {
	intPart := amount / constants.NativeScale
	fracPart := amount % constants.NativeScale
	if fracPart == 0 {
		return new(big.Int).SetUint64(intPart).String()
	}
	fracStr := new(big.Int).SetUint64(fracPart).Text(10)
	if len(fracStr) < int(constants.NativeDecimals) {
		fracStr = strings.Repeat("0", int(constants.NativeDecimals)-len(fracStr)) + fracStr
	}
	fracStr = strings.TrimRight(fracStr, "0")
	return new(big.Int).SetUint64(intPart).String() + "." + fracStr
}

func ParseNativeDecimalToUnits(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty amount")
	}
	if strings.HasPrefix(s, "-") {
		return 0, errors.New("negative amount")
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return 0, errors.New("invalid decimal format")
	}
	intPart := parts[0]
	if intPart == "" {
		intPart = "0"
	}
	intBig := new(big.Int)
	if _, ok := intBig.SetString(intPart, 10); !ok || intBig.Sign() < 0 {
		return 0, errors.New("invalid integer part")
	}
	intScaled := new(big.Int).Mul(intBig, new(big.Int).SetUint64(constants.NativeScale))
	if intScaled.BitLen() > 64 {
		return 0, errors.New("amount overflow")
	}
	result := intScaled.Uint64()

	if len(parts) == 2 {
		frac := strings.TrimSpace(parts[1])
		if len(frac) > int(constants.NativeDecimals) {
			return 0, errors.New("too many fractional digits")
		}
		for len(frac) < int(constants.NativeDecimals) {
			frac += "0"
		}
		if frac != "" {
			fracBig := new(big.Int)
			if _, ok := fracBig.SetString(frac, 10); !ok || fracBig.Sign() < 0 {
				return 0, errors.New("invalid fractional part")
			}
			if fracBig.BitLen() > 64 {
				return 0, errors.New("fraction overflow")
			}
			f := fracBig.Uint64()
			if ^uint64(0)-result < f {
				return 0, errors.New("amount overflow")
			}
			result += f
		}
	}

	return result, nil
}
