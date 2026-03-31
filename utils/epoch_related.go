package utils

import (
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

	hashOfMetadataFromEpoch := Blake3(newEpochSeed)

	pubkeys := make([]string, 0, len(epochHandler.ValidatorsRegistry))
	stakes := make([]uint64, 0, len(epochHandler.ValidatorsRegistry))
	var totalStakeSum uint64

	for _, validatorPubKey := range epochHandler.ValidatorsRegistry {
		validatorData := GetValidatorFromApprovementThreadState(validatorPubKey)
		if validatorData == nil {
			continue
		}
		pubkeys = append(pubkeys, validatorPubKey)
		stakes = append(stakes, validatorData.TotalStaked)
		totalStakeSum += validatorData.TotalStaked
	}

	if totalStakeSum == 0 {
		return []string{}
	}

	tree := NewStakeFenwickTree(stakes)
	quorum := make([]string, 0, quorumSize)

	for i := 0; i < quorumSize && totalStakeSum > 0; i++ {
		hashHex := Blake3(hashOfMetadataFromEpoch + "_" + strconv.Itoa(i))
		r := hashHexToUint64(hashHex) % totalStakeSum

		idx := tree.FindByWeight(r)
		quorum = append(quorum, pubkeys[idx])
		totalStakeSum -= stakes[idx]
		tree.Remove(idx, stakes[idx])
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

	hashOfMetadataFromEpoch := Blake3(newEpochSeed)

	pubkeys := make([]string, 0, len(epochHandler.ValidatorsRegistry))
	stakes := make([]uint64, 0, len(epochHandler.ValidatorsRegistry))
	var totalStakeSum uint64

	for _, validatorPubKey := range epochHandler.ValidatorsRegistry {
		validatorData := GetValidatorFromApprovementThreadStateUnderLock(validatorPubKey)
		if validatorData == nil {
			continue
		}
		pubkeys = append(pubkeys, validatorPubKey)
		stakes = append(stakes, validatorData.TotalStaked)
		totalStakeSum += validatorData.TotalStaked
	}

	if totalStakeSum == 0 {
		return []string{}
	}

	tree := NewStakeFenwickTree(stakes)
	quorum := make([]string, 0, quorumSize)

	for i := 0; i < quorumSize && totalStakeSum > 0; i++ {
		hashHex := Blake3(hashOfMetadataFromEpoch + "_" + strconv.Itoa(i))
		r := hashHexToUint64(hashHex) % totalStakeSum

		idx := tree.FindByWeight(r)
		quorum = append(quorum, pubkeys[idx])
		totalStakeSum -= stakes[idx]
		tree.Remove(idx, stakes[idx])
	}

	return quorum
}

func SetLeadersSequence(epochHandler *structures.EpochDataHandler, epochSeed string) {
	hashOfMetadataFromOldEpoch := Blake3(epochSeed)

	pubkeys := make([]string, 0, len(epochHandler.ValidatorsRegistry))
	stakes := make([]uint64, 0, len(epochHandler.ValidatorsRegistry))
	var totalStakeSum uint64

	for _, validatorPubKey := range epochHandler.ValidatorsRegistry {
		validatorData := GetValidatorFromApprovementThreadState(validatorPubKey)
		if validatorData == nil {
			continue
		}
		pubkeys = append(pubkeys, validatorPubKey)
		stakes = append(stakes, validatorData.TotalStaked)
		totalStakeSum += validatorData.TotalStaked
	}

	tree := NewStakeFenwickTree(stakes)
	epochHandler.LeadersSequence = make([]string, 0, len(pubkeys))

	for i := 0; i < len(pubkeys) && totalStakeSum > 0; i++ {
		hashHex := Blake3(hashOfMetadataFromOldEpoch + "_" + strconv.Itoa(i))
		r := hashHexToUint64(hashHex) % totalStakeSum

		idx := tree.FindByWeight(r)
		epochHandler.LeadersSequence = append(epochHandler.LeadersSequence, pubkeys[idx])
		totalStakeSum -= stakes[idx]
		tree.Remove(idx, stakes[idx])
	}
}

// SetLeadersSequenceUnderLock is the same as SetLeadersSequence, but must be called when
// handlers.APPROVEMENT_THREAD_METADATA.RWMutex is already held in write mode (Lock).
// It avoids self-deadlocks by using GetValidatorFromApprovementThreadStateUnderLock.
func SetLeadersSequenceUnderLock(epochHandler *structures.EpochDataHandler, epochSeed string) {
	hashOfMetadataFromOldEpoch := Blake3(epochSeed)

	pubkeys := make([]string, 0, len(epochHandler.ValidatorsRegistry))
	stakes := make([]uint64, 0, len(epochHandler.ValidatorsRegistry))
	var totalStakeSum uint64

	for _, validatorPubKey := range epochHandler.ValidatorsRegistry {
		validatorData := GetValidatorFromApprovementThreadStateUnderLock(validatorPubKey)
		if validatorData == nil {
			continue
		}
		pubkeys = append(pubkeys, validatorPubKey)
		stakes = append(stakes, validatorData.TotalStaked)
		totalStakeSum += validatorData.TotalStaked
	}

	tree := NewStakeFenwickTree(stakes)
	epochHandler.LeadersSequence = make([]string, 0, len(pubkeys))

	for i := 0; i < len(pubkeys) && totalStakeSum > 0; i++ {
		hashHex := Blake3(hashOfMetadataFromOldEpoch + "_" + strconv.Itoa(i))
		r := hashHexToUint64(hashHex) % totalStakeSum

		idx := tree.FindByWeight(r)
		epochHandler.LeadersSequence = append(epochHandler.LeadersSequence, pubkeys[idx])
		totalStakeSum -= stakes[idx]
		tree.Remove(idx, stakes[idx])
	}
}
