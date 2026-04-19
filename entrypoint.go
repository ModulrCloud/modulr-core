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

	//✅ 8.0 PoD outbox: retry store messages to PoD until acknowledged (optional)
	if !globals.CONFIGURATION.DisablePoDOutbox {
		go threads.PoDOutboxThread()
	}

	//✅ 8.1 Thread to independently scan anchor blocks and persist ALFP inclusion markers (delivery confirmation)
	go threads.AlfpInclusionWatcherThread()

	//✅ 9.Thread to asynchronously find and store first block data in each epoch
	go threads.FirstBlockInEpochMonitorThread()

	//✅ 10.Thread for last mile finalization: sequences blocks locally (writes height->blockId
	//   mappings for SignHeightProof on ALL nodes) and collects AggregatedHeightProof on finalizer nodes
	go threads.LastMileFinalizerThread()

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
	applyCacheConfig()

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
	databases.FINALIZATION_THREAD_METADATA = utils.OpenDb("FINALIZATION_THREAD_METADATA")

	// Load CHAIN_CURSOR (offset mapping for network restarts; defaults to {0,0})
	if cursorRaw, err := databases.STATE.Get([]byte(constants.DBKeyChainCursor), nil); err == nil {
		var cursor structures.ChainCursor
		if err := json.Unmarshal(cursorRaw, &cursor); err == nil {
			handlers.CHAIN_CURSOR = cursor
		}
	}

	// Defensive check: chaindata must belong to the same network iteration as the loaded genesis.
	// Empty NetworkId means clean install (no cursor in STATE) — setGenesisToState() will populate it.
	if handlers.CHAIN_CURSOR.NetworkId != "" &&
		handlers.CHAIN_CURSOR.NetworkId != globals.GENESIS.NetworkId {
		return fmt.Errorf(
			"network id mismatch: chaindata belongs to %q but loaded genesis is %q — "+
				"wrong genesis file or chaindata directory",
			handlers.CHAIN_CURSOR.NetworkId, globals.GENESIS.NetworkId,
		)
	}

	// Load GT - Generation Thread handler
	if data, err := databases.BLOCKS.Get([]byte(constants.DBKeyGenerationThreadMetadata), nil); err == nil {
		var gtHandler structures.GenerationThreadMetadataHandler

		if err := json.Unmarshal(data, &gtHandler); err != nil {
			return fmt.Errorf("unmarshal GENERATION_THREAD metadata: %w", err)
		}

		handlers.GENERATION_THREAD_METADATA = gtHandler
	} else {
		handlers.GENERATION_THREAD_METADATA = structures.GenerationThreadMetadataHandler{
			EpochFullId: utils.Blake3(constants.ZeroHash+globals.GENESIS.NetworkId) + "#-1",
			PrevHash:    constants.ZeroHash,
			NextIndex:   0,
		}
	}

	if data, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(constants.DBKeyApprovementThreadMetadata), nil); err == nil {
		var atHandler structures.ApprovementThreadMetadataHandler

		if err := json.Unmarshal(data, &atHandler); err != nil {
			return fmt.Errorf("unmarshal APPROVEMENT_THREAD metadata: %w", err)
		}

		handlers.APPROVEMENT_THREAD_METADATA.Handler = atHandler
	}

	if data, err := databases.STATE.Get([]byte(constants.DBKeyExecutionThreadMetadata), nil); err == nil {
		var etHandler structures.ExecutionThreadMetadataHandler

		if err := json.Unmarshal(data, &etHandler); err != nil {
			return fmt.Errorf("unmarshal EXECUTION_THREAD metadata: %w", err)
		}

		if etHandler.EpochStatistics == nil {
			etHandler.EpochStatistics = &structures.Statistics{LastHeight: -1}
		}

		handlers.EXECUTION_THREAD_METADATA.Handler = etHandler
	}

	// Load FINALIZER_THREAD_METADATA (consensus/sequencing state, separate from execution).
	if data, err := databases.FINALIZATION_THREAD_METADATA.Get([]byte(constants.DBKeyFinalizerThreadMetadata), nil); err == nil {
		var fHandler structures.FinalizerThreadMetadataHandler

		if err := json.Unmarshal(data, &fHandler); err != nil {
			return fmt.Errorf("unmarshal FINALIZER_THREAD metadata: %w", err)
		}

		if fHandler.SequenceAlignmentData.LastBlocksByLeaders == nil {
			fHandler.SequenceAlignmentData.LastBlocksByLeaders = make(map[string]structures.ExecutionStats)
		}
		if fHandler.SequenceAlignmentData.LastBlocksByAnchors == nil {
			fHandler.SequenceAlignmentData.LastBlocksByAnchors = make(map[int]structures.ExecutionStats)
		}

		handlers.FINALIZER_THREAD_METADATA.Handler = fHandler
	}

	// Backfill CHAIN_CURSOR statistics if missing (e.g. first run after accounts were created)
	if handlers.CHAIN_CURSOR.Statistics != nil {
		if handlers.CHAIN_CURSOR.Statistics.AccountsNumber == 0 {
			handlers.CHAIN_CURSOR.Statistics.AccountsNumber = utils.CountStateAccounts()
		}
		if handlers.CHAIN_CURSOR.Statistics.StakingDelta == 0 {
			handlers.CHAIN_CURSOR.Statistics.StakingDelta = int64(utils.SumTotalStakedFromState())
		}
	}

	// Genesis init is needed when EXECUTION_THREAD_METADATA is absent from STATE.
	// This covers both first-ever launch AND network restart (where ET is deleted but CHAIN_CURSOR is preserved).
	if _, etErr := databases.STATE.Get([]byte(constants.DBKeyExecutionThreadMetadata), nil); etErr != nil {
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

		serializedFinalizerThread, err := json.Marshal(handlers.FINALIZER_THREAD_METADATA.Handler)

		if err != nil {
			return fmt.Errorf("marshal FINALIZER_THREAD metadata: %w", err)
		}

		if err := databases.APPROVEMENT_THREAD_METADATA.Put([]byte(constants.DBKeyApprovementThreadMetadata), serializedApprovementThread, nil); err != nil {
			return fmt.Errorf("save APPROVEMENT_THREAD metadata: %w", err)
		}

		if err := databases.STATE.Put([]byte(constants.DBKeyExecutionThreadMetadata), serializedExecutionThread, nil); err != nil {
			return fmt.Errorf("save EXECUTION_THREAD metadata: %w", err)
		}

		if err := databases.FINALIZATION_THREAD_METADATA.Put([]byte(constants.DBKeyFinalizerThreadMetadata), serializedFinalizerThread, nil); err != nil {
			return fmt.Errorf("save FINALIZER_THREAD metadata: %w", err)
		}
	}

	if utils.IsMyCoreVersionOld(&handlers.APPROVEMENT_THREAD_METADATA.Handler) {
		utils.LogWithTime("New version detected on APPROVEMENT_THREAD. Please, upgrade your node software", utils.YELLOW_COLOR)

		utils.GracefulShutdown()

		return fmt.Errorf("core version is outdated")
	}

	return nil
}

func applyCacheConfig() {
	if globals.CONFIGURATION.AccountsCacheMax > 0 {
		handlers.EXECUTION_THREAD_METADATA.AccountsCacheMax = globals.CONFIGURATION.AccountsCacheMax
	} else {
		handlers.EXECUTION_THREAD_METADATA.AccountsCacheMax = constants.DefaultAccountsCacheMax
	}
	if globals.CONFIGURATION.ValidatorsCacheMax > 0 {
		handlers.APPROVEMENT_THREAD_METADATA.ValidatorsCacheMax = globals.CONFIGURATION.ValidatorsCacheMax
		handlers.EXECUTION_THREAD_METADATA.ValidatorsCacheMax = globals.CONFIGURATION.ValidatorsCacheMax
	} else {
		handlers.APPROVEMENT_THREAD_METADATA.ValidatorsCacheMax = constants.DefaultValidatorsCacheMax
		handlers.EXECUTION_THREAD_METADATA.ValidatorsCacheMax = constants.DefaultValidatorsCacheMax
	}
}

func setGenesisToState() error {
	approvementThreadBatch := new(leveldb.Batch)

	execThreadBatch := new(leveldb.Batch)

	epochTimestamp := globals.GENESIS.FirstEpochStartTimestamp

	validatorsRegistryForEpochHandler := []string{}

	validatorsRegistryForEpochHandler2 := []string{}

	// __________________________________ Load info about accounts __________________________________

	genesisAccountsCount := uint64(len(globals.GENESIS.State))
	var genesisTotalStaked uint64 = 0

	for accountPubkey, accountData := range globals.GENESIS.State {
		if _, err := databases.STATE.Get([]byte(accountPubkey), nil); err == nil {
			continue
		}

		serialized, err := json.Marshal(accountData)

		if err != nil {
			return err
		}

		execThreadBatch.Put([]byte(accountPubkey), serialized)
	}

	// __________________________________ Load info about validators __________________________________

	for _, validatorStorage := range globals.GENESIS.Validators {
		genesisTotalStaked += validatorStorage.TotalStaked

		validatorPubkey := validatorStorage.Pubkey
		stateKey := constants.DBKeyPrefixValidatorStorage + validatorPubkey

		if _, err := databases.STATE.Get([]byte(stateKey), nil); err != nil {
			serializedStorage, err := json.Marshal(validatorStorage)

			if err != nil {
				return err
			}

			approvementThreadBatch.Put([]byte(stateKey), serializedStorage)
			execThreadBatch.Put([]byte(stateKey), serializedStorage)
		}

		// Populate in-memory caches so helper functions (quorum/leader selection) can read validator stake/urls
		// before the DB batch is committed.
		vsCopy := validatorStorage
		utils.PutApprovementValidatorCache(stateKey, &vsCopy)
		utils.PutExecValidatorCache(stateKey, &vsCopy)

		validatorsRegistryForEpochHandler = append(validatorsRegistryForEpochHandler, validatorPubkey)

		validatorsRegistryForEpochHandler2 = append(validatorsRegistryForEpochHandler2, validatorPubkey)
	}

	handlers.APPROVEMENT_THREAD_METADATA.Handler.CoreMajorVersion = globals.GENESIS.CoreMajorVersion

	if handlers.EXECUTION_THREAD_METADATA.Handler.EpochStatistics == nil {
		handlers.EXECUTION_THREAD_METADATA.Handler.EpochStatistics = &structures.Statistics{LastHeight: -1}
	}

	isFirstLaunch := handlers.CHAIN_CURSOR.CoreMajorVersion == -1

	handlers.CHAIN_CURSOR.CoreMajorVersion = globals.GENESIS.CoreMajorVersion
	handlers.CHAIN_CURSOR.NetworkId = globals.GENESIS.NetworkId

	if isFirstLaunch {
		handlers.CHAIN_CURSOR.Statistics = &structures.Statistics{
			LastHeight:     -1,
			AccountsNumber: genesisAccountsCount,
			StakingDelta:   int64(genesisTotalStaked),
		}
		handlers.CHAIN_CURSOR.NetworkParameters = globals.GENESIS.NetworkParameters.CopyNetworkParameters()
	}

	handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters = globals.GENESIS.NetworkParameters.CopyNetworkParameters()

	hashInput := constants.ZeroHash + globals.GENESIS.NetworkId

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

	epochHandlerForApprovementThread.Quorum = utils.GetCurrentEpochQuorum(&epochHandlerForApprovementThread, handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.QuorumSize, initEpochHash, utils.GetValidatorFromApprovementThreadState)

	epochHandlerForExecThread.Quorum = utils.GetCurrentEpochQuorum(&epochHandlerForExecThread, handlers.CHAIN_CURSOR.NetworkParameters.QuorumSize, initEpochHash, utils.GetValidatorFromApprovementThreadState)

	// Now set the block generators for epoch pseudorandomly and in deterministic way

	utils.SetLeadersSequence(&epochHandlerForApprovementThread, initEpochHash, utils.GetValidatorFromApprovementThreadState)

	utils.SetLeadersSequence(&epochHandlerForExecThread, initEpochHash, utils.GetValidatorFromApprovementThreadState)

	handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler = epochHandlerForApprovementThread

	handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler = epochHandlerForExecThread

	// FINALIZER_THREAD_METADATA starts on the genesis epoch as well, with a fresh
	// SequenceAlignmentData. Both fields will be re-persisted to FINALIZATION_THREAD_METADATA
	// at the end of prepareBlockchain().
	handlers.FINALIZER_THREAD_METADATA.Handler.EpochDataHandler = epochHandlerForExecThread
	handlers.FINALIZER_THREAD_METADATA.Handler.SequenceAlignmentData = structures.AlignmentDataHandler{
		CurrentAnchorAssumption:         0,
		CurrentAnchorBlockIndexObserved: -1,
		CurrentLeaderToExecBlocksFrom:   0,
		LastBlocksByLeaders:             make(map[string]structures.ExecutionStats),
		LastBlocksByAnchors:             make(map[int]structures.ExecutionStats),
	}

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
	approvementThreadBatch.Put([]byte(constants.DBKeyPrefixEpochHandler+strconv.Itoa(currentEpochDataHandler.Id)), jsonedCurrentEpochDataHandler)

	// Durable epoch data for API/Explorer is stored in STATE using absolute epoch ID.
	absoluteEpochId := currentEpochDataHandler.Id + handlers.CHAIN_CURSOR.EpochOffset
	execThreadBatch.Put([]byte(constants.DBKeyPrefixEpochData+strconv.Itoa(absoluteEpochId)), jsonedCurrentEpochDataHandler)

	// Persist ChainCursor (permanent state) in the same atomic batch.
	if cursorBytes, err := json.Marshal(handlers.CHAIN_CURSOR); err == nil {
		execThreadBatch.Put([]byte(constants.DBKeyChainCursor), cursorBytes)
	}

	// Commit changes
	if err := databases.APPROVEMENT_THREAD_METADATA.Write(approvementThreadBatch, nil); err != nil {
		return err
	}

	if err := databases.STATE.Write(execThreadBatch, nil); err != nil {
		return err
	}

	return nil
}
