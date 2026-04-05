package utils

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"

	"github.com/syndtr/goleveldb/leveldb/util"
)

type PodStatusResponse struct {
	Status string `json:"status"`
}

func isPodAck(resp []byte) bool {
	var s PodStatusResponse
	if json.Unmarshal(resp, &s) != nil {
		return false
	}
	return strings.EqualFold(s.Status, "OK")
}

func podOutboxKey(id string) []byte {
	return []byte(constants.DBKeyPrefixPodOutbox + id)
}

// SendToPoDWithOutbox sends a message to PoD and requires an OK ack.
// On failure, it persists the message into FINALIZATION_VOTING_STATS for retry.
func SendToPoDWithOutbox(id string, payload []byte) bool {
	if id == "" || len(payload) == 0 {
		return false
	}

	resp, err := SendWebsocketMessageToPoD(payload)
	if err == nil && isPodAck(resp) {
		_ = databases.FINALIZATION_VOTING_STATS.Delete(podOutboxKey(id), nil)
		return true
	}

	// Persist for retry.
	_ = databases.FINALIZATION_VOTING_STATS.Put(podOutboxKey(id), payload, nil)
	return false
}

// FlushPoDOutboxOnce retries up to limit pending PoD messages.
func FlushPoDOutboxOnce(limit int) int {
	if databases.FINALIZATION_VOTING_STATS == nil {
		return 0
	}
	if limit <= 0 {
		limit = 50
	}

	it := databases.FINALIZATION_VOTING_STATS.NewIterator(util.BytesPrefix([]byte(constants.DBKeyPrefixPodOutbox)), nil)
	defer it.Release()

	sent := 0
	for it.Next() {
		if sent >= limit {
			break
		}
		key := string(it.Key())
		if !strings.HasPrefix(key, constants.DBKeyPrefixPodOutbox) {
			continue
		}
		id := strings.TrimPrefix(key, constants.DBKeyPrefixPodOutbox)
		payload := append([]byte(nil), it.Value()...)
		if len(payload) == 0 {
			_ = databases.FINALIZATION_VOTING_STATS.Delete([]byte(key), nil)
			continue
		}
		if SendToPoDWithOutbox(id, payload) {
			sent++
		}
	}
	return sent
}

// Build stable outbox IDs.
func PoDOutboxIdForCoreBlock(epochIndex int, creator string, index int) string {
	return fmt.Sprintf("CORE_BLOCK:%d:%s:%d", epochIndex, creator, index)
}

func PoDOutboxIdForALFP(epochIndex int, leader string) string {
	return fmt.Sprintf("%s%d:%s", constants.DBKeyPrefixAlfp, epochIndex, leader)
}

func PoDOutboxIdForHeightAttestation(absoluteHeight int) string {
	return fmt.Sprintf(constants.DBKeyPrefixHeightAttestation+"%d", absoluteHeight)
}

func PoDOutboxIdForEpochDataAttestation(epochId int) string {
	return fmt.Sprintf(constants.DBKeyPrefixEpochDataAttestation+"%d", epochId)
}
