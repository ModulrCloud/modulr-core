package utils

import (
	"encoding/json"
	"fmt"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/cryptography"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
)

func OpenDb(dbName string) *leveldb.DB {
	db, err := leveldb.OpenFile(globals.CHAINDATA_PATH+"/DATABASES/"+dbName, nil)
	if err != nil {
		panic("Impossible to open db : " + dbName + " =>" + err.Error())
	}
	return db
}

func GetAccountFromExecThreadState(accountId string) *structures.Account {
	// If account was already touched/modified in this block, return the in-memory object.
	if val, ok := handlers.EXECUTION_THREAD_METADATA.AccountsTouched[accountId]; ok && val != nil {
		TouchExecAccountCache(accountId)
		return val
	}

	if val, ok := handlers.EXECUTION_THREAD_METADATA.AccountsCache[accountId]; ok && val != nil {
		TouchExecAccountCache(accountId)
		MarkExecAccountTouched(accountId, val)
		return val
	}

	data, err := databases.STATE.Get([]byte(accountId), nil)

	if err != nil && err != leveldb.ErrNotFound {
		panic("STATE.Get failed for account " + accountId + ": " + err.Error())
	}

	if err == leveldb.ErrNotFound {
		acc := &structures.Account{}
		PutExecAccountCache(accountId, acc)
		MarkExecAccountTouched(accountId, acc)
		if handlers.EXECUTION_THREAD_METADATA.ChainCursor.Statistics != nil {
			handlers.EXECUTION_THREAD_METADATA.ChainCursor.Statistics.AccountsNumber++
		}
		if handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochStatistics != nil {
			handlers.EXECUTION_THREAD_METADATA.ChainCursor.EpochStatistics.AccountsNumber++
		}
		return acc
	}

	var account structures.Account

	if err := json.Unmarshal(data, &account); err != nil {
		panic("Failed to unmarshal account " + accountId + ": " + err.Error())
	}

	acc := &account
	PutExecAccountCache(accountId, acc)
	MarkExecAccountTouched(accountId, acc)
	return acc
}

func CountStateAccounts() uint64 {
	it := databases.STATE.NewIterator(nil, nil)
	defer it.Release()

	var count uint64
	for it.Next() {
		key := string(it.Key())
		if cryptography.IsValidPubKey(key) {
			count++
		}
	}

	return count
}

func SumTotalStakedFromState() uint64 {
	it := databases.STATE.NewIterator(util.BytesPrefix([]byte(constants.DBKeyPrefixValidatorStorage)), nil)
	defer it.Release()

	var total uint64
	for it.Next() {
		var storage structures.ValidatorStorage
		if err := json.Unmarshal(it.Value(), &storage); err != nil {
			continue
		}
		total += storage.TotalStaked
	}

	return total
}

func GetValidatorFromApprovementThreadState(validatorPubkey string) *structures.ValidatorStorage {
	validatorStorageKey := constants.DBKeyPrefixValidatorStorage + validatorPubkey

	// Fast path: cache hit under RLock (read-only).
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsTouched[validatorStorageKey]; ok && val != nil {
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
		return val
	}
	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache[validatorStorageKey]; ok {
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
		return val
	}
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	// Cache miss: fetch from DB without holding RWMutex to avoid lock-upgrade deadlocks
	// when callers already hold RLock elsewhere.
	data, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(validatorStorageKey), nil)

	if err != nil && err != leveldb.ErrNotFound {
		panic("APPROVEMENT_THREAD_METADATA.Get failed for validator " + validatorPubkey + ": " + err.Error())
	}

	if err == leveldb.ErrNotFound {
		return nil
	}

	var validatorStorage structures.ValidatorStorage
	if err := json.Unmarshal(data, &validatorStorage); err != nil {
		panic("Failed to unmarshal validator " + validatorPubkey + " from APPROVEMENT_THREAD_METADATA: " + err.Error())
	}

	// Store in cache under write lock (double-check to avoid overwriting on races).
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Lock()
	defer handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Unlock()

	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsTouched[validatorStorageKey]; ok && val != nil {
		return val
	}
	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache[validatorStorageKey]; ok {
		return val
	}

	vs := &validatorStorage
	PutApprovementValidatorCache(validatorStorageKey, vs)
	return vs
}

// GetValidatorFromApprovementThreadStateUnderLock reads/writes the AT validators cache.
// Caller MUST already hold handlers.APPROVEMENT_THREAD_METADATA.RWMutex in write mode (Lock).
func GetValidatorFromApprovementThreadStateUnderLock(validatorPubkey string) *structures.ValidatorStorage {
	validatorStorageKey := constants.DBKeyPrefixValidatorStorage + validatorPubkey

	// Prefer touched value if already accessed/modified in the current AT batch.
	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsTouched[validatorStorageKey]; ok && val != nil {
		TouchApprovementValidatorCache(validatorStorageKey)
		return val
	}

	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsStoragesCache[validatorStorageKey]; ok && val != nil {
		TouchApprovementValidatorCache(validatorStorageKey)
		MarkApprovementValidatorTouched(validatorStorageKey, val)
		return val
	}

	data, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(validatorStorageKey), nil)

	if err != nil && err != leveldb.ErrNotFound {
		panic("APPROVEMENT_THREAD_METADATA.Get failed for validator " + validatorPubkey + ": " + err.Error())
	}

	if err == leveldb.ErrNotFound {
		return nil
	}

	var validatorStorage structures.ValidatorStorage

	if err := json.Unmarshal(data, &validatorStorage); err != nil {
		panic("Failed to unmarshal validator " + validatorPubkey + " from APPROVEMENT_THREAD_METADATA: " + err.Error())
	}

	vs := &validatorStorage
	PutApprovementValidatorCache(validatorStorageKey, vs)
	MarkApprovementValidatorTouched(validatorStorageKey, vs)
	return vs
}

func GetValidatorFromExecThreadState(validatorPubkey string) *structures.ValidatorStorage {
	validatorStorageKey := constants.DBKeyPrefixValidatorStorage + validatorPubkey

	// Prefer touched value if already accessed/modified in the current block/batch.
	if val, ok := handlers.EXECUTION_THREAD_METADATA.ValidatorsTouched[validatorStorageKey]; ok && val != nil {
		TouchExecValidatorCache(validatorStorageKey)
		return val
	}

	if val, ok := handlers.EXECUTION_THREAD_METADATA.ValidatorsStoragesCache[validatorStorageKey]; ok && val != nil {
		TouchExecValidatorCache(validatorStorageKey)
		MarkExecValidatorTouched(validatorStorageKey, val)
		return val
	}

	data, err := databases.STATE.Get([]byte(validatorStorageKey), nil)

	if err != nil && err != leveldb.ErrNotFound {
		panic("STATE.Get failed for validator " + validatorPubkey + ": " + err.Error())
	}

	if err == leveldb.ErrNotFound {
		return nil
	}

	var validatorStorage structures.ValidatorStorage

	if err := json.Unmarshal(data, &validatorStorage); err != nil {
		panic("Failed to unmarshal validator " + validatorPubkey + " from STATE: " + err.Error())
	}

	vs := &validatorStorage
	PutExecValidatorCache(validatorStorageKey, vs)
	MarkExecValidatorTouched(validatorStorageKey, vs)
	return vs
}

// LoadAggregatedHeightProofInfo reads an AggregatedHeightProof from FINALIZATION_THREAD_METADATA
// and returns a summary suitable for the recovery API.
func LoadAggregatedHeightProofInfo(absoluteHeight int) *structures.AggregatedHeightProofInfo {
	key := []byte(fmt.Sprintf("%s%d", constants.DBKeyPrefixAggregatedHeightProof, absoluteHeight))

	raw, err := databases.FINALIZATION_THREAD_METADATA.Get(key, nil)
	if err != nil {
		return nil
	}

	var proof structures.AggregatedHeightProof
	if json.Unmarshal(raw, &proof) != nil {
		return nil
	}

	return &structures.AggregatedHeightProofInfo{
		AbsoluteHeight: proof.AbsoluteHeight,
		BlockId:        proof.BlockId,
		BlockHash:      proof.BlockHash,
		EpochId:        proof.EpochId,
	}
}

func StoreAlfpIncluded(epochId int, leader string) {
	if leader == "" {
		return
	}

	key := []byte(fmt.Sprintf("ALFP_INCLUDED:%d:%s", epochId, leader))

	_ = databases.FINALIZATION_THREAD_METADATA.Put(key, []byte{1}, nil)
}

func HasAnyAlfpIncluded(epochId int, leader string) bool {
	if leader == "" {
		return false
	}

	key := []byte(fmt.Sprintf("ALFP_INCLUDED:%d:%s", epochId, leader))

	_, err := databases.FINALIZATION_THREAD_METADATA.Get(key, nil)
	return err == nil
}
