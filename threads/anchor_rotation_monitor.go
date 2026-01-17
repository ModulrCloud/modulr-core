package threads

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modulrcloud/modulr-core/anchors_pack"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

var RANDOM_GENERATOR = rand.New(rand.NewSource(time.Now().UnixNano()))

type executionThreadMetadataSnapshot struct {
	EpochID                 int
	CurrentAnchorAssumption int
	AnchorStatsKnown        bool
}

func takeExecutionThreadMetadataSnapshot(anchorIndex int) executionThreadMetadataSnapshot {
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	handler := handlers.EXECUTION_THREAD_METADATA.Handler
	_, exists := handler.SequenceAlignmentData.LastBlocksByAnchors[anchorIndex]

	return executionThreadMetadataSnapshot{
		EpochID:                 handler.EpochDataHandler.Id,
		CurrentAnchorAssumption: handler.SequenceAlignmentData.CurrentAnchorAssumption,
		AnchorStatsKnown:        exists,
	}
}

func AnchorRotationMonitorThread() {

	client := &http.Client{Timeout: 5 * time.Second}

	for {

		handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()

		anchorIndex := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.CurrentAnchorAssumption

		epochHandler := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler

		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

		checkSequenceAlignmentData(anchorIndex, &epochHandler, client)

		time.Sleep(5 * time.Second)
	}

}

func checkSequenceAlignmentData(anchorIndex int, epochHandler *structures.EpochDataHandler, client *http.Client) {

	if anchorIndex < 0 || anchorIndex >= len(globals.ANCHORS) || client == nil {
		return
	}

	targetAnchor := globals.ANCHORS[RANDOM_GENERATOR.Intn(len(globals.ANCHORS))]

	resp, err := client.Get(fmt.Sprintf("%s/sequence_alignment_data/%d/%d", targetAnchor.AnchorUrl, epochHandler.Id, anchorIndex))

	if err != nil {
		return
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return
	}

	defer resp.Body.Close()

	dec := json.NewDecoder(io.LimitReader(resp.Body, 10<<20))

	var alignmentData SequenceAlignmentDataResponse

	if err := dec.Decode(&alignmentData); err != nil {
		return
	}

	metadataSnapshot := takeExecutionThreadMetadataSnapshot(anchorIndex)
	if metadataSnapshot.AnchorStatsKnown || metadataSnapshot.CurrentAnchorAssumption != anchorIndex || metadataSnapshot.EpochID != epochHandler.Id {
		return
	}

	earliestRotationStats, ok := processSequenceAlignmentDataResponse(&alignmentData, anchorIndex, epochHandler)
	if !ok {
		return
	}

	handlers.EXECUTION_THREAD_METADATA.RWMutex.Lock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.Unlock()

	currentHandler := &handlers.EXECUTION_THREAD_METADATA.Handler
	if currentHandler.EpochDataHandler.Id != metadataSnapshot.EpochID ||
		currentHandler.SequenceAlignmentData.CurrentAnchorAssumption != metadataSnapshot.CurrentAnchorAssumption {
		return
	}

	if currentHandler.SequenceAlignmentData.LastBlocksByAnchors == nil {
		currentHandler.SequenceAlignmentData.LastBlocksByAnchors = make(map[int]structures.ExecutionStats)
	}

	if _, exists := currentHandler.SequenceAlignmentData.LastBlocksByAnchors[anchorIndex]; !exists {
		currentHandler.SequenceAlignmentData.LastBlocksByAnchors[anchorIndex] = earliestRotationStats
	}

}

func processSequenceAlignmentDataResponse(alignmentData *SequenceAlignmentDataResponse, anchorIndex int, epochHandler *structures.EpochDataHandler) (structures.ExecutionStats, bool) {

	if alignmentData == nil || alignmentData.Afp == nil || epochHandler == nil {
		return structures.ExecutionStats{}, false
	}

	if alignmentData.FoundInAnchorIndex <= anchorIndex || alignmentData.FoundInAnchorIndex >= len(globals.ANCHORS) {
		return structures.ExecutionStats{}, false
	}

	if !utils.VerifyAggregatedFinalizationProofForAnchorBlock(alignmentData.Afp, epochHandler) {
		return structures.ExecutionStats{}, false
	}

	blockIdParts := strings.Split(alignmentData.Afp.BlockId, ":")

	if len(blockIdParts) != 3 {
		return structures.ExecutionStats{}, false
	}

	epochIDFromBlock, err := strconv.Atoi(blockIdParts[0])

	if err != nil || epochIDFromBlock != epochHandler.Id {
		return structures.ExecutionStats{}, false
	}

	blockIndexInAfp, err := strconv.Atoi(blockIdParts[2])

	if err != nil {
		return structures.ExecutionStats{}, false
	}

	anchorFromBlock := blockIdParts[1]

	expectedAnchor := globals.ANCHORS[alignmentData.FoundInAnchorIndex]

	if anchorFromBlock != expectedAnchor.Pubkey {
		return structures.ExecutionStats{}, false
	}

	maxFoundInBlock := -1

	anchorIndexMap := make(map[string]int, len(globals.ANCHORS))
	for idx, anchor := range globals.ANCHORS {
		anchorIndexMap[strings.ToLower(anchor.Pubkey)] = idx
	}

	for _, anchorData := range alignmentData.Anchors {
		if anchorData.FoundInBlock > maxFoundInBlock {
			maxFoundInBlock = anchorData.FoundInBlock
		}
	}

	if blockIndexInAfp != maxFoundInBlock+1 {
		return structures.ExecutionStats{}, false
	}

	for i := anchorIndex; i < alignmentData.FoundInAnchorIndex; i++ {
		anchorData, exists := alignmentData.Anchors[i]

		if !exists {
			return structures.ExecutionStats{}, false
		}

		if !anchors_pack.VerifyAggregatedAnchorRotationProof(&anchorData.AggregatedAnchorRotationProof) {
			return structures.ExecutionStats{}, false
		}
	}

	earliestRotationStats, found := findEarliestAnchorRotationProof(anchorIndex, alignmentData.FoundInAnchorIndex, blockIndexInAfp, epochHandler, anchorIndexMap)
	if !found {
		return structures.ExecutionStats{}, false
	}

	return earliestRotationStats, true
}

func findEarliestAnchorRotationProof(currentAnchor, foundInAnchorIndex, blockLimit int, epochHandler *structures.EpochDataHandler, anchorIndexMap map[string]int) (structures.ExecutionStats, bool) {

	if epochHandler == nil || anchorIndexMap == nil || currentAnchor < 0 || foundInAnchorIndex >= len(globals.ANCHORS) {
		return structures.ExecutionStats{}, false
	}

	searchLimit := blockLimit
	earliestStats := structures.ExecutionStats{}

	for anchorIdx := foundInAnchorIndex; anchorIdx > currentAnchor; anchorIdx-- {
		anchor := globals.ANCHORS[anchorIdx]
		neededProofs := anchorIdx - currentAnchor
		foundProofs := make(map[int]int)
		proofStats := make(map[int]structures.ExecutionStats)

		for blockIndex := 0; blockIndex < searchLimit; blockIndex++ {
			blockID := fmt.Sprintf("%d:%s:%d", epochHandler.Id, anchor.Pubkey, blockIndex)
			response := getAnchorBlockAndAfpFromAnchorsPoD(blockID, epochHandler)

			if response == nil || response.Block == nil {
				utils.LogWithTimeThrottled(
					"anchor_rotation_monitor:no_anchor_block:"+blockID,
					2*time.Second,
					fmt.Sprintf("Anchor rotation monitor: can't fetch anchor block %s (Anchors-PoD/HTTP)", blockID),
					utils.YELLOW_COLOR,
				)
				return structures.ExecutionStats{}, false
			}

			block := response.Block

			if block.Creator != anchor.Pubkey || block.Index != blockIndex || !block.VerifySignature() {
				utils.LogWithTimeThrottled(
					fmt.Sprintf("anchor_rotation_monitor:invalid_anchor_block:%s", blockID),
					2*time.Second,
					fmt.Sprintf("Anchor rotation monitor: invalid anchor block %s (creator/index/sig mismatch)", blockID),
					utils.YELLOW_COLOR,
				)
				return structures.ExecutionStats{}, false
			}

			for _, proof := range block.ExtraData.AggregatedAnchorRotationProofs {
				targetIdx, exists := anchorIndexMap[strings.ToLower(proof.Anchor)]

				if !exists || targetIdx < currentAnchor || targetIdx >= anchorIdx {
					continue
				}

				if !anchors_pack.VerifyAggregatedAnchorRotationProof(&proof) {
					continue
				}

				if _, alreadyFound := foundProofs[targetIdx]; !alreadyFound {
					foundProofs[targetIdx] = blockIndex
					proofStats[targetIdx] = structures.ExecutionStats{
						Index: proof.VotingStat.Index,
						Hash:  proof.VotingStat.Hash,
					}
				}
			}

			if len(foundProofs) == neededProofs {
				break
			}
		}

		if len(foundProofs) != neededProofs {
			return structures.ExecutionStats{}, false
		}

		nextLimit, ok := foundProofs[anchorIdx-1]

		if !ok {
			return structures.ExecutionStats{}, false
		}

		if stats, ok := proofStats[anchorIdx-1]; ok {
			earliestStats = stats
		}

		searchLimit = nextLimit

		if anchorIdx-1 == currentAnchor {
			return earliestStats, true
		}
	}

	return structures.ExecutionStats{}, false
}
