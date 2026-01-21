package threads

import (
	"fmt"
	"strconv"
	"time"

	"github.com/modulrcloud/modulr-core/anchors_pack"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

type SequenceAlignmentAnchorData struct {
	AggregatedAnchorRotationProof anchors_pack.AggregatedAnchorRotationProof `json:"aarp"`
	FoundInBlock                  int                                        `json:"foundInBlock"`
}

type SequenceAlignmentDataResponse struct {
	FoundInAnchorIndex int                                     `json:"foundInAnchorIndex"`
	Anchors            map[int]SequenceAlignmentAnchorData     `json:"anchors"`
	Afp                *structures.AggregatedFinalizationProof `json:"afp,omitempty"`
}

func SequenceAlignmentThread() {

	var cachedFirstBlockEpoch int = -1
	var cachedFirstBlockExists bool = false

	for {

		handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()

		epochSnapshot := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler
		alignmentSnapshot := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData
		currentAnchorIndex := alignmentSnapshot.CurrentAnchorAssumption
		currentAnchorBlockPointerObserved := alignmentSnapshot.CurrentAnchorBlockIndexObserved
		infoAboutAnchorLastBlock, infoAboutAnchorLastBlockExists := alignmentSnapshot.LastBlocksByAnchors[currentAnchorIndex]

		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

		if infoAboutAnchorLastBlockExists && infoAboutAnchorLastBlock.Index == currentAnchorBlockPointerObserved {
			handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()
			if handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id == epochSnapshot.Id &&
				handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption == currentAnchorIndex &&
				handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorBlockIndexObserved == currentAnchorBlockPointerObserved {

				handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption++
				handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorBlockIndexObserved = -1
			}
			handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()
			continue
		}

		anchorData := globals.ANCHORS[currentAnchorIndex]
		blockId := strconv.Itoa(epochSnapshot.Id) + ":" + anchorData.Pubkey + ":" + strconv.Itoa(currentAnchorBlockPointerObserved+1)

		response := getAnchorBlockAndAfpFromAnchorsPoD(blockId, &epochSnapshot)
		if response == nil || response.Block == nil {
			utils.LogWithTimeThrottled(
				"sequence_alignment:no_anchor_block:"+blockId,
				2*time.Second,
				fmt.Sprintf("Sequence alignment: can't fetch anchor block %s (Anchors-PoD/HTTP)", blockId),
				utils.YELLOW_COLOR,
			)
			continue
		}

		validLeaderStats := make(map[string]structures.ExecutionStats)
		for _, proof := range response.Block.ExtraData.AggregatedLeaderFinalizationProofs {
			if !utils.VerifyAggregatedLeaderFinalizationProof(&proof, &epochSnapshot) {
				continue
			}

			validLeaderStats[proof.Leader] = structures.ExecutionStats{
				Index: proof.VotingStat.Index,
				Hash:  proof.VotingStat.Hash,
			}
		}

		afpValid := response.Afp != nil && utils.VerifyAggregatedFinalizationProofForAnchorBlock(response.Afp, &epochSnapshot)
		responseBlockHash := response.Block.GetHash()

		handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()

		if handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id != epochSnapshot.Id ||
			handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption != currentAnchorIndex {

			handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()
			continue

		}

		alignmentData := &handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData
		observedIndex := alignmentData.CurrentAnchorBlockIndexObserved
		infoAboutAnchorLastBlock, infoAboutAnchorLastBlockExists = alignmentData.LastBlocksByAnchors[currentAnchorIndex]

		currentBlockMatchesAnchor := infoAboutAnchorLastBlockExists && infoAboutAnchorLastBlock.Index == observedIndex && responseBlockHash == infoAboutAnchorLastBlock.Hash

		if !(currentBlockMatchesAnchor || afpValid) {
			handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()
			continue
		}

		for leader, stats := range validLeaderStats {
			if _, exists := alignmentData.LastBlocksByLeaders[leader]; !exists {

				alignmentData.LastBlocksByLeaders[leader] = stats

				hashPreview := stats.Hash
				if len(hashPreview) > 8 {
					hashPreview = hashPreview[:8]
				}

				utils.LogWithTime(
					fmt.Sprintf("Sequence alignment: last block for leader %s set at index %d (hash %s...) in epoch %d", leader, stats.Index, hashPreview, epochSnapshot.Id),
					utils.CYAN_COLOR,
				)

			}
		}

		// Check if this is the first block of the epoch (blockIndex == 0) and store it if not already stored
		blockIndex := observedIndex + 1
		if blockIndex == 0 {
			// Update cache if epoch changed
			if cachedFirstBlockEpoch != epochSnapshot.Id {
				cachedFirstBlockExists = getFirstBlockDataFromDB(epochSnapshot.Id) != nil
				cachedFirstBlockEpoch = epochSnapshot.Id
			}

			// Only check DB and store if not already cached
			if !cachedFirstBlockExists {
				firstBlockData := &FirstBlockData{
					FirstBlockCreator: anchorData.Pubkey,
					FirstBlockHash:    responseBlockHash,
				}
				if err := storeFirstBlockData(epochSnapshot.Id, firstBlockData); err != nil {
					utils.LogWithTime(fmt.Sprintf("failed to store first anchor block data for epoch %d: %v", epochSnapshot.Id, err), utils.RED_COLOR)
				} else {
					cachedFirstBlockExists = true
					hashPreview := responseBlockHash
					if len(hashPreview) > 8 {
						hashPreview = hashPreview[:8]
					}
					utils.LogWithTime(fmt.Sprintf("First anchor block found for epoch %d: creator=%s, hash=%s...", epochSnapshot.Id, anchorData.Pubkey, hashPreview), utils.GREEN_COLOR)
				}
			}
		}

		alignmentData.CurrentAnchorBlockIndexObserved++

		handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

	}

}
