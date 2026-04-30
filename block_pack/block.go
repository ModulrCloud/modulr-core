package block_pack

import (
	"context"
	"encoding/json"
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
)

type Block struct {
	Creator      string                   `json:"creator"`
	Time         int64                    `json:"time"`
	Epoch        string                   `json:"epoch"`
	Transactions []structures.Transaction `json:"transactions"`
	ExtraData    ExtraDataToBlock         `json:"extraData"`
	Index        int                      `json:"index"`
	PrevHash     string                   `json:"prevHash"`
	Sig          string                   `json:"sig"`
}

func NewBlock(transactions []structures.Transaction, extraData ExtraDataToBlock, epochFullID string) *Block {
	return &Block{
		Creator:      globals.CONFIGURATION.PublicKey,
		Time:         utils.GetUTCTimestampInMilliSeconds(),
		Epoch:        epochFullID,
		Transactions: transactions,
		ExtraData:    extraData,
		Index:        handlers.GENERATION_THREAD_METADATA.NextIndex,
		PrevHash:     handlers.GENERATION_THREAD_METADATA.PrevHash,
		Sig:          "",
	}
}

func (block *Block) GetHash() string {
	jsonedTransactions, err := json.Marshal(block.Transactions)

	if err != nil {
		panic("GetHash: failed to marshal transactions: " + err.Error())
	}

	jsonedExtraData, err := json.Marshal(block.ExtraData)

	if err != nil {
		panic("GetHash: failed to marshal extraData: " + err.Error())
	}

	dataToHash := strings.Join([]string{
		block.Creator,
		strconv.FormatInt(block.Time, 10),
		string(jsonedTransactions),
		string(jsonedExtraData),
		globals.GENESIS.NetworkId,
		block.Epoch,
		strconv.Itoa(block.Index),
		block.PrevHash,
	}, ":")

	return utils.Blake3(dataToHash)
}

func (block *Block) SignBlock() {
	block.Sig = cryptography.GenerateSignature(globals.CONFIGURATION.PrivateKey, block.GetHash())
}

func (block *Block) VerifySignature() bool {
	return cryptography.VerifySignature(block.GetHash(), block.Creator, block.Sig)
}

func GetBlock(epochIndex int, blockCreator string, index uint, epochHandler *structures.EpochDataHandler) *Block {

	blockID := strconv.Itoa(epochIndex) + ":" + blockCreator + ":" + strconv.Itoa(int(index))

	blockAsBytes, err := databases.BLOCKS.Get([]byte(blockID), nil)

	if err == nil {
		var blockParsed *Block

		err = json.Unmarshal(blockAsBytes, &blockParsed)

		if err == nil {
			return blockParsed
		}
	}

	// Find from other nodes

	quorumUrlsAndPubkeys := utils.GetQuorumUrlsAndPubkeys(epochHandler)

	var quorumUrls []string

	for _, quorumMember := range quorumUrlsAndPubkeys {
		quorumUrls = append(quorumUrls, quorumMember.Url)
	}

	allKnownNodes := append(quorumUrls, globals.CONFIGURATION.BootstrapNodes...)

	resultChan := make(chan *Block, len(allKnownNodes))
	var wg sync.WaitGroup

	for _, node := range allKnownNodes {
		if node == globals.CONFIGURATION.MyHostname {
			continue
		}

		wg.Add(1)
		go func(endpoint string) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			url := endpoint + "/block/" + blockID
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				return
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				return
			}
			defer resp.Body.Close()

			var block Block

			if err := json.NewDecoder(resp.Body).Decode(&block); err == nil {
				resultChan <- &block
			}
		}(node)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	for block := range resultChan {
		if block != nil {
			return block
		}
	}

	return nil
}
