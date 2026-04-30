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
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
)

func allowedPubkeysMap(pubkeys []string) map[string]bool {
	allowed := make(map[string]bool, len(pubkeys))
	for _, pk := range pubkeys {
		allowed[pk] = true
	}

	return allowed
}

func countVerifiedUniqueSignatures(signatures map[string]string, allowed map[string]bool, payload string) int {
	verified := 0
	seen := make(map[string]bool, len(signatures))

	for pubKey, signature := range signatures {
		if signature == "" || !allowed[pubKey] || seen[pubKey] {
			continue
		}
		if cryptography.VerifySignature(payload, pubKey, signature) {
			seen[pubKey] = true
			verified++
		}
	}

	return verified
}

func VerifyAggregatedFinalizationProof(proof *structures.AggregatedFinalizationProof, epochHandler *structures.EpochDataHandler) bool {
	if proof == nil || epochHandler == nil {
		return false
	}

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	dataThatShouldBeSigned := strings.Join([]string{proof.PrevBlockHash, proof.BlockId, proof.BlockHash, epochFullID}, ":")

	majority := GetQuorumMajority(epochHandler)
	okSignatures := countVerifiedUniqueSignatures(proof.Proofs, allowedPubkeysMap(epochHandler.Quorum), dataThatShouldBeSigned)

	return okSignatures >= majority
}

func VerifyAggregatedFinalizationProofForAnchorBlock(proof *structures.AggregatedFinalizationProof, epochHandler *structures.EpochDataHandler) bool {
	if proof == nil || epochHandler == nil {
		return false
	}

	epochIndex := strconv.Itoa(epochHandler.Id)

	dataThatShouldBeSigned := strings.Join([]string{proof.PrevBlockHash, proof.BlockId, proof.BlockHash, epochIndex}, ":")

	majority := GetAnchorsQuorumMajority()
	okSignatures := countVerifiedUniqueSignatures(proof.Proofs, allowedPubkeysMap(globals.ANCHORS_PUBKEYS), dataThatShouldBeSigned)

	return okSignatures >= majority
}

func VerifyAggregatedLeaderFinalizationProof(proof *structures.AggregatedLeaderFinalizationProof, epochHandler *structures.EpochDataHandler) bool {
	if proof == nil || epochHandler == nil || proof.EpochIndex != epochHandler.Id {
		return false
	}

	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	majority := GetQuorumMajority(epochHandler)

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
	okSignatures := countVerifiedUniqueSignatures(proof.Signatures, allowedPubkeysMap(epochHandler.Quorum), dataToVerify)

	return okSignatures >= majority
}

func VerifyAggregatedHeightProof(proof *structures.AggregatedHeightProof, epochHandler *structures.EpochDataHandler) bool {
	if proof == nil || epochHandler == nil {
		return false
	}

	majority := GetQuorumMajority(epochHandler)

	dataToVerify := strings.Join([]string{
		constants.SigningPrefixHeightProof,
		strconv.Itoa(proof.AbsoluteHeight),
		proof.BlockId,
		proof.BlockHash,
		strconv.Itoa(proof.EpochId),
		strconv.Itoa(proof.HeightInEpoch),
	}, ":")
	okSignatures := countVerifiedUniqueSignatures(proof.Proofs, allowedPubkeysMap(epochHandler.Quorum), dataToVerify)

	return okSignatures >= majority
}

func ComputeEpochDataHash(data *structures.NextEpochDataHandler) string {
	raw, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return Blake3(string(raw))
}

func BuildEpochRotationProofSigningPayload(
	epochId int,
	nextEpochId int,
	epochDataHash string,
	finishedOnHeight int64,
	finishedOnBlockId string,
	finishedOnHash string,
) string {
	return strings.Join([]string{
		constants.SigningPrefixEpochRotationProof,
		strconv.Itoa(epochId),
		strconv.Itoa(nextEpochId),
		epochDataHash,
		strconv.FormatInt(finishedOnHeight, 10),
		finishedOnBlockId,
		finishedOnHash,
	}, ":")
}

func VerifyAggregatedEpochRotationProof(proof *structures.AggregatedEpochRotationProof, epochHandler *structures.EpochDataHandler) bool {
	if proof == nil || epochHandler == nil {
		return false
	}

	if proof.NextEpochId != proof.EpochId+1 || proof.FinishedOnHeight < -1 || proof.FinishedOnHash == "" {
		return false
	}

	recomputedHash := ComputeEpochDataHash(&proof.EpochData)
	if recomputedHash == "" || recomputedHash != proof.EpochDataHash {
		return false
	}

	majority := GetQuorumMajority(epochHandler)

	dataToVerify := BuildEpochRotationProofSigningPayload(
		proof.EpochId,
		proof.NextEpochId,
		proof.EpochDataHash,
		proof.FinishedOnHeight,
		proof.FinishedOnBlockId,
		proof.FinishedOnHash,
	)
	okSignatures := countVerifiedUniqueSignatures(proof.Proofs, allowedPubkeysMap(epochHandler.Quorum), dataToVerify)

	return okSignatures >= majority
}

func VerifyAggregatedAnchorEpochAckProof(proof *structures.AggregatedAnchorEpochAckProof) bool {
	if proof == nil {
		return false
	}

	majority := GetAnchorsQuorumMajority()

	dataToVerify := strings.Join([]string{
		constants.SigningPrefixAnchorEpochAckProof,
		strconv.Itoa(proof.EpochId),
		strconv.Itoa(proof.NextEpochId),
		proof.EpochDataHash,
	}, ":")
	okSignatures := countVerifiedUniqueSignatures(proof.Proofs, allowedPubkeysMap(globals.ANCHORS_PUBKEYS), dataToVerify)

	return okSignatures >= majority
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

// GetAggregatedHeightProofFromQuorumByHeight fetches an AggregatedHeightProof by absolute height from quorum HTTP endpoints.
// The proof itself is the source of truth for which block is at this height.
func GetAggregatedHeightProofFromQuorumByHeight(absoluteHeight int, epochHandler *structures.EpochDataHandler) *structures.AggregatedHeightProof {
	if epochHandler == nil {
		return nil
	}

	quorum := GetQuorumUrlsAndPubkeys(epochHandler)
	resultChan := make(chan *structures.AggregatedHeightProof, len(quorum))
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, node := range quorum {
		wg.Add(1)
		go func(endpoint string) {
			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, "GET", endpoint+"/aggregated_height_proof/"+strconv.Itoa(absoluteHeight), nil)
			if err != nil {
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var proof structures.AggregatedHeightProof
			if json.NewDecoder(resp.Body).Decode(&proof) == nil &&
				proof.AbsoluteHeight == absoluteHeight &&
				VerifyAggregatedHeightProof(&proof, epochHandler) {
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

	select {
	case res := <-resultChan:
		return res
	case <-ctx.Done():
		return nil
	}
}

// GetFirstBlockAggregatedHeightProofFromQuorum fetches the AggregatedHeightProof with HeightInEpoch==0
// for the given epoch from any quorum member via GET /first_block_in_epoch/{epochId}.
// Verifies the response cryptographically (majority signature + HeightInEpoch == 0).
func GetFirstBlockAggregatedHeightProofFromQuorum(epochId int) *structures.AggregatedHeightProof {
	snapshot := GetEpochSnapshot(epochId)
	if snapshot == nil {
		return nil
	}
	epochHandler := &snapshot.EpochDataHandler

	quorum := GetQuorumUrlsAndPubkeys(epochHandler)
	allNodes := make([]string, 0, len(quorum)+len(globals.CONFIGURATION.BootstrapNodes))
	for _, node := range quorum {
		allNodes = append(allNodes, node.Url)
	}
	allNodes = append(allNodes, globals.CONFIGURATION.BootstrapNodes...)

	resultChan := make(chan *structures.AggregatedHeightProof, len(allNodes))
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, endpoint := range allNodes {
		if endpoint == globals.CONFIGURATION.MyHostname {
			continue
		}
		wg.Add(1)
		go func(url string) {
			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, "GET", url+"/first_block_in_epoch/"+strconv.Itoa(epochId), nil)
			if err != nil {
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var proof structures.AggregatedHeightProof
			if json.NewDecoder(resp.Body).Decode(&proof) == nil &&
				proof.EpochId == epochId &&
				proof.HeightInEpoch == 0 &&
				VerifyAggregatedHeightProof(&proof, epochHandler) {
				select {
				case resultChan <- &proof:
					cancel()
				default:
				}
			}
		}(endpoint)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	select {
	case res := <-resultChan:
		return res
	case <-ctx.Done():
		return nil
	}
}

// GetAggregatedEpochRotationProofFromQuorumByHTTP fetches an AggregatedEpochRotationProof from quorum/bootstrap
// nodes via GET /aggregated_epoch_rotation_proof/{epochId}. Used as a fallback when PoD is unavailable.
func GetAggregatedEpochRotationProofFromQuorumByHTTP(epochId int, epochHandler *structures.EpochDataHandler) *structures.AggregatedEpochRotationProof {
	if epochHandler == nil {
		return nil
	}

	quorum := GetQuorumUrlsAndPubkeys(epochHandler)
	resultChan := make(chan *structures.AggregatedEpochRotationProof, len(quorum)+len(globals.CONFIGURATION.BootstrapNodes))
	var wg sync.WaitGroup

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	allNodes := make([]string, 0, len(quorum)+len(globals.CONFIGURATION.BootstrapNodes))
	for _, node := range quorum {
		allNodes = append(allNodes, node.Url)
	}
	allNodes = append(allNodes, globals.CONFIGURATION.BootstrapNodes...)

	for _, endpoint := range allNodes {
		if endpoint == globals.CONFIGURATION.MyHostname {
			continue
		}
		wg.Add(1)
		go func(url string) {
			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, "GET", url+"/aggregated_epoch_rotation_proof/"+strconv.Itoa(epochId), nil)
			if err != nil {
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			var proof structures.AggregatedEpochRotationProof
			if json.NewDecoder(resp.Body).Decode(&proof) == nil &&
				proof.EpochId == epochId &&
				VerifyAggregatedEpochRotationProof(&proof, epochHandler) {
				select {
				case resultChan <- &proof:
					cancel()
				default:
				}
			}
		}(endpoint)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	select {
	case res := <-resultChan:
		return res
	case <-ctx.Done():
		return nil
	}
}
