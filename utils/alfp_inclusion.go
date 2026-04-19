package utils

import (
	"fmt"

	"github.com/modulrcloud/modulr-core/databases"
)

func alfpIncludedKey(epochId int, leader string) []byte {
	return []byte(fmt.Sprintf("ALFP_INCLUDED:%d:%s", epochId, leader))
}

func StoreAlfpIncluded(epochId int, leader string) {
	if leader == "" {
		return
	}

	_ = databases.FINALIZATION_THREAD_METADATA.Put(alfpIncludedKey(epochId, leader), []byte{1}, nil)
}

func HasAnyAlfpIncluded(epochId int, leader string) bool {
	if leader == "" {
		return false
	}

	_, err := databases.FINALIZATION_THREAD_METADATA.Get(alfpIncludedKey(epochId, leader), nil)
	return err == nil
}
