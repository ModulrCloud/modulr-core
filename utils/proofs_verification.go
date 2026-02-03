package utils

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
)

var (
	epochQuorumLowerCache = struct {
		mu sync.RWMutex
		m  map[string]map[string]struct{}
	}{
		m: make(map[string]map[string]struct{}),
	}

	anchorsQuorumLowerOnce sync.Once
	anchorsQuorumLower     map[string]struct{}

	seenSetPool = sync.Pool{
		New: func() any {
			return make(map[string]struct{})
		},
	}
)

func getEpochQuorumLower(epochHandler *structures.EpochDataHandler) map[string]struct{} {
	if epochHandler == nil {
		return nil
	}

	cacheKey := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	epochQuorumLowerCache.mu.RLock()
	if cached, ok := epochQuorumLowerCache.m[cacheKey]; ok {
		epochQuorumLowerCache.mu.RUnlock()
		return cached
	}
	epochQuorumLowerCache.mu.RUnlock()

	quorumMap := make(map[string]struct{}, len(epochHandler.Quorum))
	for _, pk := range epochHandler.Quorum {
		quorumMap[strings.ToLower(pk)] = struct{}{}
	}

	epochQuorumLowerCache.mu.Lock()
	epochQuorumLowerCache.m[cacheKey] = quorumMap
	epochQuorumLowerCache.mu.Unlock()

	return quorumMap
}

func getAnchorsQuorumLower() map[string]struct{} {
	anchorsQuorumLowerOnce.Do(func() {
		anchorsQuorumLower = make(map[string]struct{}, len(globals.ANCHORS_PUBKEYS))
		for _, pk := range globals.ANCHORS_PUBKEYS {
			anchorsQuorumLower[strings.ToLower(pk)] = struct{}{}
		}
	})

	return anchorsQuorumLower
}

func getSeenSet() map[string]struct{} {
	return seenSetPool.Get().(map[string]struct{})
}

func putSeenSet(seen map[string]struct{}) {
	for k := range seen {
		delete(seen, k)
	}
	seenSetPool.Put(seen)
}

func VerifyAggregatedFinalizationProof(proof *structures.AggregatedFinalizationProof, epochHandler *structures.EpochDataHandler) bool {

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	dataThatShouldBeSigned := strings.Join([]string{proof.PrevBlockHash, proof.BlockId, proof.BlockHash, epochFullID}, ":")

	majority := GetQuorumMajority(epochHandler)

	okSignatures := 0

	seen := getSeenSet()
	defer putSeenSet(seen)

	quorumMap := getEpochQuorumLower(epochHandler)
	if quorumMap == nil {
		return false
	}

	for pubKey, signature := range proof.Proofs {

		if cryptography.VerifySignature(dataThatShouldBeSigned, pubKey, signature) {

			loweredPubKey := strings.ToLower(pubKey)

			if _, isMember := quorumMap[loweredPubKey]; isMember {
				if _, alreadySeen := seen[loweredPubKey]; !alreadySeen {
					seen[loweredPubKey] = struct{}{}
					okSignatures++
				}
			}
		}
	}

	return okSignatures >= majority
}

func VerifyAggregatedFinalizationProofForAnchorBlock(proof *structures.AggregatedFinalizationProof, epochHandler *structures.EpochDataHandler) bool {

	epochIndex := strconv.Itoa(epochHandler.Id)

	dataThatShouldBeSigned := strings.Join([]string{proof.PrevBlockHash, proof.BlockId, proof.BlockHash, epochIndex}, ":")

	majority := GetAnchorsQuorumMajority()

	okSignatures := 0

	seen := getSeenSet()
	defer putSeenSet(seen)

	quorumMap := getAnchorsQuorumLower()

	for pubKey, signature := range proof.Proofs {

		if cryptography.VerifySignature(dataThatShouldBeSigned, pubKey, signature) {

			loweredPubKey := strings.ToLower(pubKey)

			if _, isMember := quorumMap[loweredPubKey]; isMember {
				if _, alreadySeen := seen[loweredPubKey]; !alreadySeen {
					seen[loweredPubKey] = struct{}{}
					okSignatures++
				}
			}
		}
	}

	return okSignatures >= majority
}

func VerifyAggregatedLeaderFinalizationProof(proof *structures.AggregatedLeaderFinalizationProof, epochHandler *structures.EpochDataHandler) bool {

	if proof == nil || epochHandler == nil || proof.EpochIndex != epochHandler.Id {
		return false
	}

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	majority := GetQuorumMajority(epochHandler)

	quorumMap := getEpochQuorumLower(epochHandler)
	if quorumMap == nil {
		return false
	}

	if proof.VotingStat.Index >= 0 {
		parts := strings.Split(proof.VotingStat.Afp.BlockId, ":")
		if len(parts) != 3 || parts[0] != strconv.Itoa(epochHandler.Id) || parts[1] != proof.Leader {
			return false
		}

		indexFromId, err := strconv.Atoi(parts[2])
		if err != nil || indexFromId != proof.VotingStat.Index || proof.VotingStat.Hash != proof.VotingStat.Afp.BlockHash {
			return false
		}

		if !VerifyAggregatedFinalizationProof(&proof.VotingStat.Afp, epochHandler) {
			return false
		}
	}

	dataToVerify := strings.Join([]string{"LEADER_FINALIZATION_PROOF", proof.Leader, strconv.Itoa(proof.VotingStat.Index), proof.VotingStat.Hash, epochFullID}, ":")

	okSignatures := 0
	seen := getSeenSet()
	defer putSeenSet(seen)

	for pubKey, signature := range proof.Signatures {
		if cryptography.VerifySignature(dataToVerify, pubKey, signature) {
			lowered := strings.ToLower(pubKey)
			if _, isMember := quorumMap[lowered]; isMember {
				if _, alreadySeen := seen[lowered]; !alreadySeen {
					seen[lowered] = struct{}{}
					okSignatures++
				}
			}
		}
	}

	return okSignatures >= majority
}

func GetVerifiedAggregatedFinalizationProofByBlockId(blockID string, epochHandler *structures.EpochDataHandler) *structures.AggregatedFinalizationProof {

	localAfpAsBytes, err := databases.EPOCH_DATA.Get([]byte("AFP:"+blockID), nil)

	if err == nil {

		var localAfpParsed structures.AggregatedFinalizationProof

		err = json.Unmarshal(localAfpAsBytes, &localAfpParsed)

		if err != nil {
			return nil
		}

		return &localAfpParsed

	}

	quorum := GetQuorumUrlsAndPubkeys(epochHandler)

	resultChan := make(chan *structures.AggregatedFinalizationProof, len(quorum))

	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())

	defer cancel() // ensure cancellation if function exits early

	for _, node := range quorum {

		wg.Add(1)

		go func(endpoint string) {

			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/aggregated_finalization_proof/"+blockID, nil)
			if err != nil {
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var afp structures.AggregatedFinalizationProof

			if json.NewDecoder(resp.Body).Decode(&afp) == nil && VerifyAggregatedFinalizationProof(&afp, epochHandler) {

				// send the first valid result and immediately cancel all other requests
				select {

				case resultChan <- &afp:
					cancel() // stop all remaining goroutines and HTTP requests
				default:

				}
			}

		}(node.Url)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Return first valid AFP
	for res := range resultChan {
		if res != nil {
			return res
		}
	}

	return nil
}

// GetVerifiedAnchorsAggregatedFinalizationProofByBlockId fetches an aggregated finalization proof for an anchor block
// from anchors (HTTP) and returns the first proof that verifies against the anchors quorum.
//
// This is intended as a fallback when Anchors-PoD hasn't received/stored AFP yet.
func GetVerifiedAnchorsAggregatedFinalizationProofByBlockId(blockID string, epochHandler *structures.EpochDataHandler) *structures.AggregatedFinalizationProof {
	if blockID == "" || epochHandler == nil {
		return nil
	}

	// Use a short timeout to avoid stalling threads when some anchors are down.
	client := &http.Client{Timeout: 2 * time.Second}

	resultChan := make(chan *structures.AggregatedFinalizationProof, len(globals.ANCHORS))
	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, anchor := range globals.ANCHORS {
		if anchor.AnchorUrl == "" {
			continue
		}

		wg.Add(1)
		go func(endpoint string) {
			defer wg.Done()

			endpoint = strings.TrimRight(endpoint, "/")
			req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/aggregated_finalization_proof/"+blockID, nil)
			if err != nil {
				return
			}

			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var afp structures.AggregatedFinalizationProof
			if json.NewDecoder(resp.Body).Decode(&afp) == nil && VerifyAggregatedFinalizationProofForAnchorBlock(&afp, epochHandler) {
				select {
				case resultChan <- &afp:
					cancel()
				default:
				}
			}
		}(anchor.AnchorUrl)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	for res := range resultChan {
		if res != nil {
			return res
		}
	}

	return nil
}
