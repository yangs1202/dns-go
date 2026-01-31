package storage

import (
	"database/sql"
	"dns-go/model"
	"fmt"
	"sync"
	"time"
)

// ZoneCache는 Zone 조회 결과를 캐싱합니다 (L2 캐시)
type ZoneCache struct {
	mu     sync.RWMutex
	zones  map[string]*model.Zone // key: zone name
	expiry time.Time
	ttl    time.Duration
}

// NewZoneCache는 새로운 Zone 캐시를 생성합니다
func NewZoneCache(ttl time.Duration) *ZoneCache {
	return &ZoneCache{
		zones: make(map[string]*model.Zone),
		ttl:   ttl,
	}
}

// Get은 캐시에서 Zone을 조회합니다
func (c *ZoneCache) Get(name string) (*model.Zone, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if time.Now().After(c.expiry) {
		return nil, false
	}

	zone, ok := c.zones[name]
	return zone, ok
}

// Set은 Zone을 캐시에 저장합니다
func (c *ZoneCache) Set(zones []*model.Zone) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.zones = make(map[string]*model.Zone)
	for _, zone := range zones {
		c.zones[zone.Name] = zone
	}
	c.expiry = time.Now().Add(c.ttl)
}

// Invalidate는 캐시를 무효화합니다
func (c *ZoneCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.zones = make(map[string]*model.Zone)
	c.expiry = time.Time{}
}

// ZoneStorage는 Zone 저장소입니다
type ZoneStorage struct {
	db    *Database
	cache *ZoneCache
}

// NewZoneStorage는 새로운 Zone 저장소를 생성합니다
func NewZoneStorage(db *Database) *ZoneStorage {
	return &ZoneStorage{
		db:    db,
		cache: NewZoneCache(5 * time.Minute), // 5분 TTL
	}
}

// GetZone은 ID로 Zone을 조회합니다
func (s *ZoneStorage) GetZone(id int64) (*model.Zone, error) {
	query := `SELECT id, name, soa_mname, soa_rname, soa_serial, soa_refresh, soa_retry, soa_expire, soa_minimum,
	                 enabled, allow_fallback, created_at, updated_at
	          FROM zones WHERE id = ?`

	var zone model.Zone
	err := s.db.Reader.QueryRow(query, id).Scan(
		&zone.ID,
		&zone.Name,
		&zone.SOAMname,
		&zone.SOARname,
		&zone.SOASerial,
		&zone.SOARefresh,
		&zone.SOARetry,
		&zone.SOAExpire,
		&zone.SOAMinimum,
		&zone.Enabled,
		&zone.AllowFallback,
		&zone.CreatedAt,
		&zone.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("Zone 조회 실패: %w", err)
	}

	return &zone, nil
}

// GetZoneByName은 이름으로 Zone을 조회합니다 (캐시 활용)
func (s *ZoneStorage) GetZoneByName(name string) (*model.Zone, error) {
	// L2 캐시 확인
	if zone, ok := s.cache.Get(name); ok {
		return zone, nil
	}

	// DB 조회
	query := `SELECT id, name, soa_mname, soa_rname, soa_serial, soa_refresh, soa_retry, soa_expire, soa_minimum,
	                 enabled, allow_fallback, created_at, updated_at
	          FROM zones WHERE name = ? AND enabled = 1`

	var zone model.Zone
	err := s.db.Reader.QueryRow(query, name).Scan(
		&zone.ID,
		&zone.Name,
		&zone.SOAMname,
		&zone.SOARname,
		&zone.SOASerial,
		&zone.SOARefresh,
		&zone.SOARetry,
		&zone.SOAExpire,
		&zone.SOAMinimum,
		&zone.Enabled,
		&zone.AllowFallback,
		&zone.CreatedAt,
		&zone.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("Zone 조회 실패: %w", err)
	}

	// L2 캐시에 저장
	s.cache.Set([]*model.Zone{&zone})

	return &zone, nil
}

// ListZones는 모든 Zone을 조회합니다
func (s *ZoneStorage) ListZones() ([]*model.Zone, error) {
	query := `SELECT id, name, soa_mname, soa_rname, soa_serial, soa_refresh, soa_retry, soa_expire, soa_minimum,
	                 enabled, allow_fallback, created_at, updated_at
	          FROM zones ORDER BY name`

	rows, err := s.db.Reader.Query(query)
	if err != nil {
		return nil, fmt.Errorf("Zone 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var zones []*model.Zone
	for rows.Next() {
		var zone model.Zone
		err := rows.Scan(
			&zone.ID,
			&zone.Name,
			&zone.SOAMname,
			&zone.SOARname,
			&zone.SOASerial,
			&zone.SOARefresh,
			&zone.SOARetry,
			&zone.SOAExpire,
			&zone.SOAMinimum,
			&zone.Enabled,
			&zone.AllowFallback,
			&zone.CreatedAt,
			&zone.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("Zone 스캔 실패: %w", err)
		}
		zones = append(zones, &zone)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Zone 행 반복 실패: %w", err)
	}

	// 캐시 업데이트
	s.cache.Set(zones)

	return zones, nil
}

// CreateZone은 새로운 Zone을 생성합니다
func (s *ZoneStorage) CreateZone(zone *model.Zone) (int64, error) {
	// 기본값 설정
	if zone.SOASerial == 0 {
		zone.SOASerial = 1
	}
	if zone.SOARefresh == 0 {
		zone.SOARefresh = 3600
	}
	if zone.SOARetry == 0 {
		zone.SOARetry = 900
	}
	if zone.SOAExpire == 0 {
		zone.SOAExpire = 86400
	}
	if zone.SOAMinimum == 0 {
		zone.SOAMinimum = 300
	}

	// 트랜잭션 시작
	tx, err := s.db.Writer.Begin()
	if err != nil {
		return 0, fmt.Errorf("트랜잭션 시작 실패: %w", err)
	}
	defer tx.Rollback()

	query := `INSERT INTO zones (name, soa_mname, soa_rname, soa_serial, soa_refresh, soa_retry, soa_expire, soa_minimum, enabled, allow_fallback)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	result, err := tx.Exec(query,
		zone.Name,
		zone.SOAMname,
		zone.SOARname,
		zone.SOASerial,
		zone.SOARefresh,
		zone.SOARetry,
		zone.SOAExpire,
		zone.SOAMinimum,
		zone.Enabled,
		zone.AllowFallback,
	)

	if err != nil {
		return 0, fmt.Errorf("Zone 생성 실패: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("Zone ID 조회 실패: %w", err)
	}

	// 동기화 버전 증가
	_, err = tx.Exec(`
		UPDATE sync_state
		SET last_sync_version = last_sync_version + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`)
	if err != nil {
		return 0, fmt.Errorf("동기화 버전 업데이트 실패: %w", err)
	}

	// 트랜잭션 커밋
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("트랜잭션 커밋 실패: %w", err)
	}

	// 캐시 무효화
	s.cache.Invalidate()

	return id, nil
}

// UpdateZone은 Zone을 업데이트합니다
func (s *ZoneStorage) UpdateZone(zone *model.Zone) error {
	// 트랜잭션 시작
	tx, err := s.db.Writer.Begin()
	if err != nil {
		return fmt.Errorf("트랜잭션 시작 실패: %w", err)
	}
	defer tx.Rollback()

	query := `UPDATE zones
	          SET name = ?, soa_mname = ?, soa_rname = ?, soa_serial = ?, soa_refresh = ?, soa_retry = ?,
	              soa_expire = ?, soa_minimum = ?, enabled = ?, allow_fallback = ?, updated_at = CURRENT_TIMESTAMP
	          WHERE id = ?`

	result, err := tx.Exec(query,
		zone.Name,
		zone.SOAMname,
		zone.SOARname,
		zone.SOASerial,
		zone.SOARefresh,
		zone.SOARetry,
		zone.SOAExpire,
		zone.SOAMinimum,
		zone.Enabled,
		zone.AllowFallback,
		zone.ID,
	)

	if err != nil {
		return fmt.Errorf("Zone 업데이트 실패: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("Zone을 찾을 수 없습니다")
	}

	// 동기화 버전 증가
	_, err = tx.Exec(`
		UPDATE sync_state
		SET last_sync_version = last_sync_version + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`)
	if err != nil {
		return fmt.Errorf("동기화 버전 업데이트 실패: %w", err)
	}

	// 트랜잭션 커밋
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("트랜잭션 커밋 실패: %w", err)
	}

	// 캐시 무효화
	s.cache.Invalidate()

	return nil
}

// DeleteZone은 Zone을 삭제합니다 (CASCADE로 레코드도 함께 삭제)
func (s *ZoneStorage) DeleteZone(id int64) error {
	// 트랜잭션 시작
	tx, err := s.db.Writer.Begin()
	if err != nil {
		return fmt.Errorf("트랜잭션 시작 실패: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec("DELETE FROM zones WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("Zone 삭제 실패: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("Zone을 찾을 수 없습니다")
	}

	// 동기화 버전 증가
	_, err = tx.Exec(`
		UPDATE sync_state
		SET last_sync_version = last_sync_version + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`)
	if err != nil {
		return fmt.Errorf("동기화 버전 업데이트 실패: %w", err)
	}

	// 트랜잭션 커밋
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("트랜잭션 커밋 실패: %w", err)
	}

	// 캐시 무효화
	s.cache.Invalidate()

	return nil
}
