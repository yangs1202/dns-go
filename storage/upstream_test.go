package storage

import (
	"dns-go/model"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetUpstreamServer는 ID로 UpstreamServer를 조회하는 테스트입니다
func TestGetUpstreamServer(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)

	// UpstreamServer 삽입
	serverID := insertTestUpstreamServer(t, db, "Google DNS", "8.8.8.8:53", "udp", 10)

	// UpstreamServer 조회
	server, err := storage.GetUpstreamServer(serverID)
	require.NoError(t, err)
	require.NotNil(t, server)
	assert.Equal(t, serverID, server.ID)
	assert.Equal(t, "Google DNS", server.Name)
	assert.Equal(t, "8.8.8.8:53", server.Address)
	assert.Equal(t, "udp", server.Protocol)
	assert.Equal(t, int64(10), server.Priority)
	assert.True(t, server.Enabled)

	// 존재하지 않는 ID
	server, err = storage.GetUpstreamServer(9999)
	require.NoError(t, err)
	assert.Nil(t, server)
}

// TestListUpstreamServers는 전체 UpstreamServer 목록을 조회하는 테스트입니다 (L2 캐시 활용, priority 오름차순)
func TestListUpstreamServers(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)

	// UpstreamServer 삽입 (우선순위 역순으로 삽입)
	insertTestUpstreamServer(t, db, "Cloudflare DNS", "1.1.1.1:53", "udp", 20)
	insertTestUpstreamServer(t, db, "Google DNS", "8.8.8.8:53", "udp", 10)
	insertTestUpstreamServer(t, db, "Quad9 DNS", "9.9.9.9:53", "tcp", 30)

	// 목록 조회 - 캐시 미스 (DB 조회)
	servers, err := storage.ListUpstreamServers()
	require.NoError(t, err)
	require.Len(t, servers, 3)

	// priority 오름차순 정렬 확인
	assert.Equal(t, "Google DNS", servers[0].Name)
	assert.Equal(t, int64(10), servers[0].Priority)
	assert.Equal(t, "Cloudflare DNS", servers[1].Name)
	assert.Equal(t, int64(20), servers[1].Priority)
	assert.Equal(t, "Quad9 DNS", servers[2].Name)
	assert.Equal(t, int64(30), servers[2].Priority)

	// 캐시가 업데이트되었는지 확인
	cachedServers, ok := storage.cache.Get()
	assert.True(t, ok)
	require.Len(t, cachedServers, 3)
	assert.Equal(t, "Google DNS", cachedServers[0].Name)

	// 두 번째 조회 - 캐시 히트
	servers2, err := storage.ListUpstreamServers()
	require.NoError(t, err)
	require.Len(t, servers2, 3)
	assert.Equal(t, servers[0].ID, servers2[0].ID)
}

// TestListEnabledUpstreamServers는 활성화된 UpstreamServer만 조회하는 테스트입니다 (L2 캐시 활용)
func TestListEnabledUpstreamServers(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)

	// UpstreamServer 삽입 (활성화/비활성화 혼합)
	insertTestUpstreamServer(t, db, "Google DNS", "8.8.8.8:53", "udp", 10)
	insertTestUpstreamServerWithEnabled(t, db, "Cloudflare DNS", "1.1.1.1:53", "udp", 20, false)
	insertTestUpstreamServer(t, db, "Quad9 DNS", "9.9.9.9:53", "tcp", 30)

	// 활성화된 서버만 조회 - 캐시 미스 (DB 조회)
	enabled, err := storage.ListEnabledUpstreamServers()
	require.NoError(t, err)
	require.Len(t, enabled, 2)
	assert.Equal(t, "Google DNS", enabled[0].Name)
	assert.Equal(t, "Quad9 DNS", enabled[1].Name)
	assert.True(t, enabled[0].Enabled)
	assert.True(t, enabled[1].Enabled)

	// 전체 목록 조회로 캐시 업데이트
	_, err = storage.ListUpstreamServers()
	require.NoError(t, err)

	// 활성화된 서버만 조회 - 캐시 히트 (필터링)
	enabled2, err := storage.ListEnabledUpstreamServers()
	require.NoError(t, err)
	require.Len(t, enabled2, 2)
	assert.Equal(t, "Google DNS", enabled2[0].Name)
	assert.Equal(t, "Quad9 DNS", enabled2[1].Name)

	// 캐시 무효화 후 조회
	storage.cache.Invalidate()
	enabled3, err := storage.ListEnabledUpstreamServers()
	require.NoError(t, err)
	require.Len(t, enabled3, 2)
}

// TestCreateUpstreamServer는 UpstreamServer 생성 테스트입니다 (캐시 무효화)
func TestCreateUpstreamServer(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)

	// 캐시 초기화
	servers, err := storage.ListUpstreamServers()
	require.NoError(t, err)
	require.Empty(t, servers)

	// UpstreamServer 생성
	server := &model.UpstreamServer{
		Name:     "Google DNS",
		Address:  "8.8.8.8:53",
		Protocol: "udp",
		Priority: 10,
		Enabled:  true,
	}

	id, err := storage.CreateUpstreamServer(server)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	// DB에서 확인
	created, err := storage.GetUpstreamServer(id)
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, "Google DNS", created.Name)
	assert.Equal(t, "8.8.8.8:53", created.Address)
	assert.Equal(t, "udp", created.Protocol)
	assert.Equal(t, int64(10), created.Priority)
	assert.True(t, created.Enabled)

	// 캐시가 무효화되었는지 확인
	_, ok := storage.cache.Get()
	assert.False(t, ok)

	// 다른 프로토콜로 생성
	tcpServer := &model.UpstreamServer{
		Name:     "Cloudflare DNS TLS",
		Address:  "1.1.1.1:853",
		Protocol: "tcp-tls",
		Priority: 20,
		Enabled:  false,
	}

	tcpID, err := storage.CreateUpstreamServer(tcpServer)
	require.NoError(t, err)

	tcpCreated, err := storage.GetUpstreamServer(tcpID)
	require.NoError(t, err)
	assert.Equal(t, "tcp-tls", tcpCreated.Protocol)
	assert.False(t, tcpCreated.Enabled)
}

// TestUpdateUpstreamServer는 UpstreamServer 업데이트 테스트입니다 (캐시 무효화)
func TestUpdateUpstreamServer(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)

	// UpstreamServer 생성
	server := &model.UpstreamServer{
		Name:     "Google DNS",
		Address:  "8.8.8.8:53",
		Protocol: "udp",
		Priority: 10,
		Enabled:  true,
	}

	id, err := storage.CreateUpstreamServer(server)
	require.NoError(t, err)

	// 캐시 업데이트
	_, err = storage.ListUpstreamServers()
	require.NoError(t, err)

	// UpstreamServer 업데이트
	updated := &model.UpstreamServer{
		ID:       id,
		Name:     "Cloudflare DNS",
		Address:  "1.1.1.1:53",
		Protocol: "tcp",
		Priority: 5,
		Enabled:  false,
	}

	err = storage.UpdateUpstreamServer(updated)
	require.NoError(t, err)

	// 업데이트 확인
	result, err := storage.GetUpstreamServer(id)
	require.NoError(t, err)
	assert.Equal(t, "Cloudflare DNS", result.Name)
	assert.Equal(t, "1.1.1.1:53", result.Address)
	assert.Equal(t, "tcp", result.Protocol)
	assert.Equal(t, int64(5), result.Priority)
	assert.False(t, result.Enabled)

	// 캐시가 무효화되었는지 확인
	_, ok := storage.cache.Get()
	assert.False(t, ok)

	// 존재하지 않는 UpstreamServer 업데이트
	nonExistent := &model.UpstreamServer{
		ID:      9999,
		Name:    "Not Found",
		Address: "0.0.0.0:53",
	}

	err = storage.UpdateUpstreamServer(nonExistent)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "UpstreamServer를 찾을 수 없습니다")
}

// TestDeleteUpstreamServer는 UpstreamServer 삭제 테스트입니다 (캐시 무효화)
func TestDeleteUpstreamServer(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)

	// UpstreamServer 생성
	server := &model.UpstreamServer{
		Name:    "Google DNS",
		Address: "8.8.8.8:53",
		Enabled: true,
	}

	id, err := storage.CreateUpstreamServer(server)
	require.NoError(t, err)

	// 캐시 업데이트
	_, err = storage.ListUpstreamServers()
	require.NoError(t, err)

	// UpstreamServer 삭제
	err = storage.DeleteUpstreamServer(id)
	require.NoError(t, err)

	// 삭제 확인
	deleted, err := storage.GetUpstreamServer(id)
	require.NoError(t, err)
	assert.Nil(t, deleted)

	// 캐시가 무효화되었는지 확인
	_, ok := storage.cache.Get()
	assert.False(t, ok)

	// 존재하지 않는 UpstreamServer 삭제
	err = storage.DeleteUpstreamServer(9999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "UpstreamServer를 찾을 수 없습니다")
}

// TestUpstreamCache_TTL은 캐시 TTL 만료 테스트입니다
func TestUpstreamCache_TTL(t *testing.T) {
	db := setupTestDB(t)

	// 짧은 TTL로 캐시 생성 (1초)
	cache := NewUpstreamCache(1 * time.Second)
	storage := &UpstreamStorage{
		db:    db,
		cache: cache,
	}

	// UpstreamServer 생성
	insertTestUpstreamServer(t, db, "Google DNS", "8.8.8.8:53", "udp", 10)

	// 캐시 업데이트
	servers, err := storage.ListUpstreamServers()
	require.NoError(t, err)
	require.Len(t, servers, 1)

	// 캐시 히트 확인
	cached, ok := storage.cache.Get()
	assert.True(t, ok)
	assert.Len(t, cached, 1)

	// TTL 만료 대기
	time.Sleep(1100 * time.Millisecond)

	// 캐시 미스 확인 (TTL 만료)
	cached, ok = storage.cache.Get()
	assert.False(t, ok)
	assert.Nil(t, cached)

	// 다시 캐시 업데이트
	servers, err = storage.ListUpstreamServers()
	require.NoError(t, err)
	require.Len(t, servers, 1)

	// 캐시 히트 확인
	cached, ok = storage.cache.Get()
	assert.True(t, ok)
	assert.Len(t, cached, 1)
}

// TestUpstreamCache_Concurrency는 캐시 동시성 테스트입니다
func TestUpstreamCache_Concurrency(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)

	// 테스트 UpstreamServer 생성
	for i := 1; i <= 10; i++ {
		insertTestUpstreamServer(t, db, fmt.Sprintf("DNS Server %d", i), fmt.Sprintf("10.0.0.%d:53", i), "udp", int64(i*10))
	}

	// 캐시 초기화
	servers, err := storage.ListUpstreamServers()
	require.NoError(t, err)
	require.Len(t, servers, 10)

	// 동시 읽기 테스트
	var wg sync.WaitGroup
	readCount := 50

	for i := 0; i < readCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 읽기
			cached, ok := storage.cache.Get()
			assert.True(t, ok)
			assert.Len(t, cached, 10)
		}(i)
	}

	wg.Wait()

	// 동시 쓰기 테스트 (캐시 무효화)
	writeCount := 10

	for i := 0; i < writeCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 무효화
			storage.cache.Invalidate()
		}(i)
	}

	wg.Wait()

	// 캐시가 무효화되었는지 확인
	_, ok := storage.cache.Get()
	assert.False(t, ok)

	// 동시 읽기/쓰기 혼합 테스트
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 업데이트
			_, err := storage.ListUpstreamServers()
			assert.NoError(t, err)
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 읽기
			storage.cache.Get()
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 무효화
			storage.cache.Invalidate()
		}(i)
	}

	wg.Wait()
}

// TestUpstreamCache_Set은 캐시 Set 메서드 테스트입니다
func TestUpstreamCache_Set(t *testing.T) {
	cache := NewUpstreamCache(10 * time.Minute)

	// 빈 캐시
	servers, ok := cache.Get()
	assert.False(t, ok)
	assert.Nil(t, servers)

	// UpstreamServer 추가
	testServers := []*model.UpstreamServer{
		{ID: 1, Name: "Google DNS", Address: "8.8.8.8:53", Priority: 10},
		{ID: 2, Name: "Cloudflare DNS", Address: "1.1.1.1:53", Priority: 20},
	}

	cache.Set(testServers)

	// 캐시 히트
	servers, ok = cache.Get()
	assert.True(t, ok)
	require.Len(t, servers, 2)
	assert.Equal(t, int64(1), servers[0].ID)
	assert.Equal(t, "Google DNS", servers[0].Name)
	assert.Equal(t, int64(2), servers[1].ID)
	assert.Equal(t, "Cloudflare DNS", servers[1].Name)
}

// TestUpstreamCache_Invalidate는 캐시 무효화 테스트입니다
func TestUpstreamCache_Invalidate(t *testing.T) {
	cache := NewUpstreamCache(10 * time.Minute)

	// UpstreamServer 추가
	testServers := []*model.UpstreamServer{
		{ID: 1, Name: "Google DNS", Address: "8.8.8.8:53"},
	}
	cache.Set(testServers)

	// 캐시 히트
	servers, ok := cache.Get()
	assert.True(t, ok)
	assert.Len(t, servers, 1)

	// 캐시 무효화
	cache.Invalidate()

	// 캐시 미스
	servers, ok = cache.Get()
	assert.False(t, ok)
	assert.Nil(t, servers)
}

// TestNewUpstreamStorage는 UpstreamStorage 생성 테스트입니다
func TestNewUpstreamStorage(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)

	assert.NotNil(t, storage)
	assert.NotNil(t, storage.db)
	assert.NotNil(t, storage.cache)
	assert.Equal(t, 10*time.Minute, storage.cache.ttl)
}

// TestNewUpstreamCache는 UpstreamCache 생성 테스트입니다
func TestNewUpstreamCache(t *testing.T) {
	ttl := 15 * time.Minute
	cache := NewUpstreamCache(ttl)

	assert.NotNil(t, cache)
	assert.NotNil(t, cache.servers)
	assert.Equal(t, ttl, cache.ttl)
	assert.True(t, cache.expiry.IsZero())
	assert.Empty(t, cache.servers)
}

// === Error path tests (using closed DB) ===

func TestGetUpstreamServer_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)
	_ = db.Reader.Close()

	_, err := storage.GetUpstreamServer(1)
	assert.Error(t, err)
}

func TestListUpstreamServers_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)
	_ = db.Reader.Close()

	_, err := storage.ListUpstreamServers()
	assert.Error(t, err)
}

func TestListEnabledUpstreamServers_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)
	_ = db.Reader.Close()

	_, err := storage.ListEnabledUpstreamServers()
	assert.Error(t, err)
}

func TestCreateUpstreamServer_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)
	_ = db.Writer.Close()

	server := &model.UpstreamServer{Name: "Test", Address: "1.2.3.4:53", Protocol: "udp", Enabled: true}
	_, err := storage.CreateUpstreamServer(server)
	assert.Error(t, err)
}

func TestUpdateUpstreamServer_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)
	_ = db.Writer.Close()

	server := &model.UpstreamServer{ID: 1, Name: "Test", Address: "1.2.3.4:53", Protocol: "udp", Enabled: true}
	err := storage.UpdateUpstreamServer(server)
	assert.Error(t, err)
}

func TestDeleteUpstreamServer_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewUpstreamStorage(db)
	_ = db.Writer.Close()

	err := storage.DeleteUpstreamServer(1)
	assert.Error(t, err)
}

// 헬퍼 함수: 테스트 UpstreamServer 삽입 (활성화)
func insertTestUpstreamServer(t *testing.T, db *Database, name, address, protocol string, priority int64) int64 {
	return insertTestUpstreamServerWithEnabled(t, db, name, address, protocol, priority, true)
}

// 헬퍼 함수: 테스트 UpstreamServer 삽입 (활성화 여부 지정)
func insertTestUpstreamServerWithEnabled(t *testing.T, db *Database, name, address, protocol string, priority int64, enabled bool) int64 {
	result, err := db.Writer.Exec(
		"INSERT INTO upstream_servers (name, address, protocol, priority, enabled) VALUES (?, ?, ?, ?, ?)",
		name, address, protocol, priority, enabled,
	)
	require.NoError(t, err)

	id, err := result.LastInsertId()
	require.NoError(t, err)

	return id
}
