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

func LogWithTime(msg, msgColor string) {

	formattedDate := time.Now().Format("02 January 2006 at 03:04:05 PM")

	fmt.Printf(DEEP_GREEN_COLOR+"[%s]"+MAGENTA_COLOR+"(pid:%d)"+msgColor+"  %s\n"+RESET_COLOR, formattedDate, os.Getpid(), msg)

}

func LogWithTime2(msg, msgColor string) {

	formattedDate := time.Now().Format("02 January 2006 at 03:04:05 PM")

	fmt.Printf(DEEP_YELLOW+"[%s]"+MAGENTA_COLOR+"(pid:%d)"+msgColor+"  %s\n"+RESET_COLOR, formattedDate, os.Getpid(), msg)

}

func Blake3(data string) string {

	blake3Hash := blake3.Sum256([]byte(data))

	return hex.EncodeToString(blake3Hash[:])

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
