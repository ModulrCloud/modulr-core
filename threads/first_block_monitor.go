package threads

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

func FirstBlockMonitorThread() {

	var epochUnderObservation int

	initialized := false

	for {

		handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
		currentEpoch := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

		if !initialized || currentEpoch != epochUnderObservation {
			epochUnderObservation = currentEpoch
			initialized = true
		}

		if getFirstBlockDataFromDB(epochUnderObservation) != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if firstBlockData := findFirstBlockDataFromAlignment(epochUnderObservation); firstBlockData != nil {
			hashPreview := firstBlockData.FirstBlockHash
			if len(hashPreview) > 8 {
				hashPreview = hashPreview[:8]
			}

			utils.LogWithTime(fmt.Sprintf("First block found for epoch %d: creator=%s, hash=%s...", epochUnderObservation, firstBlockData.FirstBlockCreator, hashPreview), utils.GREEN_COLOR)

			if err := storeFirstBlockData(epochUnderObservation, firstBlockData); err != nil {
				utils.LogWithTime(fmt.Sprintf("failed to store first block data for epoch %d: %v", epochUnderObservation, err), utils.RED_COLOR)
			}
		} else {
			utils.LogWithTimeThrottled(
				fmt.Sprintf("first_block_monitor:missing:%d", epochUnderObservation),
				5*time.Second,
				fmt.Sprintf("FirstBlockMonitor: can't determine first block for epoch %d yet (alignment/DB/network)", epochUnderObservation),
				utils.YELLOW_COLOR,
			)
		}

		time.Sleep(200 * time.Millisecond)
	}
}

func storeFirstBlockData(epochIndex int, data *FirstBlockData) error {

	raw, err := json.Marshal(data)

	if err != nil {

		return err

	}

	return databases.APPROVEMENT_THREAD_METADATA.Put(firstBlockDataKey(epochIndex), raw, nil)
}

func findFirstBlockDataFromAlignment(epochIndex int) *FirstBlockData {

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	if handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id != epochIndex {
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()
		return nil
	}

	leaderSequence := append([]string{}, handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.LeadersSequence...)
	alignmentData := make(map[string]structures.ExecutionStats, len(handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders))
	for leader, stat := range handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders {
		alignmentData[leader] = stat
	}

	epochHandlerCopy := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	for _, leader := range leaderSequence {

		if leaderStats, exists := alignmentData[leader]; exists {

			if leaderStats.Index >= 0 {

				if leaderStats.Index == 0 {

					return &FirstBlockData{FirstBlockCreator: leader, FirstBlockHash: leaderStats.Hash}

				} else {

					blockID := fmt.Sprintf("%d:%s:%d", epochIndex, leader, 0)

					block := block_pack.GetBlock(epochIndex, leader, 0, &epochHandlerCopy)

					afp := utils.GetVerifiedAggregatedFinalizationProofByBlockId(blockID, &epochHandlerCopy)

					if block == nil || afp == nil {
						break
					}

					if block.Creator != leader || block.Index != 0 || !block.VerifySignature() {
						break
					}

					firstBlockHash := block.GetHash()

					if afp.BlockHash == firstBlockHash && afp.BlockId == blockID && utils.VerifyAggregatedFinalizationProof(afp, &epochHandlerCopy) {

						return &FirstBlockData{FirstBlockCreator: leader, FirstBlockHash: firstBlockHash}

					} else {
						break
					}

				}

			} else {
				continue
			}

		} else {
			break
		}
	}

	return nil
}
