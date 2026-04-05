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
	Route                     string                        `json:"route"`
	AbsoluteHeight            int                           `json:"absoluteHeight"`
	BlockId                   string                        `json:"blockId"`
	BlockHash                 string                        `json:"blockHash"`
	EpochId                   int                           `json:"epochId"`
	HeightInEpoch             int                           `json:"heightInEpoch"`
	PreviousHeightAttestation *structures.HeightAttestation `json:"previousHeightAttestation,omitempty"`
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

// Epoch data attestation (quorum member signs next-epoch data hash)

type WsEpochDataAttestationRequest struct {
	Route         string `json:"route"`
	EpochId       int    `json:"epochId"`
	NextEpochId   int    `json:"nextEpochId"`
	EpochDataHash string `json:"epochDataHash"`
}

type WsEpochDataAttestationResponse struct {
	Voter string `json:"voter"`
	Sig   string `json:"sig"`
}

// PoD storage/retrieval for epoch data attestations

type WsEpochDataAttestationStoreRequest struct {
	Route       string                          `json:"route"`
	Attestation structures.EpochDataAttestation `json:"attestation"`
}

type WsEpochDataAttestationGetRequest struct {
	Route   string `json:"route"`
	EpochId int    `json:"epochId"`
}

type WsEpochDataAttestationGetResponse struct {
	Attestation *structures.EpochDataAttestation `json:"attestation"`
}

// Anchor epoch ack (quorum member receives proof that anchors acknowledged epoch transition)

type WsAcceptAnchorEpochAckRequest struct {
	Route string                         `json:"route"`
	Proof structures.AnchorEpochAckProof `json:"proof"`
}

// PoD storage/retrieval for anchor epoch ack proofs

type WsAnchorEpochAckStoreRequest struct {
	Route string                         `json:"route"`
	Proof structures.AnchorEpochAckProof `json:"proof"`
}

type WsAnchorEpochAckGetRequest struct {
	Route   string `json:"route"`
	EpochId int    `json:"epochId"`
}

type WsAnchorEpochAckGetResponse struct {
	Proof *structures.AnchorEpochAckProof `json:"proof"`
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
