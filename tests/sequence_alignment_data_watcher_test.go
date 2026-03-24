package tests

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modulrcloud/modulr-core/anchors_pack"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/threads"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/modulrcloud/modulr-core/websocket_pack"
)

type testRotationScenario struct {
	blockMap              map[string]*websocket_pack.WsAnchorBlockWithAfpResponse
	anchor0Hashes         map[int]string
	responseA             threads.SequenceAlignmentDataResponse
	responseB             threads.SequenceAlignmentDataResponse
	anchor0ProofAtAnchor1 int
	anchor1ProofAtAnchor2 int
	anchor0ProofAtAnchor2 int
}

func TestSequenceAlignmentWatcherConvergesOnRotationHeight(t *testing.T) {

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	anchorKeys := make([]cryptography.Ed25519Box, 3)
	for i := range anchorKeys {
		anchorKeys[i] = cryptography.GenerateKeyPair("", "", nil)
	}

	globals.ANCHORS = []structures.Anchor{
		{Pubkey: anchorKeys[0].Pub},
		{Pubkey: anchorKeys[1].Pub},
		{Pubkey: anchorKeys[2].Pub},
	}

	globals.ANCHORS_PUBKEYS = []string{
		anchorKeys[0].Pub,
		anchorKeys[1].Pub,
		anchorKeys[2].Pub,
	}

	epochHandler := structures.EpochDataHandler{Id: 1, Hash: "epoch_hash", Quorum: []string{anchorKeys[0].Pub, anchorKeys[1].Pub, anchorKeys[2].Pub}}

	scenario := buildRotationScenarioForTest(t, rng, anchorKeys, epochHandler)

	t.Logf("Proof placement:\n  anchor1 proof emitted by anchor2 at block %d\n  anchor0 proof emitted by anchor2 at block %d\n  anchor0 proof emitted by anchor1 at block %d",
		scenario.anchor1ProofAtAnchor2, scenario.anchor0ProofAtAnchor2, scenario.anchor0ProofAtAnchor1)

	t.Logf("Anchor blocks with rotation proofs:\n%s", formatAnchorBlocksLog(anchorKeys, scenario.blockMap, epochHandler))

	if scenario.anchor0ProofAtAnchor2 <= scenario.anchor0ProofAtAnchor1 {
		t.Fatalf("expected anchor0 proof in anchor2 blocks to appear after anchor1 proof (got %d <= %d)", scenario.anchor0ProofAtAnchor2, scenario.anchor0ProofAtAnchor1)
	}

	fetcher := func(blockID string) *websocket_pack.WsAnchorBlockWithAfpResponse {
		return scenario.blockMap[blockID]
	}

	metaA := structures.ExecutionThreadMetadataHandler{SequenceAlignmentData: structures.AlignmentDataHandler{CurrentAnchorAssumption: 0, LastBlocksByLeaders: make(map[string]structures.ExecutionStats), LastBlocksByAnchors: make(map[int]structures.ExecutionStats)}}
	metaB := structures.ExecutionThreadMetadataHandler{SequenceAlignmentData: structures.AlignmentDataHandler{CurrentAnchorAssumption: 0, LastBlocksByLeaders: make(map[string]structures.ExecutionStats), LastBlocksByAnchors: make(map[int]structures.ExecutionStats)}}

	// Node A receives a higher anchor response, Node B receives a nearer response; both should still agree on the last block for anchor 0.
	for idx, resp := range []*threads.SequenceAlignmentDataResponse{&scenario.responseA} {
		current := metaA.SequenceAlignmentData.CurrentAnchorAssumption
		if resp.FoundInAnchorIndex <= current {
			continue
		}
		if !processSequenceAlignmentDataResponseForTest(resp, current, &epochHandler, &metaA, fetcher) {
			t.Fatalf("node A failed to process alignment data on iteration %d", idx)
		}
	}

	for idx, resp := range []*threads.SequenceAlignmentDataResponse{&scenario.responseB} {
		current := metaB.SequenceAlignmentData.CurrentAnchorAssumption
		if resp.FoundInAnchorIndex <= current {
			continue
		}
		if !processSequenceAlignmentDataResponseForTest(resp, current, &epochHandler, &metaB, fetcher) {
			t.Fatalf("node B failed to process alignment data on step %d", idx)
		}
	}

	t.Logf("Input responses:"+
		"\n  Node A -> FoundInAnchorIndex=%d, AFP block=%s\n%s"+
		"\n  Node B -> FoundInAnchorIndex=%d, AFP block=%s\n%s",
		scenario.responseA.FoundInAnchorIndex,
		afpBlockIndex(scenario.responseA.Afp),
		formatAnchorProofSummary("    ", scenario.responseA.Anchors),
		scenario.responseB.FoundInAnchorIndex,
		afpBlockIndex(scenario.responseB.Afp),
		formatAnchorProofSummary("    ", scenario.responseB.Anchors))

	if scenario.anchor0ProofAtAnchor2 > scenario.anchor0ProofAtAnchor1 {
		t.Logf("Proof selection: node A ignored later anchor0 proof at block %d (anchor2) and accepted earlier block %d from anchor1; node B used the same earlier block %d",
			scenario.anchor0ProofAtAnchor2, scenario.anchor0ProofAtAnchor1, scenario.anchor0ProofAtAnchor1)
	}

	if !reflect.DeepEqual(metaA.SequenceAlignmentData.LastBlocksByAnchors, metaB.SequenceAlignmentData.LastBlocksByAnchors) {
		t.Fatalf("anchor catch-up targets diverged: A=%v B=%v", metaA.SequenceAlignmentData.LastBlocksByAnchors, metaB.SequenceAlignmentData.LastBlocksByAnchors)
	}

	if metaA.SequenceAlignmentData.CurrentAnchorAssumption != metaB.SequenceAlignmentData.CurrentAnchorAssumption {
		t.Fatalf("current anchor assumptions differ: %d vs %d", metaA.SequenceAlignmentData.CurrentAnchorAssumption, metaB.SequenceAlignmentData.CurrentAnchorAssumption)
	}

	expected := map[int]structures.ExecutionStats{
		0: {Index: scenario.anchor0ProofAtAnchor1, Hash: scenario.anchor0Hashes[scenario.anchor0ProofAtAnchor1]},
	}
	if !reflect.DeepEqual(metaA.SequenceAlignmentData.LastBlocksByAnchors, expected) {
		t.Fatalf("unexpected catch-up targets: got %v, expected %v", metaA.SequenceAlignmentData.LastBlocksByAnchors, expected)
	}

	simulateSequenceAlignmentThreadForTest(t, &metaA, epochHandler, fetcher, 10)
	simulateSequenceAlignmentThreadForTest(t, &metaB, epochHandler, fetcher, 10)

	if metaA.SequenceAlignmentData.CurrentAnchorAssumption != 1 {
		t.Fatalf("expected anchor assumption to advance to %d, got %d", 1, metaA.SequenceAlignmentData.CurrentAnchorAssumption)
	}

	if metaB.SequenceAlignmentData.CurrentAnchorAssumption != 1 {
		t.Fatalf("expected anchor assumption for node B to advance to %d, got %d", 1, metaB.SequenceAlignmentData.CurrentAnchorAssumption)
	}

	t.Logf("Converged outputs:"+
		"\n  Node A -> catch-up targets=%s, current anchor=%d"+
		"\n  Node B -> catch-up targets=%s, current anchor=%d"+
		"\n  Per-anchor last blocks: %s",
		formatCatchUpTargets(metaA.SequenceAlignmentData.LastBlocksByAnchors), metaA.SequenceAlignmentData.CurrentAnchorAssumption,
		formatCatchUpTargets(metaB.SequenceAlignmentData.LastBlocksByAnchors), metaB.SequenceAlignmentData.CurrentAnchorAssumption,
		formatPerAnchorHeights(metaA.SequenceAlignmentData.LastBlocksByAnchors))
}

func buildRotationScenarioForTest(t *testing.T, rng *rand.Rand, anchorKeys []cryptography.Ed25519Box, epochHandler structures.EpochDataHandler) testRotationScenario {

	t.Helper()

	if rng == nil {
		rng = rand.New(rand.NewSource(42))
	}

	anchor0At1 := rng.Intn(3) + 1
	anchor1At2 := anchor0At1 + rng.Intn(3) + 2
	anchor0At2 := anchor0At1 + rng.Intn(2) + 1

	if anchor0At2 >= anchor1At2 {
		anchor0At2 = anchor1At2 - 1
	}

	afpIndexB := anchor0At1 + 1

	maxProof := anchor1At2
	if anchor0At2 > maxProof {
		maxProof = anchor0At2
	}

	afpIndexA := maxProof + 1

	blockMap := make(map[string]*websocket_pack.WsAnchorBlockWithAfpResponse)

	anchor0Blocks := anchor0At2
	if anchor0Blocks < anchor0At1 {
		anchor0Blocks = anchor0At1
	}
	anchor0Hashes := addAnchorBlocksForScenario(blockMap, anchorKeys[0], epochHandler, anchor0Blocks+1, nil)
	anchor0RotationAtAnchor1 := buildAggregatedAnchorRotationProof(anchorKeys, epochHandler, anchorKeys[0].Pub, anchor0At1, anchor0Hashes[anchor0At1])
	anchor0RotationAtAnchor2 := buildAggregatedAnchorRotationProof(anchorKeys, epochHandler, anchorKeys[0].Pub, anchor0At2, anchor0Hashes[anchor0At2])

	anchor1Blocks := anchor1At2
	if afpIndexB > anchor1Blocks {
		anchor1Blocks = afpIndexB
	}
	anchor1Hashes := addAnchorBlocksForScenario(blockMap, anchorKeys[1], epochHandler, anchor1Blocks+1, map[int][]anchors_pack.AggregatedAnchorRotationProof{
		anchor0At1: {anchor0RotationAtAnchor1},
	})
	anchor1RotationAtAnchor2 := buildAggregatedAnchorRotationProof(anchorKeys, epochHandler, anchorKeys[1].Pub, anchor1At2, anchor1Hashes[anchor1At2])

	addAnchorBlocksForScenario(blockMap, anchorKeys[2], epochHandler, afpIndexA, map[int][]anchors_pack.AggregatedAnchorRotationProof{
		anchor1At2: {anchor1RotationAtAnchor2},
		anchor0At2: {anchor0RotationAtAnchor2},
	})

	afpABlockID := fmt.Sprintf("%d:%s:%d", epochHandler.Id, anchorKeys[2].Pub, afpIndexA)
	afpA := buildAggregatedFinalizationProof(anchorKeys, epochHandler, afpABlockID, fmt.Sprintf("block_hash_%s", afpABlockID))

	afpBBlockID := fmt.Sprintf("%d:%s:%d", epochHandler.Id, anchorKeys[1].Pub, afpIndexB)
	afpB := buildAggregatedFinalizationProof(anchorKeys, epochHandler, afpBBlockID, fmt.Sprintf("block_hash_%s", afpBBlockID))

	return testRotationScenario{
		blockMap:      blockMap,
		anchor0Hashes: anchor0Hashes,
		responseA: threads.SequenceAlignmentDataResponse{
			FoundInAnchorIndex: 2,
			Afp:                &afpA,
			Anchors: map[int]threads.SequenceAlignmentAnchorData{
				0: {AggregatedAnchorRotationProof: anchor0RotationAtAnchor1, FoundInBlock: anchor0At1},
				1: {AggregatedAnchorRotationProof: anchor1RotationAtAnchor2, FoundInBlock: anchor1At2},
			},
		},
		responseB: threads.SequenceAlignmentDataResponse{
			FoundInAnchorIndex: 1,
			Afp:                &afpB,
			Anchors: map[int]threads.SequenceAlignmentAnchorData{
				0: {AggregatedAnchorRotationProof: anchor0RotationAtAnchor1, FoundInBlock: anchor0At1},
			},
		},
		anchor0ProofAtAnchor1: anchor0At1,
		anchor1ProofAtAnchor2: anchor1At2,
		anchor0ProofAtAnchor2: anchor0At2,
	}
}

func addAnchorBlocksForScenario(blockMap map[string]*websocket_pack.WsAnchorBlockWithAfpResponse, anchor cryptography.Ed25519Box, epochHandler structures.EpochDataHandler, totalBlocks int, proofsByIndex map[int][]anchors_pack.AggregatedAnchorRotationProof) map[int]string {

	hashes := make(map[int]string, totalBlocks)

	for i := 0; i < totalBlocks; i++ {
		var proofs []anchors_pack.AggregatedAnchorRotationProof
		if proofsByIndex != nil {
			proofs = proofsByIndex[i]
		}

		prevHash := ""
		if i > 0 {
			prevHash = fmt.Sprintf("prev-%d", i-1)
		}

		block := buildSignedAnchorBlock(anchor.Pub, anchor.Prv, epochHandler, i, prevHash, proofs)
		blockMap[fmt.Sprintf("%d:%s:%d", epochHandler.Id, anchor.Pub, i)] = &websocket_pack.WsAnchorBlockWithAfpResponse{Block: block}
		hashes[i] = block.GetHash()
	}

	return hashes
}

func buildSignedAnchorBlock(creatorPub, creatorPrv string, epochHandler structures.EpochDataHandler, index int, prevHash string, proofs []anchors_pack.AggregatedAnchorRotationProof) *anchors_pack.AnchorBlock {

	block := &anchors_pack.AnchorBlock{
		Creator:  creatorPub,
		Time:     time.Now().UnixMilli(),
		Epoch:    fmt.Sprintf("%s#%d", epochHandler.Hash, epochHandler.Id),
		Index:    index,
		PrevHash: prevHash,
		ExtraData: anchors_pack.ExtraDataToBlock{
			AggregatedAnchorRotationProofs: proofs,
		},
	}

	block.Sig = cryptography.GenerateSignature(creatorPrv, block.GetHash())
	return block
}

func formatAnchorBlocksLog(anchorKeys []cryptography.Ed25519Box, blockMap map[string]*websocket_pack.WsAnchorBlockWithAfpResponse, epochHandler structures.EpochDataHandler) string {

	pubToIndex := make(map[string]int)
	for idx, key := range anchorKeys {
		pubToIndex[key.Pub] = idx
	}

	lines := make([]string, 0, len(anchorKeys))
	for idx, key := range anchorKeys {
		prefix := fmt.Sprintf("%d:%s:", epochHandler.Id, key.Pub)
		var blockIndices []int
		for k := range blockMap {
			if strings.HasPrefix(k, prefix) {
				parts := strings.Split(k, ":")
				if len(parts) != 3 {
					continue
				}
				blockIdx, err := strconv.Atoi(parts[2])
				if err != nil {
					continue
				}
				blockIndices = append(blockIndices, blockIdx)
			}
		}

		sort.Ints(blockIndices)

		blocks := make([]string, 0, len(blockIndices))
		for _, blockIdx := range blockIndices {
			block := blockMap[fmt.Sprintf("%d:%s:%d", epochHandler.Id, key.Pub, blockIdx)]
			proofs := block.Block.ExtraData.AggregatedAnchorRotationProofs
			blocks = append(blocks, fmt.Sprintf("%d[%s]", blockIdx, formatProofAnchors(proofs, pubToIndex)))
		}

		lines = append(lines, fmt.Sprintf("  anchor%d (pub=%s...): %s", idx, shortenPub(key.Pub), strings.Join(blocks, ", ")))
	}

	return strings.Join(lines, "\n")
}

func formatProofAnchors(proofs []anchors_pack.AggregatedAnchorRotationProof, pubToIndex map[string]int) string {

	if len(proofs) == 0 {
		return "no proofs"
	}

	entries := make([]string, 0, len(proofs))
	for _, proof := range proofs {
		if idx, ok := pubToIndex[proof.Anchor]; ok {
			entries = append(entries, fmt.Sprintf("anchor%d", idx))
			continue
		}
		entries = append(entries, shortenPub(proof.Anchor))
	}

	return strings.Join(entries, ",")
}

func shortenPub(pub string) string {

	if len(pub) <= 6 {
		return pub
	}

	return pub[:6]
}

func formatAnchorProofSummary(prefix string, anchors map[int]threads.SequenceAlignmentAnchorData) string {
	if len(anchors) == 0 {
		return prefix + "anchor proofs: none"
	}

	keys := make([]int, 0, len(anchors))
	for idx := range anchors {
		keys = append(keys, idx)
	}
	sort.Ints(keys)

	lines := make([]string, 0, len(keys)+1)
	lines = append(lines, prefix+"anchor proofs:")
	for _, idx := range keys {
		entry := anchors[idx]
		lines = append(lines, fmt.Sprintf("%s- proof for anchorIndex=%d (anchorPub=%s...) found in block %d", prefix, idx, shortPub(entry.AggregatedAnchorRotationProof.Anchor), entry.FoundInBlock))
	}

	return strings.Join(lines, "\n")
}

func formatCatchUpTargets(targets map[int]structures.ExecutionStats) string {
	if len(targets) == 0 {
		return "none"
	}

	keys := make([]int, 0, len(targets))
	for k := range targets {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("anchor %d -> block %d (hash=%s)", k, targets[k].Index, shortenHash(targets[k].Hash)))
	}

	return strings.Join(parts, "; ")
}

func formatPerAnchorHeights(targets map[int]structures.ExecutionStats) string {
	if len(targets) == 0 {
		return "none"
	}

	keys := make([]int, 0, len(targets))
	for k := range targets {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("anchor %d finalized at block %d (hash=%s)", k, targets[k].Index, shortenHash(targets[k].Hash)))
	}

	return strings.Join(parts, "; ")
}

func shortPub(pub string) string {
	if len(pub) <= 6 {
		return pub
	}
	return pub[:6]
}

func shortenHash(hash string) string {
	if len(hash) <= 8 {
		return hash
	}

	return hash[:8]
}

func afpBlockIndex(afp *structures.AggregatedFinalizationProof) string {
	if afp == nil {
		return "n/a"
	}

	parts := strings.Split(afp.BlockId, ":")
	if len(parts) == 0 {
		return afp.BlockId
	}

	return parts[len(parts)-1]
}

func buildAggregatedFinalizationProof(anchorKeys []cryptography.Ed25519Box, epochHandler structures.EpochDataHandler, blockID string, blockHash string) structures.AggregatedFinalizationProof {

	proof := structures.AggregatedFinalizationProof{PrevBlockHash: "prev_hash", BlockId: blockID, BlockHash: blockHash, Proofs: make(map[string]string)}

	dataToSign := fmt.Sprintf("%s:%s:%s:%d", proof.PrevBlockHash, proof.BlockId, proof.BlockHash, epochHandler.Id)

	for _, key := range anchorKeys {
		proof.Proofs[key.Pub] = cryptography.GenerateSignature(key.Prv, dataToSign)
	}

	return proof
}

func buildAggregatedAnchorRotationProof(anchorKeys []cryptography.Ed25519Box, epochHandler structures.EpochDataHandler, anchorPub string, blockIndex int, blockHash string) anchors_pack.AggregatedAnchorRotationProof {
	blockID := fmt.Sprintf("%d:%s:%d", epochHandler.Id, anchorPub, blockIndex)

	proof := anchors_pack.AggregatedAnchorRotationProof{
		EpochIndex: epochHandler.Id,
		Anchor:     anchorPub,
		VotingStat: structures.VotingStat{Index: blockIndex, Hash: blockHash},
		Signatures: make(map[string]string),
	}

	proof.VotingStat.Afp = buildAggregatedFinalizationProof(anchorKeys, epochHandler, blockID, blockHash)

	signPayload := anchors_pack.BuildAnchorRotationProofPayload(anchorPub, blockIndex, blockHash, epochHandler.Id)
	for _, key := range anchorKeys {
		proof.Signatures[key.Pub] = cryptography.GenerateSignature(key.Prv, signPayload)
	}

	return proof
}

type testAnchorBlockFetcher func(blockID string) *websocket_pack.WsAnchorBlockWithAfpResponse

func processSequenceAlignmentDataResponseForTest(alignmentData *threads.SequenceAlignmentDataResponse, anchorIndex int, epochHandler *structures.EpochDataHandler, metadata *structures.ExecutionThreadMetadataHandler, fetcher testAnchorBlockFetcher) bool {

	if alignmentData == nil || alignmentData.Afp == nil || metadata == nil || epochHandler == nil || fetcher == nil {
		return false
	}

	if alignmentData.FoundInAnchorIndex <= anchorIndex || alignmentData.FoundInAnchorIndex >= len(globals.ANCHORS) {
		return false
	}

	if _, exists := metadata.SequenceAlignmentData.LastBlocksByAnchors[anchorIndex]; exists {
		return false
	}

	if !utils.VerifyAggregatedFinalizationProofForAnchorBlock(alignmentData.Afp, epochHandler) {
		return false
	}

	blockIdParts := strings.Split(alignmentData.Afp.BlockId, ":")

	if len(blockIdParts) != 3 {
		return false
	}

	epochIDFromBlock, err := strconv.Atoi(blockIdParts[0])

	if err != nil || epochIDFromBlock != epochHandler.Id {
		return false
	}

	blockIndexInAfp, err := strconv.Atoi(blockIdParts[2])

	if err != nil {
		return false
	}

	anchorFromBlock := blockIdParts[1]
	expectedAnchor := globals.ANCHORS[alignmentData.FoundInAnchorIndex]

	if anchorFromBlock != expectedAnchor.Pubkey {
		return false
	}

	maxFoundInBlock := -1

	anchorIndexMap := make(map[string]int, len(globals.ANCHORS))
	for idx, anchor := range globals.ANCHORS {
		anchorIndexMap[anchor.Pubkey] = idx
	}

	for _, anchorData := range alignmentData.Anchors {
		if anchorData.FoundInBlock > maxFoundInBlock {
			maxFoundInBlock = anchorData.FoundInBlock
		}
	}

	if blockIndexInAfp != maxFoundInBlock+1 {
		return false
	}

	for i := anchorIndex; i < alignmentData.FoundInAnchorIndex; i++ {
		anchorData, exists := alignmentData.Anchors[i]

		if !exists {
			return false
		}

		if !anchors_pack.VerifyAggregatedAnchorRotationProof(&anchorData.AggregatedAnchorRotationProof) {
			return false
		}
	}

	earliestRotationBlock, found := findEarliestAnchorRotationProofForTest(anchorIndex, alignmentData.FoundInAnchorIndex, blockIndexInAfp, epochHandler, anchorIndexMap, fetcher)
	if !found {
		return false
	}

	if metadata.SequenceAlignmentData.LastBlocksByAnchors == nil {
		metadata.SequenceAlignmentData.LastBlocksByAnchors = make(map[int]structures.ExecutionStats)
	}

	if _, exists := metadata.SequenceAlignmentData.LastBlocksByAnchors[anchorIndex]; !exists {
		metadata.SequenceAlignmentData.LastBlocksByAnchors[anchorIndex] = earliestRotationBlock
	}

	return true
}

func findEarliestAnchorRotationProofForTest(currentAnchor, foundInAnchorIndex, blockLimit int, epochHandler *structures.EpochDataHandler, anchorIndexMap map[string]int, fetcher testAnchorBlockFetcher) (structures.ExecutionStats, bool) {

	if epochHandler == nil || anchorIndexMap == nil || fetcher == nil || currentAnchor < 0 || foundInAnchorIndex >= len(globals.ANCHORS) {
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
			response := fetcher(blockID)

			if response == nil || response.Block == nil {
				return structures.ExecutionStats{}, false
			}

			block := response.Block

			if block.Creator != anchor.Pubkey || block.Index != blockIndex || !block.VerifySignature() {
				return structures.ExecutionStats{}, false
			}

			for _, proof := range block.ExtraData.AggregatedAnchorRotationProofs {
				targetIdx, exists := anchorIndexMap[proof.Anchor]

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

func simulateSequenceAlignmentThreadForTest(t *testing.T, metadata *structures.ExecutionThreadMetadataHandler, epochHandler structures.EpochDataHandler, fetcher testAnchorBlockFetcher, maxSteps int) {

	t.Helper()

	if metadata == nil || fetcher == nil || maxSteps <= 0 {
		return
	}

	for step := 0; step < maxSteps; step++ {

		anchorIndex := metadata.SequenceAlignmentData.CurrentAnchorAssumption

		if anchorIndex < 0 || anchorIndex >= len(globals.ANCHORS) {
			return
		}

		target, ok := metadata.SequenceAlignmentData.LastBlocksByAnchors[anchorIndex]

		if !ok {
			return
		}

		anchor := globals.ANCHORS[anchorIndex]
		currentExec := metadata.SequenceAlignmentData.CurrentAnchorBlockIndexObserved

		blockID := fmt.Sprintf("%d:%s:%d", epochHandler.Id, anchor.Pubkey, currentExec)
		response := fetcher(blockID)

		if response == nil || response.Block == nil {
			return
		}

		block := response.Block

		if block.Creator != anchor.Pubkey || block.Index != currentExec || !block.VerifySignature() {
			return
		}

		if currentExec < target.Index {

			if response.Afp != nil {
				if response.Afp.BlockId != blockID || !utils.VerifyAggregatedFinalizationProofForAnchorBlock(response.Afp, &epochHandler) {
					return
				}
			}

			metadata.SequenceAlignmentData.CurrentAnchorBlockIndexObserved = currentExec + 1
			continue
		}

		if currentExec == target.Index {

			if target.Hash != "" && block.GetHash() != target.Hash {
				return
			}

			metadata.SequenceAlignmentData.LastBlocksByLeaders[anchor.Pubkey] = structures.ExecutionStats{
				Index: target.Index,
				Hash:  block.GetHash(),
			}

			metadata.SequenceAlignmentData.CurrentAnchorAssumption = anchorIndex + 1
			metadata.SequenceAlignmentData.CurrentAnchorBlockIndexObserved = -1
			continue
		}

		return
	}
}
