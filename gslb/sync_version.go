package gslb

import (
	"database/sql"
	"dns-go/storage"
)

func incrementSyncVersion(db *storage.Database, tx *sql.Tx) error {
	return storage.NewSyncVersion(db).IncrementVersion(tx)
}
