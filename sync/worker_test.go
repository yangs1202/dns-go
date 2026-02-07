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

// setupTestDBFile은 파일 기반 SQLite를 생성하여 Reader/Writer 공유 가능하게 합니다
// incrementalSync 테스트에서 Reader가 Writer의 데이터를 볼 수 있어야 하므로 필요
func setupTestDBFile(t *testing.T) *storage.Database {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	db, err := storage.NewDatabase(dbPath)
	require.NoError(t, err)

	return db
}

func TestWorker_New(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	worker := NewWorker("http://primary:8080", db, 1*time.Second)

	assert.NotNil(t, worker)
	assert.Equal(t, "http://primary:8080", worker.primaryURL)
	assert.Equal(t, 1*time.Second, worker.interval)
}

func TestWorker_FullSync(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

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
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// Full Sync 실행
	err = worker.fullSync()
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
	db := setupTestDBFile(t)
	defer func() { _ = db.Close() }()

	// sync_state 초기화 (버전 10)
	_, err := db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 10,
		    data_checksum = 'abc123'
		WHERE id = 1
	`)
	require.NoError(t, err)

	metadataCalled := false

	// Mock Primary 서버 (변경 없음)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sync/metadata":
			metadataCalled = true
			response := map[string]interface{}{
				"version":  int64(10),
				"checksum": "abc123",
			}
			_ = json.NewEncoder(w).Encode(response)
		case "/api/sync/full":
			// Full Sync 데이터도 제공 (혹시 필요할 수 있음)
			response := map[string]interface{}{
				"version":  int64(10),
				"checksum": "abc123",
				"data": map[string]interface{}{
					"zones":   []map[string]interface{}{},
					"records": []map[string]interface{}{},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// Incremental Sync 실행
	err = worker.incrementalSync()
	require.NoError(t, err)

	// Metadata 엔드포인트가 호출되었는지 확인
	assert.True(t, metadataCalled, "Metadata 엔드포인트가 호출되어야 함")

	// 버전 확인 (변경 없어야 함)
	var version int64
	err = db.Writer.QueryRow("SELECT last_sync_version FROM sync_state WHERE id = 1").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, int64(10), version)
}

func TestWorker_IncrementalSync_WithChanges(t *testing.T) {
	db := setupTestDBFile(t)
	defer func() { _ = db.Close() }()

	// sync_state 초기화 (버전 5)
	_, err := db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 5,
		    data_checksum = 'old123'
		WHERE id = 1
	`)
	require.NoError(t, err)

	metadataCalled := false
	fullSyncCallCount := 0

	// Mock Primary 서버
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sync/metadata":
			metadataCalled = true
			// 버전 불일치
			response := map[string]interface{}{
				"version":  int64(10),
				"checksum": "new456",
			}
			_ = json.NewEncoder(w).Encode(response)
		case "/api/sync/full":
			fullSyncCallCount++
			// Full Sync 데이터
			response := map[string]interface{}{
				"version":  int64(10),
				"checksum": "new456",
				"data": map[string]interface{}{
					"zones":   []map[string]interface{}{},
					"records": []map[string]interface{}{},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// Incremental Sync 실행 (버전 불일치 → Full Sync)
	err = worker.incrementalSync()
	require.NoError(t, err)

	// Metadata 및 Full Sync 호출 확인
	assert.True(t, metadataCalled, "Metadata 엔드포인트가 호출되어야 함")
	assert.Equal(t, 1, fullSyncCallCount, "버전 불일치 시 Full Sync가 호출되어야 함")

	// 버전 확인 (업데이트되어야 함)
	var version int64
	err = db.Writer.QueryRow("SELECT last_sync_version FROM sync_state WHERE id = 1").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, int64(10), version)
}

func TestWorker_IncrementalSync_InitialState(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

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
			_ = json.NewEncoder(w).Encode(response)
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
	defer func() { _ = db.Close() }()

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
			_ = json.NewEncoder(w).Encode(response)
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
	defer func() { _ = db.Close() }()

	// 존재하지 않는 서버
	worker := NewWorker("http://localhost:99999", db, 1*time.Second)

	// Full Sync 실행 (실패 예상)
	err := worker.fullSync()
	assert.Error(t, err, "연결 실패 시 에러가 발생해야 함")
	assert.Contains(t, err.Error(), "primary 연결 실패")
}

func TestWorker_FullSync_InvalidJSON(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버 (잘못된 JSON)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// Full Sync 실행 (실패 예상)
	err := worker.fullSync()
	assert.Error(t, err, "잘못된 JSON 시 에러가 발생해야 함")
	assert.Contains(t, err.Error(), "json 파싱 실패")
}

func TestWorker_InsertZone(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	worker := NewWorker("http://dummy", db, 1*time.Second)

	tx, err := db.Writer.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

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
	defer func() { _ = db.Close() }()

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
	defer func() { _ = tx.Rollback() }()

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
	defer func() { _ = db.Close() }()

	worker := NewWorker("http://dummy", db, 1*time.Second)

	tx, err := db.Writer.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

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
	defer func() { _ = db.Close() }()

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
		_ = json.NewEncoder(w).Encode(response)
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

func TestWorker_SetSyncCompleteCallback(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	callbackCalled := false

	// Mock Primary 서버
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(1),
				"checksum": "test",
				"data": map[string]interface{}{
					"zones":            []map[string]interface{}{},
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// 콜백 설정
	worker.SetSyncCompleteCallback(func() {
		callbackCalled = true
	})

	assert.NotNil(t, worker.onSyncComplete, "콜백이 설정되어야 함")

	// Full Sync 실행 (콜백 호출됨)
	err := worker.fullSync()
	require.NoError(t, err)
	assert.True(t, callbackCalled, "Full Sync 완료 후 콜백이 호출되어야 함")
}

func TestWorker_SetSyncCompleteCallback_Nil(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(1),
				"checksum": "test",
				"data": map[string]interface{}{
					"zones":            []map[string]interface{}{},
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// 콜백 미설정 상태에서 Full Sync (panic 없어야 함)
	err := worker.fullSync()
	require.NoError(t, err)
}

func TestWorker_FullSync_WithGSLBData(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버 (GSLB 데이터 포함)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(5),
				"checksum": "gslb123",
				"data": map[string]interface{}{
					"zones":            []map[string]interface{}{},
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
					"gslb_policies": []map[string]interface{}{
						{
							"id":          1,
							"name":        "test-policy",
							"domain":      "app.example.com.",
							"record_type": "A",
							"ttl":         30,
							"enabled":     1,
							"created_at":  "2026-01-31T12:00:00Z",
						},
					},
					"gslb_pools": []map[string]interface{}{
						{
							"id":            1,
							"policy_id":     1,
							"name":          "korea-pool",
							"match_type":    "geo_country",
							"match_value":   "KR",
							"priority":      10,
							"fallback_pool": 0,
						},
						{
							"id":            2,
							"policy_id":     1,
							"name":          "default-pool",
							"match_type":    "default",
							"match_value":   "*",
							"priority":      100,
							"fallback_pool": 1,
						},
					},
					"gslb_members": []map[string]interface{}{
						{
							"id":      1,
							"pool_id": 1,
							"address": "10.0.1.1",
							"weight":  100,
							"enabled": 1,
						},
						{
							"id":      2,
							"pool_id": 1,
							"address": "10.0.1.2",
							"weight":  50,
							"enabled": 1,
						},
						{
							"id":      3,
							"pool_id": 2,
							"address": "10.0.2.1",
							"weight":  100,
							"enabled": 1,
						},
					},
					"health_checks": []map[string]interface{}{
						{
							"id":                  1,
							"policy_id":           1,
							"check_type":          "http",
							"target":              "http://app.example.com/health",
							"interval_sec":        10,
							"timeout_sec":         5,
							"healthy_threshold":   3,
							"unhealthy_threshold": 2,
							"enabled":             1,
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	// Full Sync 실행
	err := worker.fullSync()
	require.NoError(t, err)

	// 버전 확인
	var version int64
	err = db.Writer.QueryRow("SELECT last_sync_version FROM sync_state WHERE id = 1").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, int64(5), version)

	// GSLB Policy 확인
	var policyName, policyDomain string
	err = db.Writer.QueryRow("SELECT name, domain FROM gslb_policies WHERE id = 1").Scan(&policyName, &policyDomain)
	require.NoError(t, err)
	assert.Equal(t, "test-policy", policyName)
	assert.Equal(t, "app.example.com.", policyDomain)

	// GSLB Pool 확인
	var poolCount int
	err = db.Writer.QueryRow("SELECT COUNT(*) FROM gslb_pools").Scan(&poolCount)
	require.NoError(t, err)
	assert.Equal(t, 2, poolCount)

	var poolName string
	err = db.Writer.QueryRow("SELECT name FROM gslb_pools WHERE id = 1").Scan(&poolName)
	require.NoError(t, err)
	assert.Equal(t, "korea-pool", poolName)

	// GSLB Member 확인
	var memberCount int
	err = db.Writer.QueryRow("SELECT COUNT(*) FROM gslb_members").Scan(&memberCount)
	require.NoError(t, err)
	assert.Equal(t, 3, memberCount)

	var memberAddr string
	err = db.Writer.QueryRow("SELECT address FROM gslb_members WHERE id = 1").Scan(&memberAddr)
	require.NoError(t, err)
	assert.Equal(t, "10.0.1.1", memberAddr)

	// Health Check 확인
	var checkType, checkTarget string
	err = db.Writer.QueryRow("SELECT check_type, target FROM health_checks WHERE id = 1").Scan(&checkType, &checkTarget)
	require.NoError(t, err)
	assert.Equal(t, "http", checkType)
	assert.Equal(t, "http://app.example.com/health", checkTarget)
}

func TestWorker_FullSync_HTTPErrorStatus(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버 (500 Internal Server Error)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err := worker.fullSync()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "primary 응답 오류: 500")
}

func TestWorker_FullSync_WithCallback(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	callbackCount := 0

	// Mock Primary 서버 (GSLB 데이터 포함)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(3),
				"checksum": "cb123",
				"data": map[string]interface{}{
					"zones":            []map[string]interface{}{},
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
					"gslb_policies": []map[string]interface{}{
						{
							"id":          1,
							"name":        "cb-policy",
							"domain":      "cb.example.com.",
							"record_type": "A",
							"ttl":         30,
							"enabled":     1,
							"created_at":  "2026-01-31T12:00:00Z",
						},
					},
					"gslb_pools":   []map[string]interface{}{},
					"gslb_members": []map[string]interface{}{},
					"health_checks": []map[string]interface{}{
						{
							"id":                  1,
							"policy_id":           1,
							"check_type":          "tcp",
							"target":              "cb.example.com:443",
							"interval_sec":        10,
							"timeout_sec":         5,
							"healthy_threshold":   3,
							"unhealthy_threshold": 2,
							"enabled":             1,
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)
	worker.SetSyncCompleteCallback(func() {
		callbackCount++
	})

	// Full Sync 2회 실행
	err := worker.fullSync()
	require.NoError(t, err)
	assert.Equal(t, 1, callbackCount)

	err = worker.fullSync()
	require.NoError(t, err)
	assert.Equal(t, 2, callbackCount)
}

func TestWorker_InsertGSLBPolicy(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	worker := NewWorker("http://dummy", db, 1*time.Second)

	tx, err := db.Writer.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	policyData := map[string]interface{}{
		"id":          1,
		"name":        "test-policy",
		"domain":      "gslb.example.com.",
		"record_type": "A",
		"ttl":         30,
		"enabled":     1,
		"created_at":  "2026-01-31T12:00:00Z",
	}

	err = worker.insertGSLBPolicy(tx, policyData)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// 확인
	var name, domain, recordType string
	var ttl int
	err = db.Writer.QueryRow("SELECT name, domain, record_type, ttl FROM gslb_policies WHERE id = 1").Scan(&name, &domain, &recordType, &ttl)
	require.NoError(t, err)
	assert.Equal(t, "test-policy", name)
	assert.Equal(t, "gslb.example.com.", domain)
	assert.Equal(t, "A", recordType)
	assert.Equal(t, 30, ttl)
}

func TestWorker_InsertGSLBPool(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	worker := NewWorker("http://dummy", db, 1*time.Second)

	// Policy 먼저 생성 (Foreign Key)
	_, err := db.Writer.Exec(`INSERT INTO gslb_policies (id, name, domain, record_type, ttl, enabled, created_at) VALUES (1, 'test-policy', 'gslb.example.com.', 'A', 30, 1, '2026-01-31T12:00:00Z')`)
	require.NoError(t, err)

	tx, err := db.Writer.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	poolData := map[string]interface{}{
		"id":            1,
		"policy_id":     1,
		"name":          "asia-pool",
		"match_type":    "geo_continent",
		"match_value":   "AS",
		"priority":      10,
		"fallback_pool": 0,
	}

	err = worker.insertGSLBPool(tx, poolData)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// 확인
	var name, matchType, matchValue string
	var priority int
	err = db.Writer.QueryRow("SELECT name, match_type, match_value, priority FROM gslb_pools WHERE id = 1").Scan(&name, &matchType, &matchValue, &priority)
	require.NoError(t, err)
	assert.Equal(t, "asia-pool", name)
	assert.Equal(t, "geo_continent", matchType)
	assert.Equal(t, "AS", matchValue)
	assert.Equal(t, 10, priority)
}

func TestWorker_InsertGSLBMember(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	worker := NewWorker("http://dummy", db, 1*time.Second)

	// Policy, Pool 먼저 생성 (Foreign Key)
	_, err := db.Writer.Exec(`INSERT INTO gslb_policies (id, name, domain, record_type, ttl, enabled, created_at) VALUES (1, 'test-policy', 'gslb.example.com.', 'A', 30, 1, '2026-01-31T12:00:00Z')`)
	require.NoError(t, err)
	_, err = db.Writer.Exec(`INSERT INTO gslb_pools (id, policy_id, name, match_type, match_value, priority, fallback_pool) VALUES (1, 1, 'test-pool', 'default', '*', 0, 0)`)
	require.NoError(t, err)

	tx, err := db.Writer.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	memberData := map[string]interface{}{
		"id":      1,
		"pool_id": 1,
		"address": "192.168.1.100",
		"weight":  75,
		"enabled": 1,
	}

	err = worker.insertGSLBMember(tx, memberData)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// 확인
	var address string
	var weight int
	err = db.Writer.QueryRow("SELECT address, weight FROM gslb_members WHERE id = 1").Scan(&address, &weight)
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.100", address)
	assert.Equal(t, 75, weight)
}

func TestWorker_InsertHealthCheck(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	worker := NewWorker("http://dummy", db, 1*time.Second)

	// Policy 먼저 생성 (Foreign Key)
	_, err := db.Writer.Exec(`INSERT INTO gslb_policies (id, name, domain, record_type, ttl, enabled, created_at) VALUES (1, 'test-policy', 'gslb.example.com.', 'A', 30, 1, '2026-01-31T12:00:00Z')`)
	require.NoError(t, err)

	tx, err := db.Writer.Begin()
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	checkData := map[string]interface{}{
		"id":                  1,
		"policy_id":           1,
		"check_type":          "https",
		"target":              "https://gslb.example.com/health",
		"interval_sec":        15,
		"timeout_sec":         3,
		"healthy_threshold":   2,
		"unhealthy_threshold": 3,
		"enabled":             1,
	}

	err = worker.insertHealthCheck(tx, checkData)
	require.NoError(t, err)

	err = tx.Commit()
	require.NoError(t, err)

	// 확인
	var checkType, target string
	var intervalSec, timeoutSec, healthyThreshold, unhealthyThreshold int
	err = db.Writer.QueryRow("SELECT check_type, target, interval_sec, timeout_sec, healthy_threshold, unhealthy_threshold FROM health_checks WHERE id = 1").Scan(&checkType, &target, &intervalSec, &timeoutSec, &healthyThreshold, &unhealthyThreshold)
	require.NoError(t, err)
	assert.Equal(t, "https", checkType)
	assert.Equal(t, "https://gslb.example.com/health", target)
	assert.Equal(t, 15, intervalSec)
	assert.Equal(t, 3, timeoutSec)
	assert.Equal(t, 2, healthyThreshold)
	assert.Equal(t, 3, unhealthyThreshold)
}

func TestWorker_IncrementalSync_MetadataConnectionError(t *testing.T) {
	db := setupTestDBFile(t)
	defer func() { _ = db.Close() }()

	// sync_state 버전 설정 (0이 아닌 값 - Reader에서도 보여야 함)
	_, err := db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 5,
		    data_checksum = 'test'
		WHERE id = 1
	`)
	require.NoError(t, err)

	// 존재하지 않는 서버
	worker := NewWorker("http://localhost:99999", db, 1*time.Second)

	err = worker.incrementalSync()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "primary 연결 실패")
}

func TestWorker_IncrementalSync_MetadataHTTPError(t *testing.T) {
	db := setupTestDBFile(t)
	defer func() { _ = db.Close() }()

	// sync_state 버전 설정 (Reader에서도 보여야 함)
	_, err := db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 5,
		    data_checksum = 'test'
		WHERE id = 1
	`)
	require.NoError(t, err)

	// Mock Primary 서버 (metadata에 500 반환)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/metadata" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err = worker.incrementalSync()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "primary 응답 오류: 500")
}

func TestWorker_IncrementalSync_MetadataInvalidJSON(t *testing.T) {
	db := setupTestDBFile(t)
	defer func() { _ = db.Close() }()

	// sync_state 버전 설정 (Reader에서도 보여야 함)
	_, err := db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 5,
		    data_checksum = 'test'
		WHERE id = 1
	`)
	require.NoError(t, err)

	// Mock Primary 서버 (잘못된 JSON)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/metadata" {
			_, _ = w.Write([]byte("not valid json"))
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err = worker.incrementalSync()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "json 파싱 실패")
}

func TestWorker_FullSync_GSLBDataReplacement(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// 기존 GSLB 데이터 삽입
	_, err := db.Writer.Exec(`INSERT INTO gslb_policies (id, name, domain, record_type, ttl, enabled, created_at) VALUES (99, 'old-policy', 'old.example.com.', 'A', 60, 1, '2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	_, err = db.Writer.Exec(`INSERT INTO gslb_pools (id, policy_id, name, match_type, match_value, priority, fallback_pool) VALUES (99, 99, 'old-pool', 'default', '*', 0, 0)`)
	require.NoError(t, err)
	_, err = db.Writer.Exec(`INSERT INTO gslb_members (id, pool_id, address, weight, enabled) VALUES (99, 99, '1.2.3.4', 100, 1)`)
	require.NoError(t, err)
	_, err = db.Writer.Exec(`INSERT INTO health_checks (id, policy_id, check_type, target, interval_sec, timeout_sec, healthy_threshold, unhealthy_threshold, enabled) VALUES (99, 99, 'tcp', 'old.example.com:80', 10, 5, 3, 2, 1)`)
	require.NoError(t, err)

	// Mock Primary 서버 (새 GSLB 데이터)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(10),
				"checksum": "new-gslb",
				"data": map[string]interface{}{
					"zones":            []map[string]interface{}{},
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
					"gslb_policies": []map[string]interface{}{
						{
							"id":          1,
							"name":        "new-policy",
							"domain":      "new.example.com.",
							"record_type": "AAAA",
							"ttl":         15,
							"enabled":     1,
							"created_at":  "2026-02-01T00:00:00Z",
						},
					},
					"gslb_pools": []map[string]interface{}{
						{
							"id":            1,
							"policy_id":     1,
							"name":          "new-pool",
							"match_type":    "cidr",
							"match_value":   "10.0.0.0/8",
							"priority":      5,
							"fallback_pool": 0,
						},
					},
					"gslb_members": []map[string]interface{}{
						{
							"id":      1,
							"pool_id": 1,
							"address": "10.0.0.1",
							"weight":  80,
							"enabled": 1,
						},
					},
					"health_checks": []map[string]interface{}{
						{
							"id":                  1,
							"policy_id":           1,
							"check_type":          "http",
							"target":              "http://new.example.com/health",
							"interval_sec":        5,
							"timeout_sec":         2,
							"healthy_threshold":   2,
							"unhealthy_threshold": 3,
							"enabled":             1,
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err = worker.fullSync()
	require.NoError(t, err)

	// 기존 GSLB 데이터가 삭제되고 새 데이터로 교체 확인
	var policyCount int
	err = db.Writer.QueryRow("SELECT COUNT(*) FROM gslb_policies").Scan(&policyCount)
	require.NoError(t, err)
	assert.Equal(t, 1, policyCount, "GSLB Policy가 1개만 있어야 함")

	var policyName string
	err = db.Writer.QueryRow("SELECT name FROM gslb_policies WHERE id = 1").Scan(&policyName)
	require.NoError(t, err)
	assert.Equal(t, "new-policy", policyName, "새 Policy로 교체되어야 함")

	// 기존 id=99 데이터 삭제 확인
	var oldPolicyCount int
	err = db.Writer.QueryRow("SELECT COUNT(*) FROM gslb_policies WHERE id = 99").Scan(&oldPolicyCount)
	require.NoError(t, err)
	assert.Equal(t, 0, oldPolicyCount, "기존 Policy가 삭제되어야 함")

	var poolCount int
	err = db.Writer.QueryRow("SELECT COUNT(*) FROM gslb_pools").Scan(&poolCount)
	require.NoError(t, err)
	assert.Equal(t, 1, poolCount)

	var memberCount int
	err = db.Writer.QueryRow("SELECT COUNT(*) FROM gslb_members").Scan(&memberCount)
	require.NoError(t, err)
	assert.Equal(t, 1, memberCount)

	var healthCheckCount int
	err = db.Writer.QueryRow("SELECT COUNT(*) FROM health_checks").Scan(&healthCheckCount)
	require.NoError(t, err)
	assert.Equal(t, 1, healthCheckCount)
}

func TestWorker_IncrementalSync_VersionMismatchTriggersFullSync(t *testing.T) {
	db := setupTestDBFile(t)
	defer func() { _ = db.Close() }()

	// sync_state 버전 설정 (버전 3, Reader에서도 보여야 함)
	_, err := db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 3,
		    data_checksum = 'old'
		WHERE id = 1
	`)
	require.NoError(t, err)

	metadataCalled := false
	fullSyncCalled := false

	// Mock Primary 서버
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sync/metadata":
			metadataCalled = true
			response := map[string]interface{}{
				"version":  int64(7),
				"checksum": "new-check",
			}
			_ = json.NewEncoder(w).Encode(response)
		case "/api/sync/full":
			fullSyncCalled = true
			response := map[string]interface{}{
				"version":  int64(7),
				"checksum": "new-check",
				"data": map[string]interface{}{
					"zones":            []map[string]interface{}{},
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err = worker.incrementalSync()
	require.NoError(t, err)

	assert.True(t, metadataCalled, "Metadata 엔드포인트가 호출되어야 함")
	assert.True(t, fullSyncCalled, "버전 불일치 시 Full Sync가 호출되어야 함")

	// 업데이트된 버전 확인
	var version int64
	err = db.Writer.QueryRow("SELECT last_sync_version FROM sync_state WHERE id = 1").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, int64(7), version)
}

func TestWorker_FullSync_ZoneInsertError(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버 (중복 Zone ID로 삽입 에러 유도)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(1),
				"checksum": "err",
				"data": map[string]interface{}{
					"zones": []map[string]interface{}{
						{
							"id":             1,
							"name":           "dup.com.",
							"soa_mname":      "ns1.dup.com.",
							"soa_rname":      "admin.dup.com.",
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
						{
							"id":             1,
							"name":           "dup2.com.",
							"soa_mname":      "ns1.dup2.com.",
							"soa_rname":      "admin.dup2.com.",
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
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err := worker.fullSync()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "zone 삽입 실패")
}

func TestWorker_FullSync_RecordInsertError(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버 (존재하지 않는 zone_id 참조로 record 삽입 에러)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(1),
				"checksum": "err",
				"data": map[string]interface{}{
					"zones": []map[string]interface{}{},
					"records": []map[string]interface{}{
						{
							"id":         1,
							"zone_id":    999,
							"name":       "bad.example.com.",
							"type":       "A",
							"content":    "10.0.0.1",
							"ttl":        300,
							"priority":   0,
							"enabled":    1,
							"created_at": "2026-01-31T12:00:00Z",
							"updated_at": "2026-01-31T12:00:00Z",
						},
					},
					"upstream_servers": []map[string]interface{}{},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err := worker.fullSync()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "record 삽입 실패")
}

func TestWorker_FullSync_UpstreamIgnored(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버 (중복 upstream ID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(1),
				"checksum": "err",
				"data": map[string]interface{}{
					"zones":   []map[string]interface{}{},
					"records": []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{
						{
							"id":         1,
							"name":       "dns1",
							"address":    "8.8.8.8:53",
							"protocol":   "udp",
							"priority":   10,
							"enabled":    1,
							"created_at": "2026-01-31T12:00:00Z",
							"updated_at": "2026-01-31T12:00:00Z",
						},
						{
							"id":         1,
							"name":       "dns2",
							"address":    "8.8.4.4:53",
							"protocol":   "udp",
							"priority":   20,
							"enabled":    1,
							"created_at": "2026-01-31T12:00:00Z",
							"updated_at": "2026-01-31T12:00:00Z",
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err := worker.fullSync()
	require.NoError(t, err)

	// Upstream은 Secondary 동기화 대상이 아니므로 삽입되지 않아야 함
	var count int
	err = db.Writer.QueryRow("SELECT COUNT(*) FROM upstream_servers").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestWorker_FullSync_GSLBPolicyInsertError(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버 (중복 GSLB Policy ID)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(1),
				"checksum": "err",
				"data": map[string]interface{}{
					"zones":            []map[string]interface{}{},
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
					"gslb_policies": []map[string]interface{}{
						{
							"id":          1,
							"name":        "policy1",
							"domain":      "p1.example.com.",
							"record_type": "A",
							"ttl":         30,
							"enabled":     1,
							"created_at":  "2026-01-31T12:00:00Z",
						},
						{
							"id":          1,
							"name":        "policy2",
							"domain":      "p2.example.com.",
							"record_type": "A",
							"ttl":         30,
							"enabled":     1,
							"created_at":  "2026-01-31T12:00:00Z",
						},
					},
					"gslb_pools":    []map[string]interface{}{},
					"gslb_members":  []map[string]interface{}{},
					"health_checks": []map[string]interface{}{},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err := worker.fullSync()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gslb policy 삽입 실패")
}

func TestWorker_FullSync_GSLBPoolInsertError(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버 (존재하지 않는 policy_id 참조)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(1),
				"checksum": "err",
				"data": map[string]interface{}{
					"zones":            []map[string]interface{}{},
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
					"gslb_policies":    []map[string]interface{}{},
					"gslb_pools": []map[string]interface{}{
						{
							"id":            1,
							"policy_id":     999,
							"name":          "bad-pool",
							"match_type":    "default",
							"match_value":   "*",
							"priority":      0,
							"fallback_pool": 0,
						},
					},
					"gslb_members":  []map[string]interface{}{},
					"health_checks": []map[string]interface{}{},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err := worker.fullSync()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gslb pool 삽입 실패")
}

func TestWorker_FullSync_GSLBMemberInsertError(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버 (존재하지 않는 pool_id 참조)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(1),
				"checksum": "err",
				"data": map[string]interface{}{
					"zones":            []map[string]interface{}{},
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
					"gslb_policies":    []map[string]interface{}{},
					"gslb_pools":       []map[string]interface{}{},
					"gslb_members": []map[string]interface{}{
						{
							"id":      1,
							"pool_id": 999,
							"address": "10.0.0.1",
							"weight":  100,
							"enabled": 1,
						},
					},
					"health_checks": []map[string]interface{}{},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err := worker.fullSync()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gslb member 삽입 실패")
}

func TestWorker_FullSync_HealthCheckInsertError(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버 (존재하지 않는 policy_id 참조)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			response := map[string]interface{}{
				"version":  int64(1),
				"checksum": "err",
				"data": map[string]interface{}{
					"zones":            []map[string]interface{}{},
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
					"gslb_policies":    []map[string]interface{}{},
					"gslb_pools":       []map[string]interface{}{},
					"gslb_members":     []map[string]interface{}{},
					"health_checks": []map[string]interface{}{
						{
							"id":                  1,
							"policy_id":           999,
							"check_type":          "tcp",
							"target":              "bad.example.com:80",
							"interval_sec":        10,
							"timeout_sec":         5,
							"healthy_threshold":   3,
							"unhealthy_threshold": 2,
							"enabled":             1,
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err := worker.fullSync()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "health check 삽입 실패")
}

func TestWorker_Start_InitialFullSyncError(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// 연결 불가능한 서버로 Start (초기 Full Sync 에러 경로)
	worker := NewWorker("http://localhost:99999", db, 10*time.Second)

	worker.Start()

	// 초기 Full Sync 실패 대기 (2초 딜레이 + 실행)
	time.Sleep(3 * time.Second)

	worker.Stop()
	time.Sleep(200 * time.Millisecond)
}

func TestWorker_Start_IncrementalSyncError(t *testing.T) {
	db := setupTestDBFile(t)
	defer func() { _ = db.Close() }()

	// sync_state 버전 설정 (incrementalSync가 metadata 조회하도록)
	_, err := db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 5,
		    data_checksum = 'test'
		WHERE id = 1
	`)
	require.NoError(t, err)

	requestCount := 0

	// Mock 서버: 첫번째 요청(full)은 성공, metadata는 500 에러
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		switch r.URL.Path {
		case "/api/sync/full":
			response := map[string]interface{}{
				"version":  int64(5),
				"checksum": "test",
				"data": map[string]interface{}{
					"zones":            []map[string]interface{}{},
					"records":          []map[string]interface{}{},
					"upstream_servers": []map[string]interface{}{},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		case "/api/sync/metadata":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 200*time.Millisecond)

	worker.Start()

	// 초기 Full Sync (2초 후) + incrementalSync 에러 (200ms 간격)
	time.Sleep(3 * time.Second)

	worker.Stop()
	time.Sleep(200 * time.Millisecond)
}

func TestWorker_FullSync_ReadBodyError(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	// Mock Primary 서버 (응답 본문 읽기 실패 유도)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/full" {
			// Content-Length를 설정하고 일부만 전송하여 io.ReadAll 에러 유도
			w.Header().Set("Content-Length", "99999")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{"))
			// 연결을 강제 종료하기 위해 Hijack 사용
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err := worker.fullSync()
	assert.Error(t, err)
	// Hijack으로 연결 종료시 io.ReadAll 실패 ("응답 읽기 실패") 또는 불완전 JSON ("json 파싱 실패")
	errMsg := err.Error()
	isReadError := len(errMsg) >= len("응답 읽기 실패") && errMsg[:len("응답 읽기 실패")] == "응답 읽기 실패"
	isJSONError := len(errMsg) >= len("json 파싱 실패") && errMsg[:len("json 파싱 실패")] == "json 파싱 실패"
	assert.True(t, isReadError || isJSONError,
		"응답 읽기 또는 JSON 파싱 에러가 발생해야 함: %v", err)
}

func TestWorker_IncrementalSync_MetadataReadBodyError(t *testing.T) {
	db := setupTestDBFile(t)
	defer func() { _ = db.Close() }()

	// sync_state 버전 설정 (Reader에서도 보여야 함)
	_, err := db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 5,
		    data_checksum = 'test'
		WHERE id = 1
	`)
	require.NoError(t, err)

	// Mock Primary 서버 (metadata 응답 본문 읽기 실패 유도)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/metadata" {
			w.Header().Set("Content-Length", "99999")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{"))
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, _ := hj.Hijack()
				_ = conn.Close()
			}
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)

	err = worker.incrementalSync()
	assert.Error(t, err)
	// metadata 응답 읽기 실패 또는 json 파싱 실패
	errMsg := err.Error()
	isReadError := len(errMsg) >= len("응답 읽기 실패") && errMsg[:len("응답 읽기 실패")] == "응답 읽기 실패"
	isJSONError := len(errMsg) >= len("json 파싱 실패") && errMsg[:len("json 파싱 실패")] == "json 파싱 실패"
	assert.True(t, isReadError || isJSONError,
		"응답 읽기 또는 JSON 파싱 에러가 발생해야 함: %v", err)
}

func TestWorker_IncrementalSync_FullSyncViaMismatch_WithGSLBData(t *testing.T) {
	db := setupTestDBFile(t)
	defer func() { _ = db.Close() }()

	// sync_state 버전 설정 (Reader에서도 보여야 함)
	_, err := db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 1,
		    data_checksum = 'old'
		WHERE id = 1
	`)
	require.NoError(t, err)

	callbackCalled := false

	// Mock Primary 서버 (버전 불일치 -> Full Sync with GSLB data)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/sync/metadata":
			response := map[string]interface{}{
				"version":  int64(5),
				"checksum": "new-gslb",
			}
			_ = json.NewEncoder(w).Encode(response)
		case "/api/sync/full":
			response := map[string]interface{}{
				"version":  int64(5),
				"checksum": "new-gslb",
				"data": map[string]interface{}{
					"zones": []map[string]interface{}{
						{
							"id":             1,
							"name":           "incr.example.com.",
							"soa_mname":      "ns1.incr.example.com.",
							"soa_rname":      "admin.incr.example.com.",
							"soa_serial":     5,
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
							"name":       "app.incr.example.com.",
							"type":       "A",
							"content":    "10.0.0.50",
							"ttl":        60,
							"priority":   0,
							"enabled":    1,
							"created_at": "2026-01-31T12:00:00Z",
							"updated_at": "2026-01-31T12:00:00Z",
						},
					},
					"upstream_servers": []map[string]interface{}{
						{
							"id":         1,
							"name":       "DNS1",
							"address":    "1.1.1.1:53",
							"protocol":   "udp",
							"priority":   10,
							"enabled":    1,
							"created_at": "2026-01-31T12:00:00Z",
							"updated_at": "2026-01-31T12:00:00Z",
						},
					},
					"gslb_policies": []map[string]interface{}{
						{
							"id":          1,
							"name":        "incr-policy",
							"domain":      "app.incr.example.com.",
							"record_type": "A",
							"ttl":         15,
							"enabled":     1,
							"created_at":  "2026-01-31T12:00:00Z",
						},
					},
					"gslb_pools": []map[string]interface{}{
						{
							"id":            1,
							"policy_id":     1,
							"name":          "incr-pool",
							"match_type":    "default",
							"match_value":   "*",
							"priority":      0,
							"fallback_pool": 1,
						},
					},
					"gslb_members": []map[string]interface{}{
						{
							"id":      1,
							"pool_id": 1,
							"address": "10.0.0.50",
							"weight":  100,
							"enabled": 1,
						},
					},
					"health_checks": []map[string]interface{}{
						{
							"id":                  1,
							"policy_id":           1,
							"check_type":          "tcp",
							"target":              "10.0.0.50:80",
							"interval_sec":        5,
							"timeout_sec":         3,
							"healthy_threshold":   2,
							"unhealthy_threshold": 3,
							"enabled":             1,
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	worker := NewWorker(server.URL, db, 1*time.Second)
	worker.SetSyncCompleteCallback(func() {
		callbackCalled = true
	})

	// incrementalSync -> metadata check -> version mismatch -> fullSync with GSLB data
	err = worker.incrementalSync()
	require.NoError(t, err)

	assert.True(t, callbackCalled, "콜백이 호출되어야 함")

	// 데이터 확인
	var version int64
	err = db.Writer.QueryRow("SELECT last_sync_version FROM sync_state WHERE id = 1").Scan(&version)
	require.NoError(t, err)
	assert.Equal(t, int64(5), version)

	var policyName string
	err = db.Writer.QueryRow("SELECT name FROM gslb_policies WHERE id = 1").Scan(&policyName)
	require.NoError(t, err)
	assert.Equal(t, "incr-policy", policyName)
}
