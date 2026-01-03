package utils

import (
	"encoding/json"

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

	if val, ok := handlers.EXECUTION_THREAD_METADATA.Handler.AccountsCache[accountId]; ok {
		return val
	}

	data, err := databases.STATE.Get([]byte(accountId), nil)

	if err == leveldb.ErrNotFound {

		handlers.EXECUTION_THREAD_METADATA.Handler.AccountsCache[accountId] = &structures.Account{}

		return handlers.EXECUTION_THREAD_METADATA.Handler.AccountsCache[accountId]

	}

	if err == nil {

		var account structures.Account

		parseErr := json.Unmarshal(data, &account)

		if parseErr == nil {

			handlers.EXECUTION_THREAD_METADATA.Handler.AccountsCache[accountId] = &account

			return handlers.EXECUTION_THREAD_METADATA.Handler.AccountsCache[accountId]

		}

	}

	return nil

}

func GetValidatorFromApprovementThreadState(validatorPubkey string) *structures.ValidatorStorage {
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	defer handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()
	return GetValidatorFromApprovementThreadStateUnderLock(validatorPubkey)
}

// GetValidatorFromApprovementThreadStateUnderLock reads/writes the AT validators cache.
// Caller MUST already hold handlers.APPROVEMENT_THREAD_METADATA.RWMutex (RLock or Lock).
func GetValidatorFromApprovementThreadStateUnderLock(validatorPubkey string) *structures.ValidatorStorage {

	validatorStorageKey := validatorPubkey + "_VALIDATOR_STORAGE"

	if val, ok := handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[validatorStorageKey]; ok {
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

	handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[validatorStorageKey] = &validatorStorage

	return &validatorStorage

}

func GetValidatorFromExecThreadState(validatorPubkey string) *structures.ValidatorStorage {

	validatorStorageKey := validatorPubkey + "_VALIDATOR_STORAGE"

	if val, ok := handlers.EXECUTION_THREAD_METADATA.Handler.ValidatorsStoragesCache[validatorStorageKey]; ok {
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

	handlers.EXECUTION_THREAD_METADATA.Handler.ValidatorsStoragesCache[validatorStorageKey] = &validatorStorage

	return &validatorStorage

}
