package system_contracts

import (
	"slices"
	"strconv"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/utils"
)

type DelayedTxExecutorFunction = func(map[string]string, string) bool

var DELAYED_TRANSACTIONS_MAP = map[string]DelayedTxExecutorFunction{
	"createValidator": CreateValidator,
	"updateValidator": UpdateValidator,
	"stake":           Stake,
	"unstake":         Unstake,
}

func CreateValidator(delayedTransaction map[string]string, context string) bool {

	validatorPubkey := delayedTransaction["creator"]
	percentage := utils.StrToUint8(delayedTransaction["percentage"])
	validatorURL := delayedTransaction["validatorURL"]
	wssValidatorURL := delayedTransaction["wssValidatorURL"]

	if validatorURL != "" && wssValidatorURL != "" && percentage <= 100 {

		validatorStorageKey := constants.DBKeyPrefixValidatorStorage + validatorPubkey

		if context == constants.ContextApprovementThread {

			if _, existsInCache := handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[validatorStorageKey]; existsInCache {

				return false

			}

			_, existErr := databases.APPROVEMENT_THREAD_METADATA.Get([]byte(validatorStorageKey), nil)

			// Activate this branch only in case we still don't have this validator in db

			if existErr != nil {

				vs := &structures.ValidatorStorage{
					Pubkey:      validatorPubkey,
					Percentage:  percentage,
					TotalStaked: 0,
					Stakers: map[string]uint64{
						validatorPubkey: 0,
					},
					ValidatorUrl:    validatorURL,
					WssValidatorUrl: wssValidatorURL,
				}

				utils.PutApprovementValidatorCache(validatorStorageKey, vs)
				utils.MarkApprovementValidatorTouched(validatorStorageKey, vs)

				return true

			}

			return false

		}

		if context == constants.ContextExecutionThread {

			if _, existsInCache := handlers.EXECUTION_THREAD_METADATA.Handler.ValidatorsStoragesCache[validatorStorageKey]; existsInCache {

				return false

			}

			_, existErr := databases.STATE.Get([]byte(validatorStorageKey), nil)

			if existErr != nil {

				vs := &structures.ValidatorStorage{
					Pubkey:      validatorPubkey,
					Percentage:  percentage,
					TotalStaked: 0,
					Stakers: map[string]uint64{
						validatorPubkey: 0,
					},
					ValidatorUrl:    validatorURL,
					WssValidatorUrl: wssValidatorURL,
				}

				utils.PutExecValidatorCache(validatorStorageKey, vs)
				utils.MarkExecValidatorTouched(validatorStorageKey, vs)

				return true

			}

			return false

		}

		// Unknown context tag -> don't touch state.
		return false

	}

	return false
}

func UpdateValidator(delayedTransaction map[string]string, context string) bool {

	validatorPubkey := delayedTransaction["creator"]
	percentage := utils.StrToUint8(delayedTransaction["percentage"])
	validatorURL := delayedTransaction["validatorURL"]
	wssValidatorURL := delayedTransaction["wssValidatorURL"]

	if validatorURL != "" && wssValidatorURL != "" && percentage <= 100 {

		validatorStorageId := constants.DBKeyPrefixValidatorStorage + validatorPubkey

		if context == constants.ContextApprovementThread {

			validatorStorage := utils.GetValidatorFromApprovementThreadStateUnderLock(validatorPubkey)

			if validatorStorage != nil {

				validatorStorage.Percentage = percentage

				validatorStorage.ValidatorUrl = validatorURL

				validatorStorage.WssValidatorUrl = wssValidatorURL

				utils.MarkApprovementValidatorTouched(validatorStorageId, validatorStorage)
				utils.TouchApprovementValidatorCache(validatorStorageId)

				return true

			}

			return false

		}

		if context == constants.ContextExecutionThread {

			validatorStorage := utils.GetValidatorFromExecThreadState(validatorPubkey)

			if validatorStorage != nil {

				validatorStorage.Percentage = percentage

				validatorStorage.ValidatorUrl = validatorURL

				validatorStorage.WssValidatorUrl = wssValidatorURL

				utils.MarkExecValidatorTouched(validatorStorageId, validatorStorage)
				utils.TouchExecValidatorCache(validatorStorageId)

				return true

			}

			return false

		}

		// Unknown context tag -> don't touch state.
		return false

	}

	return false

}

func Stake(delayedTransaction map[string]string, context string) bool {

	staker := delayedTransaction["staker"]
	validatorPubkey := delayedTransaction["validatorPubKey"]
	amount, err := strconv.ParseUint(delayedTransaction["amount"], 10, 64)

	if err != nil {

		return false

	}

	if context == constants.ContextApprovementThread {

		validatorStorage := utils.GetValidatorFromApprovementThreadStateUnderLock(validatorPubkey)

		if validatorStorage != nil {

			minStake := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.MinimalStakePerStaker

			if amount < minStake {

				return false

			}

			currentStake := validatorStorage.Stakers[staker]

			currentStake += amount

			validatorStorage.TotalStaked += amount

			validatorStorage.Stakers[staker] = currentStake

			requiredStake := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.ValidatorRequiredStake

			if validatorStorage.TotalStaked >= requiredStake {

				if !slices.Contains(handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry, validatorPubkey) {

					handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry = append(
						handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry, validatorPubkey,
					)

				}

			}

			return true

		}

		return false

	}

	if context == constants.ContextExecutionThread {

		validatorStorage := utils.GetValidatorFromExecThreadState(validatorPubkey)

		if validatorStorage != nil {

			minStake := handlers.EXECUTION_THREAD_METADATA.Handler.NetworkParameters.MinimalStakePerStaker

			if amount < minStake {

				return false

			}

			currentStake := validatorStorage.Stakers[staker]

			currentStake += amount

			validatorStorage.Stakers[staker] = currentStake

			validatorStorage.TotalStaked += amount
			handlers.EXECUTION_THREAD_METADATA.Handler.Statistics.StakingDelta += int64(amount)
			handlers.EXECUTION_THREAD_METADATA.Handler.EpochStatistics.StakingDelta += int64(amount)

			requiredStake := handlers.EXECUTION_THREAD_METADATA.Handler.NetworkParameters.ValidatorRequiredStake

			if validatorStorage.TotalStaked >= requiredStake {

				if !slices.Contains(handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry, validatorPubkey) {

					handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry = append(
						handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry, validatorPubkey,
					)

				}

			}

			return true

		}

		return false

	}

	// Unknown context tag -> don't touch state.
	return false

}

func Unstake(delayedTransaction map[string]string, context string) bool {

	unstaker := delayedTransaction["unstaker"]
	validatorPubkey := delayedTransaction["validatorPubKey"]
	amount, err := strconv.ParseUint(delayedTransaction["amount"], 10, 64)

	if err != nil {

		return false

	}

	if context == constants.ContextApprovementThread {

		validatorStorage := utils.GetValidatorFromApprovementThreadStateUnderLock(validatorPubkey)

		if validatorStorage != nil {

			stakerStake, exists := validatorStorage.Stakers[unstaker]

			if !exists {

				return false

			}

			if stakerStake < amount {

				return false

			}

			stakerStake -= amount

			validatorStorage.TotalStaked -= amount
			handlers.EXECUTION_THREAD_METADATA.Handler.Statistics.StakingDelta -= int64(amount)
			handlers.EXECUTION_THREAD_METADATA.Handler.EpochStatistics.StakingDelta -= int64(amount)

			if stakerStake == 0 {

				delete(validatorStorage.Stakers, unstaker) // no sense to store staker with 0 balance in stakers list

			} else {

				validatorStorage.Stakers[unstaker] = stakerStake

			}

			requiredStake := handlers.APPROVEMENT_THREAD_METADATA.Handler.NetworkParameters.ValidatorRequiredStake

			if validatorStorage.TotalStaked < requiredStake {

				reg := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry

				handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry = removeFromSlice(reg, validatorPubkey)

			}

			return true

		}

		return false

	}

	if context == constants.ContextExecutionThread {

		validatorStorage := utils.GetValidatorFromExecThreadState(validatorPubkey)

		if validatorStorage != nil {

			stakerStake, exists := validatorStorage.Stakers[unstaker]

			if !exists {

				return false

			}

			if stakerStake < amount {

				return false

			}

			stakerStake -= amount

			validatorStorage.TotalStaked -= amount

			if stakerStake == 0 {

				delete(validatorStorage.Stakers, unstaker) // no sense to store staker with 0 balance in stakers list

			} else {

				validatorStorage.Stakers[unstaker] = stakerStake

			}

			requiredStake := handlers.EXECUTION_THREAD_METADATA.Handler.NetworkParameters.ValidatorRequiredStake

			if validatorStorage.TotalStaked < requiredStake {

				reg := handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry

				handlers.EXECUTION_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry = removeFromSlice(reg, validatorPubkey)

			}

			return true

		}

		return false

	}

	// Unknown context tag -> don't touch state.
	return false

}

func removeFromSlice[T comparable](s []T, v T) []T {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}
