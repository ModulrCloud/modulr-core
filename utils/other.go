package utils

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"

	"lukechampine.com/blake3"
)

// ANSI escape codes for text colors
const (
	RESET_COLOR      = "\033[0m"
	RED_COLOR        = "\033[31;1m"
	DEEP_GREEN_COLOR = "\u001b[38;5;23m"
	DEEP_GRAY        = "\u001b[38;5;240m"
	DEEP_MAGENTA     = "\u001b[38;5;90m"
	DEEP_YELLOW      = "\u001b[38;5;214m"
	GREEN_COLOR      = "\033[32;1m"
	YELLOW_COLOR     = "\033[33m"
	MAGENTA_COLOR    = "\033[38;5;99m"
	CYAN_COLOR       = "\033[36;1m"
	WHITE_COLOR      = "\033[37;1m"
)

var SHUTDOWN_ONCE sync.Once

func StrToUint8(s string) uint8 {
	v, err := strconv.ParseUint(s, 10, 8)
	if err != nil {
		return 0
	}
	return uint8(v)
}

func GracefulShutdown() {
	SHUTDOWN_ONCE.Do(func() {
		LogWithTime("Stop signal has been initiated.Keep waiting...", CYAN_COLOR)

		LogWithTime("Closing server connections...", CYAN_COLOR)

		if err := databases.CloseAll(); err != nil {
			LogWithTime(fmt.Sprintf("failed to close databases: %v", err), RED_COLOR)
		}

		LogWithTime("Node was gracefully stopped", GREEN_COLOR)

		os.Exit(0)
	})
}

func ShortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

func Blake3(data string) string {
	blake3Hash := blake3.Sum256([]byte(data))

	return hex.EncodeToString(blake3Hash[:])
}

// HashHexToUint64 converts the first 8 bytes (16 hex chars) of a hex string
// into a big-endian uint64. Returns 0 if the input is too short or invalid.
func HashHexToUint64(hashHex string) uint64 {
	if len(hashHex) < 16 {
		return 0
	}
	b, err := hex.DecodeString(hashHex[:16])
	if err != nil {
		return 0
	}
	var r uint64
	for _, by := range b {
		r = (r << 8) | uint64(by)
	}
	return r
}

func GetUTCTimestampInMilliSeconds() int64 {
	return time.Now().UTC().UnixMilli()
}

func IsMyCoreVersionOld(thread structures.LogicalThread) bool {
	return thread.GetCoreMajorVersion() > globals.CORE_MAJOR_VERSION
}

func EpochStillFresh(thread structures.LogicalThread) bool {
	return (thread.GetEpochHandler().StartTimestamp + uint64(thread.GetNetworkParams().EpochDuration)) > uint64(GetUTCTimestampInMilliSeconds())
}

func SignalAboutEpochRotationExists(epochIndex int) bool {
	keyValue := []byte(constants.DBKeyPrefixEpochFinish + strconv.Itoa(epochIndex))

	if readyToChangeEpochRaw, err := databases.EPOCH_DATA.Get(keyValue, nil); err == nil && string(readyToChangeEpochRaw) == "TRUE" {
		return true
	}

	return false
}
