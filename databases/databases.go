package databases

import (
	"errors"
	"fmt"

	"github.com/syndtr/goleveldb/leveldb"
)

var BLOCKS, STATE, EPOCH_DATA, APPROVEMENT_THREAD_METADATA, FINALIZATION_THREAD_METADATA *leveldb.DB

type namedDB struct {
	name string
	db   *leveldb.DB
}

// CloseAll safely closes all initialized LevelDB instances
func CloseAll() error {

	databases := []namedDB{
		{name: "BLOCKS", db: BLOCKS},
		{name: "STATE", db: STATE},
		{name: "EPOCH_DATA", db: EPOCH_DATA},
		{name: "APPROVEMENT_THREAD_METADATA", db: APPROVEMENT_THREAD_METADATA},
		{name: "FINALIZATION_THREAD_METADATA", db: FINALIZATION_THREAD_METADATA},
	}

	var errs []error
	for _, database := range databases {
		if database.db == nil {
			continue
		}

		if err := database.db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", database.name, err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
