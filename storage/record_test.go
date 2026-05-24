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

func TestBatchUpdateLastQueryAt(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	zoneID := insertTestZone(t, db, "example.com.")
	recordID := insertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")

	first := time.Date(2026, 2, 7, 13, 0, 0, 0, time.UTC)
	second := first.Add(5 * time.Minute)

	err := storage.BatchUpdateLastQueryAt(map[string]time.Time{
		"www.example.com.": first,
	})
	require.NoError(t, err)

	record, err := storage.GetRecord(recordID)
	require.NoError(t, err)
	require.NotNil(t, record)
	require.NotNil(t, record.LastQueryAt)
	assert.Equal(t, first.Unix(), record.LastQueryAt.Unix())

	err = storage.BatchUpdateLastQueryAt(map[string]time.Time{
		"www.example.com.": second,
	})
	require.NoError(t, err)

	record, err = storage.GetRecord(recordID)
	require.NoError(t, err)
	require.NotNil(t, record.LastQueryAt)
	assert.Equal(t, second.Unix(), record.LastQueryAt.Unix())
}

func TestBatchUpdateLastQueryAt_EmptyAndUnknown(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	err := storage.BatchUpdateLastQueryAt(nil)
	require.NoError(t, err)

	err = storage.BatchUpdateLastQueryAt(map[string]time.Time{
		"unknown.example.com.": time.Now().UTC(),
	})
	require.NoError(t, err)
}

func TestBatchUpdateLastQueryAt_WildcardMatch(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	zoneID := insertTestZone(t, db, "yangs.sh.")
	recordID := insertTestRecord(t, db, zoneID, "*.gs.kube.yangs.sh.", "A", "10.0.0.1")
	queriedAt := time.Date(2026, 5, 24, 13, 0, 0, 0, time.UTC)

	err := storage.BatchUpdateLastQueryAt(map[string]time.Time{
		"lb.gs.kube.yangs.sh.": queriedAt,
	})
	require.NoError(t, err)

	record, err := storage.GetRecord(recordID)
	require.NoError(t, err)
	require.NotNil(t, record)
	require.NotNil(t, record.LastQueryAt)
	assert.Equal(t, queriedAt.Unix(), record.LastQueryAt.Unix())
}

func TestBatchUpdateLastQueryAt_ExactMatchDoesNotUpdateWildcard(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	zoneID := insertTestZone(t, db, "yangs.sh.")
	wildcardID := insertTestRecord(t, db, zoneID, "*.gs.kube.yangs.sh.", "A", "10.0.0.1")
	exactID := insertTestRecord(t, db, zoneID, "lb.gs.kube.yangs.sh.", "A", "10.0.0.2")
	queriedAt := time.Date(2026, 5, 24, 13, 5, 0, 0, time.UTC)

	err := storage.BatchUpdateLastQueryAt(map[string]time.Time{
		"lb.gs.kube.yangs.sh.": queriedAt,
	})
	require.NoError(t, err)

	exactRecord, err := storage.GetRecord(exactID)
	require.NoError(t, err)
	require.NotNil(t, exactRecord.LastQueryAt)
	assert.Equal(t, queriedAt.Unix(), exactRecord.LastQueryAt.Unix())

	wildcardRecord, err := storage.GetRecord(wildcardID)
	require.NoError(t, err)
	assert.Nil(t, wildcardRecord.LastQueryAt)
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
	assert.Contains(t, err.Error(), "record를 찾을 수 없습니다")
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
	assert.Contains(t, err.Error(), "record를 찾을 수 없습니다")
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

// TestCreateRecord_VersionIncrement는 Record 생성 시 버전 증가를 테스트합니다
func TestCreateRecord_VersionIncrement(t *testing.T) {
	db := setupTestDB(t)
	recordStorage := NewRecordStorage(db)
	syncVersion := NewSyncVersion(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "test.com.")

	// 현재 버전 확인
	version, err := syncVersion.GetVersion()
	require.NoError(t, err)

	// Record 생성
	record := &model.Record{
		ZoneID:   zoneID,
		Name:     "www.test.com.",
		Type:     "A",
		Content:  "10.0.0.1",
		TTL:      300,
		Priority: 0,
		Enabled:  true,
	}

	_, err = recordStorage.CreateRecord(record)
	require.NoError(t, err)

	// 버전 증가 확인
	newVersion, err := syncVersion.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, version+1, newVersion, "Record 생성 시 버전이 증가해야 함")
}

// TestUpdateRecord_VersionIncrement는 Record 업데이트 시 버전 증가를 테스트합니다
func TestUpdateRecord_VersionIncrement(t *testing.T) {
	db := setupTestDB(t)
	recordStorage := NewRecordStorage(db)
	syncVersion := NewSyncVersion(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "test.com.")

	// Record 생성
	record := &model.Record{
		ZoneID:   zoneID,
		Name:     "www.test.com.",
		Type:     "A",
		Content:  "10.0.0.1",
		TTL:      300,
		Priority: 0,
		Enabled:  true,
	}

	id, err := recordStorage.CreateRecord(record)
	require.NoError(t, err)

	// 현재 버전 확인
	version, err := syncVersion.GetVersion()
	require.NoError(t, err)

	// Record 업데이트
	record.ID = id
	record.Content = "10.0.0.2"
	err = recordStorage.UpdateRecord(record)
	require.NoError(t, err)

	// 버전 증가 확인
	newVersion, err := syncVersion.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, version+1, newVersion, "Record 업데이트 시 버전이 증가해야 함")
}

// TestDeleteRecord_VersionIncrement는 Record 삭제 시 버전 증가를 테스트합니다
func TestDeleteRecord_VersionIncrement(t *testing.T) {
	db := setupTestDB(t)
	recordStorage := NewRecordStorage(db)
	syncVersion := NewSyncVersion(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "test.com.")

	// Record 생성
	record := &model.Record{
		ZoneID:   zoneID,
		Name:     "www.test.com.",
		Type:     "A",
		Content:  "10.0.0.1",
		TTL:      300,
		Priority: 0,
		Enabled:  true,
	}

	id, err := recordStorage.CreateRecord(record)
	require.NoError(t, err)

	// 현재 버전 확인
	version, err := syncVersion.GetVersion()
	require.NoError(t, err)

	// Record 삭제
	err = recordStorage.DeleteRecord(id)
	require.NoError(t, err)

	// 버전 증가 확인
	newVersion, err := syncVersion.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, version+1, newVersion, "Record 삭제 시 버전이 증가해야 함")
}

// TestDomainExistsInZone은 특정 Zone에 도메인이 존재하는지 확인하는 테스트입니다
func TestDomainExistsInZone(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "example.com.")

	// enabled=1인 Record 삽입
	_, err := db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "www.example.com.", "A", "192.0.2.1", 1)
	require.NoError(t, err)

	// enabled=0인 Record 삽입
	_, err = db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "disabled.example.com.", "A", "192.0.2.2", 0)
	require.NoError(t, err)

	// enabled된 도메인 존재 확인
	exists, err := storage.DomainExistsInZone(zoneID, "www.example.com.")
	require.NoError(t, err)
	assert.True(t, exists)

	// disabled된 도메인은 존재하지 않음
	exists, err = storage.DomainExistsInZone(zoneID, "disabled.example.com.")
	require.NoError(t, err)
	assert.False(t, exists)

	// 존재하지 않는 도메인
	exists, err = storage.DomainExistsInZone(zoneID, "notfound.example.com.")
	require.NoError(t, err)
	assert.False(t, exists)

	// 존재하지 않는 Zone
	exists, err = storage.DomainExistsInZone(9999, "www.example.com.")
	require.NoError(t, err)
	assert.False(t, exists)
}

// TestGetRecordsByNameAndZone은 zone_id, 이름, 타입으로 Record를 조회하는 테스트입니다
func TestGetRecordsByNameAndZone(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "example.com.")

	// enabled=1인 Record 삽입
	_, err := db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, priority, enabled)
	                          VALUES (?, ?, ?, ?, ?, ?)`, zoneID, "www.example.com.", "A", "192.0.2.1", 10, 1)
	require.NoError(t, err)

	_, err = db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, priority, enabled)
	                          VALUES (?, ?, ?, ?, ?, ?)`, zoneID, "www.example.com.", "A", "192.0.2.2", 20, 1)
	require.NoError(t, err)

	// 같은 이름이지만 다른 타입
	_, err = db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "www.example.com.", "AAAA", "2001:db8::1", 1)
	require.NoError(t, err)

	// disabled Record
	_, err = db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "www.example.com.", "A", "192.0.2.3", 0)
	require.NoError(t, err)

	// name과 type으로 조회 (enabled만)
	records, err := storage.GetRecordsByNameAndZone(zoneID, "www.example.com.", "A")
	require.NoError(t, err)
	require.Len(t, records, 2)

	// AAAA 타입 조회
	records, err = storage.GetRecordsByNameAndZone(zoneID, "www.example.com.", "AAAA")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "2001:db8::1", records[0].Content)

	// 존재하지 않는 이름
	records, err = storage.GetRecordsByNameAndZone(zoneID, "notfound.example.com.", "A")
	require.NoError(t, err)
	assert.Empty(t, records)

	// 존재하지 않는 타입
	records, err = storage.GetRecordsByNameAndZone(zoneID, "www.example.com.", "MX")
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestListAllRecords는 모든 Record를 조회하는 테스트입니다
func TestListAllRecords(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// 빈 목록
	records, err := storage.ListAllRecords()
	require.NoError(t, err)
	assert.Empty(t, records)

	// 여러 Zone에 Record 삽입
	zoneID1 := insertTestZone(t, db, "example1.com.")
	zoneID2 := insertTestZone(t, db, "example2.com.")

	insertTestRecord(t, db, zoneID1, "www.example1.com.", "A", "192.0.2.1")
	insertTestRecord(t, db, zoneID1, "mail.example1.com.", "A", "192.0.2.2")
	insertTestRecord(t, db, zoneID2, "www.example2.com.", "A", "192.0.2.3")

	// 모든 Record 조회
	records, err = storage.ListAllRecords()
	require.NoError(t, err)
	require.Len(t, records, 3)

	// zone_id, name, type 순 정렬 확인
	assert.Equal(t, zoneID1, records[0].ZoneID)
	assert.Equal(t, zoneID1, records[1].ZoneID)
	assert.Equal(t, zoneID2, records[2].ZoneID)
}

// TestRecordStorage_ClearCache는 Record 캐시 클리어 테스트입니다
func TestRecordStorage_ClearCache(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone과 Record 생성
	zoneID := insertTestZone(t, db, "example.com.")
	insertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")

	// 캐시 업데이트
	records, err := storage.GetRecordsByZone(zoneID)
	require.NoError(t, err)
	require.Len(t, records, 1)

	// 캐시 히트 확인
	_, ok := storage.cache.Get(zoneID)
	assert.True(t, ok)

	// ClearCache 호출
	storage.ClearCache()

	// 캐시 미스 확인
	_, ok = storage.cache.Get(zoneID)
	assert.False(t, ok)
}

// TestRecordStorage_ClearCache_NilCache는 nil 캐시에서도 안전한지 테스트합니다
func TestRecordStorage_ClearCache_NilCache(t *testing.T) {
	db := setupTestDB(t)
	storage := &RecordStorage{
		db:    db,
		cache: nil,
	}

	// Should not panic
	storage.ClearCache()
}

// === Error path tests (using closed DB) ===

func TestGetRecord_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)
	_ = db.Reader.Close()

	_, err := storage.GetRecord(1)
	assert.Error(t, err)
}

func TestGetRecordsByZone_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)
	_ = db.Reader.Close()

	_, err := storage.GetRecordsByZone(1)
	assert.Error(t, err)
}

func TestListAllRecords_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)
	_ = db.Reader.Close()

	_, err := storage.ListAllRecords()
	assert.Error(t, err)
}

func TestGetRecordsByName_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)
	_ = db.Reader.Close()

	_, err := storage.GetRecordsByName("www.example.com.", "A")
	assert.Error(t, err)
}

func TestGetRecordsByNameAndZone_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)
	_ = db.Reader.Close()

	_, err := storage.GetRecordsByNameAndZone(1, "www.example.com.", "A")
	assert.Error(t, err)
}

func TestDomainExistsInZone_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)
	_ = db.Reader.Close()

	_, err := storage.DomainExistsInZone(1, "www.example.com.")
	assert.Error(t, err)
}

func TestCreateRecord_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)
	_ = db.Writer.Close()

	record := &model.Record{ZoneID: 1, Name: "test.com.", Type: "A", Content: "1.2.3.4", Enabled: true}
	_, err := storage.CreateRecord(record)
	assert.Error(t, err)
}

func TestUpdateRecord_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)
	_ = db.Writer.Close()

	record := &model.Record{ID: 1, ZoneID: 1, Name: "test.com.", Type: "A", Content: "1.2.3.4", Enabled: true}
	err := storage.UpdateRecord(record)
	assert.Error(t, err)
}

func TestDeleteRecord_DBError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Create Zone and Record first
	zoneID := insertTestZone(t, db, "example.com.")
	recordID := insertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")

	// Close writer to trigger error during delete
	_ = db.Writer.Close()

	err := storage.DeleteRecord(recordID)
	assert.Error(t, err)
}

func TestDeleteRecord_ReadError(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)
	_ = db.Reader.Close()

	err := storage.DeleteRecord(1)
	assert.Error(t, err)
}

// TestCreateRecord_TransactionRollback는 트랜잭션 롤백 테스트입니다
func TestCreateRecord_TransactionRollback(t *testing.T) {
	db := setupTestDB(t)
	recordStorage := NewRecordStorage(db)
	syncVersion := NewSyncVersion(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "test.com.")

	// 초기 버전
	initialVersion, err := syncVersion.GetVersion()
	require.NoError(t, err)

	// 정상 Record 생성
	record1 := &model.Record{
		ZoneID:   zoneID,
		Name:     "www.test.com.",
		Type:     "A",
		Content:  "10.0.0.1",
		TTL:      300,
		Priority: 0,
		Enabled:  true,
	}

	_, err = recordStorage.CreateRecord(record1)
	require.NoError(t, err)

	// 잘못된 Record 생성 시도 (존재하지 않는 Zone)
	record2 := &model.Record{
		ZoneID:   9999,
		Name:     "invalid.test.com.",
		Type:     "A",
		Content:  "10.0.0.2",
		TTL:      300,
		Priority: 0,
		Enabled:  true,
	}

	_, err = recordStorage.CreateRecord(record2)
	assert.Error(t, err, "존재하지 않는 Zone의 Record 생성은 실패해야 함")

	// 버전 확인 (실패한 트랜잭션은 버전 증가 안 함)
	currentVersion, err := syncVersion.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, initialVersion+1, currentVersion, "성공한 트랜잭션만 버전 증가")
}

// TestWildcardMatching은 와일드카드 레코드 매칭 테스트입니다
func TestWildcardMatching(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "example.com.")

	// 와일드카드 레코드 삽입
	_, err := db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "*.example.com.", "A", "192.0.2.100", 1)
	require.NoError(t, err)

	// 와일드카드 매칭 테스트
	records, err := storage.GetRecordsByNameAndZone(zoneID, "foo.example.com.", "A")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "foo.example.com.", records[0].Name, "응답에 쿼리 도메인이 표시되어야 함")
	assert.Equal(t, "192.0.2.100", records[0].Content)

	// 다른 서브도메인도 매칭
	records, err = storage.GetRecordsByNameAndZone(zoneID, "bar.example.com.", "A")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "bar.example.com.", records[0].Name)
	assert.Equal(t, "192.0.2.100", records[0].Content)

	// 2레벨 서브도메인은 매칭 안 됨 (와일드카드는 단일 레벨만)
	records, err = storage.GetRecordsByNameAndZone(zoneID, "sub.foo.example.com.", "A")
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestWildcardWithExactMatch는 정확한 매칭이 와일드카드보다 우선하는지 테스트합니다
func TestWildcardWithExactMatch(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "example.com.")

	// 와일드카드 레코드
	_, err := db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "*.example.com.", "A", "192.0.2.100", 1)
	require.NoError(t, err)

	// 정확한 레코드
	_, err = db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "www.example.com.", "A", "192.0.2.1", 1)
	require.NoError(t, err)

	// 정확한 매칭이 우선
	records, err := storage.GetRecordsByNameAndZone(zoneID, "www.example.com.", "A")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "www.example.com.", records[0].Name)
	assert.Equal(t, "192.0.2.1", records[0].Content, "정확한 매칭이 와일드카드보다 우선")

	// 다른 서브도메인은 와일드카드 매칭
	records, err = storage.GetRecordsByNameAndZone(zoneID, "other.example.com.", "A")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "other.example.com.", records[0].Name)
	assert.Equal(t, "192.0.2.100", records[0].Content)
}

// TestWildcardMultiLevel은 다중 레벨 와일드카드 테스트입니다
func TestWildcardMultiLevel(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "example.com.")

	// 상위 와일드카드
	_, err := db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "*.example.com.", "A", "192.0.2.100", 1)
	require.NoError(t, err)

	// 하위 와일드카드 (더 구체적)
	_, err = db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "*.sub.example.com.", "A", "192.0.2.200", 1)
	require.NoError(t, err)

	// 하위 와일드카드 매칭 (더 구체적)
	records, err := storage.GetRecordsByNameAndZone(zoneID, "foo.sub.example.com.", "A")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "foo.sub.example.com.", records[0].Name)
	assert.Equal(t, "192.0.2.200", records[0].Content, "더 구체적인 와일드카드 우선")

	// 상위 와일드카드 매칭
	records, err = storage.GetRecordsByNameAndZone(zoneID, "bar.example.com.", "A")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "bar.example.com.", records[0].Name)
	assert.Equal(t, "192.0.2.100", records[0].Content)

	// 3레벨은 매칭 안 됨
	records, err = storage.GetRecordsByNameAndZone(zoneID, "deep.foo.sub.example.com.", "A")
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestWildcardDifferentTypes은 와일드카드가 타입별로 독립적으로 동작하는지 테스트합니다
func TestWildcardDifferentTypes(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "example.com.")

	// A 타입 와일드카드
	_, err := db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "*.example.com.", "A", "192.0.2.100", 1)
	require.NoError(t, err)

	// AAAA 타입 와일드카드
	_, err = db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "*.example.com.", "AAAA", "2001:db8::100", 1)
	require.NoError(t, err)

	// A 타입 쿼리
	records, err := storage.GetRecordsByNameAndZone(zoneID, "test.example.com.", "A")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "A", records[0].Type)
	assert.Equal(t, "192.0.2.100", records[0].Content)

	// AAAA 타입 쿼리
	records, err = storage.GetRecordsByNameAndZone(zoneID, "test.example.com.", "AAAA")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "AAAA", records[0].Type)
	assert.Equal(t, "2001:db8::100", records[0].Content)

	// MX 타입은 매칭 안 됨
	records, err = storage.GetRecordsByNameAndZone(zoneID, "test.example.com.", "MX")
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestWildcardDisabled는 비활성화된 와일드카드가 매칭되지 않는지 테스트합니다
func TestWildcardDisabled(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "example.com.")

	// 비활성화된 와일드카드
	_, err := db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "*.example.com.", "A", "192.0.2.100", 0)
	require.NoError(t, err)

	// 매칭 안 됨
	records, err := storage.GetRecordsByNameAndZone(zoneID, "foo.example.com.", "A")
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestDomainExistsInZone_Wildcard는 와일드카드 도메인 존재 확인 테스트입니다
func TestDomainExistsInZone_Wildcard(t *testing.T) {
	db := setupTestDB(t)
	storage := NewRecordStorage(db)

	// Zone 생성
	zoneID := insertTestZone(t, db, "example.com.")

	// 와일드카드 레코드
	_, err := db.Writer.Exec(`INSERT INTO records (zone_id, name, type, content, enabled)
	                          VALUES (?, ?, ?, ?, ?)`, zoneID, "*.example.com.", "A", "192.0.2.100", 1)
	require.NoError(t, err)

	// 와일드카드로 매칭되는 도메인 존재 확인
	exists, err := storage.DomainExistsInZone(zoneID, "foo.example.com.")
	require.NoError(t, err)
	assert.True(t, exists, "와일드카드로 매칭되는 도메인은 존재해야 함")

	// 와일드카드 자체는 존재하지 않음
	exists, err = storage.DomainExistsInZone(zoneID, "*.example.com.")
	require.NoError(t, err)
	assert.True(t, exists, "와일드카드 레코드 자체도 존재함")

	// 2레벨 서브도메인은 존재하지 않음
	exists, err = storage.DomainExistsInZone(zoneID, "sub.foo.example.com.")
	require.NoError(t, err)
	assert.False(t, exists)
}
