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
	WsRouteGetFinalizationProof                 = "get_finalization_proof"
	WsRouteGetLeaderFinalizationProof           = "get_leader_finalization_proof"
	WsRouteGetBlockWithAfp                      = "get_block_with_afp"
	WsRouteGetAnchorBlockWithAfp                = "get_anchor_block_with_afp"
	WsRouteAcceptBlockWithAfp                   = "accept_block_with_afp"
	WsRouteAcceptAggregatedLeaderFinalization   = "accept_aggregated_leader_finalization_proof"
	WsRouteGetAggregatedLeaderFinalizationProof = "get_aggregated_leader_finalization_proof"
	WsRouteSignHeightAttestation                = "sign_height_attestation"
	WsRouteSignQuorumRotation                   = "sign_quorum_rotation"
	WsRouteAcceptHeightAttestation              = "accept_height_attestation"
	WsRouteGetHeightAttestationFromPoD          = "get_height_attestation_from_pod"
	WsRouteAcceptQuorumRotationAttestation      = "accept_quorum_rotation_attestation"
	WsRouteGetQuorumRotationAttestationFromPoD  = "get_quorum_rotation_attestation_from_pod"
)

// Common DB key fragments/prefixes.
const (
	DBKeyPrefixEpochFinish      = "EPOCH_FINISH:"
	DBKeyPrefixTxReceipt        = "TX:"
	DBKeyPrefixValidatorStorage = "VALIDATOR_STORAGE:"
	DBKeyPrefixPodOutbox        = "POD_OUTBOX:"
)

// Default in-memory cache limits (bounded caches to avoid unbounded growth).
// These are intentionally conservative; adjust after profiling on real testnet load.
const (
	DefaultAccountsCacheMax   = 50_000
	DefaultValidatorsCacheMax = 5_000
)

// Native currency precision:
// 1 coin = 10^9 smallest units.
const (
	NativeDecimals uint8  = 9
	NativeScale    uint64 = 1_000_000_000
)
