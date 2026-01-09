package websocket_pack

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/modulrcloud/modulr-core/block_pack"
	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

func SendBlockAndAfpToPoD(block block_pack.Block, afp structures.AggregatedFinalizationProof) {
	req := WsBlockWithAfpStoreRequest{Route: constants.WsRouteAcceptBlockWithAfp, Block: block, Afp: afp}
	if reqBytes, err := json.Marshal(req); err == nil {
		if globals.CONFIGURATION.DisablePoDOutbox {
			_, _ = utils.SendWebsocketMessageToPoD(reqBytes)
			return
		}
		epochIndex := -1
		parts := strings.Split(block.Epoch, "#")
		if len(parts) > 0 {
			last := parts[len(parts)-1]
			if v, convErr := strconv.Atoi(last); convErr == nil {
				epochIndex = v
			}
		}
		_ = utils.SendToPoDWithOutbox(utils.PoDOutboxIdForCoreBlock(epochIndex, block.Creator, block.Index), reqBytes)
	}
}

func SendAggregatedLeaderFinalizationProofToPoD(proof structures.AggregatedLeaderFinalizationProof) {
	req := WsAggregatedLeaderFinalizationProofStoreRequest{Route: constants.WsRouteAcceptAggregatedLeaderFinalization, Proof: proof}
	if reqBytes, err := json.Marshal(req); err == nil {
		if globals.CONFIGURATION.DisablePoDOutbox {
			_, _ = utils.SendWebsocketMessageToPoD(reqBytes)
			return
		}
		_ = utils.SendToPoDWithOutbox(utils.PoDOutboxIdForALFP(proof.EpochIndex, proof.Leader), reqBytes)
	}
}

func GetAggregatedLeaderFinalizationProofFromPoD(epochIndex int, leader string) *structures.AggregatedLeaderFinalizationProof {
	req := WsAggregatedLeaderFinalizationProofRequest{Route: constants.WsRouteGetAggregatedLeaderFinalizationProof, EpochIndex: epochIndex, Leader: leader}
	if reqBytes, err := json.Marshal(req); err == nil {
		if respBytes, err := utils.SendWebsocketMessageToPoD(reqBytes); err == nil {
			var resp WsAggregatedLeaderFinalizationProofResponse
			if err := json.Unmarshal(respBytes, &resp); err == nil {
				return resp.Proof
			}
		}
	}
	return nil
}
