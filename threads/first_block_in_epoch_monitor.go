// Thread to monitor and cache the first block of each epoch.
// Reads epoch data from APPROVEMENT_THREAD to maintain unidirectional dependency (ET depends on AT, not vice versa).
//
// For quorum nodes: the first block aggregated height proof (HeightInEpoch == 0) is stored locally
// by LastMileFinalizerThread when it collects the AggregatedHeightProof.
//
// For non-quorum nodes: this thread fetches the signed AggregatedHeightProof with HeightInEpoch == 0
// from quorum members via HTTP GET /first_block_in_epoch/{epochId}, verifies its Proofs
// (majority signature), and extracts FirstBlockData from the BlockId field.
package threads

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

func FirstBlockInEpochMonitorThread() {
	epochUnderObservation := -1
	var cachedFirstBlockData *FirstBlockData

	for {
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
		currentEpoch := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.Id
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		if currentEpoch != epochUnderObservation {
			epochUnderObservation = currentEpoch
			cachedFirstBlockData = nil
		}

		if cachedFirstBlockData == nil {
			cachedFirstBlockData = getFirstBlockDataFromDB(epochUnderObservation)
		}

		if cachedFirstBlockData != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		attestation := fetchFirstBlockAggregatedHeightProofForEpoch(epochUnderObservation)

		if attestation != nil {
			data := extractFirstBlockData(attestation)
			if data != nil {
				_ = storeDataAboutFirstBlockInEpoch(epochUnderObservation, data)
				storeFirstBlockAggregatedHeightProof(attestation)
				cachedFirstBlockData = data
				utils.LogWithTime(
					fmt.Sprintf("FirstBlockMonitor: first core block for epoch %d found => creator=%s, hash=%s...",
						epochUnderObservation, data.FirstBlockCreator, utils.ShortHash(data.FirstBlockHash)),
					utils.GREEN_COLOR,
				)
				continue
			}
		}

		utils.LogWithTimeThrottled(
			fmt.Sprintf("first_block_monitor:missing:%d", epochUnderObservation),
			5*time.Second,
			fmt.Sprintf("FirstBlockMonitor: waiting for first core block attestation for epoch %d",
				epochUnderObservation),
			utils.YELLOW_COLOR,
		)

		time.Sleep(200 * time.Millisecond)
	}
}

// fetchFirstBlockAggregatedHeightProofForEpoch tries local DB first, then quorum HTTP.
func fetchFirstBlockAggregatedHeightProofForEpoch(epochId int) *structures.AggregatedHeightProof {
	if local := loadFirstBlockAggregatedHeightProof(epochId); local != nil {
		epochHandler := getEpochHandlerForTracker(local.EpochId)
		if epochHandler != nil && local.HeightInEpoch == 0 && utils.VerifyAggregatedHeightProof(local, epochHandler) {
			return local
		}
	}

	return utils.GetFirstBlockAggregatedHeightProofFromQuorum(epochId)
}

func extractFirstBlockData(proof *structures.AggregatedHeightProof) *FirstBlockData {
	parts := strings.Split(proof.BlockId, ":")
	if len(parts) != 3 {
		return nil
	}
	return &FirstBlockData{
		FirstBlockCreator: parts[1],
		FirstBlockHash:    proof.BlockHash,
	}
}

func storeDataAboutFirstBlockInEpoch(epochIndex int, data *FirstBlockData) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return databases.APPROVEMENT_THREAD_METADATA.Put(firstBlockDataKey(epochIndex), raw, nil)
}

func storeFirstBlockAggregatedHeightProof(proof *structures.AggregatedHeightProof) {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixFirstBlockAggregatedHeightProof, proof.EpochId))
	if value, err := json.Marshal(proof); err == nil {
		_ = databases.FINALIZATION_THREAD_METADATA.Put(key, value, nil)
	}
}

func loadFirstBlockAggregatedHeightProof(epochId int) *structures.AggregatedHeightProof {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixFirstBlockAggregatedHeightProof, epochId))
	raw, err := databases.FINALIZATION_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return nil
	}
	var proof structures.AggregatedHeightProof
	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}
	return &proof
}
