package websocket_pack

import (
	"github.com/modulrcloud/modulr-core/anchors_pack"
	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/structures"
)

type WsLeaderFinalizationProofRequest struct {
	Route                   string                `json:"route"`
	EpochIndex              int                   `json:"epochIndex"`
	IndexOfLeaderToFinalize int                   `json:"indexOfLeaderToFinalize"`
	SkipData                structures.VotingStat `json:"skipData"`
}

type WsLeaderFinalizationProofResponseOk struct {
	Voter           string `json:"voter"`
	ForLeaderPubkey string `json:"forLeaderPubkey"`
	Status          string `json:"status"`
	Sig             string `json:"sig"`
}

type WsLeaderFinalizationProofResponseUpgrade struct {
	Voter           string                `json:"voter"`
	ForLeaderPubkey string                `json:"forLeaderPubkey"`
	Status          string                `json:"status"`
	SkipData        structures.VotingStat `json:"skipData"`
}

type WsFinalizationProofRequest struct {
	Route            string                                 `json:"route"`
	Block            block_pack.Block                       `json:"block"`
	PreviousBlockAfp structures.AggregatedFinalizationProof `json:"previousBlockAfp"`
}

type WsFinalizationProofResponse struct {
	Voter             string `json:"voter"`
	FinalizationProof string `json:"finalizationProof"`
	VotedForHash      string `json:"votedForHash"`
}

type WsBlockWithAfpRequest struct {
	Route   string `json:"route"`
	BlockId string `json:"blockID"`
}

type WsBlockWithAfpResponse struct {
	Block                 *block_pack.Block                       `json:"block"`
	Afp                   *structures.AggregatedFinalizationProof `json:"afp"`
	AggregatedHeightProof *structures.AggregatedHeightProof       `json:"aggregatedHeightProof,omitempty"`
}

type WsAnchorBlockWithAfpRequest struct {
	Route   string `json:"route"`
	BlockId string `json:"blockID"`
}

type WsAnchorBlockWithAfpResponse struct {
	Block *anchors_pack.AnchorBlock               `json:"block"`
	Afp   *structures.AggregatedFinalizationProof `json:"afp"`
}

type WsBlockWithAfpStoreRequest struct {
	Route string                                 `json:"route"`
	Block block_pack.Block                       `json:"block"`
	Afp   structures.AggregatedFinalizationProof `json:"afp"`
}

type WsAggregatedLeaderFinalizationProofStoreRequest struct {
	Route string                                       `json:"route"`
	Proof structures.AggregatedLeaderFinalizationProof `json:"proof"`
}

type WsAggregatedLeaderFinalizationProofRequest struct {
	Route      string `json:"route"`
	EpochIndex int    `json:"epochIndex"`
	Leader     string `json:"leader"`
}

type WsAggregatedLeaderFinalizationProofResponse struct {
	Proof *structures.AggregatedLeaderFinalizationProof `json:"proof"`
}

// WsHeightProofRequest is sent by the last-mile finalizer to request a single
// height-proof signature from a quorum member.
type WsHeightProofRequest struct {
	Route                         string                            `json:"route"`
	AbsoluteHeight                int                               `json:"absoluteHeight"`
	BlockId                       string                            `json:"blockId"`
	BlockHash                     string                            `json:"blockHash"`
	EpochId                       int                               `json:"epochId"`
	HeightInEpoch                 int                               `json:"heightInEpoch"`
	PreviousAggregatedHeightProof *structures.AggregatedHeightProof `json:"previousAggregatedHeightProof,omitempty"`
}

type WsHeightProofResponse struct {
	Voter string `json:"voter"`
	Sig   string `json:"sig"`
}

// PoD storage/retrieval for aggregated height proofs

type WsAggregatedHeightProofStoreRequest struct {
	Route string                           `json:"route"`
	Proof structures.AggregatedHeightProof `json:"proof"`
}

type WsAggregatedHeightProofGetRequest struct {
	Route          string `json:"route"`
	AbsoluteHeight int    `json:"absoluteHeight"`
}

type WsAggregatedHeightProofGetResponse struct {
	Proof *structures.AggregatedHeightProof `json:"proof"`
}

// WsEpochRotationProofRequest is sent by the last-mile finalizer to request a single
// epoch-rotation-proof signature from a quorum member.
type WsEpochRotationProofRequest struct {
	Route             string `json:"route"`
	EpochId           int    `json:"epochId"`
	NextEpochId       int    `json:"nextEpochId"`
	EpochDataHash     string `json:"epochDataHash"`
	FinishedOnHeight  int64  `json:"finishedOnHeight"`
	FinishedOnBlockId string `json:"finishedOnBlockId"`
	FinishedOnHash    string `json:"finishedOnHash"`
}

type WsEpochRotationProofResponse struct {
	Voter string `json:"voter"`
	Sig   string `json:"sig"`
}

// PoD storage/retrieval for aggregated epoch rotation proofs

type WsAggregatedEpochRotationProofStoreRequest struct {
	Route string                                  `json:"route"`
	Proof structures.AggregatedEpochRotationProof `json:"proof"`
}

type WsAggregatedEpochRotationProofGetRequest struct {
	Route   string `json:"route"`
	EpochId int    `json:"epochId"`
}

type WsAggregatedEpochRotationProofGetResponse struct {
	Proof *structures.AggregatedEpochRotationProof `json:"proof"`
}

// Anchor epoch ack (quorum member receives proof that anchors acknowledged epoch transition)

type WsAcceptAggregatedAnchorEpochAckProofRequest struct {
	Route string                                   `json:"route"`
	Proof structures.AggregatedAnchorEpochAckProof `json:"proof"`
}

// PoD storage/retrieval for aggregated anchor epoch ack proofs

type WsAggregatedAnchorEpochAckProofGetRequest struct {
	Route   string `json:"route"`
	EpochId int    `json:"epochId"`
}

type WsAggregatedAnchorEpochAckProofGetResponse struct {
	Proof *structures.AggregatedAnchorEpochAckProof `json:"proof"`
}

// Combined block + proof retrieval by absolute height

type WsBlockByHeightRequest struct {
	Route          string `json:"route"`
	AbsoluteHeight int    `json:"absoluteHeight"`
}

type WsBlockByHeightResponse struct {
	Block                 *block_pack.Block                 `json:"block"`
	AggregatedHeightProof *structures.AggregatedHeightProof `json:"aggregatedHeightProof"`
}
