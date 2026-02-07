package storage

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// SetupTestDB는 테스트용 임시 데이터베이스를 생성합니다
func SetupTestDB(t *testing.T) *Database {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDatabase(dbPath)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

// InsertTestZone은 테스트용 Zone을 삽입합니다
func InsertTestZone(t *testing.T, db *Database, name string) int64 {
	result, err := db.Writer.Exec("INSERT INTO zones (name) VALUES (?)", name)
	require.NoError(t, err)

	id, err := result.LastInsertId()
	require.NoError(t, err)

	return id
}

// InsertTestRecord는 테스트용 Record를 삽입합니다
func InsertTestRecord(t *testing.T, db *Database, zoneID int64, name, recordType, content string) int64 {
	result, err := db.Writer.Exec(
		"INSERT INTO records (zone_id, name, type, content) VALUES (?, ?, ?, ?)",
		zoneID, name, recordType, content,
	)
	require.NoError(t, err)

	id, err := result.LastInsertId()
	require.NoError(t, err)

	return id
}
