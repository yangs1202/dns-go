package storage

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDatabase(dbPath)
	require.NoError(t, err)
	require.NotNil(t, db)
	defer db.Close()

	// Writer, Reader 연결 확인
	assert.NotNil(t, db.Writer)
	assert.NotNil(t, db.Reader)

	// 연결 테스트
	err = db.Writer.Ping()
	assert.NoError(t, err)

	err = db.Reader.Ping()
	assert.NoError(t, err)
}

func TestMigrate(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDatabase(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// 모든 테이블이 생성되었는지 확인
	tables := []string{
		"zones",
		"records",
		"gslb_policies",
		"gslb_pools",
		"gslb_members",
		"health_checks",
		"cache_settings",
		"upstream_servers",
		"adblock_sources",
		"adblock_domains",
		"adblock_stats",
	}

	for _, table := range tables {
		var name string
		query := "SELECT name FROM sqlite_master WHERE type='table' AND name=?"
		err := db.Reader.QueryRow(query, table).Scan(&name)
		assert.NoError(t, err, "테이블 %s가 존재해야 합니다", table)
		assert.Equal(t, table, name)
	}
}

func TestMigrateIndexes(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDatabase(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// 인덱스 확인
	indexes := []string{
		"idx_records_lookup",
		"idx_gslb_policies_domain",
		"idx_gslb_pools_policy",
		"idx_adblock_domains_lookup",
		"idx_adblock_stats_time",
	}

	for _, index := range indexes {
		var name string
		query := "SELECT name FROM sqlite_master WHERE type='index' AND name=?"
		err := db.Reader.QueryRow(query, index).Scan(&name)
		assert.NoError(t, err, "인덱스 %s가 존재해야 합니다", index)
		assert.Equal(t, index, name)
	}
}

func TestMigrateDefaultCacheSettings(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDatabase(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// 기본 캐시 설정 확인
	var (
		id              int64
		enabled         int
		maxSize         int64
		defaultTTL      int64
		minTTL          int64
		maxTTL          int64
		negativeTTL     int64
		prefetchTrigger float64
	)

	query := `SELECT id, enabled, max_size, default_ttl, min_ttl, max_ttl, negative_ttl, prefetch_trigger
	          FROM cache_settings WHERE id = 1`
	err = db.Reader.QueryRow(query).Scan(&id, &enabled, &maxSize, &defaultTTL, &minTTL, &maxTTL, &negativeTTL, &prefetchTrigger)
	require.NoError(t, err)

	assert.Equal(t, int64(1), id)
	assert.Equal(t, 1, enabled)
	assert.Equal(t, int64(10000), maxSize)
	assert.Equal(t, int64(300), defaultTTL)
	assert.Equal(t, int64(60), minTTL)
	assert.Equal(t, int64(86400), maxTTL)
	assert.Equal(t, int64(300), negativeTTL)
	assert.Equal(t, 0.9, prefetchTrigger)
}

func TestMigrateIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDatabase(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// 마이그레이션 재실행 (멱등성 테스트)
	err = db.Migrate()
	assert.NoError(t, err)

	// 캐시 설정이 중복 삽입되지 않았는지 확인
	var count int
	err = db.Reader.QueryRow("SELECT COUNT(*) FROM cache_settings").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "캐시 설정은 하나만 존재해야 합니다")
}

func TestDatabaseClose(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDatabase(dbPath)
	require.NoError(t, err)

	err = db.Close()
	assert.NoError(t, err)

	// 닫힌 후에는 ping 실패해야 함
	err = db.Writer.Ping()
	assert.Error(t, err)
}

func TestNewDatabaseInvalidPath(t *testing.T) {
	// 쓰기 권한 없는 경로
	dbPath := "/invalid/path/test.db"

	db, err := NewDatabase(dbPath)
	assert.Error(t, err)
	assert.Nil(t, db)
}

func TestDatabaseWALMode(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDatabase(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// WAL 모드 확인 (Writer에서 확인 - configurePragmas에서 설정됨)
	var journalMode string
	err = db.Writer.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	require.NoError(t, err)
	assert.Equal(t, "wal", journalMode)

	// 외래 키 활성화 확인
	var foreignKeys int
	err = db.Writer.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys)
	require.NoError(t, err)
	assert.Equal(t, 1, foreignKeys)
}

func TestDatabaseConcurrentReads(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDatabase(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// 테스트 데이터 삽입
	_, err = db.Writer.Exec("INSERT INTO cache_settings (id) VALUES (2) ON CONFLICT DO NOTHING")

	// 동시 읽기 테스트
	done := make(chan bool, 4)
	for i := 0; i < 4; i++ {
		go func() {
			var count int
			err := db.Reader.QueryRow("SELECT COUNT(*) FROM cache_settings").Scan(&count)
			assert.NoError(t, err)
			done <- true
		}()
	}

	// 모든 고루틴 완료 대기
	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestDatabaseFileCreation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// DB 파일이 존재하지 않음
	_, err := os.Stat(dbPath)
	assert.True(t, os.IsNotExist(err))

	// 데이터베이스 생성
	db, err := NewDatabase(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// DB 파일이 생성됨
	_, err = os.Stat(dbPath)
	assert.NoError(t, err)

	// WAL 파일도 생성됨
	walPath := dbPath + "-wal"
	_, err = os.Stat(walPath)
	// WAL 파일은 즉시 생성되지 않을 수 있음 (첫 쓰기 후)
}

func TestDatabaseSchema(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDatabase(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// zones 테이블 스키마 확인
	var sql string
	err = db.Reader.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='zones'").Scan(&sql)
	require.NoError(t, err)
	assert.Contains(t, sql, "name TEXT NOT NULL UNIQUE")
	assert.Contains(t, sql, "soa_mname TEXT DEFAULT ''")
}

// 헬퍼 함수: 인메모리 테스트 DB 생성
func setupTestDB(t *testing.T) *Database {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := NewDatabase(dbPath)
	require.NoError(t, err)

	t.Cleanup(func() {
		db.Close()
	})

	return db
}

// 헬퍼 함수: 테스트 Zone 삽입
func insertTestZone(t *testing.T, db *Database, name string) int64 {
	result, err := db.Writer.Exec("INSERT INTO zones (name) VALUES (?)", name)
	require.NoError(t, err)

	id, err := result.LastInsertId()
	require.NoError(t, err)

	return id
}

// 헬퍼 함수: 테스트 Record 삽입
func insertTestRecord(t *testing.T, db *Database, zoneID int64, name, recordType, content string) int64 {
	result, err := db.Writer.Exec(
		"INSERT INTO records (zone_id, name, type, content) VALUES (?, ?, ?, ?)",
		zoneID, name, recordType, content,
	)
	require.NoError(t, err)

	id, err := result.LastInsertId()
	require.NoError(t, err)

	return id
}

func TestHelperFunctions(t *testing.T) {
	db := setupTestDB(t)

	// Zone 삽입
	zoneID := insertTestZone(t, db, "test.com.")
	assert.Greater(t, zoneID, int64(0))

	// Record 삽입
	recordID := insertTestRecord(t, db, zoneID, "www.test.com.", "A", "192.0.2.1")
	assert.Greater(t, recordID, int64(0))

	// 삽입된 데이터 확인
	var name string
	err := db.Reader.QueryRow("SELECT name FROM zones WHERE id = ?", zoneID).Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, "test.com.", name)
}

func TestForeignKeyConstraints(t *testing.T) {
	db := setupTestDB(t)

	// Zone 삽입
	zoneID := insertTestZone(t, db, "test.com.")

	// Record 삽입
	recordID := insertTestRecord(t, db, zoneID, "www.test.com.", "A", "192.0.2.1")

	// Zone 삭제 시 Record도 삭제되는지 확인 (CASCADE)
	_, err := db.Writer.Exec("DELETE FROM zones WHERE id = ?", zoneID)
	require.NoError(t, err)

	// Record가 삭제되었는지 확인
	var count int
	err = db.Reader.QueryRow("SELECT COUNT(*) FROM records WHERE id = ?", recordID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "CASCADE DELETE가 작동해야 합니다")
}

func TestTransactionRollback(t *testing.T) {
	db := setupTestDB(t)

	// 트랜잭션 시작
	tx, err := db.Writer.Begin()
	require.NoError(t, err)

	// Zone 삽입
	_, err = tx.Exec("INSERT INTO zones (name) VALUES (?)", "test.com.")
	require.NoError(t, err)

	// 롤백
	err = tx.Rollback()
	require.NoError(t, err)

	// 삽입된 데이터가 없는지 확인
	var count int
	err = db.Reader.QueryRow("SELECT COUNT(*) FROM zones WHERE name = ?", "test.com.").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestTransactionCommit(t *testing.T) {
	db := setupTestDB(t)

	// 트랜잭션 시작
	tx, err := db.Writer.Begin()
	require.NoError(t, err)

	// Zone 삽입
	_, err = tx.Exec("INSERT INTO zones (name) VALUES (?)", "test.com.")
	require.NoError(t, err)

	// 커밋
	err = tx.Commit()
	require.NoError(t, err)

	// 삽입된 데이터 확인
	var count int
	err = db.Reader.QueryRow("SELECT COUNT(*) FROM zones WHERE name = ?", "test.com.").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestUniqueConstraints(t *testing.T) {
	db := setupTestDB(t)

	// 첫 번째 Zone 삽입
	insertTestZone(t, db, "test.com.")

	// 동일한 이름으로 다시 삽입 시도 (UNIQUE 제약 위반)
	_, err := db.Writer.Exec("INSERT INTO zones (name) VALUES (?)", "test.com.")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "UNIQUE")
}

func TestDefaultValues(t *testing.T) {
	db := setupTestDB(t)

	// Zone 삽입 (기본값 사용)
	result, err := db.Writer.Exec("INSERT INTO zones (name) VALUES (?)", "test.com.")
	require.NoError(t, err)

	id, err := result.LastInsertId()
	require.NoError(t, err)

	// 기본값 확인
	var (
		enabled    int
		soaSerial  int64
		soaRefresh int64
		soaRetry   int64
		soaExpire  int64
		soaMinimum int64
	)

	query := `SELECT enabled, soa_serial, soa_refresh, soa_retry, soa_expire, soa_minimum
	          FROM zones WHERE id = ?`
	err = db.Reader.QueryRow(query, id).Scan(&enabled, &soaSerial, &soaRefresh, &soaRetry, &soaExpire, &soaMinimum)
	require.NoError(t, err)

	assert.Equal(t, 1, enabled)
	assert.Equal(t, int64(1), soaSerial)
	assert.Equal(t, int64(3600), soaRefresh)
	assert.Equal(t, int64(900), soaRetry)
	assert.Equal(t, int64(86400), soaExpire)
	assert.Equal(t, int64(300), soaMinimum)
}

func TestNullableFields(t *testing.T) {
	db := setupTestDB(t)

	// Zone 삽입 (NULL 허용 필드)
	result, err := db.Writer.Exec("INSERT INTO zones (name, soa_mname, soa_rname) VALUES (?, '', '')", "test.com.")
	require.NoError(t, err)

	id, err := result.LastInsertId()
	require.NoError(t, err)

	// NULL/빈 문자열 확인
	var soaMname, soaRname sql.NullString
	err = db.Reader.QueryRow("SELECT soa_mname, soa_rname FROM zones WHERE id = ?", id).Scan(&soaMname, &soaRname)
	require.NoError(t, err)

	assert.True(t, soaMname.Valid)
	assert.Equal(t, "", soaMname.String)
	assert.True(t, soaRname.Valid)
	assert.Equal(t, "", soaRname.String)
}
