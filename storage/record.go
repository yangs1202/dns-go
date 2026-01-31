package storage

import (
	"database/sql"
	"dns-go/model"
	"fmt"
	"sync"
	"time"
)

// RecordCache는 Record 조회 결과를 캐싱합니다 (L2 캐시)
type RecordCache struct {
	mu     sync.RWMutex
	cache  map[int64][]*model.Record // key: zone_id
	expiry map[int64]time.Time       // key별 만료 시간
	ttl    time.Duration
}

// NewRecordCache는 새로운 Record 캐시를 생성합니다
func NewRecordCache(ttl time.Duration) *RecordCache {
	return &RecordCache{
		cache:  make(map[int64][]*model.Record),
		expiry: make(map[int64]time.Time),
		ttl:    ttl,
	}
}

// Get은 캐시에서 특정 Zone의 Record 목록을 조회합니다
func (c *RecordCache) Get(zoneID int64) ([]*model.Record, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	expiry, ok := c.expiry[zoneID]
	if !ok || time.Now().After(expiry) {
		return nil, false
	}

	records, ok := c.cache[zoneID]
	return records, ok
}

// Set은 특정 Zone의 Record 목록을 캐시에 저장합니다
func (c *RecordCache) Set(zoneID int64, records []*model.Record) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[zoneID] = records
	c.expiry[zoneID] = time.Now().Add(c.ttl)
}

// Invalidate는 특정 Zone의 캐시만 무효화합니다
func (c *RecordCache) Invalidate(zoneID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.cache, zoneID)
	delete(c.expiry, zoneID)
}

// InvalidateAll은 전체 캐시를 무효화합니다
func (c *RecordCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[int64][]*model.Record)
	c.expiry = make(map[int64]time.Time)
}

// RecordStorage는 Record 저장소입니다
type RecordStorage struct {
	db    *Database
	cache *RecordCache
}

// NewRecordStorage는 새로운 Record 저장소를 생성합니다
func NewRecordStorage(db *Database) *RecordStorage {
	return &RecordStorage{
		db:    db,
		cache: NewRecordCache(1 * time.Minute), // 1분 TTL
	}
}

// GetRecord는 ID로 Record를 조회합니다
func (s *RecordStorage) GetRecord(id int64) (*model.Record, error) {
	query := `SELECT id, zone_id, name, type, content, ttl, priority, enabled, created_at, updated_at
	          FROM records WHERE id = ?`

	var record model.Record
	err := s.db.Reader.QueryRow(query, id).Scan(
		&record.ID,
		&record.ZoneID,
		&record.Name,
		&record.Type,
		&record.Content,
		&record.TTL,
		&record.Priority,
		&record.Enabled,
		&record.CreatedAt,
		&record.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("Record 조회 실패: %w", err)
	}

	return &record, nil
}

// GetRecordsByZone은 특정 Zone의 모든 Record를 조회합니다 (L2 캐시 활용)
func (s *RecordStorage) GetRecordsByZone(zoneID int64) ([]*model.Record, error) {
	// L2 캐시 확인
	if records, ok := s.cache.Get(zoneID); ok {
		return records, nil
	}

	// DB 조회
	query := `SELECT id, zone_id, name, type, content, ttl, priority, enabled, created_at, updated_at
	          FROM records WHERE zone_id = ? ORDER BY name, type`

	rows, err := s.db.Reader.Query(query, zoneID)
	if err != nil {
		return nil, fmt.Errorf("Record 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var records []*model.Record
	for rows.Next() {
		var record model.Record
		err := rows.Scan(
			&record.ID,
			&record.ZoneID,
			&record.Name,
			&record.Type,
			&record.Content,
			&record.TTL,
			&record.Priority,
			&record.Enabled,
			&record.CreatedAt,
			&record.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("Record 스캔 실패: %w", err)
		}
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Record 행 반복 실패: %w", err)
	}

	// 캐시 업데이트
	s.cache.Set(zoneID, records)

	return records, nil
}

// ListAllRecords는 모든 Zone의 모든 Record를 조회합니다
func (s *RecordStorage) ListAllRecords() ([]*model.Record, error) {
	query := `SELECT id, zone_id, name, type, content, ttl, priority, enabled, created_at, updated_at
	          FROM records ORDER BY zone_id, name, type`

	rows, err := s.db.Reader.Query(query)
	if err != nil {
		return nil, fmt.Errorf("Record 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var records []*model.Record
	for rows.Next() {
		var record model.Record
		err := rows.Scan(
			&record.ID,
			&record.ZoneID,
			&record.Name,
			&record.Type,
			&record.Content,
			&record.TTL,
			&record.Priority,
			&record.Enabled,
			&record.CreatedAt,
			&record.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("Record 스캔 실패: %w", err)
		}
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Record 행 반복 실패: %w", err)
	}

	return records, nil
}

// GetRecordsByName은 이름과 타입으로 Record를 조회합니다
func (s *RecordStorage) GetRecordsByName(name, recordType string) ([]*model.Record, error) {
	query := `SELECT id, zone_id, name, type, content, ttl, priority, enabled, created_at, updated_at
	          FROM records WHERE name = ? AND type = ? AND enabled = 1
	          ORDER BY priority, id`

	rows, err := s.db.Reader.Query(query, name, recordType)
	if err != nil {
		return nil, fmt.Errorf("Record 조회 실패: %w", err)
	}
	defer rows.Close()

	var records []*model.Record
	for rows.Next() {
		var record model.Record
		err := rows.Scan(
			&record.ID,
			&record.ZoneID,
			&record.Name,
			&record.Type,
			&record.Content,
			&record.TTL,
			&record.Priority,
			&record.Enabled,
			&record.CreatedAt,
			&record.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("Record 스캔 실패: %w", err)
		}
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Record 행 반복 실패: %w", err)
	}

	return records, nil
}

// CreateRecord는 새로운 Record를 생성합니다
func (s *RecordStorage) CreateRecord(record *model.Record) (int64, error) {
	// 기본값 설정
	if record.TTL == 0 {
		record.TTL = 300
	}
	if record.Priority == 0 {
		record.Priority = 0
	}

	query := `INSERT INTO records (zone_id, name, type, content, ttl, priority, enabled)
	          VALUES (?, ?, ?, ?, ?, ?, ?)`

	result, err := s.db.Writer.Exec(query,
		record.ZoneID,
		record.Name,
		record.Type,
		record.Content,
		record.TTL,
		record.Priority,
		record.Enabled,
	)

	if err != nil {
		return 0, fmt.Errorf("Record 생성 실패: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("Record ID 조회 실패: %w", err)
	}

	// 해당 Zone의 캐시 무효화
	s.cache.Invalidate(record.ZoneID)

	return id, nil
}

// UpdateRecord는 Record를 업데이트합니다
func (s *RecordStorage) UpdateRecord(record *model.Record) error {
	query := `UPDATE records
	          SET zone_id = ?, name = ?, type = ?, content = ?, ttl = ?, priority = ?, enabled = ?,
	              updated_at = CURRENT_TIMESTAMP
	          WHERE id = ?`

	result, err := s.db.Writer.Exec(query,
		record.ZoneID,
		record.Name,
		record.Type,
		record.Content,
		record.TTL,
		record.Priority,
		record.Enabled,
		record.ID,
	)

	if err != nil {
		return fmt.Errorf("Record 업데이트 실패: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("Record를 찾을 수 없습니다")
	}

	// 해당 Zone의 캐시 무효화
	s.cache.Invalidate(record.ZoneID)

	return nil
}

// DeleteRecord는 Record를 삭제합니다
func (s *RecordStorage) DeleteRecord(id int64) error {
	// Record를 조회하여 ZoneID를 얻습니다 (캐시 무효화를 위해)
	record, err := s.GetRecord(id)
	if err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("Record를 찾을 수 없습니다")
	}

	result, err := s.db.Writer.Exec("DELETE FROM records WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("Record 삭제 실패: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("Record를 찾을 수 없습니다")
	}

	// 해당 Zone의 캐시 무효화
	s.cache.Invalidate(record.ZoneID)

	return nil
}
