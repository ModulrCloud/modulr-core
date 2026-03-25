package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"runtime"
	"syscall"

	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

func main() {

	configsRawJson, readError := os.ReadFile(globals.CHAINDATA_PATH + "/configs.json")

	if readError != nil {

		panic("Error while reading configs: " + readError.Error())

	}

	if err := json.Unmarshal(configsRawJson, &globals.CONFIGURATION); err != nil {

		panic("Error with configs parsing: " + err.Error())

	}

	anchorsRawJson, readError := os.ReadFile(globals.CHAINDATA_PATH + "/anchors.json")

	if readError != nil {

		panic("Error while reading anchors: " + readError.Error())

	}

	var anchorsList []structures.Anchor

	if err := json.Unmarshal(anchorsRawJson, &anchorsList); err != nil {

		panic("Error with anchors parsing: " + err.Error())

	}

	globals.ANCHORS = anchorsList

	globals.ANCHORS_PUBKEYS = make([]string, len(anchorsList))

	for i, anchor := range anchorsList {
		globals.ANCHORS_PUBKEYS[i] = anchor.Pubkey
	}

	genesisRawJson, readError := os.ReadFile(globals.CHAINDATA_PATH + "/genesis.json")

	if readError != nil {

		panic("Error while reading genesis: " + readError.Error())

	}

	if err := json.Unmarshal(genesisRawJson, &globals.GENESIS); err != nil {

		panic("Error with genesis parsing: " + err.Error())

	}

	username := "unknown"
	if currentUser, err := user.Current(); err == nil {
		username = currentUser.Username
	}

	statsStringToPrint := fmt.Sprintf("System info \x1b[31mgolang:%s \033[36;1m/\x1b[31m os info:%s # %s # cpu:%d \033[36;1m/\x1b[31m runned as:%s\x1b[0m", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), username)

	utils.LogWithTime(statsStringToPrint, utils.CYAN_COLOR)

	go signalHandler()

	// Function that runs the main logic

	RunBlockchain()

}

// Function to handle Ctrl+C interruptions
func signalHandler() {

	sig := make(chan os.Signal, 1)

	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	<-sig

	utils.GracefulShutdown()

}
