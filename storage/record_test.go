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

// TestGetRecord는 ID로 Record를 조회하는 테스트입니다
func TestGetRecord(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone과 Record 삽입
	zoneID := insertTestZone(t, db, "example.com.")
	recordID := insertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")

	// Record 조회
	record, err := storage.GetRecord(recordID)
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, recordID, record.ID)
	assert.Equal(t, zoneID, record.ZoneID)
	assert.Equal(t, "www.example.com.", record.Name)
	assert.Equal(t, "A", record.Type)
	assert.Equal(t, "192.0.2.1", record.Content)

	// 존재하지 않는 ID
	record, err = storage.GetRecord(9999)
	require.NoError(t, err)
	assert.Nil(t, record)
}

// TestGetRecordsByZone은 특정 Zone의 모든 Record를 조회하는 테스트입니다 (L2 캐시 활용)
func TestGetRecordsByZone(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "example.com.")

	// Record 삽입
	insertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")
	insertTestRecord(t, db, zoneID, "www.example.com.", "AAAA", "2001:db8::1")
	insertTestRecord(t, db, zoneID, "mail.example.com.", "A", "192.0.2.2")

	// 첫 번째 조회 - 캐시 미스 (DB 조회)
	records, err := storage.GetRecordsByZone(zoneID)
	require.NoError(t, err)
	require.Len(t, records, 3)

	// 이름순, 타입순 정렬 확인
	assert.Equal(t, "mail.example.com.", records[0].Name)
	assert.Equal(t, "A", records[0].Type)
	assert.Equal(t, "www.example.com.", records[1].Name)
	assert.Equal(t, "A", records[1].Type)
	assert.Equal(t, "www.example.com.", records[2].Name)
	assert.Equal(t, "AAAA", records[2].Type)

	// 두 번째 조회 - 캐시 히트
	cachedRecords, err := storage.GetRecordsByZone(zoneID)
	require.NoError(t, err)
	require.Len(t, cachedRecords, 3)
	assert.Equal(t, records[0].ID, cachedRecords[0].ID)

	// 존재하지 않는 Zone
	records, err = storage.GetRecordsByZone(9999)
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestGetRecordsByName은 이름과 타입으로 Record를 조회하는 테스트입니다
func TestGetRecordsByName(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "example.com.")

	// Record 삽입 (enabled=1)
	_, err := db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, priority, enabled)
	                          VALUES (?, ?, ?, ?, ?, ?)`, zoneID, "www.example.com.", "A", "192.0.2.1", 10, 1)
	require.NoError(t, err)

	_, err = db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, priority, enabled)
	                          VALUES (?, ?, ?, ?, ?, ?)`, zoneID, "www.example.com.", "A", "192.0.2.2", 20, 1)
	require.NoError(t, err)

	// 비활성화된 Record (조회되지 않아야 함)
	_, err = db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, priority, enabled)
	                          VALUES (?, ?, ?, ?, ?, ?)`, zoneID, "www.example.com.", "A", "192.0.2.3", 5, 0)
	require.NoError(t, err)

	// 다른 타입
	_, err = db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "www.example.com.", "AAAA", "2001:db8::1", 1)
	require.NoError(t, err)

	// 이름과 타입으로 조회
	records, err := storage.GetRecordsByName("www.example.com.", "A")
	require.NoError(t, err)
	require.Len(t, records, 2)

	// Priority 순으로 정렬 확인
	assert.Equal(t, "192.0.2.1", records[0].Content)
	assert.Equal(t, int64(10), records[0].Priority)
	assert.Equal(t, "192.0.2.2", records[1].Content)
	assert.Equal(t, int64(20), records[1].Priority)

	// 다른 타입 조회
	aaaaRecords, err := storage.GetRecordsByName("www.example.com.", "AAAA")
	require.NoError(t, err)
	require.Len(t, aaaaRecords, 1)
	assert.Equal(t, "2001:db8::1", aaaaRecords[0].Content)

	// 존재하지 않는 이름
	records, err = storage.GetRecordsByName("notfound.example.com.", "A")
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestCreateRecord는 Record 생성 테스트입니다 (캐시 무효화)
func TestCreateRecord(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "example.com.")

	// 캐시 초기화
	records, err := storage.GetRecordsByZone(zoneID)
	require.NoError(t, err)
	require.Empty(t, records)

	// Record 생성 (기본값 자동 설정)
	record := &model.Record{
		ZoneID:  zoneID,
		Name:    "www.example.com.",
		Type:    "A",
		Content: "192.0.2.1",
		Enabled: true,
	}

	id, err := storage.CreateRecord(record)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	// DB에서 확인
	created, err := storage.GetRecord(id)
	require.NoError(t, err)
	require.NotNil(t, created)
	assert.Equal(t, zoneID, created.ZoneID)
	assert.Equal(t, "www.example.com.", created.Name)
	assert.Equal(t, "A", created.Type)
	assert.Equal(t, "192.0.2.1", created.Content)
	assert.Equal(t, int64(300), created.TTL) // 기본값
	assert.Equal(t, int64(0), created.Priority)
	assert.True(t, created.Enabled)

	// 캐시가 무효화되었는지 확인
	_, ok := storage.cache.Get(zoneID)
	assert.False(t, ok)

	// 사용자 정의 값으로 생성
	customRecord := &model.Record{
		ZoneID:   zoneID,
		Name:     "mail.example.com.",
		Type:     "MX",
		Content:  "mail.example.com.",
		TTL:      3600,
		Priority: 10,
		Enabled:  false,
	}

	customID, err := storage.CreateRecord(customRecord)
	require.NoError(t, err)

	custom, err := storage.GetRecord(customID)
	require.NoError(t, err)
	assert.Equal(t, int64(3600), custom.TTL)
	assert.Equal(t, int64(10), custom.Priority)
	assert.False(t, custom.Enabled)
}

// TestUpdateRecord는 Record 업데이트 테스트입니다 (캐시 무효화)
func TestUpdateRecord(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone과 Record 생성
	zoneID := insertTestZone(t, db, "example.com.")
	recordID := insertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")

	// 캐시 업데이트
	records, err := storage.GetRecordsByZone(zoneID)
	require.NoError(t, err)
	require.Len(t, records, 1)

	// Record 업데이트
	updated := &model.Record{
		ID:       recordID,
		ZoneID:   zoneID,
		Name:     "updated.example.com.",
		Type:     "AAAA",
		Content:  "2001:db8::1",
		TTL:      7200,
		Priority: 20,
		Enabled:  false,
	}

	err = storage.UpdateRecord(updated)
	require.NoError(t, err)

	// 업데이트 확인
	result, err := storage.GetRecord(recordID)
	require.NoError(t, err)
	assert.Equal(t, "updated.example.com.", result.Name)
	assert.Equal(t, "AAAA", result.Type)
	assert.Equal(t, "2001:db8::1", result.Content)
	assert.Equal(t, int64(7200), result.TTL)
	assert.Equal(t, int64(20), result.Priority)
	assert.False(t, result.Enabled)

	// 캐시가 무효화되었는지 확인
	_, ok := storage.cache.Get(zoneID)
	assert.False(t, ok)

	// 존재하지 않는 Record 업데이트
	nonExistent := &model.Record{
		ID:      9999,
		ZoneID:  zoneID,
		Name:    "notfound.example.com.",
		Type:    "A",
		Content: "192.0.2.99",
	}

	err = storage.UpdateRecord(nonExistent)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Record를 찾을 수 없습니다")
}

// TestDeleteRecord는 Record 삭제 테스트입니다 (캐시 무효화)
func TestDeleteRecord(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone과 Record 생성
	zoneID := insertTestZone(t, db, "example.com.")
	recordID := insertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")

	// 캐시 업데이트
	records, err := storage.GetRecordsByZone(zoneID)
	require.NoError(t, err)
	require.Len(t, records, 1)

	// Record 삭제
	err = storage.DeleteRecord(recordID)
	require.NoError(t, err)

	// 삭제 확인
	deleted, err := storage.GetRecord(recordID)
	require.NoError(t, err)
	assert.Nil(t, deleted)

	// 캐시가 무효화되었는지 확인
	_, ok := storage.cache.Get(zoneID)
	assert.False(t, ok)

	// 존재하지 않는 Record 삭제
	err = storage.DeleteRecord(9999)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Record를 찾을 수 없습니다")
}

// TestRecordCache_TTL은 캐시 TTL 만료 테스트입니다
func TestRecordCache_TTL(t *testing.T) {
	db := setupTestDB(t)

	// 짧은 TTL로 캐시 생성 (1초)
	cache := NewRecordCache(1 * time.Second)
	storage := &RecordStorage{
		db:    db,
		cache: cache,
	}

	// Zone과 Record 생성
	zoneID := insertTestZone(t, db, "example.com.")
	insertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")

	// 캐시 업데이트
	records, err := storage.GetRecordsByZone(zoneID)
	require.NoError(t, err)
	require.Len(t, records, 1)

	// 캐시 히트 확인
	cachedRecords, ok := storage.cache.Get(zoneID)
	assert.True(t, ok)
	assert.NotNil(t, cachedRecords)

	// TTL 만료 대기
	time.Sleep(1100 * time.Millisecond)

	// 캐시 미스 확인 (TTL 만료)
	cachedRecords, ok = storage.cache.Get(zoneID)
	assert.False(t, ok)
	assert.Nil(t, cachedRecords)

	// 다시 캐시 업데이트
	records, err = storage.GetRecordsByZone(zoneID)
	require.NoError(t, err)
	require.Len(t, records, 1)

	// 캐시 히트 확인
	cachedRecords, ok = storage.cache.Get(zoneID)
	assert.True(t, ok)
	assert.NotNil(t, cachedRecords)
}

// TestRecordCache_Invalidate는 캐시 무효화 테스트입니다
func TestRecordCache_Invalidate(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone과 Record 생성
	zoneID1 := insertTestZone(t, db, "example1.com.")
	zoneID2 := insertTestZone(t, db, "example2.com.")
	insertTestRecord(t, db, zoneID1, "www.example1.com.", "A", "192.0.2.1")
	insertTestRecord(t, db, zoneID2, "www.example2.com.", "A", "192.0.2.2")

	// 캐시 업데이트
	_, err := storage.GetRecordsByZone(zoneID1)
	require.NoError(t, err)
	_, err = storage.GetRecordsByZone(zoneID2)
	require.NoError(t, err)

	// 캐시 히트 확인
	records1, ok1 := storage.cache.Get(zoneID1)
	assert.True(t, ok1)
	assert.NotNil(t, records1)

	records2, ok2 := storage.cache.Get(zoneID2)
	assert.True(t, ok2)
	assert.NotNil(t, records2)

	// 특정 Zone 캐시 무효화
	storage.cache.Invalidate(zoneID1)

	// Zone 1 캐시는 무효화되었고, Zone 2 캐시는 유지되어야 함
	records1, ok1 = storage.cache.Get(zoneID1)
	assert.False(t, ok1)
	assert.Nil(t, records1)

	records2, ok2 = storage.cache.Get(zoneID2)
	assert.True(t, ok2)
	assert.NotNil(t, records2)
}

// TestRecordCache_InvalidateAll은 전체 캐시 무효화 테스트입니다
func TestRecordCache_InvalidateAll(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone과 Record 생성
	zoneID1 := insertTestZone(t, db, "example1.com.")
	zoneID2 := insertTestZone(t, db, "example2.com.")
	insertTestRecord(t, db, zoneID1, "www.example1.com.", "A", "192.0.2.1")
	insertTestRecord(t, db, zoneID2, "www.example2.com.", "A", "192.0.2.2")

	// 캐시 업데이트
	_, err := storage.GetRecordsByZone(zoneID1)
	require.NoError(t, err)
	_, err = storage.GetRecordsByZone(zoneID2)
	require.NoError(t, err)

	// 전체 캐시 무효화
	storage.cache.InvalidateAll()

	// 모든 캐시가 무효화되었는지 확인
	records1, ok1 := storage.cache.Get(zoneID1)
	assert.False(t, ok1)
	assert.Nil(t, records1)

	records2, ok2 := storage.cache.Get(zoneID2)
	assert.False(t, ok2)
	assert.Nil(t, records2)
}

// TestRecordCache_Concurrency는 캐시 동시성 테스트입니다
func TestRecordCache_Concurrency(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone과 Record 생성
	zoneID := insertTestZone(t, db, "example.com.")
	for i := 1; i <= 10; i++ {
		insertTestRecord(t, db, zoneID, fmt.Sprintf("www%d.example.com.", i), "A", fmt.Sprintf("192.0.2.%d", i))
	}

	// 캐시 초기화
	records, err := storage.GetRecordsByZone(zoneID)
	require.NoError(t, err)
	require.Len(t, records, 10)

	// 동시 읽기 테스트
	var wg sync.WaitGroup
	readCount := 50

	for i := 0; i < readCount; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 읽기
			cachedRecords, ok := storage.cache.Get(zoneID)
			assert.True(t, ok)
			assert.NotNil(t, cachedRecords)
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
			storage.cache.Invalidate(zoneID)
		}(i)
	}

	wg.Wait()

	// 캐시가 무효화되었는지 확인
	_, ok := storage.cache.Get(zoneID)
	assert.False(t, ok)

	// 동시 읽기/쓰기 혼합 테스트
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 업데이트
			_, err := storage.GetRecordsByZone(zoneID)
			assert.NoError(t, err)
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 읽기
			storage.cache.Get(zoneID)
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			// 캐시 무효화
			storage.cache.Invalidate(zoneID)
		}(i)
	}

	wg.Wait()
}

// TestRecordCache_Set은 캐시 Set 메서드 테스트입니다
func TestRecordCache_Set(t *testing.T) {
	cache := NewRecordCache(1 * time.Minute)
	zoneID := int64(1)

	// 빈 캐시
	records, ok := cache.Get(zoneID)
	assert.False(t, ok)
	assert.Nil(t, records)

	// Record 추가
	testRecords := []*model.Record{
		{ID: 1, ZoneID: zoneID, Name: "www.example.com.", Type: "A", Content: "192.0.2.1"},
		{ID: 2, ZoneID: zoneID, Name: "mail.example.com.", Type: "A", Content: "192.0.2.2"},
	}

	cache.Set(zoneID, testRecords)

	// 캐시 히트
	records, ok = cache.Get(zoneID)
	assert.True(t, ok)
	assert.NotNil(t, records)
	assert.Len(t, records, 2)
	assert.Equal(t, int64(1), records[0].ID)
	assert.Equal(t, int64(2), records[1].ID)

	// 다른 Zone은 캐시되지 않음
	otherZoneRecords, ok := cache.Get(999)
	assert.False(t, ok)
	assert.Nil(t, otherZoneRecords)
}

// TestRecordCache_MultipleZones는 여러 Zone의 캐시 관리 테스트입니다
func TestRecordCache_MultipleZones(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// 여러 Zone 생성
	zoneID1 := insertTestZone(t, db, "example1.com.")
	zoneID2 := insertTestZone(t, db, "example2.com.")
	zoneID3 := insertTestZone(t, db, "example3.com.")

	insertTestRecord(t, db, zoneID1, "www.example1.com.", "A", "192.0.2.1")
	insertTestRecord(t, db, zoneID2, "www.example2.com.", "A", "192.0.2.2")
	insertTestRecord(t, db, zoneID3, "www.example3.com.", "A", "192.0.2.3")

	// 각 Zone의 캐시 업데이트
	records1, err := storage.GetRecordsByZone(zoneID1)
	require.NoError(t, err)
	require.Len(t, records1, 1)

	records2, err := storage.GetRecordsByZone(zoneID2)
	require.NoError(t, err)
	require.Len(t, records2, 1)

	records3, err := storage.GetRecordsByZone(zoneID3)
	require.NoError(t, err)
	require.Len(t, records3, 1)

	// 각 Zone의 캐시가 독립적으로 관리되는지 확인
	cached1, ok1 := storage.cache.Get(zoneID1)
	assert.True(t, ok1)
	assert.Equal(t, "www.example1.com.", cached1[0].Name)

	cached2, ok2 := storage.cache.Get(zoneID2)
	assert.True(t, ok2)
	assert.Equal(t, "www.example2.com.", cached2[0].Name)

	cached3, ok3 := storage.cache.Get(zoneID3)
	assert.True(t, ok3)
	assert.Equal(t, "www.example3.com.", cached3[0].Name)

	// Zone 1만 무효화
	storage.cache.Invalidate(zoneID1)

	// Zone 1 캐시는 무효화되었고, 나머지는 유지
	_, ok1 = storage.cache.Get(zoneID1)
	assert.False(t, ok1)

	_, ok2 = storage.cache.Get(zoneID2)
	assert.True(t, ok2)

	_, ok3 = storage.cache.Get(zoneID3)
	assert.True(t, ok3)

	// 전체 무효화
	storage.cache.InvalidateAll()

	// 모든 캐시가 무효화됨
	_, ok1 = storage.cache.Get(zoneID1)
	assert.False(t, ok1)

	_, ok2 = storage.cache.Get(zoneID2)
	assert.False(t, ok2)

	_, ok3 = storage.cache.Get(zoneID3)
	assert.False(t, ok3)
}

// TestNewRecordStorage는 RecordStorage 생성 테스트입니다
func TestNewRecordStorage(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	assert.NotNil(t, storage)
	assert.NotNil(t, storage.db)
	assert.NotNil(t, storage.cache)
	assert.Equal(t, 1*time.Minute, storage.cache.ttl)
}

// TestNewRecordCache는 RecordCache 생성 테스트입니다
func TestNewRecordCache(t *testing.T) {
	ttl := 10 * time.Minute
	cache := NewRecordCache(ttl)

	assert.NotNil(t, cache)
	assert.NotNil(t, cache.cache)
	assert.NotNil(t, cache.expiry)
	assert.Equal(t, ttl, cache.ttl)
	assert.Empty(t, cache.cache)
	assert.Empty(t, cache.expiry)
}
