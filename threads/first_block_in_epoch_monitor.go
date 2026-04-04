// Thread to monitor and cache the first block of each epoch
// From this block we'll retrieve the delayed transactions and execute them on the end of epoch
package threads

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/utils"
)

func FirstBlockInEpochMonitorThread() {
	epochUnderObservation := -1
	var cachedFirstBlockData *FirstBlockData

	for {
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
		currentEpoch := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

		if currentEpoch != epochUnderObservation {
			epochUnderObservation = currentEpoch
			cachedFirstBlockData = nil
		}

		// Only cache non-nil results; keep checking until found
		if cachedFirstBlockData == nil {
			cachedFirstBlockData = getFirstBlockDataFromDB(epochUnderObservation)
		}

		if cachedFirstBlockData != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		utils.LogWithTimeThrottled(
			fmt.Sprintf("first_block_monitor:missing:%d", epochUnderObservation),
			5*time.Second,
			fmt.Sprintf("FirstBlockMonitor: waiting for first core block for epoch %d (height attestation voting)", epochUnderObservation),
			utils.YELLOW_COLOR,
		)

		time.Sleep(200 * time.Millisecond)
	}
}

func storeDataAboutFirstBlockInEpoch(epochIndex int, data *FirstBlockData) error {
	raw, err := json.Marshal(data)

	if err != nil {
		return err
	}

	return databases.APPROVEMENT_THREAD_METADATA.Put(firstBlockDataKey(epochIndex), raw, nil)
}
