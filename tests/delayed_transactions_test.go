package tests

import (
	_ "github.com/modulrcloud/modulr-core/tests/testenv"

	"path/filepath"
	"testing"

	"github.com/modulrcloud/modulr-core/constants"
	"github.com/modulrcloud/modulr-core/databases"
	"github.com/modulrcloud/modulr-core/handlers"
	"github.com/modulrcloud/modulr-core/structures"
	"github.com/modulrcloud/modulr-core/system_contracts"

	"github.com/syndtr/goleveldb/leveldb"
)

func setupApprovementHandler(network structures.NetworkParameters) {
	handlers.APPROVEMENT_THREAD_METADATA.Handler = structures.ApprovementThreadMetadataHandler{
		NetworkParameters:       network,
		EpochDataHandler:        structures.EpochDataHandler{},
		ValidatorsStoragesCache: make(map[string]*structures.ValidatorStorage),
	}
}

func openTempDB(t *testing.T) *leveldb.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "db")
	db, err := leveldb.OpenFile(dbPath, nil)
	if err != nil {
		t.Fatalf("failed to open temp db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return db
}

func TestCreateValidatorAddsNewValidatorToApprovementCache(t *testing.T) {
	setupApprovementHandler(structures.NetworkParameters{})
	databases.APPROVEMENT_THREAD_METADATA = openTempDB(t)

	delayedTx := map[string]string{
		"creator":         "validator1",
		"percentage":      "75",
		"validatorURL":    "http://validator",
		"wssValidatorURL": "ws://validator",
	}

	if !system_contracts.CreateValidator(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected CreateValidator to succeed")
	}

	key := constants.DBKeyPrefixValidatorStorage + "validator1"
	stored := handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[key]
	if stored == nil {
		t.Fatalf("expected validator to be stored in cache")
	}
	if stored.Pubkey != "validator1" || stored.Percentage != 75 || stored.ValidatorUrl != "http://validator" || stored.WssValidatorUrl != "ws://validator" {
		t.Fatalf("unexpected validator data in cache: %+v", stored)
	}
}

func TestUpdateValidatorUpdatesExistingValidatorInApprovementCache(t *testing.T) {
	setupApprovementHandler(structures.NetworkParameters{})
	existing := &structures.ValidatorStorage{
		Pubkey:          "validator1",
		Percentage:      50,
		TotalStaked:     0,
		Stakers:         map[string]uint64{"validator1": 0},
		ValidatorUrl:    "old",
		WssValidatorUrl: "old-wss",
	}
	key := constants.DBKeyPrefixValidatorStorage + "validator1"
	handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[key] = existing

	delayedTx := map[string]string{
		"creator":         "validator1",
		"percentage":      "65",
		"validatorURL":    "http://new",
		"wssValidatorURL": "ws://new",
	}

	if !system_contracts.UpdateValidator(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected UpdateValidator to succeed")
	}

	updated := handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[key]
	if updated.Percentage != 65 || updated.ValidatorUrl != "http://new" || updated.WssValidatorUrl != "ws://new" {
		t.Fatalf("unexpected updated validator data: %+v", updated)
	}
}

func TestStakeAddsStakeAndRegistersValidator(t *testing.T) {
	setupApprovementHandler(structures.NetworkParameters{
		ValidatorRequiredStake: 100,
		MinimalStakePerStaker:  10,
	})

	validator := &structures.ValidatorStorage{
		Pubkey:          "validator1",
		Percentage:      80,
		TotalStaked:     90,
		Stakers:         map[string]uint64{"alice": 90},
		ValidatorUrl:    "http://validator",
		WssValidatorUrl: "ws://validator",
	}
	key := constants.DBKeyPrefixValidatorStorage + "validator1"
	handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[key] = validator

	delayedTx := map[string]string{
		"staker":          "alice",
		"validatorPubKey": "validator1",
		"amount":          "20",
	}

	if !system_contracts.Stake(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected Stake to succeed")
	}

	updated := handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[key]
	if updated.TotalStaked != 110 {
		t.Fatalf("expected total staked 110, got %d", updated.TotalStaked)
	}
	if updated.Stakers["alice"] != 110 {
		t.Fatalf("expected staker balance 110, got %d", updated.Stakers["alice"])
	}
	if len(handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry) != 1 {
		t.Fatalf("expected validator to be registered")
	}
}

func TestUnstakeRemovesValidatorFromRegistryWhenBelowRequiredStake(t *testing.T) {
	setupApprovementHandler(structures.NetworkParameters{
		ValidatorRequiredStake: 100,
		MinimalStakePerStaker:  10,
	})

	validator := &structures.ValidatorStorage{
		Pubkey:          "validator1",
		Percentage:      80,
		TotalStaked:     120,
		Stakers:         map[string]uint64{"alice": 120},
		ValidatorUrl:    "http://validator",
		WssValidatorUrl: "ws://validator",
	}
	key := constants.DBKeyPrefixValidatorStorage + "validator1"
	handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[key] = validator
	handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry = []string{"validator1"}

	delayedTx := map[string]string{
		"unstaker":        "alice",
		"validatorPubKey": "validator1",
		"amount":          "30",
	}

	if !system_contracts.Unstake(delayedTx, constants.ContextApprovementThread) {
		t.Fatalf("expected Unstake to succeed")
	}

	updated := handlers.APPROVEMENT_THREAD_METADATA.Handler.ValidatorsStoragesCache[key]
	if updated.TotalStaked != 90 {
		t.Fatalf("expected total staked 90, got %d", updated.TotalStaked)
	}
	if updated.Stakers["alice"] != 90 {
		t.Fatalf("expected staker balance 90, got %d", updated.Stakers["alice"])
	}
	if len(handlers.APPROVEMENT_THREAD_METADATA.Handler.EpochDataHandler.ValidatorsRegistry) != 0 {
		t.Fatalf("expected validator to be removed from registry")
	}
}
