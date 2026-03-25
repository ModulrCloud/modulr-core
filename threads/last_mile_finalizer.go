// Thread for the final stage of block finalization (last mile)
package threads

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

const LAST_MILE_FINALIZERS_COUNT = 5

func LastMileFinalizerThread() {

	lastProcessedEpoch := -1

	for {

		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
		epochSnapshot := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

		if epochSnapshot.Id != lastProcessedEpoch {

			lastProcessedEpoch = epochSnapshot.Id

			selectedIndices := selectLastMileFinalizersForEpoch(&epochSnapshot)

			if len(selectedIndices) > 0 {

				pubkeys := make([]string, len(selectedIndices))
				for i, idx := range selectedIndices {
					pubkeys[i] = globals.ANCHORS[idx].Pubkey
				}

				utils.LogWithTime(
					fmt.Sprintf("Last mile finalizer: epoch %d => selected %d anchors %v", epochSnapshot.Id, len(selectedIndices), pubkeys),
					utils.CYAN_COLOR,
				)

			}

		}

		time.Sleep(200 * time.Millisecond)

	}
}

func selectLastMileFinalizersForEpoch(epochHandler *structures.EpochDataHandler) []int {

	anchorsCount := len(globals.ANCHORS)

	if anchorsCount == 0 {
		return nil
	}

	count := LAST_MILE_FINALIZERS_COUNT
	if count > anchorsCount {
		count = anchorsCount
	}

	seed := utils.Blake3(fmt.Sprintf("LAST_MILE_FINALIZERS_SELECTION:%d:%s", epochHandler.Id, epochHandler.Hash))

	indices := make([]int, anchorsCount)
	for i := range indices {
		indices[i] = i
	}

	// Deterministic Fisher-Yates shuffle: pick `count` elements from the front
	for i := 0; i < count; i++ {
		hashHex := utils.Blake3(seed + "_" + strconv.Itoa(i))
		r := hashHexToUint64ForLastMile(hashHex) % uint64(anchorsCount-i)
		j := i + int(r)
		indices[i], indices[j] = indices[j], indices[i]
	}

	return indices[:count]
}

func hashHexToUint64ForLastMile(hashHex string) uint64 {

	if len(hashHex) < 16 {
		return 0
	}

	b, err := hex.DecodeString(hashHex[:16])

	if err != nil {
		return 0
	}

	return binary.BigEndian.Uint64(b)
}
