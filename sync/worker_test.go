package sync

import (
	"dns-go/model"
	"dns-go/storage"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *storage.Database {
	t.Helper()
	db, err := storage.NewDatabase(":memory:")
	require.NoError(t, err)

	// 마이그레이션이 NewDatabase에서 자동 실행됨
	// 추가 확인 불필요

	return db
}

func TestWorker_New(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	worker := NewWorker("http://primary:8080", db, 1*time.Second)

	assert.NotNil(t, worker)
	assert.Equal(t, "http://primary:8080", worker.primaryURL)
	assert.Equal(t, 1*time.Second, worker.interval)
}

func TestWorker_FullSync(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	upstreamStorage := storage.NewUpstreamStorage(db)
	_, err := upstreamStorage.CreateUpstreamServer(&model.UpstreamServer{
		Name:     "Local DNS",
		Address:  "1.1.1.1:53",
		Protocol: "udp",
		Priority: 10,
		Enabled:  true,
	})
	require.NoError(t, err)

	// Mock Primary 서버
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(10),
				"checksum": "abc123",
				"data": map[string]interface{}{
					"zones": []map[string]interface{}{
						{
							"id":             1,
							"name":           "example.com.",
							"soa_mname":      "ns1.example.com.",
							"soa_rname":      "admin.example.com.",
							"soa_serial":     1,
							"soa_refresh":    3600,
							"soa_retry":      900,
							"soa_expire":     86400,
							"soa_minimum":    300,
							"enabled":        1,
							"allow_fallback": 1,
							"created_at":     "2026-01-31T12:00:00Z",
							"updated_at":     "2026-01-31T12:00:00Z",
						},
					},
					"records": []map[string]interface{}{
						{
							"id":         1,
							"zone_id":    1,
							"name":       "www.example.com.",
							"type":       "A",
							"content":    "10.0.0.100",
							"ttl":        300,
							"priority":   0,
							"enabled":    1,
							"created_at": "2026-01-31T12:00:00Z",
							"updated_at": "2026-01-31T12:00:00Z",
						},
					},
				},
			}
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// Full Sync 실행
	err := worker.fullSync()
	require.NoError(t, err)

	// 동기화 결과 확인 (Writer로 확인 - SQLite :memory:는 단일 연결)
	var version int64
	err = db.Writer.QueryRow("SELECT last_sync_version FROM sync_state WHERE id = 1").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, int64(10), version)

	// Zone 확인
	var zoneName string
	err = db.Writer.QueryRow("SELECT name FROM zones WHERE id = 1").Scan(&zoneName)
	require.NoError(t, err)
	assert.Equal(t, "example.com.", zoneName)

	// Record 확인
	var recordName string
	err = db.Writer.QueryRow("SELECT name FROM records WHERE id = 1").Scan(&recordName)
	require.NoError(t, err)
	assert.Equal(t, "www.example.com.", recordName)

	// Upstream 확인 (Slave에서는 동기화하지 않음)
	var upstreamName string
	err = db.Writer.QueryRow("SELECT name FROM upstream_servers WHERE id = 1").Scan(&upstreamName)
	require.NoError(t, err)
	assert.Equal(t, "Local DNS", upstreamName)
}

func TestWorker_IncrementalSync_NoChanges(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// sync_state 초기화 (버전 10)
	_, err := db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 10,
		    data_checksum = 'abc123'
		WHERE id = 1
	`)
	require.NoError(t, err)

	// Mock Primary 서버 (변경 없음)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/metadata" {
			response := map[string]interface{}{
				"version":  int64(10),
				"checksum": "abc123",
			}
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/api/sync/full" {
			// Full Sync 데이터도 제공 (혹시 필요할 수 있음)
			response := map[string]interface{}{
				"version":  int64(10),
				"checksum": "abc123",
				"data": map[string]interface{}{
					"zones":   []map[string]interface{}{},
					"records": []map[string]interface{}{},
				},
			}
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// Incremental Sync 실행
	err = worker.incrementalSync()
	require.NoError(t, err)

	// 버전 확인 (변경 없어야 함)
	var version int64
	err = db.Writer.QueryRow("SELECT last_sync_version FROM sync_state WHERE id = 1").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, int64(10), version)
}

func TestWorker_IncrementalSync_WithChanges(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// sync_state 초기화 (버전 5)
	_, err := db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 5,
		    data_checksum = 'old123'
		WHERE id = 1
	`)
	require.NoError(t, err)

	callCount := 0

	// Mock Primary 서버
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/metadata" {
			// 버전 불일치
			response := map[string]interface{}{
				"version":  int64(10),
				"checksum": "new456",
			}
			json.NewEncoder(w).Encode(response)
		} else if r.URL.Path == "/api/sync/full" {
			callCount++
			// Full Sync 데이터
			response := map[string]interface{}{
				"version":  int64(10),
				"checksum": "new456",
				"data": map[string]interface{}{
					"zones":   []map[string]interface{}{},
					"records": []map[string]interface{}{},
				},
			}
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// Incremental Sync 실행 (버전 불일치 → Full Sync)
	err = worker.incrementalSync()
	require.NoError(t, err)

	// Full Sync가 호출되었는지 확인
	assert.Equal(t, 1, callCount, "버전 불일치 시 Full Sync가 호출되어야 함")

	// 버전 확인 (업데이트되어야 함)
	var version int64
	err = db.Writer.QueryRow("SELECT last_sync_version FROM sync_state WHERE id = 1").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, int64(10), version)
}

func TestWorker_IncrementalSync_InitialState(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// sync_state 초기 상태 (버전 0)
	callCount := 0

	// Mock Primary 서버
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			callCount++
			response := map[string]interface{}{
				"version":  int64(1),
				"checksum": "init",
				"data": map[string]interface{}{
					"zones":   []map[string]interface{}{},
					"records": []map[string]interface{}{},
				},
			}
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// Incremental Sync 실행 (초기 상태 → Full Sync)
	err := worker.incrementalSync()
	require.NoError(t, err)

	// Full Sync가 호출되었는지 확인
	assert.Equal(t, 1, callCount, "초기 상태에서 Full Sync가 호출되어야 함")
}

func TestWorker_FullSync_DataReplacement(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 기존 데이터 삽입
	zoneStorage := storage.NewZoneStorage(db)
	_, err := zoneStorage.CreateZone(&model.Zone{
		Name:       "old.com.",
		SOAMname:   "ns1.old.com.",
		SOARname:   "admin.old.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	})
	require.NoError(t, err)

	// Mock Primary 서버 (새 데이터)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(2),
				"checksum": "new",
				"data": map[string]interface{}{
					"zones": []map[string]interface{}{
						{
							"id":             1,
							"name":           "new.com.",
							"soa_mname":      "ns1.new.com.",
							"soa_rname":      "admin.new.com.",
							"soa_serial":     1,
							"soa_refresh":    3600,
							"soa_retry":      900,
							"soa_expire":     86400,
							"soa_minimum":    300,
							"enabled":        1,
							"allow_fallback": 1,
							"created_at":     "2026-01-31T12:00:00Z",
							"updated_at":     "2026-01-31T12:00:00Z",
						},
					},
					"records": []map[string]interface{}{},
				},
			}
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// Full Sync 실행
	err = worker.fullSync()
	require.NoError(t, err)

	// 기존 데이터 삭제 확인
	var count int
	err = db.Writer.QueryRow("SELECT COUNT(*) FROM zones").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "Zone이 1개만 있어야 함")

	// 새 데이터 확인
	var zoneName string
	err = db.Writer.QueryRow("SELECT name FROM zones WHERE id = 1").Scan(&zoneName)
	require.NoError(t, err)
	assert.Equal(t, "new.com.", zoneName, "새 데이터로 교체되어야 함")
}

func TestWorker_FullSync_ConnectionError(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 존재하지 않는 서버
	worker := NewWorker("http://localhost:99999", db, 1*time.Second)

	// Full Sync 실행 (실패 예상)
	err := worker.fullSync()
	assert.Error(t, err, "연결 실패 시 에러가 발생해야 함")
	assert.Contains(t, err.Error(), "Primary 연결 실패")
}

func TestWorker_FullSync_InvalidJSON(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Mock Primary 서버 (잘못된 JSON)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// Full Sync 실행 (실패 예상)
	err := worker.fullSync()
	assert.Error(t, err, "잘못된 JSON 시 에러가 발생해야 함")
	assert.Contains(t, err.Error(), "JSON 파싱 실패")
}

func TestWorker_InsertZone(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	worker := NewWorker("http://dummy", db, 1*time.Second)

	tx, err := db.Writer.Begin()
	require.NoError(t, err)
	defer tx.Rollback()

	zoneData := map[string]interface{}{
		"id":             1,
		"name":           "test.com.",
		"soa_mname":      "ns1.test.com.",
		"soa_rname":      "admin.test.com.",
		"soa_serial":     1,
		"soa_refresh":    3600,
		"soa_retry":      900,
		"soa_expire":     86400,
		"soa_minimum":    300,
		"enabled":        1,
		"allow_fallback": 1,
		"created_at":     "2026-01-31T12:00:00Z",
		"updated_at":     "2026-01-31T12:00:00Z",
	}

	err = worker.insertZone(tx, zoneData)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// 확인
	var name string
	err = db.Writer.QueryRow("SELECT name FROM zones WHERE id = 1").Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, "test.com.", name)
}

func TestWorker_InsertRecord(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	worker := NewWorker("http://dummy", db, 1*time.Second)

	// Zone 먼저 생성
	zoneStorage := storage.NewZoneStorage(db)
	_, err := zoneStorage.CreateZone(&model.Zone{
		Name:       "test.com.",
		SOAMname:   "ns1.test.com.",
		SOARname:   "admin.test.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	})
	require.NoError(t, err)

	tx, err := db.Writer.Begin()
	require.NoError(t, err)
	defer tx.Rollback()

	recordData := map[string]interface{}{
		"id":         1,
		"zone_id":    1,
		"name":       "www.test.com.",
		"type":       "A",
		"content":    "10.0.0.1",
		"ttl":        300,
		"priority":   0,
		"enabled":    1,
		"created_at": "2026-01-31T12:00:00Z",
		"updated_at": "2026-01-31T12:00:00Z",
	}

	err = worker.insertRecord(tx, recordData)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// 확인
	var name string
	err = db.Writer.QueryRow("SELECT name FROM records WHERE id = 1").Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, "www.test.com.", name)
}

func TestWorker_InsertUpstream(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	worker := NewWorker("http://dummy", db, 1*time.Second)

	tx, err := db.Writer.Begin()
	require.NoError(t, err)
	defer tx.Rollback()

	upstreamData := map[string]interface{}{
		"id":         1,
		"name":       "Test DNS",
		"address":    "1.1.1.1:53",
		"protocol":   "udp",
		"priority":   10,
		"enabled":    1,
		"created_at": "2026-01-31T12:00:00Z",
		"updated_at": "2026-01-31T12:00:00Z",
	}

	err = worker.insertUpstream(tx, upstreamData)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// 확인
	var name string
	err = db.Writer.QueryRow("SELECT name FROM upstream_servers WHERE id = 1").Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, "Test DNS", name)
}

func TestWorker_StartStop(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Dummy server
		response := map[string]interface{}{
			"version":  int64(1),
			"checksum": "test",
			"data": map[string]interface{}{
				"zones":   []map[string]interface{}{},
				"records": []map[string]interface{}{},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 100*time.Millisecond)

	// Start
	worker.Start()

	// 실행 대기
	time.Sleep(500 * time.Millisecond)

	// Stop
	worker.Stop()

	// Stop 후 추가 대기
	time.Sleep(200 * time.Millisecond)
}
