package constants

// Logical thread contexts (used by system contracts / delayed tx execution).
const (
	ContextApprovementThread = "APPROVEMENT_THREAD"
	ContextExecutionThread   = "EXECUTION_THREAD"
)

// Nonce-check bypass (temporary compatibility for high-rate faucet usage).
// When many faucet requests arrive concurrently, nonce races cause otherwise valid tx to fail.
// For these specific sender addresses we intentionally disable nonce validation during execution.
var NonceCheckBypassFromAddresses = map[string]struct{}{
	"58mwkdnD1jejKhRQFrSgSwNbU4ajebSYqPvzVxfbDCHT": {},
	"ALkfNAViKa13c9KT7Zo6GUJ8ccq8Liy5aqpZwNQb1rL6": {},
}

func ShouldBypassNonceCheck(from string) bool {
	_, ok := NonceCheckBypassFromAddresses[from]
	return ok
}

// Websocket routes (PoD + node-to-node).
const (
	WsRouteGetFinalizationProof                   = "get_finalization_proof"
	WsRouteGetLeaderFinalizationProof             = "get_leader_finalization_proof"
	WsRouteGetBlockWithAfp                        = "get_block_with_afp"
	WsRouteGetAnchorBlockWithAfp                  = "get_anchor_block_with_afp"
	WsRouteAcceptBlockWithAfp                     = "accept_block_with_afp"
	WsRouteAcceptAggregatedLeaderFinalization     = "accept_aggregated_leader_finalization_proof"
	WsRouteGetAggregatedLeaderFinalizationProof   = "get_aggregated_leader_finalization_proof"
	WsRouteSignHeightProof                        = "sign_height_proof"
	WsRouteSignEpochRotationProof                 = "sign_epoch_rotation_proof"
	WsRouteAcceptAggregatedHeightProof            = "accept_aggregated_height_proof"
	WsRouteGetAggregatedHeightProofFromPoD        = "get_aggregated_height_proof_from_pod"
	WsRouteAcceptAggregatedEpochRotationProof     = "accept_aggregated_epoch_rotation_proof"
	WsRouteGetAggregatedEpochRotationProofFromPoD = "get_aggregated_epoch_rotation_proof_from_pod"
	WsRouteGetBlockByHeight                       = "get_block_by_height"
	WsRouteAcceptAggregatedAnchorEpochAckProof    = "accept_aggregated_anchor_epoch_ack_proof"
	WsRouteGetAnchorEpochAckFromPoD               = "get_anchor_epoch_ack_proof"
)

// Common DB key fragments/prefixes.
const (
	DBKeyExecutionThreadMetadata   = "EXECUTION_THREAD_METADATA"
	DBKeyApprovementThreadMetadata = "APPROVEMENT_THREAD_METADATA"
	DBKeyGenerationThreadMetadata  = "GENERATION_THREAD_METADATA"
	DBKeyLatestBatchIndex          = "LATEST_BATCH_INDEX"

	DBKeyPrefixBlockIndex                   = "BLOCK_INDEX:"
	DBKeyPrefixEpochData                    = "EPOCH_DATA:"
	DBKeyPrefixEpochHandler                 = "EPOCH_HANDLER:"
	DBKeyPrefixEpochFinish                  = "EPOCH_FINISH:"
	DBKeyPrefixEpochStats                   = "EPOCH_STATS:"
	DBKeyPrefixTxReceipt                    = "TX:"
	DBKeyPrefixValidatorStorage             = "VALIDATOR_STORAGE:"
	DBKeyPrefixPodOutbox                    = "POD_OUTBOX:"
	DBKeyPrefixDelayedTransactions          = "DELAYED_TRANSACTIONS:"
	DBKeyPrefixFirstBlockData               = "FIRST_BLOCK_DATA:"
	DBKeyPrefixAggregatedHeightProof        = "HEIGHT_PROOF:"
	DBKeyPrefixLastMileHeightMap            = "LAST_MILE_HEIGHT_MAP:"
	DBKeyPrefixAfp                          = "AFP:"
	DBKeyPrefixAggregatedEpochRotationProof = "EPOCH_ROTATION_PROOF:"

	DBKeyPrefixFirstBlockAggregatedHeightProof = "FIRST_BLOCK_AGGREGATED_HEIGHT_PROOF:"
	DBKeyPrefixAggregatedAnchorEpochAckProof   = "ANCHOR_EPOCH_ACK_PROOF:"
	DBKeyPrefixAlfp                            = "ALFP:"

	DBKeyAlfpProgress             = "ALFP_PROGRESS"
	DBKeyLastMileFinalizerTracker = "LAST_MILE_FINALIZER_TRACKER"
)

// Signing prefixes (used as salts for cryptographic signatures).
const (
	SigningPrefixEpochRotationProof  = "EPOCH_ROTATION_PROOF"
	SigningPrefixDelayedOperations   = "SIG_DELAYED_OPERATIONS:"
	SigningPrefixHeightProof         = "HEIGHT_PROOF"
	SigningPrefixLeaderFinalization  = "LEADER_FINALIZATION_PROOF"
	SigningPrefixAnchorEpochAckProof = "ANCHOR_EPOCH_ACK_PROOF"
)

// Default in-memory cache limits (bounded caches to avoid unbounded growth).
// These are intentionally conservative; adjust after profiling on real testnet load.
const (
	DefaultAccountsCacheMax   = 50_000
	DefaultValidatorsCacheMax = 5_000
)

const ZeroHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// Native currency precision:
// 1 coin = 10^9 smallest units.
const (
	NativeDecimals uint8  = 9
	NativeScale    uint64 = 1_000_000_000
)
