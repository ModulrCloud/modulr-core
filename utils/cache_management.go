package utils

import (
	"container/list"

	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
)

// ---- Touched sets (write-back sets) ----

func ResetExecTouchedSets() {
	clear(handlers.EXECUTION_THREAD_METADATA.AccountsTouched)
	clear(handlers.EXECUTION_THREAD_METADATA.ValidatorsTouched)
}

func ResetApprovementTouchedSets() {
	clear(handlers.APPROVEMENT_THREAD_METADATA.ValidatorsTouched)
}

func MarkExecAccountTouched(accountID string, account *structures.Account) {
	handlers.EXECUTION_THREAD_METADATA.AccountsTouched[accountID] = account
}

func MarkExecValidatorTouched(storageKey string, vs *structures.ValidatorStorage) {
	handlers.EXECUTION_THREAD_METADATA.ValidatorsTouched[storageKey] = vs
}

func MarkApprovementValidatorTouched(storageKey string, vs *structures.ValidatorStorage) {
	handlers.APPROVEMENT_THREAD_METADATA.ValidatorsTouched[storageKey] = vs
}

// ---- LRU helpers (bounded caches) ----

type lruState struct {
	lru   *list.List
	index map[string]*list.Element
}

func touch(state lruState, key string) {
	if state.lru == nil {
		return
	}
	if el, ok := state.index[key]; ok && el != nil {
		state.lru.MoveToFront(el)
		return
	}
	state.index[key] = state.lru.PushFront(key)
}

func remove(state lruState, key string) {
	if state.lru == nil {
		return
	}
	if el, ok := state.index[key]; ok && el != nil {
		state.lru.Remove(el)
	}
	delete(state.index, key)
}

// evictIfNeeded removes least-recently-used entries from cache until len(cache) <= cap.
// It prefers NOT to evict keys that are currently touched (pending write-back).
func evictIfNeeded[V any](cache map[string]V, touched map[string]V, cap int, state lruState) {
	if cap <= 0 || state.lru == nil {
		return
	}
	for len(cache) > cap {
		back := state.lru.Back()
		if back == nil {
			break
		}
		key, _ := back.Value.(string)
		if key == "" {
			state.lru.Remove(back)
			continue
		}
		if _, isTouched := touched[key]; isTouched {
			// Don't evict touched keys; keep them hot to reduce churn and scan further.
			state.lru.MoveToFront(back)
			// If everything is touched, we can't evict anything safely.
			if len(touched) >= len(cache) {
				break
			}
			continue
		}
		// Evict.
		delete(cache, key)
		remove(state, key)
	}
}

// ---- Exec-thread cache touch/evict ----

func TouchExecAccountCache(accountID string) {
	touch(lruState{lru: handlers.EXECUTION_THREAD_METADATA.AccountsLRU, index: handlers.EXECUTION_THREAD_METADATA.AccountsLRUIndex}, accountID)
	evictIfNeeded(handlers.EXECUTION_THREAD_METADATA.Handler.AccountsCache, handlers.EXECUTION_THREAD_METADATA.AccountsTouched, handlers.EXECUTION_THREAD_METADATA.AccountsCacheMax,
		lruState{lru: handlers.EXECUTION_THREAD_METADATA.AccountsLRU, index: handlers.EXECUTION_THREAD_METADATA.AccountsLRUIndex},
	)
}

func TouchExecValidatorCache(storageKey string) {
	touch(lruState{lru: handlers.EXECUTION_THREAD_METADATA.ValidatorsLRU, index: handlers.EXECUTION_THREAD_METADATA.ValidatorsLRUIndex}, storageKey)
	evictIfNeeded(handlers.EXECUTION_THREAD_METADATA.Handler.ValidatorsStoragesCache, handlers.EXECUTION_THREAD_METADATA.ValidatorsTouched, handlers.EXECUTION_THREAD_METADATA.ValidatorsCacheMax,
		lruState{lru: handlers.EXECUTION_THREAD_METADATA.ValidatorsLRU, index: handlers.EXECUTION_THREAD_METADATA.ValidatorsLRUIndex},
	)
}

func PutExecAccountCache(accountID string, account *structures.Account) {
	handlers.EXECUTION_THREAD_METADATA.Handler.AccountsCache[accountID] = account
	TouchExecAccountCache(accountID)
}

func PutExecValidatorCache(storageKey string, vs *structures.ValidatorStorage) {
	handlers.EXECUTION_THREAD_METADATA.Handler.ValidatorsStoragesCache[storageKey] = vs
	TouchExecValidatorCache(storageKey)
}

// ---- Approvement-thread validator cache touch/evict ----

func TouchApprovementValidatorCache(storageKey string) {
	touch(lruState{lru: handlers.APPROVEMENT_THREAD_METADATA.ValidatorsLRU, index: handlers.APPROVEMENT_THREAD_METADATA.ValidatorsLRUIndex}, storageKey)
	evictIfNeeded(handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache, handlers.APPROVEMENT_THREAD_METADATA.ValidatorsTouched, handlers.APPROVEMENT_THREAD_METADATA.ValidatorsCacheMax,
		lruState{lru: handlers.APPROVEMENT_THREAD_METADATA.ValidatorsLRU, index: handlers.APPROVEMENT_THREAD_METADATA.ValidatorsLRUIndex},
	)
}

func PutApprovementValidatorCache(storageKey string, vs *structures.ValidatorStorage) {
	handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[storageKey] = vs
	TouchApprovementValidatorCache(storageKey)
}
