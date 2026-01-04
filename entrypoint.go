package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/http_pack"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/threads"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/modulrcloud/modulr-core/websocket_pack"

	"github.com/syndtr/goleveldb/leveldb"
)

func RunBlockchain() {

	if err := prepareBlockchain(); err != nil {

		utils.LogWithTime(fmt.Sprintf("Failed to prepare blockchain: %v", err), utils.RED_COLOR)

		utils.GracefulShutdown()

		return

	}

	waitUntilFirstEpochStart()

	//_________________________ RUN SEVERAL LOGICAL THREADS _________________________

	//✅ 1.Thread to rotate the epoch
	go threads.EpochRotationThread()

	//✅ 2.Share our blocks within quorum members and get the finalization proofs
	go threads.BlocksSharingAndProofsGrabingThread()

	//✅ 3.Start to generate blocks
	go threads.BlockGenerationThread()

	//✅ 4.Start a separate thread rotate the leaders and move from one to another
	go threads.LeaderRotationThread()

	//✅ 5.Thread to resolve the block sequence and prepare linear blocks data for ExecutionThread
	go threads.SequenceAlignmentThread()

	//✅ 6.Thread to jump between anchors and support SequenceAlignmentThread
	go threads.AnchorRotationMonitorThread()

	//✅ 7.Start execution process - take blocks and execute transactions to modify the state
	go threads.BlockExecutionThread()

	//✅ 8.Thread to get consensus about the last block by each leader, grab proofs and send to anchors
	go threads.LeaderFinalizationThread()

	//✅ 9.Thread to asynchronously find and store first block data in each epoch
	go threads.FirstBlockMonitorThread()

	//___________________ RUN SERVERS - WEBSOCKET AND HTTP __________________

	// Set the atomic flag to true

	globals.FLOOD_PREVENTION_FLAG_FOR_ROUTES.Store(true)

	go websocket_pack.CreateWebsocketServer()

	http_pack.CreateHTTPServer()

}

func waitUntilFirstEpochStart() {
	startMs := globals.GENESIS.FirstEpochStartTimestamp
	nowMs := uint64(utils.GetUTCTimestampInMilliSeconds())

	// If genesis start is in the future, sleep until that time so multiple nodes can start simultaneously.
	if startMs <= nowMs {
		return
	}

	diffMs := startMs - nowMs

	// Avoid overflow when converting to time.Duration (nanoseconds, int64).
	maxMs := uint64(math.MaxInt64) / uint64(time.Millisecond)
	if diffMs > maxMs {
		diffMs = maxMs
	}

	until := time.Duration(diffMs) * time.Millisecond

	utils.LogWithTime(
		fmt.Sprintf(
			"Genesis start is in the future. Sleeping for %s until FIRST_EPOCH_START_TIMESTAMP=%d ...",
			until.String(),
			startMs,
		),
		utils.YELLOW_COLOR,
	)

	// Sleep in chunks so we can be interrupted by SIGINT (and to keep logs reasonable for long delays).
	deadline := time.Now().Add(until)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		step := remaining
		if step > 5*time.Second {
			step = 5 * time.Second
		}
		time.Sleep(step)
	}

	utils.LogWithTime("Genesis start reached. Starting node threads...", utils.GREEN_COLOR)
}

func prepareBlockchain() error {

	if info, err := os.Stat(globals.CHAINDATA_PATH); err != nil {

		if os.IsNotExist(err) {

			if err := os.MkdirAll(globals.CHAINDATA_PATH, 0755); err != nil {

				return fmt.Errorf("create chaindata directory: %w", err)

			}

		} else {

			return fmt.Errorf("check chaindata directory: %w", err)

		}

	} else if !info.IsDir() {

		return fmt.Errorf("chaindata path %s exists and is not a directory", globals.CHAINDATA_PATH)

	}

	databases.BLOCKS = utils.OpenDb("BLOCKS")
	databases.STATE = utils.OpenDb("STATE")
	databases.EPOCH_DATA = utils.OpenDb("EPOCH_DATA")
	databases.APPROVEMENT_THREAD_METADATA = utils.OpenDb("APPROVEMENT_THREAD_METADATA")
	databases.FINALIZATION_VOTING_STATS = utils.OpenDb("FINALIZATION_VOTING_STATS")

	// Load GT - Generation Thread handler
	if data, err := databases.BLOCKS.Get([]byte("GT"), nil); err == nil {

		var gtHandler structures.GenerationThreadMetadataHandler

		if err := json.Unmarshal(data, &gtHandler); err != nil {
			return fmt.Errorf("unmarshal GENERATION_THREAD metadata: %w", err)
		}

		handlers.GENERATION_THREAD_METADATA = gtHandler

	} else {

		handlers.GENERATION_THREAD_METADATA = structures.GenerationThreadMetadataHandler{
			EpochFullId: utils.Blake3("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"+globals.GENESIS.NetworkId) + "#-1",
			PrevHash:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			NextIndex:   0,
		}

	}

	if data, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte("AT"), nil); err == nil {

		var atHandler structures.ApprovementThreadMetadataHandler

		if err := json.Unmarshal(data, &atHandler); err != nil {
			return fmt.Errorf("unmarshal APPROVEMENT_THREAD metadata: %w", err)
		}

		if atHandler.ValidatorsStoragesCache == nil {
			atHandler.ValidatorsStoragesCache = make(map[string]*structures.ValidatorStorage)
		}

		handlers.APPROVEMENT_THREAD_METADATA.Handler = atHandler

	}

	if data, err := databases.STATE.Get([]byte("ET"), nil); err == nil {

		var etHandler structures.ExecutionThreadMetadataHandler

		if err := json.Unmarshal(data, &etHandler); err != nil {
			return fmt.Errorf("unmarshal EXECUTION_THREAD metadata: %w", err)
		}

		if etHandler.AccountsCache == nil {
			etHandler.AccountsCache = make(map[string]*structures.Account)
		}

		if etHandler.ValidatorsStoragesCache == nil {
			etHandler.ValidatorsStoragesCache = make(map[string]*structures.ValidatorStorage)
		}

		if etHandler.Statistics == nil {
			etHandler.Statistics = &structures.Statistics{LastHeight: -1}
		}

		handlers.EXECUTION_THREAD_METADATA.Handler = etHandler

	}

	if handlers.EXECUTION_THREAD_METADATA.Handler.CoreMajorVersion == -1 {

		if err := setGenesisToState(); err != nil {
			return fmt.Errorf("write genesis to state: %w", err)
		}

		serializedApprovementThread, err := json.Marshal(handlers.APPROVEMENT_THREAD_METADATA.Handler)

		if err != nil {
			return fmt.Errorf("marshal APPROVEMENT_THREAD metadata: %w", err)
		}

		serializedExecutionThread, err := json.Marshal(handlers.EXECUTION_THREAD_METADATA.Handler)

		if err != nil {
			return fmt.Errorf("marshal EXECUTION_THREAD metadata: %w", err)
		}

		if err := databases.APPROVEMENT_THREAD_METADATA.Put([]byte("AT"), serializedApprovementThread, nil); err != nil {
			return fmt.Errorf("save APPROVEMENT_THREAD metadata: %w", err)
		}

		if err := databases.STATE.Put([]byte("ET"), serializedExecutionThread, nil); err != nil {
			return fmt.Errorf("save EXECUTION_THREAD metadata: %w", err)
		}
	}

	if utils.IsMyCoreVersionOld(&handlers.APPROVEMENT_THREAD_METADATA.Handler) {

		utils.LogWithTime("New version detected on APPROVEMENT_THREAD. Please, upgrade your node software", utils.YELLOW_COLOR)

		utils.GracefulShutdown()

		return fmt.Errorf("core version is outdated")

	}

	return nil
}

func setGenesisToState() error {

	approvementThreadBatch := new(leveldb.Batch)

	execThreadBatch := new(leveldb.Batch)

	epochTimestamp := globals.GENESIS.FirstEpochStartTimestamp

	validatorsRegistryForEpochHandler := []string{}

	validatorsRegistryForEpochHandler2 := []string{}

	// Ensure caches exist during genesis init, since quorum/leader selection reads validator data via caches/DB.
	if handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache == nil {
		handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache = make(map[string]*structures.ValidatorStorage)
	}
	if handlers.EXECUTION_THREAD_METADATA.Handler.ValidatorsStoragesCache == nil {
		handlers.EXECUTION_THREAD_METADATA.Handler.ValidatorsStoragesCache = make(map[string]*structures.ValidatorStorage)
	}

	// __________________________________ Load info about accounts __________________________________

	for accountPubkey, accountData := range globals.GENESIS.State {

		serialized, err := json.Marshal(accountData)

		if err != nil {
			return err
		}

		execThreadBatch.Put([]byte(accountPubkey), serialized)

	}

	// __________________________________ Load info about validators __________________________________

	for _, validatorStorage := range globals.GENESIS.Validators {

		validatorPubkey := validatorStorage.Pubkey

		serializedStorage, err := json.Marshal(validatorStorage)

		if err != nil {
			return err
		}

		approvementThreadBatch.Put([]byte(constants.DBKeyPrefixValidatorStorage+validatorPubkey), serializedStorage)

		execThreadBatch.Put([]byte(constants.DBKeyPrefixValidatorStorage+validatorPubkey), serializedStorage)

		// Populate in-memory caches so helper functions (quorum/leader selection) can read validator stake/urls
		// before the DB batch is committed.
		key := constants.DBKeyPrefixValidatorStorage + validatorPubkey
		vsCopy := validatorStorage
		utils.PutApprovementValidatorCache(key, &vsCopy)
		utils.PutExecValidatorCache(key, &vsCopy)

		validatorsRegistryForEpochHandler = append(validatorsRegistryForEpochHandler, validatorPubkey)

		validatorsRegistryForEpochHandler2 = append(validatorsRegistryForEpochHandler2, validatorPubkey)

		handlers.EXECUTION_THREAD_METADATA.Handler.ExecutionData[validatorPubkey] = structures.NewExecutionStatsTemplate()

	}

	handlers.APPROVEMENT_THREAD_METADATA.Handler.CoreMajorVersion = globals.GENESIS.CoreMajorVersion

	handlers.EXECUTION_THREAD_METADATA.Handler.CoreMajorVersion = globals.GENESIS.CoreMajorVersion

	handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters = globals.GENESIS.NetworkParameters.CopyNetworkParameters()

	handlers.EXECUTION_THREAD_METADATA.Handler.NetworkParameters = globals.GENESIS.NetworkParameters.CopyNetworkParameters()

	hashInput := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" + globals.GENESIS.NetworkId + strconv.FormatUint(epochTimestamp, 10)

	initEpochHash := utils.Blake3(hashInput)

	epochHandlerForApprovementThread := structures.EpochDataHandler{
		Id:                 0,
		Hash:               initEpochHash,
		ValidatorsRegistry: validatorsRegistryForEpochHandler,
		StartTimestamp:     epochTimestamp,
		Quorum:             []string{}, // will be assigned
		LeadersSequence:    []string{}, // will be assigned
		CurrentLeaderIndex: 0,
	}

	epochHandlerForExecThread := structures.EpochDataHandler{
		Id:                 0,
		Hash:               initEpochHash,
		ValidatorsRegistry: validatorsRegistryForEpochHandler2,
		StartTimestamp:     epochTimestamp,
		Quorum:             []string{}, // will be assigned
		LeadersSequence:    []string{}, // will be assigned
		CurrentLeaderIndex: 0,
	}

	// Assign quorum - pseudorandomly and in deterministic way

	epochHandlerForApprovementThread.Quorum = utils.GetCurrentEpochQuorum(&epochHandlerForApprovementThread, handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.QuorumSize, initEpochHash)

	epochHandlerForExecThread.Quorum = utils.GetCurrentEpochQuorum(&epochHandlerForExecThread, handlers.EXECUTION_THREAD_METADATA.Handler.NetworkParameters.QuorumSize, initEpochHash)

	// Now set the block generators for epoch pseudorandomly and in deterministic way

	utils.SetLeadersSequence(&epochHandlerForApprovementThread, initEpochHash)

	utils.SetLeadersSequence(&epochHandlerForExecThread, initEpochHash)

	handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler = epochHandlerForApprovementThread

	handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler = epochHandlerForExecThread

	// Store epoch data snapshot for API/finalization

	currentEpochDataHandler := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler
	currentNetworkParams := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.CopyNetworkParameters()
	currentSnapshot := structures.EpochDataSnapshot{EpochDataHandler: currentEpochDataHandler, NetworkParameters: currentNetworkParams}

	jsonedCurrentEpochDataHandler, err := json.Marshal(currentSnapshot)

	if err != nil {
		return fmt.Errorf("marshal current epoch handler: %w", err)
	}

	// EPOCH_HANDLER snapshots are stored in APPROVEMENT_THREAD_METADATA DB.
	// For genesis init we also store it via the same batch as validator metadata,
	// so it becomes visible atomically with the rest of the genesis bootstrap data.
	approvementThreadBatch.Put([]byte("EPOCH_HANDLER:"+strconv.Itoa(currentEpochDataHandler.Id)), jsonedCurrentEpochDataHandler)

	// Commit changes
	if err := databases.APPROVEMENT_THREAD_METADATA.Write(approvementThreadBatch, nil); err != nil {
		return err
	}

	if err := databases.STATE.Write(execThreadBatch, nil); err != nil {
		return err
	}

	return nil

}
