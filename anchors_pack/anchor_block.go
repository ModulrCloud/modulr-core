package anchors_pack

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/utils"
)

type AnchorBlock struct {
	Creator   string           `json:"creator"`
	Time      int64            `json:"time"`
	Epoch     string           `json:"epoch"`
	ExtraData ExtraDataToBlock `json:"extraData"`
	Index     int              `json:"index"`
	PrevHash  string           `json:"prevHash"`
	Sig       string           `json:"sig"`
}

func (block *AnchorBlock) GetHash() string {
	jsonedExtraData, err := json.Marshal(block.ExtraData)

	if err != nil {
		panic("GetHash: failed to marshal extraData: " + err.Error())
	}

	dataToHash := strings.Join([]string{
		block.Creator,
		strconv.FormatInt(block.Time, 10),
		globals.GENESIS.NetworkId,
		block.Epoch,
		string(jsonedExtraData),
		strconv.Itoa(block.Index),
		block.PrevHash,
	}, ":")

	return utils.Blake3(dataToHash)
}

func (block *AnchorBlock) VerifySignature() bool {
	return cryptography.VerifySignature(block.GetHash(), block.Creator, block.Sig)
}
