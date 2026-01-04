package utils

import (
	"encoding/json"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"

	"github.com/syndtr/goleveldb/leveldb"
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

	if val, ok := handlers.EXECUTION_THREAD_METADATA.Handler.AccountsCache[accountId]; ok && val != nil {
		TouchExecAccountCache(accountId)
		MarkExecAccountTouched(accountId, val)
		return val
	}

	data, err := databases.STATE.Get([]byte(accountId), nil)

	if err == leveldb.ErrNotFound {

		acc := &structures.Account{}
		PutExecAccountCache(accountId, acc)
		MarkExecAccountTouched(accountId, acc)
		return acc

	}

	if err == nil {

		var account structures.Account

		parseErr := json.Unmarshal(data, &account)

		if parseErr == nil {

			acc := &account
			PutExecAccountCache(accountId, acc)
			MarkExecAccountTouched(accountId, acc)
			return acc

		}

	}

	return nil

}

func GetValidatorFromApprovementThreadState(validatorPubkey string) *structures.ValidatorStorage {
	validatorStorageKey := constants.DBKeyPrefixValidatorStorage + validatorPubkey

	// Fast path: cache hit under RLock (read-only).
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsTouched[validatorStorageKey]; ok && val != nil {
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
		return val
	}
	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[validatorStorageKey]; ok {
		handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
		return val
	}
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	// Cache miss: fetch from DB without holding RWMutex to avoid lock-upgrade deadlocks
	// when callers already hold RLock elsewhere.
	data, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(validatorStorageKey), nil)
	if err != nil {
		return nil
	}

	var validatorStorage structures.ValidatorStorage
	if err := json.Unmarshal(data, &validatorStorage); err != nil {
		return nil
	}

	// Store in cache under write lock (double-check to avoid overwriting on races).
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Lock()
	defer handlers.APPROVEMENT_THREAD_METADATA.RWMutex.Unlock()

	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.ValidatorsTouched[validatorStorageKey]; ok && val != nil {
		return val
	}
	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[validatorStorageKey]; ok {
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

	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[validatorStorageKey]; ok && val != nil {
		TouchApprovementValidatorCache(validatorStorageKey)
		MarkApprovementValidatorTouched(validatorStorageKey, val)
		return val
	}

	data, err := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(validatorStorageKey), nil)

	if err != nil {
		return nil
	}

	var validatorStorage structures.ValidatorStorage

	err = json.Unmarshal(data, &validatorStorage)

	if err != nil {
		return nil
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

	if val, ok := handlers.EXECUTION_THREAD_METADATA.Handler.ValidatorsStoragesCache[validatorStorageKey]; ok && val != nil {
		TouchExecValidatorCache(validatorStorageKey)
		MarkExecValidatorTouched(validatorStorageKey, val)
		return val
	}

	data, err := databases.STATE.Get([]byte(validatorStorageKey), nil)

	if err != nil {
		return nil
	}

	var validatorStorage structures.ValidatorStorage

	err = json.Unmarshal(data, &validatorStorage)

	if err != nil {
		return nil
	}

	vs := &validatorStorage
	PutExecValidatorCache(validatorStorageKey, vs)
	MarkExecValidatorTouched(validatorStorageKey, vs)
	return vs

}
