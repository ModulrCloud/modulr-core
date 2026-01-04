package constants

// Logical thread contexts (used by system contracts / delayed tx execution).
const (
	ContextApprovementThread = "APPROVEMENT_THREAD"
	ContextExecutionThread   = "EXECUTION_THREAD"
)

// Websocket routes (PoD + node-to-node).
const (
	WsRouteGetFinalizationProof                 = "get_finalization_proof"
	WsRouteGetLeaderFinalizationProof           = "get_leader_finalization_proof"
	WsRouteGetBlockWithAfp                      = "get_block_with_afp"
	WsRouteGetAnchorBlockWithAfp                = "get_anchor_block_with_afp"
	WsRouteAcceptBlockWithAfp                   = "accept_block_with_afp"
	WsRouteAcceptAggregatedLeaderFinalization   = "accept_aggregated_leader_finalization_proof"
	WsRouteGetAggregatedLeaderFinalizationProof = "get_aggregated_leader_finalization_proof"
)

// Common DB key fragments/prefixes.
const (
	DBKeyPrefixEpochFinish      = "EPOCH_FINISH:"
	DBKeyPrefixTxReceipt        = "TX:"
	DBKeyPrefixValidatorStorage = "VALIDATOR_STORAGE:"
)
