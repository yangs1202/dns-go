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

// TestGetZone은 ID로 Zone을 조회하는 테스트입니다
func TestGetZone(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)

	// Zone 삽입
	zoneID := insertTestZone(t, db, "example.com.")

	// Zone 조회
	zone, err := storage.GetZone(zoneID)
	require.NoError(t, err)
	require.NotNil(t, zone)
	assert.Equal(t, zoneID, zone.ID)
	assert.Equal(t, "example.com.", zone.Name)
	assert.True(t, zone.Enabled)

	// 존재하지 않는 ID
	zone, err = storage.GetZone(9999)
	require.NoError(t, err)
	assert.Nil(t, zone)
}

// TestGetZoneByName은 이름으로 Zone을 조회하는 테스트입니다 (캐시 히트/미스)
func TestGetZoneByName(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)

	// Zone 삽입
	_, err := db.Writer.Exec(`INSERT INTO zones (name, soa_mname, soa_rname, enabled)
	                          VALUES (?, ?, ?, ?)`, "example.com.", "ns1.example.com.", "admin.example.com.", 1)
	require.NoError(t, err)

	// 첫 번째 조회 - 캐시 미스 (DB 조회)
	zone, err := storage.GetZoneByName("example.com.")
	require.NoError(t, err)
	require.NotNil(t, zone)
	assert.Equal(t, "example.com.", zone.Name)
	assert.Equal(t, "ns1.example.com.", zone.SOAMname)
	assert.Equal(t, "admin.example.com.", zone.SOARname)

	// ListZones로 캐시 업데이트
	zones, err := storage.ListZones()
	require.NoError(t, err)
	require.Len(t, zones, 1)

	// 두 번째 조회 - 캐시 히트
	zone, err = storage.GetZoneByName("example.com.")
	require.NoError(t, err)
	require.NotNil(t, zone)
	assert.Equal(t, "example.com.", zone.Name)

	// 비활성화된 Zone은 조회되지 않음
	_, err = db.Writer.Exec("UPDATE zones SET enabled = 0 WHERE name = ?", "example.com.")
	require.NoError(t, err)

	// 캐시 무효화
	storage.cache.Invalidate()

	zone, err = storage.GetZoneByName("example.com.")
	require.NoError(t, err)
	assert.Nil(t, zone)

	// 존재하지 않는 Zone
	zone, err = storage.GetZoneByName("notfound.com.")
	require.NoError(t, err)
	assert.Nil(t, zone)
}

// TestListZones는 전체 Zone 목록을 조회하는 테스트입니다 (캐시 업데이트)
func TestListZones(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)

	// 빈 목록
	zones, err := storage.ListZones()
	require.NoError(t, err)
	assert.Empty(t, zones)

	// Zone 삽입
	insertTestZone(t, db, "example.com.")
	insertTestZone(t, db, "test.com.")
	insertTestZone(t, db, "another.com.")

	// 목록 조회
	zones, err = storage.ListZones()
	require.NoError(t, err)
	require.Len(t, zones, 3)

	// 이름순 정렬 확인
	assert.Equal(t, "another.com.", zones[0].Name)
	assert.Equal(t, "example.com.", zones[1].Name)
	assert.Equal(t, "test.com.", zones[2].Name)

	// 캐시가 업데이트되었는지 확인
	zone, ok := storage.cache.Get("example.com.")
	assert.True(t, ok)
	assert.NotNil(t, zone)
	assert.Equal(t, "example.com.", zone.Name)
}

// TestCreateZone은 Zone 생성 테스트입니다 (캐시 무효화)
func TestCreateZone(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)

	// 캐시 초기화
	zones, err := storage.ListZones()
	require.NoError(t, err)
	require.Empty(t, zones)

	// Zone 생성 (기본값 자동 설정)
	zone := &model.Zone{
		Name:     "example.com.",
		SOAMname: "ns1.example.com.",
		SOARname: "admin.example.com.",
		Enabled:  true,
	}

	id, err := storage.CreateZone(zone)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	// DB에서 확인
	created, err := storage.GetZone(id)
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, "example.com.", created.Name)
	assert.Equal(t, "ns1.example.com.", created.SOAMname)
	assert.Equal(t, "admin.example.com.", created.SOARname)
	assert.Equal(t, int64(1), created.SOASerial)
	assert.Equal(t, int64(3600), created.SOARefresh)
	assert.Equal(t, int64(900), created.SOARetry)
	assert.Equal(t, int64(86400), created.SOAExpire)
	assert.Equal(t, int64(300), created.SOAMinimum)
	assert.True(t, created.Enabled)

	// 캐시가 무효화되었는지 확인
	_, ok := storage.cache.Get("example.com.")
	assert.False(t, ok)

	// 사용자 정의 값으로 생성
	customZone := &model.Zone{
		Name:       "custom.com.",
		SOAMname:   "ns1.custom.com.",
		SOARname:   "admin.custom.com.",
		SOASerial:  100,
		SOARefresh: 7200,
		SOARetry:   1800,
		SOAExpire:  172800,
		SOAMinimum: 600,
		Enabled:    false,
	}

	customID, err := storage.CreateZone(customZone)
	require.NoError(t, err)

	custom, err := storage.GetZone(customID)
	require.NoError(t, err)
	assert.Equal(t, int64(100), custom.SOASerial)
	assert.Equal(t, int64(7200), custom.SOARefresh)
	assert.False(t, custom.Enabled)
}

// TestUpdateZone은 Zone 업데이트 테스트입니다 (캐시 무효화)
func TestUpdateZone(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)

	// Zone 생성
	zone := &model.Zone{
		Name:     "example.com.",
		SOAMname: "ns1.example.com.",
		SOARname: "admin.example.com.",
		Enabled:  true,
	}

	id, err := storage.CreateZone(zone)
	require.NoError(t, err)

	// 캐시 업데이트
	_, err = storage.ListZones()
	require.NoError(t, err)

	// Zone 업데이트
	updated := &model.Zone{
		ID:         id,
		Name:       "updated.com.",
		SOAMname:   "ns2.updated.com.",
		SOARname:   "admin.updated.com.",
		SOASerial:  2,
		SOARefresh: 7200,
		SOARetry:   1800,
		SOAExpire:  172800,
		SOAMinimum: 600,
		Enabled:    false,
	}

	err = storage.UpdateZone(updated)
	require.NoError(t, err)

	// 업데이트 확인
	result, err := storage.GetZone(id)
	require.NoError(t, err)
	assert.Equal(t, "updated.com.", result.Name)
	assert.Equal(t, "ns2.updated.com.", result.SOAMname)
	assert.Equal(t, "admin.updated.com.", result.SOARname)
	assert.Equal(t, int64(2), result.SOASerial)
	assert.Equal(t, int64(7200), result.SOARefresh)
	assert.False(t, result.Enabled)

	// 캐시가 무효화되었는지 확인
	_, ok := storage.cache.Get("example.com.")
	assert.False(t, ok)

	// 존재하지 않는 Zone 업데이트
	nonExistent := &model.Zone{
		ID:   9999,
		Name: "notfound.com.",
	}

	err = storage.UpdateZone(nonExistent)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "zone을 찾을 수 없습니다")
}

// TestDeleteZone은 Zone 삭제 테스트입니다 (캐시 무효화)
func TestDeleteZone(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)

	// Zone 생성
	zone := &model.Zone{
		Name:    "example.com.",
		Enabled: true,
	}

	id, err := storage.CreateZone(zone)
	require.NoError(t, err)

	// 캐시 업데이트
	_, err = storage.ListZones()
	require.NoError(t, err)

	// Zone 삭제
	err = storage.DeleteZone(id)
	require.NoError(t, err)

	// 삭제 확인
	deleted, err := storage.GetZone(id)
	require.NoError(t, err)
	assert.Nil(t, deleted)

	// 캐시가 무효화되었는지 확인
	_, ok := storage.cache.Get("example.com.")
	assert.False(t, ok)

	// 존재하지 않는 Zone 삭제
	err = storage.DeleteZone(9999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "zone을 찾을 수 없습니다")

	// CASCADE 삭제 테스트 (레코드도 함께 삭제)
	zoneID := insertTestZone(t, db, "cascade.com.")
	recordID := insertTestRecord(t, db, zoneID, "www.cascade.com.", "A", "192.0.2.1")

	err = storage.DeleteZone(zoneID)
	require.NoError(t, err)

	// 레코드도 삭제되었는지 확인
	var count int
	err = db.Reader.QueryRow("SELECT COUNT(*) FROM records WHERE id = ?", recordID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// TestZoneCache_TTL은 캐시 TTL 만료 테스트입니다
func TestZoneCache_TTL(t *testing.T) {
	db := setupTestDB(t)

	// 짧은 TTL로 캐시 생성 (1초)
	cache := NewZoneCache(1 * time.Second)
	storage := &ZoneStorage{
		db:    db,
		cache: cache,
	}

	// Zone 생성
	insertTestZone(t, db, "example.com.")

	// 캐시 업데이트
	zones, err := storage.ListZones()
	require.NoError(t, err)
	require.Len(t, zones, 1)

	// 캐시 히트 확인
	zone, ok := storage.cache.Get("example.com.")
	assert.True(t, ok)
	assert.NotNil(t, zone)

	// TTL 만료 대기
	time.Sleep(1100 * time.Millisecond)

	// 캐시 미스 확인 (TTL 만료)
	zone, ok = storage.cache.Get("example.com.")
	assert.False(t, ok)
	assert.Nil(t, zone)

	// 다시 캐시 업데이트
	zones, err = storage.ListZones()
	require.NoError(t, err)
	require.Len(t, zones, 1)

	// 캐시 히트 확인
	zone, ok = storage.cache.Get("example.com.")
	assert.True(t, ok)
	assert.NotNil(t, zone)
}

// TestZoneCache_Concurrency는 캐시 동시성 테스트입니다
func TestZoneCache_Concurrency(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)

	// 테스트 Zone 생성
	for i := 1; i <= 10; i++ {
		insertTestZone(t, db, fmt.Sprintf("example%d.com.", i))
	}

	// 캐시 초기화
	zones, err := storage.ListZones()
	require.NoError(t, err)
	require.Len(t, zones, 10)

	// 동시 읽기 테스트
	var wg sync.WaitGroup
	readCount := 50

	for i := 0; i < readCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 읽기
			zoneName := "example1.com."
			zone, ok := storage.cache.Get(zoneName)
			assert.True(t, ok)
			assert.NotNil(t, zone)
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
	_, ok := storage.cache.Get("example1.com.")
	assert.False(t, ok)

	// 동시 읽기/쓰기 혼합 테스트
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 업데이트
			_, err := storage.ListZones()
			assert.NoError(t, err)
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 읽기
			storage.cache.Get("example1.com.")
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

// TestZoneCache_Set은 캐시 Set 메서드 테스트입니다
func TestZoneCache_Set(t *testing.T) {
	cache := NewZoneCache(5 * time.Minute)

	// 빈 캐시
	zone, ok := cache.Get("example.com.")
	assert.False(t, ok)
	assert.Nil(t, zone)

	// Zone 추가
	zones := []*model.Zone{
		{ID: 1, Name: "example.com."},
		{ID: 2, Name: "test.com."},
	}

	cache.Set(zones)

	// 캐시 히트
	zone, ok = cache.Get("example.com.")
	assert.True(t, ok)
	assert.NotNil(t, zone)
	assert.Equal(t, int64(1), zone.ID)

	zone, ok = cache.Get("test.com.")
	assert.True(t, ok)
	assert.NotNil(t, zone)
	assert.Equal(t, int64(2), zone.ID)

	// 존재하지 않는 Zone
	zone, ok = cache.Get("notfound.com.")
	assert.False(t, ok)
	assert.Nil(t, zone)
}

// TestZoneCache_Invalidate는 캐시 무효화 테스트입니다
func TestZoneCache_Invalidate(t *testing.T) {
	cache := NewZoneCache(5 * time.Minute)

	// Zone 추가
	zones := []*model.Zone{
		{ID: 1, Name: "example.com."},
	}
	cache.Set(zones)

	// 캐시 히트
	zone, ok := cache.Get("example.com.")
	assert.True(t, ok)
	assert.NotNil(t, zone)

	// 캐시 무효화
	cache.Invalidate()

	// 캐시 미스
	zone, ok = cache.Get("example.com.")
	assert.False(t, ok)
	assert.Nil(t, zone)
}

// TestNewZoneStorage는 ZoneStorage 생성 테스트입니다
func TestNewZoneStorage(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)

	assert.NotNil(t, storage)
	assert.NotNil(t, storage.db)
	assert.NotNil(t, storage.cache)
	assert.Equal(t, 5*time.Minute, storage.cache.ttl)
}

// TestNewZoneCache는 ZoneCache 생성 테스트입니다
func TestNewZoneCache(t *testing.T) {
	ttl := 10 * time.Minute
	cache := NewZoneCache(ttl)

	assert.NotNil(t, cache)
	assert.NotNil(t, cache.zones)
	assert.Equal(t, ttl, cache.ttl)
	assert.True(t, cache.expiry.IsZero())
}

// === Error path tests (using closed DB) ===

func TestGetZone_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)
	_ = db.Reader.Close()

	_, err := storage.GetZone(1)
	assert.Error(t, err)
}

func TestGetZoneByName_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)
	_ = db.Reader.Close()

	_, err := storage.GetZoneByName("example.com.")
	assert.Error(t, err)
}

func TestListZones_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)
	_ = db.Reader.Close()

	_, err := storage.ListZones()
	assert.Error(t, err)
}

func TestCreateZone_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)
	_ = db.Writer.Close()

	zone := &model.Zone{Name: "test.com.", Enabled: true}
	_, err := storage.CreateZone(zone)
	assert.Error(t, err)
}

func TestUpdateZone_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)
	_ = db.Writer.Close()

	zone := &model.Zone{ID: 1, Name: "test.com.", Enabled: true}
	err := storage.UpdateZone(zone)
	assert.Error(t, err)
}

func TestDeleteZone_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)
	_ = db.Writer.Close()

	err := storage.DeleteZone(1)
	assert.Error(t, err)
}

// TestZoneStorage_ClearCache는 Zone 캐시 클리어 테스트입니다
func TestZoneStorage_ClearCache(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)

	// Zone 생성 및 캐시 업데이트
	insertTestZone(t, db, "example.com.")
	_, err := storage.ListZones()
	require.NoError(t, err)

	// 캐시 히트 확인
	zone, ok := storage.cache.Get("example.com.")
	assert.True(t, ok)
	assert.NotNil(t, zone)

	// ClearCache 호출
	storage.ClearCache()

	// 캐시 미스 확인
	zone, ok = storage.cache.Get("example.com.")
	assert.False(t, ok)
	assert.Nil(t, zone)
}

// TestZoneStorage_ClearCache_NilCache는 nil 캐시에서도 안전한지 테스트합니다
func TestZoneStorage_ClearCache_NilCache(t *testing.T) {
	db := setupTestDB(t)
	storage := &ZoneStorage{
		db:    db,
		cache: nil,
	}

	// Should not panic
	storage.ClearCache()
}

// TestCreateZone_VersionIncrement는 Zone 생성 시 버전 증가를 테스트합니다
func TestCreateZone_VersionIncrement(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)
	syncVersion := NewSyncVersion(db)

	// 초기 버전 확인
	version, err := syncVersion.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, int64(0), version)

	// Zone 생성
	zone := &model.Zone{
		Name:       "test.com.",
		SOAMname:   "ns1.test.com.",
		SOARname:   "admin.test.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}

	_, err = storage.CreateZone(zone)
	require.NoError(t, err)

	// 버전 증가 확인
	version, err = syncVersion.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, int64(1), version, "Zone 생성 시 버전이 증가해야 함")
}

// TestUpdateZone_VersionIncrement는 Zone 업데이트 시 버전 증가를 테스트합니다
func TestUpdateZone_VersionIncrement(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)
	syncVersion := NewSyncVersion(db)

	// Zone 생성
	zone := &model.Zone{
		Name:       "test.com.",
		SOAMname:   "ns1.test.com.",
		SOARname:   "admin.test.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}

	id, err := storage.CreateZone(zone)
	require.NoError(t, err)

	// 현재 버전 확인
	version, err := syncVersion.GetVersion()
	require.NoError(t, err)

	// Zone 업데이트
	zone.ID = id
	zone.SOASerial = 2
	err = storage.UpdateZone(zone)
	require.NoError(t, err)

	// 버전 증가 확인
	newVersion, err := syncVersion.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, version+1, newVersion, "Zone 업데이트 시 버전이 증가해야 함")
}

// TestDeleteZone_VersionIncrement는 Zone 삭제 시 버전 증가를 테스트합니다
func TestDeleteZone_VersionIncrement(t *testing.T) {
	db := setupTestDB(t)
	storage := NewZoneStorage(db)
	syncVersion := NewSyncVersion(db)

	// Zone 생성
	zone := &model.Zone{
		Name:       "test.com.",
		SOAMname:   "ns1.test.com.",
		SOARname:   "admin.test.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}

	id, err := storage.CreateZone(zone)
	require.NoError(t, err)

	// 현재 버전 확인
	version, err := syncVersion.GetVersion()
	require.NoError(t, err)

	// Zone 삭제
	err = storage.DeleteZone(id)
	require.NoError(t, err)

	// 버전 증가 확인
	newVersion, err := syncVersion.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, version+1, newVersion, "Zone 삭제 시 버전이 증가해야 함")
}

// TestCreateZone_TransactionRollback는 트랜잭션 롤백 테스트입니다
func TestCreateZone_TransactionRollback(t *testing.T) {
	db := setupTestDB(t)
	syncVersion := NewSyncVersion(db)

	// 초기 버전
	initialVersion, err := syncVersion.GetVersion()
	require.NoError(t, err)

	// 잘못된 Zone 생성 시도 (중복 이름)
	storage := NewZoneStorage(db)

	zone1 := &model.Zone{
		Name:       "duplicate.com.",
		SOAMname:   "ns1.test.com.",
		SOARname:   "admin.test.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}

	_, err = storage.CreateZone(zone1)
	require.NoError(t, err)

	// 중복 생성 시도
	zone2 := &model.Zone{
		Name:       "duplicate.com.",
		SOAMname:   "ns2.test.com.",
		SOARname:   "admin2.test.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}

	_, err = storage.CreateZone(zone2)
	assert.Error(t, err, "중복 Zone 생성은 실패해야 함")

	// 버전 확인 (실패한 트랜잭션은 버전 증가 안 함)
	currentVersion, err := syncVersion.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, initialVersion+1, currentVersion, "성공한 트랜잭션만 버전 증가")
}
