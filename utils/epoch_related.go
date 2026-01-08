package utils

import (
	"encoding/hex"
	"strconv"

	"github.com/modulrcloud/modulr-core/globals"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
)

type CurrentLeaderData struct {
	IsMeLeader bool
	Url        string
}

type ValidatorData struct {
	ValidatorPubKey string
	TotalStake      uint64
}

func GetCurrentLeader() CurrentLeaderData {

	// Snapshot leader data under RLock, then release lock before calling validator getter.
	// This avoids a potential lock-upgrade deadlock, because GetValidatorFromApprovementThreadState
	// may need a write lock to populate the cache on a miss.
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RLock()
	currentLeaderIndex := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.CurrentLeaderIndex
	currentLeaderPubKey := handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.LeadersSequence[currentLeaderIndex]
	handlers.APPROVEMENT_THREAD_METADATA.RWMutex.RUnlock()

	if currentLeaderPubKey != globals.CONFIGURATION.PublicKey {

		validatorStorage := GetValidatorFromApprovementThreadState(currentLeaderPubKey)

		if validatorStorage != nil {

			return CurrentLeaderData{IsMeLeader: false, Url: validatorStorage.ValidatorUrl}

		}

		return CurrentLeaderData{IsMeLeader: false, Url: ""}

	}

	return CurrentLeaderData{IsMeLeader: true, Url: ""}

}

func GetQuorumMajority(epochHandler *structures.EpochDataHandler) int {

	quorumSize := len(epochHandler.Quorum)

	majority := (2 * quorumSize) / 3

	majority += 1

	if majority > quorumSize {
		return quorumSize
	}

	return majority
}

func GetAnchorsQuorumMajority() int {

	quorumSize := len(globals.ANCHORS)

	majority := (2 * quorumSize) / 3

	majority += 1

	if majority > quorumSize {
		return quorumSize
	}

	return majority
}

func GetQuorumUrlsAndPubkeys(epochHandler *structures.EpochDataHandler) []structures.QuorumMemberData {

	var toReturn []structures.QuorumMemberData

	for _, pubKey := range epochHandler.Quorum {

		validatorStorage := GetValidatorFromApprovementThreadState(pubKey)

		toReturn = append(toReturn, structures.QuorumMemberData{PubKey: pubKey, Url: validatorStorage.ValidatorUrl})

	}

	return toReturn

}

func GetCurrentEpochQuorum(epochHandler *structures.EpochDataHandler, quorumSize int, newEpochSeed string) []string {

	totalNumberOfValidators := len(epochHandler.ValidatorsRegistry)

	if totalNumberOfValidators <= quorumSize {

		futureQuorum := make([]string, len(epochHandler.ValidatorsRegistry))

		copy(futureQuorum, epochHandler.ValidatorsRegistry)

		return futureQuorum
	}

	quorum := []string{}

	// Blake3 hash of epoch metadata (hex string)
	hashOfMetadataFromEpoch := Blake3(newEpochSeed)

	// Collect validator data and total stake (uint64)
	validatorsExtendedData := make([]ValidatorData, 0, len(epochHandler.ValidatorsRegistry))

	var totalStakeSum uint64 = 0

	for _, validatorPubKey := range epochHandler.ValidatorsRegistry {

		validatorData := GetValidatorFromApprovementThreadState(validatorPubKey)

		// If validator storage is missing, skip it (shouldn't happen in normal operation).
		if validatorData == nil {
			continue
		}

		totalStakeByThisValidator := validatorData.TotalStaked // uint64

		totalStakeSum += totalStakeByThisValidator

		validatorsExtendedData = append(validatorsExtendedData, ValidatorData{
			ValidatorPubKey: validatorPubKey,
			TotalStake:      totalStakeByThisValidator,
		})
	}

	// If total stake is zero, no weighted choice is possible

	if totalStakeSum == 0 {
		return quorum
	}

	// Draw 'quorumSize' validators without replacement
	for i := 0; i < quorumSize && len(validatorsExtendedData) > 0; i++ {

		// Deterministic "random": Blake3(hash || "_" || i) -> uint64
		hashInput := hashOfMetadataFromEpoch + "_" + strconv.Itoa(i)
		hashHex := Blake3(hashInput) // hex string

		// Take the first 8 bytes (16 hex chars) -> uint64 BigEndian
		var r uint64 = 0

		if len(hashHex) >= 16 {
			if b, err := hex.DecodeString(hashHex[:16]); err == nil {
				for _, by := range b {
					r = (r << 8) | uint64(by)
				}
			}
		}

		// Reduce into [0, totalStakeSum-1]
		if totalStakeSum > 0 {
			r = r % totalStakeSum
		} else {
			r = 0
		}

		// Iterate over current validators and pick the one that hits the interval
		var cumulativeSum uint64 = 0

		for idx, validator := range validatorsExtendedData {

			cumulativeSum += validator.TotalStake

			// Preserve original logic: choose when r <= cumulativeSum
			if r < cumulativeSum {
				// Add chosen validator
				quorum = append(quorum, validator.ValidatorPubKey)

				// Update total stake and remove chosen one (draw without replacement)
				if validator.TotalStake <= totalStakeSum {
					totalStakeSum -= validator.TotalStake
				} else {
					totalStakeSum = 0
				}
				validatorsExtendedData = append(validatorsExtendedData[:idx], validatorsExtendedData[idx+1:]...)
				break
			}

		}

		// If total stake became zero, no further weighted draws are possible
		if totalStakeSum == 0 || len(validatorsExtendedData) == 0 {
			break
		}
	}

	return quorum
}

// GetCurrentEpochQuorumUnderLock is the same as GetCurrentEpochQuorum, but must be called when
// handlers.APPROVEMENT_THREAD_METADATA.RWMutex is already held in write mode (Lock).
// It avoids self-deadlocks by using GetValidatorFromApprovementThreadStateUnderLock.
func GetCurrentEpochQuorumUnderLock(epochHandler *structures.EpochDataHandler, quorumSize int, newEpochSeed string) []string {

	totalNumberOfValidators := len(epochHandler.ValidatorsRegistry)

	if totalNumberOfValidators <= quorumSize {

		futureQuorum := make([]string, len(epochHandler.ValidatorsRegistry))

		copy(futureQuorum, epochHandler.ValidatorsRegistry)

		return futureQuorum
	}

	quorum := []string{}

	// Blake3 hash of epoch metadata (hex string)
	hashOfMetadataFromEpoch := Blake3(newEpochSeed)

	// Collect validator data and total stake (uint64)
	validatorsExtendedData := make([]ValidatorData, 0, len(epochHandler.ValidatorsRegistry))

	var totalStakeSum uint64 = 0

	for _, validatorPubKey := range epochHandler.ValidatorsRegistry {

		validatorData := GetValidatorFromApprovementThreadStateUnderLock(validatorPubKey)
		if validatorData == nil {
			continue
		}

		totalStakeByThisValidator := validatorData.TotalStaked // uint64

		totalStakeSum += totalStakeByThisValidator

		validatorsExtendedData = append(validatorsExtendedData, ValidatorData{
			ValidatorPubKey: validatorPubKey,
			TotalStake:      totalStakeByThisValidator,
		})
	}

	// If total stake is zero, no weighted choice is possible
	if totalStakeSum == 0 {
		return quorum
	}

	// Draw 'quorumSize' validators without replacement
	for i := 0; i < quorumSize && len(validatorsExtendedData) > 0; i++ {

		// Deterministic "random": Blake3(hash || "_" || i) -> uint64
		hashInput := hashOfMetadataFromEpoch + "_" + strconv.Itoa(i)
		hashHex := Blake3(hashInput) // hex string

		// Take the first 8 bytes (16 hex chars) -> uint64 BigEndian
		var r uint64 = 0

		if len(hashHex) >= 16 {
			if b, err := hex.DecodeString(hashHex[:16]); err == nil {
				for _, by := range b {
					r = (r << 8) | uint64(by)
				}
			}
		}

		// Reduce into [0, totalStakeSum-1]
		if totalStakeSum > 0 {
			r = r % totalStakeSum
		} else {
			r = 0
		}

		// Iterate over current validators and pick the one that hits the interval
		var cumulativeSum uint64 = 0

		for idx, validator := range validatorsExtendedData {

			cumulativeSum += validator.TotalStake

			// Preserve original logic: choose when r <= cumulativeSum
			if r < cumulativeSum {
				// Add chosen validator
				quorum = append(quorum, validator.ValidatorPubKey)

				// Update total stake and remove chosen one (draw without replacement)
				if validator.TotalStake <= totalStakeSum {
					totalStakeSum -= validator.TotalStake
				} else {
					totalStakeSum = 0
				}
				validatorsExtendedData = append(validatorsExtendedData[:idx], validatorsExtendedData[idx+1:]...)
				break
			}

		}

		// If total stake became zero, no further weighted draws are possible
		if totalStakeSum == 0 || len(validatorsExtendedData) == 0 {
			break
		}
	}

	return quorum
}

func SetLeadersSequence(epochHandler *structures.EpochDataHandler, epochSeed string) {

	epochHandler.LeadersSequence = []string{} // [pool0, pool1,...poolN]

	// Hash of metadata from the old epoch
	hashOfMetadataFromOldEpoch := Blake3(epochSeed)

	// Change order of validators pseudo-randomly
	validatorsExtendedData := make([]ValidatorData, 0, len(epochHandler.ValidatorsRegistry))

	var totalStakeSum uint64 = 0

	// Populate validator data and calculate total stake sum
	for _, validatorPubKey := range epochHandler.ValidatorsRegistry {

		validatorData := GetValidatorFromApprovementThreadState(validatorPubKey)
		if validatorData == nil {
			continue
		}

		// Calculate total stake
		totalStakeByThisValidator := validatorData.TotalStaked

		totalStakeSum += totalStakeByThisValidator

		validatorsExtendedData = append(validatorsExtendedData, ValidatorData{
			ValidatorPubKey: validatorPubKey,
			TotalStake:      totalStakeByThisValidator,
		})
	}

	// Iterate over the validatorsRegistry and pseudo-randomly choose leaders
	for i := 0; i < len(epochHandler.ValidatorsRegistry); i++ {

		cumulativeSum := uint64(0)

		// Generate deterministic random value using the hash of metadata
		hashInput := hashOfMetadataFromOldEpoch + "_" + strconv.Itoa(i)
		hashHex := Blake3(hashInput)
		var deterministicRandomValue uint64
		if len(hashHex) >= 16 {
			if b, err := hex.DecodeString(hashHex[:16]); err == nil {
				for _, by := range b {
					deterministicRandomValue = (deterministicRandomValue << 8) | uint64(by)
				}
			}
		}
		if totalStakeSum > 0 {
			deterministicRandomValue = deterministicRandomValue % totalStakeSum
		}

		// Find the validator based on the random value
		for idx, validator := range validatorsExtendedData {

			cumulativeSum += validator.TotalStake

			if deterministicRandomValue < cumulativeSum {

				// Add the chosen validator to the leaders sequence
				epochHandler.LeadersSequence = append(epochHandler.LeadersSequence, validator.ValidatorPubKey)

				// Update totalStakeSum and remove the chosen validator from the list

				if validator.TotalStake <= totalStakeSum {
					totalStakeSum -= validator.TotalStake
				} else {
					totalStakeSum = 0
				}

				validatorsExtendedData = append(validatorsExtendedData[:idx], validatorsExtendedData[idx+1:]...)

				break
			}
		}
	}
}

// SetLeadersSequenceUnderLock is the same as SetLeadersSequence, but must be called when
// handlers.APPROVEMENT_THREAD_METADATA.RWMutex is already held in write mode (Lock).
// It avoids self-deadlocks by using GetValidatorFromApprovementThreadStateUnderLock.
func SetLeadersSequenceUnderLock(epochHandler *structures.EpochDataHandler, epochSeed string) {

	epochHandler.LeadersSequence = []string{} // [pool0, pool1,...poolN]

	// Hash of metadata from the old epoch
	hashOfMetadataFromOldEpoch := Blake3(epochSeed)

	// Change order of validators pseudo-randomly
	validatorsExtendedData := make([]ValidatorData, 0, len(epochHandler.ValidatorsRegistry))

	var totalStakeSum uint64 = 0

	// Populate validator data and calculate total stake sum
	for _, validatorPubKey := range epochHandler.ValidatorsRegistry {

		validatorData := GetValidatorFromApprovementThreadStateUnderLock(validatorPubKey)
		if validatorData == nil {
			continue
		}

		// Calculate total stake
		totalStakeByThisValidator := validatorData.TotalStaked

		totalStakeSum += totalStakeByThisValidator

		validatorsExtendedData = append(validatorsExtendedData, ValidatorData{
			ValidatorPubKey: validatorPubKey,
			TotalStake:      totalStakeByThisValidator,
		})
	}

	// Iterate over the validatorsRegistry and pseudo-randomly choose leaders
	for i := 0; i < len(epochHandler.ValidatorsRegistry); i++ {

		cumulativeSum := uint64(0)

		// Generate deterministic random value using the hash of metadata
		hashInput := hashOfMetadataFromOldEpoch + "_" + strconv.Itoa(i)
		hashHex := Blake3(hashInput)
		var deterministicRandomValue uint64
		if len(hashHex) >= 16 {
			if b, err := hex.DecodeString(hashHex[:16]); err == nil {
				for _, by := range b {
					deterministicRandomValue = (deterministicRandomValue << 8) | uint64(by)
				}
			}
		}
		if totalStakeSum > 0 {
			deterministicRandomValue = deterministicRandomValue % totalStakeSum
		}

		// Find the validator based on the random value
		for idx, validator := range validatorsExtendedData {

			cumulativeSum += validator.TotalStake

			if deterministicRandomValue < cumulativeSum {

				// Add the chosen validator to the leaders sequence
				epochHandler.LeadersSequence = append(epochHandler.LeadersSequence, validator.ValidatorPubKey)

				// Update totalStakeSum and remove the chosen validator from the list
				if validator.TotalStake <= totalStakeSum {
					totalStakeSum -= validator.TotalStake
				} else {
					totalStakeSum = 0
				}

				validatorsExtendedData = append(validatorsExtendedData[:idx], validatorsExtendedData[idx+1:]...)

				break
			}
		}
	}
}
