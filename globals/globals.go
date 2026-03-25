package globals

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/modulrcloud/modulr-core/structures"
)

var CORE_MAJOR_VERSION = func() int {

	exePath, err := os.Executable()

	if err != nil {
		panic("Failed to determine executable path: " + err.Error())
	}

	versionPath := filepath.Join(filepath.Dir(exePath), "version.txt")

	data, err := os.ReadFile(versionPath)

	if err != nil {
		data, err = os.ReadFile("version.txt")
		if err != nil {
			panic("Failed to read version.txt: " + err.Error())
		}
	}

	version, err := strconv.Atoi(strings.TrimSpace(string(data)))

	if err != nil {
		panic("Invalid version format: " + err.Error())
	}

	return version

}()

var CHAINDATA_PATH = func() string {

	dirPath := os.Getenv("CHAINDATA_PATH")

	exeName := filepath.Base(os.Args[0])

	if dirPath == "" && (strings.HasSuffix(exeName, ".test") || strings.Contains(exeName, ".test")) {
		if tempDir, err := os.MkdirTemp("", "chaindata"); err == nil {
			dirPath = tempDir
		}
	}

	if dirPath == "" {

		panic("CHAINDATA_PATH environment variable is not set")

	}

	dirPath = strings.TrimRight(dirPath, "/")

	if !filepath.IsAbs(dirPath) {

		panic("CHAINDATA_PATH must be an absolute path")

	}

	// Check if exists
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {

		// If no - create
		if err := os.MkdirAll(dirPath, os.ModePerm); err != nil {

			panic("Error with creating directory for chaindata: " + err.Error())

		}

	}

	return dirPath

}()

var CONFIGURATION structures.NodeLevelConfig

var GENESIS structures.Genesis

var ANCHORS []structures.Anchor

var ANCHORS_PUBKEYS []string

var MEMPOOL struct {
	Slice []structures.Transaction
	Mutex sync.Mutex
}

// Flag to use in websocket & http routes to prevent flood of .RLock() calls on mutexes

var FLOOD_PREVENTION_FLAG_FOR_ROUTES atomic.Bool
