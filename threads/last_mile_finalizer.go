// Thread for the final stage of block finalization (last mile)
package threads

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

func LastMileFinalizerThread() {

	lastProcessedEpoch := -1

	for {

		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
		epochSnapshot := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		if epochSnapshot.Id != lastProcessedEpoch {

			lastProcessedEpoch = epochSnapshot.Id

			lastMileFinalizerIndex := selectLastMileFinalizerForEpoch(&epochSnapshot)

			if lastMileFinalizerIndex >= 0 {

				lastMileFinalizer := globals.ANCHORS[lastMileFinalizerIndex]

				utils.LogWithTime(
					fmt.Sprintf("Last mile finalizer: epoch %d => selected anchor %s (index %d)", epochSnapshot.Id, lastMileFinalizer.Pubkey, lastMileFinalizerIndex),
					utils.CYAN_COLOR,
				)

			}

		}

		time.Sleep(200 * time.Millisecond)

	}
}

func selectLastMileFinalizerForEpoch(epochHandler *structures.EpochDataHandler) int {

	anchorsCount := len(globals.ANCHORS)

	if anchorsCount == 0 {
		return -1
	}

	seed := utils.Blake3(fmt.Sprintf("LAST_MILE_ANCHOR_SELECTION:%d:%s", epochHandler.Id, epochHandler.Hash))

	seedBytes, err := hex.DecodeString(seed[:16])

	if err != nil {
		return 0
	}

	return int(binary.BigEndian.Uint64(seedBytes) % uint64(anchorsCount))
}
