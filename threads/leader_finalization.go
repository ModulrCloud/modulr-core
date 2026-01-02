package threads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

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

type EpochRotationState struct {
	EpochId  int
	Snapshot *structures.EpochDataSnapshot
	Caches   map[string]*LeaderFinalizationCache
	WsConns  map[string]*websocket.Conn
	Waiter   *utils.QuorumWaiter
	Guards   *utils.WebsocketGuards
}

var (
	ALFP_GRABBING_MUTEX    = sync.Mutex{}
	ALFP_PROCESS_METADATA  *EpochRotationState
	PROCESSING_EPOCH_INDEX = -1
)

func LeaderFinalizationThread() {

	for {

		if PROCESSING_EPOCH_INDEX == -1 {
			PROCESSING_EPOCH_INDEX = loadFinalizationProgress()
		}

		snapshot := getOrLoadEpochSnapshot(PROCESSING_EPOCH_INDEX)

		if snapshot == nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		processingHandler := &snapshot.EpochDataHandler

		if !weAreInEpochQuorum(processingHandler) {
			currentEpoch := PROCESSING_EPOCH_INDEX
			PROCESSING_EPOCH_INDEX++
			persistFinalizationProgress(PROCESSING_EPOCH_INDEX)
			cleanupLeaderFinalizationState(currentEpoch)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		state := ensureLeaderFinalizationState(processingHandler)
		majority := utils.GetQuorumMajority(processingHandler)

		networkParams := snapshot.NetworkParameters

		for _, leaderIndex := range leadersReadyForAlfp(processingHandler, &networkParams) {
			tryCollectLeaderFinalizationProofs(processingHandler, leaderIndex, majority, state)
		}

		if allLeaderFinalizationProofsCollected(processingHandler) {
			currentEpoch := PROCESSING_EPOCH_INDEX
			PROCESSING_EPOCH_INDEX++
			persistFinalizationProgress(PROCESSING_EPOCH_INDEX)
			cleanupLeaderFinalizationState(currentEpoch)
		}

		time.Sleep(200 * time.Millisecond)
	}
}

func ensureLeaderFinalizationState(epochHandler *structures.EpochDataHandler) *EpochRotationState {

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
	state := &EpochRotationState{
		EpochId: epochHandler.Id,
		Caches:  make(map[string]*LeaderFinalizationCache),
		WsConns: make(map[string]*websocket.Conn),
		Waiter:  utils.NewQuorumWaiter(len(epochHandler.Quorum), guards),
		Guards:  guards,
	}

	utils.OpenWebsocketConnectionsWithQuorum(epochHandler.Quorum, state.WsConns, guards)
	ALFP_PROCESS_METADATA = state

	return state
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

		if leaderFinalizationConfirmedByAlignment(epochHandler.Id, epochHandler.LeadersSequence[idx]) {
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

	for _, leader := range epochHandler.LeadersSequence {
		if !leaderHasAlfp(epochHandler.Id, leader) {
			return false
		}

		if executionMetadataMatches(epochHandler.Id) && !leaderFinalizationConfirmedByAlignment(epochHandler.Id, leader) {
			return false
		}
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

func tryCollectLeaderFinalizationProofs(epochHandler *structures.EpochDataHandler, leaderIndex, majority int, state *EpochRotationState) {

	leaderPubKey := epochHandler.LeadersSequence[leaderIndex]
	epochFullID := epochHandler.Hash + "#" + strconv.Itoa(epochHandler.Id)

	if leaderFinalizationConfirmedByAlignment(epochHandler.Id, leaderPubKey) {
		return
	}

	cache := ensureLeaderFinalizationCache(state, epochHandler.Id, leaderPubKey)

	if leaderHasAlfp(epochHandler.Id, leaderPubKey) || state.Waiter == nil {
		if aggregated := loadAggregatedLeaderFinalizationProof(epochHandler.Id, leaderPubKey); aggregated != nil && shouldBroadcastLeaderFinalization(cache) {
			markLeaderFinalizationBroadcast(cache)
			sendAggregatedLeaderFinalizationProofToAnchors(aggregated)
		}
		return
	}

	requestLeaderFinalizationFromPoD(epochHandler, leaderPubKey, cache)

	request := websocket_pack.WsLeaderFinalizationProofRequest{
		Route:                   "get_leader_finalization_proof",
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

	responses, ok := state.Waiter.SendAndWait(ctx, message, epochHandler.Quorum, state.WsConns, majority)
	if !ok {
		return
	}

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

func ensureLeaderFinalizationCache(state *EpochRotationState, epochId int, leaderPubKey string) *LeaderFinalizationCache {

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

func handleLeaderFinalizationResponse(raw []byte, epochHandler *structures.EpochDataHandler, leaderPubKey, epochFullID string, state *EpochRotationState) {

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

func handleLeaderFinalizationOk(response websocket_pack.WsLeaderFinalizationProofResponseOk, epochHandler *structures.EpochDataHandler, leaderPubKey, epochFullID string, state *EpochRotationState) {

	if response.ForLeaderPubkey != leaderPubKey {
		return
	}

	cache := ensureLeaderFinalizationCache(state, epochHandler.Id, leaderPubKey)

	dataToVerify := strings.Join([]string{"LEADER_FINALIZATION_PROOF", leaderPubKey, strconv.Itoa(cache.SkipData.Index), cache.SkipData.Hash, epochFullID}, ":")

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

func handleLeaderFinalizationUpgrade(response websocket_pack.WsLeaderFinalizationProofResponseUpgrade, epochHandler *structures.EpochDataHandler, leaderPubKey string, state *EpochRotationState) {

	if response.ForLeaderPubkey != leaderPubKey {
		return
	}

	if !validateUpgradePayload(response, epochHandler, leaderPubKey) {
		return
	}

	cache := ensureLeaderFinalizationCache(state, epochHandler.Id, leaderPubKey)

	ALFP_GRABBING_MUTEX.Lock()
	cache.SkipData = response.SkipData
	cache.Proofs = make(map[string]string)
	ALFP_GRABBING_MUTEX.Unlock()
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

			req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/accept_aggregated_leader_finalization_proof", anchor.AnchorUrl), bytes.NewReader(body))
			if err != nil {
				return
			}

			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				return
			}

			resp.Body.Close()
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
