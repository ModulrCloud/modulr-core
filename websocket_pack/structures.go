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
	Block             *block_pack.Block                       `json:"block"`
	Afp               *structures.AggregatedFinalizationProof `json:"afp"`
	HeightAttestation *structures.HeightAttestation           `json:"heightAttestation,omitempty"`
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

// Height attestation (quorum member signs height->block mapping)

type WsHeightAttestationRequest struct {
	Route          string `json:"route"`
	AbsoluteHeight int    `json:"absoluteHeight"`
	BlockId        string `json:"blockId"`
	BlockHash      string `json:"blockHash"`
	EpochId        int    `json:"epochId"`
	HeightInEpoch  int    `json:"heightInEpoch"`
}

type WsHeightAttestationResponse struct {
	Voter string `json:"voter"`
	Sig   string `json:"sig"`
}

// PoD storage/retrieval for height attestations

type WsHeightAttestationStoreRequest struct {
	Route string                       `json:"route"`
	Proof structures.HeightAttestation `json:"proof"`
}

type WsHeightAttestationGetRequest struct {
	Route          string `json:"route"`
	AbsoluteHeight int    `json:"absoluteHeight"`
}

type WsHeightAttestationGetResponse struct {
	Proof *structures.HeightAttestation `json:"proof"`
}

// Quorum rotation (quorum member signs next-epoch quorum)

type WsQuorumRotationRequest struct {
	Route         string   `json:"route"`
	EpochId       int      `json:"epochId"`
	NextEpochId   int      `json:"nextEpochId"`
	NextEpochHash string   `json:"nextEpochHash"`
	NextQuorum    []string `json:"nextQuorum"`
}

type WsQuorumRotationResponse struct {
	Voter string `json:"voter"`
	Sig   string `json:"sig"`
}

// PoD storage/retrieval for quorum rotation attestations

type WsQuorumRotationAttestationStoreRequest struct {
	Route       string                               `json:"route"`
	Attestation structures.QuorumRotationAttestation `json:"attestation"`
}

type WsQuorumRotationAttestationGetRequest struct {
	Route   string `json:"route"`
	EpochId int    `json:"epochId"`
}

type WsQuorumRotationAttestationGetResponse struct {
	Attestation *structures.QuorumRotationAttestation `json:"attestation"`
}

// Combined block + attestation retrieval by absolute height

type WsBlockByHeightRequest struct {
	Route          string `json:"route"`
	AbsoluteHeight int    `json:"absoluteHeight"`
}

type WsBlockByHeightResponse struct {
	Block             *block_pack.Block             `json:"block"`
	HeightAttestation *structures.HeightAttestation `json:"heightAttestation"`
}
