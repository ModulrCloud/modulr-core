package utils

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
)

func VerifyAggregatedFinalizationProof(proof *structures.AggregatedFinalizationProof, epochHandler *structures.EpochDataHandler) bool {
	if epochHandler == nil {
		return false
	}

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	dataThatShouldBeSigned := strings.Join([]string{proof.PrevBlockHash, proof.BlockId, proof.BlockHash, epochFullID}, ":")

	majority := GetQuorumMajority(epochHandler)

	quorumMap := make(map[string]bool, len(epochHandler.Quorum))
	for _, pk := range epochHandler.Quorum {
		quorumMap[pk] = true
	}

	okSignatures := 0
	seen := make(map[string]bool)

	for pubKey, signature := range proof.Proofs {
		if cryptography.VerifySignature(dataThatShouldBeSigned, pubKey, signature) {
			if quorumMap[pubKey] && !seen[pubKey] {
				seen[pubKey] = true
				okSignatures++
			}
		}
	}

	return okSignatures >= majority
}

func VerifyAggregatedFinalizationProofForAnchorBlock(proof *structures.AggregatedFinalizationProof, epochHandler *structures.EpochDataHandler) bool {
	epochIndex := strconv.Itoa(epochHandler.Id)

	dataThatShouldBeSigned := strings.Join([]string{proof.PrevBlockHash, proof.BlockId, proof.BlockHash, epochIndex}, ":")

	majority := GetAnchorsQuorumMajority()

	quorumMap := make(map[string]bool, len(globals.ANCHORS_PUBKEYS))
	for _, pk := range globals.ANCHORS_PUBKEYS {
		quorumMap[pk] = true
	}

	okSignatures := 0
	seen := make(map[string]bool)

	for pubKey, signature := range proof.Proofs {
		if cryptography.VerifySignature(dataThatShouldBeSigned, pubKey, signature) {
			if quorumMap[pubKey] && !seen[pubKey] {
				seen[pubKey] = true
				okSignatures++
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

	quorumMap := make(map[string]bool, len(epochHandler.Quorum))
	for _, pk := range epochHandler.Quorum {
		quorumMap[pk] = true
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

	dataToVerify := strings.Join([]string{constants.SigningPrefixLeaderFinalization, proof.Leader, strconv.Itoa(proof.VotingStat.Index), proof.VotingStat.Hash, epochFullID}, ":")

	okSignatures := 0
	seen := make(map[string]bool)

	for pubKey, signature := range proof.Signatures {
		if cryptography.VerifySignature(dataToVerify, pubKey, signature) {
			if quorumMap[pubKey] && !seen[pubKey] {
				seen[pubKey] = true
				okSignatures++
			}
		}
	}

	return okSignatures >= majority
}

func VerifyHeightAttestation(proof *structures.HeightAttestation, epochHandler *structures.EpochDataHandler) bool {
	if proof == nil || epochHandler == nil {
		return false
	}

	majority := GetQuorumMajority(epochHandler)

	quorumMap := make(map[string]bool, len(epochHandler.Quorum))
	for _, pk := range epochHandler.Quorum {
		quorumMap[pk] = true
	}

	dataToVerify := strings.Join([]string{
		constants.SigningPrefixHeightAttestation,
		strconv.Itoa(proof.AbsoluteHeight),
		proof.BlockId,
		proof.BlockHash,
		strconv.Itoa(proof.EpochId),
	}, ":")

	okSignatures := 0
	seen := make(map[string]bool)

	for pubKey, signature := range proof.Proofs {
		if cryptography.VerifySignature(dataToVerify, pubKey, signature) {
			if quorumMap[pubKey] && !seen[pubKey] {
				seen[pubKey] = true
				okSignatures++
			}
		}
	}

	return okSignatures >= majority
}

func GetVerifiedAggregatedFinalizationProofByBlockId(blockID string, epochHandler *structures.EpochDataHandler) *structures.AggregatedFinalizationProof {
	localAfpAsBytes, err := databases.EPOCH_DATA.Get([]byte(constants.DBKeyPrefixAfp+blockID), nil)

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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)

	defer cancel()

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

func GetVerifiedHeightAttestationFromQuorum(absoluteHeight int, expectedBlockId, expectedBlockHash string, epochHandler *structures.EpochDataHandler) *structures.HeightAttestation {
	if epochHandler == nil {
		return nil
	}

	quorum := GetQuorumUrlsAndPubkeys(epochHandler)
	resultChan := make(chan *structures.HeightAttestation, len(quorum))
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, node := range quorum {
		wg.Add(1)
		go func(endpoint string) {
			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/height_attestation/"+strconv.Itoa(absoluteHeight), nil)
			if err != nil {
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var proof structures.HeightAttestation
			if json.NewDecoder(resp.Body).Decode(&proof) == nil &&
				proof.BlockId == expectedBlockId &&
				proof.BlockHash == expectedBlockHash &&
				VerifyHeightAttestation(&proof, epochHandler) {
				select {
				case resultChan <- &proof:
					cancel()
				default:
				}
			}
		}(node.Url)
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

// GetHeightAttestationFromQuorumByHeight fetches a HeightAttestation by absolute height from quorum HTTP endpoints.
// Unlike GetVerifiedHeightAttestationFromQuorum, this does not require knowing the expected blockId/blockHash
// because the attestation itself is the source of truth for which block is at this height.
func GetHeightAttestationFromQuorumByHeight(absoluteHeight int, epochHandler *structures.EpochDataHandler) *structures.HeightAttestation {
	if epochHandler == nil {
		return nil
	}

	quorum := GetQuorumUrlsAndPubkeys(epochHandler)
	resultChan := make(chan *structures.HeightAttestation, len(quorum))
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, node := range quorum {
		wg.Add(1)
		go func(endpoint string) {
			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/height_attestation/"+strconv.Itoa(absoluteHeight), nil)
			if err != nil {
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var proof structures.HeightAttestation
			if json.NewDecoder(resp.Body).Decode(&proof) == nil &&
				proof.AbsoluteHeight == absoluteHeight &&
				VerifyHeightAttestation(&proof, epochHandler) {
				select {
				case resultChan <- &proof:
					cancel()
				default:
				}
			}
		}(node.Url)
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
