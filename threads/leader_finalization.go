package threads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
	"github.com/modulrcloud/modulr-core/websocket_pack"

	"github.com/gorilla/websocket"
)

type LeaderFinalizationCache struct {
	SkipData structures.VotingStat
	Proofs   map[string]string
	// Timestamp of the last broadcast to anchors to avoid spamming
	LastBroadcasted time.Time
	// Timestamp of the last PoD fetch to avoid spamming PoD
	LastPodFetch time.Time
}

type AlfpProcessMetadata struct {
	EpochId  int
	Snapshot *structures.EpochDataSnapshot
	Caches   map[string]*LeaderFinalizationCache
	WsConns  map[string]*websocket.Conn
	Waiter   *utils.QuorumWaiter
	Guards   *utils.WebsocketGuards
}

var (
	ALFP_GRABBING_MUTEX   = sync.Mutex{}
	ALFP_PROCESS_METADATA *AlfpProcessMetadata
)

// GetLeaderFinalizationWsConnectionCount returns the number of non-nil WS connections
// currently maintained by LeaderFinalizationThread for the given epoch.
// Returns 0 if the thread is not processing this epoch.
func GetLeaderFinalizationWsConnectionCount(epochId int) int {
	ALFP_GRABBING_MUTEX.Lock()
	defer ALFP_GRABBING_MUTEX.Unlock()

	if ALFP_PROCESS_METADATA == nil || ALFP_PROCESS_METADATA.EpochId != epochId {
		return 0
	}
	wsCount := 0
	for _, conn := range ALFP_PROCESS_METADATA.WsConns {
		if conn != nil {
			wsCount++
		}
	}
	return wsCount
}

// GetLeaderFinalizationLiveCache returns a snapshot of in-memory collection progress
// for the (epochId, leader) pair.
// ok=false if there is no active metadata/caches for this epoch or leader.
func GetLeaderFinalizationLiveCache(epochId int, leader string) (skipDataIndex int, skipDataHash string, proofsCollected int, ok bool) {
	ALFP_GRABBING_MUTEX.Lock()
	defer ALFP_GRABBING_MUTEX.Unlock()

	if ALFP_PROCESS_METADATA == nil || ALFP_PROCESS_METADATA.EpochId != epochId {
		return 0, "", 0, false
	}
	key := fmt.Sprintf("%d:%s", epochId, leader)
	cache, exists := ALFP_PROCESS_METADATA.Caches[key]
	if !exists || cache == nil {
		return 0, "", 0, false
	}
	return cache.SkipData.Index, cache.SkipData.Hash, len(cache.Proofs), true
}

func LeaderFinalizationThread() {

	processingEpochIndex := loadFinalizationProgress()

	for {

		// If execution already advanced beyond our current ALFP progress,
		// there is no point processing older epochs here. Fast-forward.
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
		execEpochId := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
		handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

		if execEpochId > processingEpochIndex {
			currentEpoch := processingEpochIndex
			processingEpochIndex = execEpochId
			persistFinalizationProgress(processingEpochIndex)
			cleanupLeaderFinalizationState(currentEpoch)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		snapshot := getOrLoadEpochSnapshot(processingEpochIndex)

		if snapshot == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		processingHandler := &snapshot.EpochDataHandler

		if !weAreInEpochQuorum(processingHandler) {
			currentEpoch := processingEpochIndex
			processingEpochIndex++
			persistFinalizationProgress(processingEpochIndex)
			cleanupLeaderFinalizationState(currentEpoch)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		state := ensureAlfpProcessMetadata(processingHandler)
		majority := utils.GetQuorumMajority(processingHandler)

		networkParams := snapshot.NetworkParameters

		for _, leaderIndex := range leadersReadyForAlfp(processingHandler, &networkParams) {
			tryCollectLeaderFinalizationProofs(processingHandler, leaderIndex, majority, state)
		}

		if allLeaderFinalizationProofsCollected(processingHandler) {
			currentEpoch := processingEpochIndex
			processingEpochIndex++
			persistFinalizationProgress(processingEpochIndex)
			cleanupLeaderFinalizationState(currentEpoch)
		}

		time.Sleep(200 * time.Millisecond)
	}
}

func ensureAlfpProcessMetadata(epochHandler *structures.EpochDataHandler) *AlfpProcessMetadata {

	ALFP_GRABBING_MUTEX.Lock()
	defer ALFP_GRABBING_MUTEX.Unlock()

	if ALFP_PROCESS_METADATA != nil && ALFP_PROCESS_METADATA.EpochId == epochHandler.Id {
		return ALFP_PROCESS_METADATA
	}

	// If we are switching epochs, close old connections and drop old caches.
	if ALFP_PROCESS_METADATA != nil {
		for _, conn := range ALFP_PROCESS_METADATA.WsConns {
			if conn != nil {
				_ = conn.Close()
			}
		}
		ALFP_PROCESS_METADATA = nil
	}

	guards := utils.NewWebsocketGuards()
	alfpProcMetadata := &AlfpProcessMetadata{
		EpochId: epochHandler.Id,
		Caches:  make(map[string]*LeaderFinalizationCache),
		WsConns: make(map[string]*websocket.Conn),
		Waiter:  utils.NewQuorumWaiter(len(epochHandler.Quorum), guards),
		Guards:  guards,
	}

	utils.OpenWebsocketConnectionsWithQuorum(epochHandler.Quorum, alfpProcMetadata.WsConns, guards)
	ALFP_PROCESS_METADATA = alfpProcMetadata

	return alfpProcMetadata
}

func getOrLoadEpochSnapshot(epochId int) *structures.EpochDataSnapshot {

	ALFP_GRABBING_MUTEX.Lock()
	if ALFP_PROCESS_METADATA != nil &&
		ALFP_PROCESS_METADATA.EpochId == epochId &&
		ALFP_PROCESS_METADATA.Snapshot != nil {
		s := ALFP_PROCESS_METADATA.Snapshot
		ALFP_GRABBING_MUTEX.Unlock()
		return s
	}
	ALFP_GRABBING_MUTEX.Unlock()

	key := []byte("EPOCH_HANDLER:" + strconv.Itoa(epochId))
	// EPOCH_HANDLER snapshots are stored in APPROVEMENT_THREAD_METADATA DB
	// to be committed atomically with AT updates.
	raw, err := databases.APPROVEMENT_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return nil
	}

	var loaded structures.EpochDataSnapshot
	if json.Unmarshal(raw, &loaded) != nil {
		return nil
	}

	// Keep snapshot in the current epoch state (single-epoch processing).
	ALFP_GRABBING_MUTEX.Lock()
	if ALFP_PROCESS_METADATA != nil && ALFP_PROCESS_METADATA.EpochId == epochId {
		ALFP_PROCESS_METADATA.Snapshot = &loaded
		ALFP_GRABBING_MUTEX.Unlock()
		return ALFP_PROCESS_METADATA.Snapshot
	}
	ALFP_GRABBING_MUTEX.Unlock()

	return &loaded
}

func loadFinalizationProgress() int {

	if raw, err := databases.FINALIZATION_VOTING_STATS.Get([]byte("ALFP_PROGRESS"), nil); err == nil {
		if idx, convErr := strconv.Atoi(string(raw)); convErr == nil {
			return idx
		}
	}

	return 0
}

func persistFinalizationProgress(epochId int) {

	_ = databases.FINALIZATION_VOTING_STATS.Put([]byte("ALFP_PROGRESS"), []byte(strconv.Itoa(epochId)), nil)
}

func cleanupLeaderFinalizationState(epochId int) {

	ALFP_GRABBING_MUTEX.Lock()
	defer ALFP_GRABBING_MUTEX.Unlock()

	if ALFP_PROCESS_METADATA == nil || ALFP_PROCESS_METADATA.EpochId != epochId {
		return
	}

	for _, conn := range ALFP_PROCESS_METADATA.WsConns {
		if conn != nil {
			_ = conn.Close()
		}
	}

	ALFP_PROCESS_METADATA = nil
}

func weAreInEpochQuorum(epochHandler *structures.EpochDataHandler) bool {

	for _, quorumMember := range epochHandler.Quorum {
		if strings.EqualFold(quorumMember, globals.CONFIGURATION.PublicKey) {
			return true
		}
	}

	return false
}

func leadersReadyForAlfp(epochHandler *structures.EpochDataHandler, networkParams *structures.NetworkParameters) []int {

	ready := make([]int, 0)

	for idx := range epochHandler.LeadersSequence {

		leader := epochHandler.LeadersSequence[idx]
		if utils.HasAnyAlfpIncluded(epochHandler.Id, leader) {
			continue
		}
		if leaderFinalizationConfirmedByAlignment(epochHandler.Id, leader) {
			continue
		}

		// Don't rely on CurrentLeaderIndex from a persisted snapshot:
		// determine "leader finished" deterministically from epoch start timestamp + leadership window.
		leaderFinished := leaderTimeIsOut(epochHandler, networkParams, idx)

		if leaderFinished {
			ready = append(ready, idx)
		}
	}

	return ready
}

func leaderHasAlfp(epochId int, leader string) bool {

	key := []byte(fmt.Sprintf("ALFP:%d:%s", epochId, leader))
	_, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	return err == nil
}

func loadAggregatedLeaderFinalizationProof(epochId int, leader string) *structures.AggregatedLeaderFinalizationProof {

	key := []byte(fmt.Sprintf("ALFP:%d:%s", epochId, leader))
	raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil)
	if err != nil {
		return nil
	}

	var proof structures.AggregatedLeaderFinalizationProof
	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}

	return &proof
}

func leaderFinalizationConfirmedByAlignment(epochId int, leader string) bool {

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id != epochId {
		return false
	}

	_, exists := handlers.EXECUTION_THREAD_METADATA.Handler.SequenceAlignmentData.LastBlocksByLeaders[leader]
	return exists
}

func allLeaderFinalizationProofsCollected(epochHandler *structures.EpochDataHandler) bool {

	// If execution already advanced beyond this epoch, it means ET has all the data it needs for it.
	// No point to keep ALFP thread blocked on older epoch.
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	execEpochId := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id
	handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	if execEpochId > epochHandler.Id {
		return true
	}

	for _, leader := range epochHandler.LeadersSequence {
		// Prefer durable inclusion markers from ALFP watcher (independent from execution epoch).
		if utils.HasAnyAlfpIncluded(epochHandler.Id, leader) {
			continue
		}

		// Fallback: if execution is on this epoch and alignment already confirmed, allow progress.
		if executionMetadataMatches(epochHandler.Id) && leaderFinalizationConfirmedByAlignment(epochHandler.Id, leader) {
			continue
		}

		// Otherwise, we still need to collect and/or deliver this leader's ALFP.
		return false
	}

	return true
}

func executionMetadataMatches(epochId int) bool {

	handlers.EXECUTION_THREAD_METADATA.RWMutex.RLock()
	defer handlers.EXECUTION_THREAD_METADATA.RWMutex.RUnlock()

	return handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.Id == epochId
}

func leaderTimeIsOut(epochHandler *structures.EpochDataHandler, networkParams *structures.NetworkParameters, leaderIndex int) bool {

	leadershipTimeframe := networkParams.LeadershipDuration
	return utils.GetUTCTimestampInMilliSeconds() >= int64(epochHandler.StartTimestamp)+(int64(leaderIndex)+1)*leadershipTimeframe
}

func tryCollectLeaderFinalizationProofs(epochHandler *structures.EpochDataHandler, leaderIndex, majority int, state *AlfpProcessMetadata) {

	leaderPubKey := epochHandler.LeadersSequence[leaderIndex]
	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	if utils.HasAnyAlfpIncluded(epochHandler.Id, leaderPubKey) {
		return
	}
	if leaderFinalizationConfirmedByAlignment(epochHandler.Id, leaderPubKey) {
		return
	}

	cache := ensureLeaderFinalizationCache(state, epochHandler.Id, leaderPubKey)

	if leaderHasAlfp(epochHandler.Id, leaderPubKey) || state.Waiter == nil {
		if aggregated := loadAggregatedLeaderFinalizationProof(epochHandler.Id, leaderPubKey); aggregated != nil && !utils.HasAnyAlfpIncluded(epochHandler.Id, leaderPubKey) && shouldBroadcastLeaderFinalization(cache) {
			markLeaderFinalizationBroadcast(cache)
			sendAggregatedLeaderFinalizationProofToAnchors(aggregated)
		}
		return
	}

	requestLeaderFinalizationFromPoD(epochHandler, leaderPubKey, cache)

	request := websocket_pack.WsLeaderFinalizationProofRequest{
		Route:                   constants.WsRouteGetLeaderFinalizationProof,
		EpochIndex:              epochHandler.Id,
		IndexOfLeaderToFinalize: leaderIndex,
		SkipData:                cache.SkipData,
	}

	message, err := json.Marshal(request)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Capture cache.SkipData snapshot for validation (it may change during processing)
	cacheSnapshot := cache.SkipData

	// Validation function for leader finalization proofs
	validateLeaderFinalization := func(id string, raw []byte) bool {
		var statusHolder map[string]any
		if err := json.Unmarshal(raw, &statusHolder); err != nil {
			return false
		}

		status, ok := statusHolder["status"].(string)
		if !ok {
			return false
		}

		switch status {
		case "OK":
			var response websocket_pack.WsLeaderFinalizationProofResponseOk
			if json.Unmarshal(raw, &response) != nil {
				return false
			}

			if response.ForLeaderPubkey != leaderPubKey {
				return false
			}

			// Verify voter is in quorum
			quorumMap := make(map[string]bool)
			for _, pk := range epochHandler.Quorum {
				quorumMap[strings.ToLower(pk)] = true
			}
			if !quorumMap[strings.ToLower(response.Voter)] {
				return false
			}

			// Verify signature
			dataToVerify := strings.Join([]string{"LEADER_FINALIZATION_PROOF", leaderPubKey, strconv.Itoa(cacheSnapshot.Index), cacheSnapshot.Hash, epochFullID}, ":")
			return cryptography.VerifySignature(dataToVerify, response.Voter, response.Sig)

		case "UPGRADE":
			var response websocket_pack.WsLeaderFinalizationProofResponseUpgrade
			if json.Unmarshal(raw, &response) != nil {
				return false
			}

			if response.ForLeaderPubkey != leaderPubKey {
				return false
			}

			return validateUpgradePayload(response, epochHandler, leaderPubKey)

		default:
			return false
		}
	}

	responses, ok := state.Waiter.SendAndWaitValidated(ctx, message, epochHandler.Quorum, state.WsConns, majority, validateLeaderFinalization)
	if !ok {
		utils.LogWithTimeThrottled(
			fmt.Sprintf("alfp:majority_failed:%d:%s", epochHandler.Id, leaderPubKey),
			5*time.Second,
			fmt.Sprintf("ALFP: failed to collect majority (epoch=%d leader=%s quorum=%d majority=%d)", epochHandler.Id, leaderPubKey, len(epochHandler.Quorum), majority),
			utils.YELLOW_COLOR,
		)
		return
	}

	// All responses are already validated, just process them
	for _, raw := range responses {
		handleLeaderFinalizationResponse(raw, epochHandler, leaderPubKey, epochFullID, state)
	}

	ALFP_GRABBING_MUTEX.Lock()
	proofsCount := len(cache.Proofs)
	ALFP_GRABBING_MUTEX.Unlock()

	if proofsCount >= majority {
		persistAggregatedLeaderFinalizationProof(cache, epochHandler.Id, leaderPubKey)
	}
}

func ensureLeaderFinalizationCache(state *AlfpProcessMetadata, epochId int, leaderPubKey string) *LeaderFinalizationCache {

	key := fmt.Sprintf("%d:%s", epochId, leaderPubKey)

	ALFP_GRABBING_MUTEX.Lock()
	defer ALFP_GRABBING_MUTEX.Unlock()

	if cache, ok := state.Caches[key]; ok {
		return cache
	}

	cache := &LeaderFinalizationCache{
		SkipData: loadLeaderSkipData(epochId, leaderPubKey),
		Proofs:   make(map[string]string),
	}

	state.Caches[key] = cache

	return cache
}

func loadLeaderSkipData(epochId int, leaderPubKey string) structures.VotingStat {

	skipData := structures.NewLeaderVotingStatTemplate()
	key := []byte(fmt.Sprintf("%d:%s", epochId, leaderPubKey))

	if raw, err := databases.FINALIZATION_VOTING_STATS.Get(key, nil); err == nil {
		_ = json.Unmarshal(raw, &skipData)
	}

	return skipData
}

func handleLeaderFinalizationResponse(raw []byte, epochHandler *structures.EpochDataHandler, leaderPubKey, epochFullID string, state *AlfpProcessMetadata) {

	var statusHolder map[string]any

	if err := json.Unmarshal(raw, &statusHolder); err != nil {
		return
	}

	status, ok := statusHolder["status"].(string)
	if !ok {
		return
	}

	switch status {
	case "OK":
		var response websocket_pack.WsLeaderFinalizationProofResponseOk
		if json.Unmarshal(raw, &response) == nil {
			handleLeaderFinalizationOk(response, epochHandler, leaderPubKey, epochFullID, state)
		}
	case "UPGRADE":
		var response websocket_pack.WsLeaderFinalizationProofResponseUpgrade
		if json.Unmarshal(raw, &response) == nil {
			handleLeaderFinalizationUpgrade(response, epochHandler, leaderPubKey, state)
		}
	}
}

func handleLeaderFinalizationOk(response websocket_pack.WsLeaderFinalizationProofResponseOk, epochHandler *structures.EpochDataHandler, leaderPubKey, epochFullID string, state *AlfpProcessMetadata) {

	if response.ForLeaderPubkey != leaderPubKey {
		return
	}

	cache := ensureLeaderFinalizationCache(state, epochHandler.Id, leaderPubKey)

	ALFP_GRABBING_MUTEX.Lock()
	skipData := cache.SkipData
	ALFP_GRABBING_MUTEX.Unlock()

	dataToVerify := strings.Join([]string{"LEADER_FINALIZATION_PROOF", leaderPubKey, strconv.Itoa(skipData.Index), skipData.Hash, epochFullID}, ":")

	quorumMap := make(map[string]bool)
	for _, pk := range epochHandler.Quorum {
		quorumMap[strings.ToLower(pk)] = true
	}

	if cryptography.VerifySignature(dataToVerify, response.Voter, response.Sig) {
		lowered := strings.ToLower(response.Voter)
		ALFP_GRABBING_MUTEX.Lock()
		if quorumMap[lowered] {
			cache.Proofs[response.Voter] = response.Sig
		}
		ALFP_GRABBING_MUTEX.Unlock()
	}
}

func handleLeaderFinalizationUpgrade(response websocket_pack.WsLeaderFinalizationProofResponseUpgrade, epochHandler *structures.EpochDataHandler, leaderPubKey string, state *AlfpProcessMetadata) {

	if response.ForLeaderPubkey != leaderPubKey {
		return
	}

	if !validateUpgradePayload(response, epochHandler, leaderPubKey) {
		return
	}

	cache := ensureLeaderFinalizationCache(state, epochHandler.Id, leaderPubKey)

	ALFP_GRABBING_MUTEX.Lock()
	prevIndex := cache.SkipData.Index
	prevHash := cache.SkipData.Hash
	cache.SkipData = response.SkipData
	cache.Proofs = make(map[string]string)
	ALFP_GRABBING_MUTEX.Unlock()

	// Helpful debug log: shows how the cluster converges on skipData via UPGRADE responses.
	utils.LogWithTimeThrottled(
		fmt.Sprintf("alfp:upgrade:%d:%s:%d->%d", epochHandler.Id, leaderPubKey, prevIndex, response.SkipData.Index),
		2*time.Second,
		fmt.Sprintf(
			"ALFP: UPGRADE skipData for leader %s in epoch %d (%d/%s... -> %d/%s...)",
			leaderPubKey,
			epochHandler.Id,
			prevIndex,
			shortHash8(prevHash),
			response.SkipData.Index,
			shortHash8(response.SkipData.Hash),
		),
		utils.CYAN_COLOR,
	)
}

func validateUpgradePayload(response websocket_pack.WsLeaderFinalizationProofResponseUpgrade, epochHandler *structures.EpochDataHandler, leaderPubKey string) bool {

	if response.SkipData.Index >= 0 {

		parts := strings.Split(response.SkipData.Afp.BlockId, ":")

		if len(parts) != 3 {
			return false
		}

		indexFromId, err := strconv.Atoi(parts[2])
		if err != nil || indexFromId != response.SkipData.Index || parts[0] != strconv.Itoa(epochHandler.Id) || parts[1] != leaderPubKey {
			return false
		}

		if response.SkipData.Hash != response.SkipData.Afp.BlockHash {
			return false
		}

		if !utils.VerifyAggregatedFinalizationProof(&response.SkipData.Afp, epochHandler) {
			return false
		}
	}

	return true
}

func persistAggregatedLeaderFinalizationProof(cache *LeaderFinalizationCache, epochId int, leaderPubKey string) {

	ALFP_GRABBING_MUTEX.Lock()

	// Capture values for logging (log outside the mutex).
	proofsCount := len(cache.Proofs)
	skipIndex := cache.SkipData.Index
	skipHash := cache.SkipData.Hash

	proofsCopy := make(map[string]string, len(cache.Proofs))
	for voter, sig := range cache.Proofs {
		proofsCopy[voter] = sig
	}

	aggregated := structures.AggregatedLeaderFinalizationProof{
		EpochIndex: epochId,
		Leader:     leaderPubKey,
		VotingStat: structures.VotingStat{
			Index: cache.SkipData.Index,
			Hash:  cache.SkipData.Hash,
			Afp:   cache.SkipData.Afp,
		},
		Signatures: proofsCopy,
	}

	persistAggregatedLeaderFinalizationProofDirect(&aggregated)

	ALFP_GRABBING_MUTEX.Unlock()

	skipHashShort := skipHash
	if len(skipHashShort) > 8 {
		skipHashShort = skipHashShort[:8] + "..."
	}
	utils.LogWithTime(
		fmt.Sprintf(
			"ALFP collected & leader finalized (epoch=%d leader=%s proofs=%d index=%d hash=%s)",
			epochId,
			leaderPubKey,
			proofsCount,
			skipIndex,
			skipHashShort,
		),
		utils.DEEP_GREEN_COLOR,
	)

	markLeaderFinalizationBroadcast(cache)
	sendAggregatedLeaderFinalizationProofToAnchors(&aggregated)
	websocket_pack.SendAggregatedLeaderFinalizationProofToPoD(aggregated)
}

func persistAggregatedLeaderFinalizationProofDirect(aggregated *structures.AggregatedLeaderFinalizationProof) {
	if aggregated == nil {
		return
	}

	key := []byte(fmt.Sprintf("ALFP:%d:%s", aggregated.EpochIndex, aggregated.Leader))
	if value, err := json.Marshal(aggregated); err == nil {
		_ = databases.FINALIZATION_VOTING_STATS.Put(key, value, nil)
	}
}

func shouldBroadcastLeaderFinalization(cache *LeaderFinalizationCache) bool {

	ALFP_GRABBING_MUTEX.Lock()
	defer ALFP_GRABBING_MUTEX.Unlock()

	return time.Since(cache.LastBroadcasted) > time.Second
}

func markLeaderFinalizationBroadcast(cache *LeaderFinalizationCache) {

	ALFP_GRABBING_MUTEX.Lock()
	cache.LastBroadcasted = time.Now()
	ALFP_GRABBING_MUTEX.Unlock()
}

func sendAggregatedLeaderFinalizationProofToAnchors(aggregated *structures.AggregatedLeaderFinalizationProof) {

	if aggregated == nil {
		return
	}

	payload := structures.AcceptLeaderFinalizationProofRequest{
		LeaderFinalizations: []structures.AggregatedLeaderFinalizationProof{*aggregated},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for _, anchor := range globals.ANCHORS {

		go func(anchor structures.Anchor) {

			url := fmt.Sprintf("%s/accept_aggregated_leader_finalization_proof", strings.TrimRight(anchor.AnchorUrl, "/"))
			req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
			if err != nil {
				utils.LogWithTimeThrottled(
					fmt.Sprintf("alfp:anchors:req_build:%d:%s:%s", aggregated.EpochIndex, aggregated.Leader, anchor.AnchorUrl),
					5*time.Second,
					fmt.Sprintf("ALFP: failed to build anchors request (epoch=%d leader=%s anchor=%s): %v", aggregated.EpochIndex, aggregated.Leader, anchor.AnchorUrl, err),
					utils.YELLOW_COLOR,
				)
				return
			}

			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				utils.LogWithTimeThrottled(
					fmt.Sprintf("alfp:anchors:post_err:%d:%s:%s", aggregated.EpochIndex, aggregated.Leader, anchor.AnchorUrl),
					5*time.Second,
					fmt.Sprintf("ALFP: anchors POST failed (epoch=%d leader=%s anchor=%s): %v", aggregated.EpochIndex, aggregated.Leader, anchor.AnchorUrl, err),
					utils.YELLOW_COLOR,
				)
				return
			}
			defer resp.Body.Close()

			// Read small body for debugging (anchors returns {"accepted":N} or {"err":...}).
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			if resp.StatusCode != http.StatusOK {
				utils.LogWithTimeThrottled(
					fmt.Sprintf("alfp:anchors:post_bad_status:%d:%s:%s:%d", aggregated.EpochIndex, aggregated.Leader, anchor.AnchorUrl, resp.StatusCode),
					5*time.Second,
					fmt.Sprintf("ALFP: anchors POST bad status (epoch=%d leader=%s anchor=%s http=%d body=%s)", aggregated.EpochIndex, aggregated.Leader, anchor.AnchorUrl, resp.StatusCode, strings.TrimSpace(string(respBody))),
					utils.YELLOW_COLOR,
				)
				return
			}

			utils.LogWithTimeThrottled(
				fmt.Sprintf("alfp:anchors:post_ok:%d:%s:%s", aggregated.EpochIndex, aggregated.Leader, anchor.AnchorUrl),
				5*time.Second,
				fmt.Sprintf("ALFP: anchors POST ok (epoch=%d leader=%s anchor=%s http=%d body=%s)", aggregated.EpochIndex, aggregated.Leader, anchor.AnchorUrl, resp.StatusCode, strings.TrimSpace(string(respBody))),
				utils.GREEN_COLOR,
			)
		}(anchor)
	}
}

func requestLeaderFinalizationFromPoD(epochHandler *structures.EpochDataHandler, leaderPubKey string, cache *LeaderFinalizationCache) {

	ALFP_GRABBING_MUTEX.Lock()
	shouldRequest := time.Since(cache.LastPodFetch) > time.Second
	if shouldRequest {
		cache.LastPodFetch = time.Now()
	}
	ALFP_GRABBING_MUTEX.Unlock()

	if !shouldRequest {
		return
	}

	go func() {
		aggregated := websocket_pack.GetAggregatedLeaderFinalizationProofFromPoD(epochHandler.Id, leaderPubKey)
		if aggregated == nil {
			return
		}

		if !utils.VerifyAggregatedLeaderFinalizationProof(aggregated, epochHandler) {
			return
		}

		persistAggregatedLeaderFinalizationProofDirect(aggregated)
	}()
}

func shortHash8(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
