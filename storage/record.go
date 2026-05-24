package storage

import (
	"database/sql"
	"dns-go/model"
	"fmt"
	"strings"
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

// findWildcardRecords는 와일드카드 레코드를 찾습니다.
// RFC 4592: 가장 구체적인(closest encloser) 와일드카드를 우선 매칭합니다.
// 예: foo.bar.example.com. → *.bar.example.com. → *.example.com. 순서
// 와일드카드는 단일 레벨만 매칭합니다 (*.example.com은 foo.example.com만 매칭, sub.foo.example.com은 안 됨)
func findWildcardRecords(allRecords []*model.Record, queryName, recordType string) []*model.Record {
	// 쿼리 도메인이 비어있거나 루트 도메인이면 와일드카드 매칭 불가
	if queryName == "" || queryName == "." {
		return nil
	}

	// 도메인을 레이블로 분리 (후행 점 제거)
	trimmedName := strings.TrimSuffix(queryName, ".")
	labels := strings.Split(trimmedName, ".")

	// 단일 레벨만 가능하므로 첫 레이블만 *로 교체
	// foo.bar.example.com → *.bar.example.com → *.example.com 순서
	for i := 1; i < len(labels); i++ {
		// 첫 레이블을 *로 교체한 와일드카드 패턴 생성
		wildcardLabels := make([]string, len(labels)-i+1)
		wildcardLabels[0] = "*"
		copy(wildcardLabels[1:], labels[i:])
		wildcardPattern := strings.Join(wildcardLabels, ".") + "."

		// 해당 와일드카드 패턴으로 매칭 시도
		var matches []*model.Record
		for _, record := range allRecords {
			if record.Name == wildcardPattern && record.Type == recordType && record.Enabled {
				// 와일드카드는 단일 레벨만 매칭해야 하므로 검증
				if !isValidWildcardMatch(wildcardPattern, queryName) {
					continue
				}
				// 원본 레코드를 복사하여 Name을 쿼리 도메인으로 교체
				copied := *record
				copied.Name = queryName
				matches = append(matches, &copied)
			}
		}

		// 가장 구체적인 와일드카드 매칭을 찾으면 즉시 반환
		if len(matches) > 0 {
			return matches
		}
	}

	return nil
}

// isValidWildcardMatch는 와일드카드가 쿼리 도메인과 올바르게 매칭되는지 검증합니다.
// RFC 4592: 와일드카드는 단일 레벨만 매칭 (*.example.com은 foo.example.com만, sub.foo.example.com은 안 됨)
func isValidWildcardMatch(wildcardPattern, queryName string) bool {
	// 와일드카드 suffix 추출 (*.example.com. → example.com.)
	suffix := strings.TrimPrefix(wildcardPattern, "*.")

	// 쿼리 도메인이 suffix로 끝나지 않으면 매칭 불가
	if !strings.HasSuffix(queryName, suffix) {
		return false
	}

	// prefix 추출 (foo.example.com. → foo)
	prefix := strings.TrimSuffix(queryName, suffix)
	prefix = strings.TrimSuffix(prefix, ".")

	// prefix가 비어있으면 매칭 불가
	if prefix == "" {
		return false
	}

	// prefix에 점이 있으면 다중 레벨이므로 매칭 불가
	return !strings.Contains(prefix, ".")
}

// hasWildcardMatch는 와일드카드 레코드가 쿼리 도메인과 매칭되는지 확인합니다.
func hasWildcardMatch(recordName, queryName string) bool {
	// 레코드가 와일드카드로 시작하지 않으면 매칭 불가
	if !strings.HasPrefix(recordName, "*.") {
		return false
	}

	// 레코드의 와일드카드 부분 제거 (예: *.example.com. → example.com.)
	suffix := strings.TrimPrefix(recordName, "*.")

	// 쿼리 도메인이 와일드카드 suffix로 끝나는지 확인
	if !strings.HasSuffix(queryName, suffix) {
		return false
	}

	// 쿼리 도메인의 prefix 부분 추출
	prefix := strings.TrimSuffix(queryName, suffix)
	prefix = strings.TrimSuffix(prefix, ".")

	// prefix가 비어있으면 매칭 안 됨 (예: *.example.com. 자체는 매칭 안 됨)
	if prefix == "" {
		return false
	}

	// prefix에 점이 없어야 함 (와일드카드는 단일 레벨만 매칭)
	return !strings.Contains(prefix, ".")
}

func wildcardQueryCandidates(queryName string) []string {
	if queryName == "" || queryName == "." {
		return nil
	}

	trimmedName := strings.TrimSuffix(queryName, ".")
	labels := strings.Split(trimmedName, ".")
	if len(labels) < 2 {
		return nil
	}

	candidates := make([]string, 0, len(labels)-1)
	for i := 1; i < len(labels); i++ {
		wildcardLabels := make([]string, len(labels)-i+1)
		wildcardLabels[0] = "*"
		copy(wildcardLabels[1:], labels[i:])
		candidates = append(candidates, strings.Join(wildcardLabels, ".")+".")
	}
	return candidates
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

// ClearCache는 Record 캐시를 클리어합니다
func (s *RecordStorage) ClearCache() {
	if s.cache != nil {
		s.cache.InvalidateAll()
	}
}

// GetRecord는 ID로 Record를 조회합니다
func (s *RecordStorage) GetRecord(id int64) (*model.Record, error) {
	query := `SELECT id, zone_id, name, type, content, ttl, priority, enabled, last_query_at, created_at, updated_at
	          FROM records WHERE id = ?`

	var record model.Record
	var lastQueryAt sql.NullTime
	err := s.db.Reader.QueryRow(query, id).Scan(
		&record.ID,
		&record.ZoneID,
		&record.Name,
		&record.Type,
		&record.Content,
		&record.TTL,
		&record.Priority,
		&record.Enabled,
		&lastQueryAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("record 조회 실패: %w", err)
	}
	if lastQueryAt.Valid {
		t := lastQueryAt.Time
		record.LastQueryAt = &t
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
	query := `SELECT id, zone_id, name, type, content, ttl, priority, enabled, last_query_at, created_at, updated_at
	          FROM records WHERE zone_id = ? ORDER BY name, type`

	rows, err := s.db.Reader.Query(query, zoneID)
	if err != nil {
		return nil, fmt.Errorf("record 목록 조회 실패: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var records []*model.Record
	for rows.Next() {
		var record model.Record
		var lastQueryAt sql.NullTime
		err := rows.Scan(
			&record.ID,
			&record.ZoneID,
			&record.Name,
			&record.Type,
			&record.Content,
			&record.TTL,
			&record.Priority,
			&record.Enabled,
			&lastQueryAt,
			&record.CreatedAt,
			&record.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("record 스캔 실패: %w", err)
		}
		if lastQueryAt.Valid {
			t := lastQueryAt.Time
			record.LastQueryAt = &t
		}
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("record 행 반복 실패: %w", err)
	}

	// 캐시 업데이트
	s.cache.Set(zoneID, records)

	return records, nil
}

// ListAllRecords는 모든 Zone의 모든 Record를 조회합니다
func (s *RecordStorage) ListAllRecords() ([]*model.Record, error) {
	query := `SELECT id, zone_id, name, type, content, ttl, priority, enabled, last_query_at, created_at, updated_at
	          FROM records ORDER BY zone_id, name, type`

	rows, err := s.db.Reader.Query(query)
	if err != nil {
		return nil, fmt.Errorf("record 목록 조회 실패: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var records []*model.Record
	for rows.Next() {
		var record model.Record
		var lastQueryAt sql.NullTime
		err := rows.Scan(
			&record.ID,
			&record.ZoneID,
			&record.Name,
			&record.Type,
			&record.Content,
			&record.TTL,
			&record.Priority,
			&record.Enabled,
			&lastQueryAt,
			&record.CreatedAt,
			&record.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("record 스캔 실패: %w", err)
		}
		if lastQueryAt.Valid {
			t := lastQueryAt.Time
			record.LastQueryAt = &t
		}
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("record 행 반복 실패: %w", err)
	}

	return records, nil
}

// GetRecordsByNameAndZone은 zone_id, 이름, 타입으로 Record를 조회합니다 (L2 캐시 활용)
// RFC 4592: 정확한 매칭 우선, 매칭 없으면 와일드카드 시도
func (s *RecordStorage) GetRecordsByNameAndZone(zoneID int64, name, recordType string) ([]*model.Record, error) {
	// L2 캐시에서 zone의 모든 레코드 조회
	allRecords, err := s.GetRecordsByZone(zoneID)
	if err != nil {
		return nil, err
	}

	// 1. 정확한 매칭 시도
	var result []*model.Record
	for _, record := range allRecords {
		if record.Name == name && record.Type == recordType && record.Enabled {
			result = append(result, record)
		}
	}

	// 2. 정확한 매칭이 있으면 반환
	if len(result) > 0 {
		return result, nil
	}

	// 3. 와일드카드 매칭 시도
	return findWildcardRecords(allRecords, name, recordType), nil
}

// DomainExistsInZone은 해당 Zone에 특정 도메인 이름의 레코드가 존재하는지 확인합니다 (타입 무관)
// RFC 4592: 정확한 매칭 우선, 매칭 없으면 와일드카드 확인
func (s *RecordStorage) DomainExistsInZone(zoneID int64, name string) (bool, error) {
	// L2 캐시에서 zone의 모든 레코드 조회
	allRecords, err := s.GetRecordsByZone(zoneID)
	if err != nil {
		return false, err
	}

	// 1. 정확한 매칭 확인 (타입 무관, enabled만 체크)
	for _, record := range allRecords {
		if record.Name == name && record.Enabled {
			return true, nil
		}
	}

	// 2. 와일드카드 매칭 확인 (모든 타입 확인)
	for _, record := range allRecords {
		if record.Enabled && hasWildcardMatch(record.Name, name) {
			return true, nil
		}
	}

	return false, nil
}

// GetRecordsByName은 이름과 타입으로 Record를 조회합니다 (하위 호환성을 위해 유지)
func (s *RecordStorage) GetRecordsByName(name, recordType string) ([]*model.Record, error) {
	// zone_id를 모르는 경우 DB 직접 조회
	query := `SELECT id, zone_id, name, type, content, ttl, priority, enabled, last_query_at, created_at, updated_at
	          FROM records WHERE name = ? AND type = ? AND enabled = 1
	          ORDER BY priority, id`

	rows, err := s.db.Reader.Query(query, name, recordType)
	if err != nil {
		return nil, fmt.Errorf("record 조회 실패: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var records []*model.Record
	for rows.Next() {
		var record model.Record
		var lastQueryAt sql.NullTime
		err := rows.Scan(
			&record.ID,
			&record.ZoneID,
			&record.Name,
			&record.Type,
			&record.Content,
			&record.TTL,
			&record.Priority,
			&record.Enabled,
			&lastQueryAt,
			&record.CreatedAt,
			&record.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("record 스캔 실패: %w", err)
		}
		if lastQueryAt.Valid {
			t := lastQueryAt.Time
			record.LastQueryAt = &t
		}
		records = append(records, &record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("record 행 반복 실패: %w", err)
	}

	return records, nil
}

// BatchUpdateLastQueryAt는 domain별 마지막 조회 시간을 일괄 업데이트합니다.
func (s *RecordStorage) BatchUpdateLastQueryAt(lastQueries map[string]time.Time) error {
	if len(lastQueries) == 0 {
		return nil
	}

	tx, err := s.db.Writer.Begin()
	if err != nil {
		return fmt.Errorf("트랜잭션 시작 실패: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`UPDATE records SET last_query_at = ? WHERE name = ?`)
	if err != nil {
		return fmt.Errorf("쿼리 준비 실패: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	wildcardStmt, err := tx.Prepare(`UPDATE records SET last_query_at = ? WHERE name = ? AND enabled = 1`)
	if err != nil {
		return fmt.Errorf("와일드카드 쿼리 준비 실패: %w", err)
	}
	defer func() { _ = wildcardStmt.Close() }()

	for domain, queriedAt := range lastQueries {
		if domain == "" {
			continue
		}

		result, err := stmt.Exec(queriedAt.UTC(), domain)
		if err != nil {
			return fmt.Errorf("last_query_at 업데이트 실패: %w", err)
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("last_query_at 업데이트 결과 확인 실패: %w", err)
		}
		if rowsAffected > 0 {
			continue
		}

		for _, candidate := range wildcardQueryCandidates(domain) {
			result, err := wildcardStmt.Exec(queriedAt.UTC(), candidate)
			if err != nil {
				return fmt.Errorf("와일드카드 last_query_at 업데이트 실패: %w", err)
			}

			rowsAffected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("와일드카드 last_query_at 업데이트 결과 확인 실패: %w", err)
			}
			if rowsAffected > 0 {
				break
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("트랜잭션 커밋 실패: %w", err)
	}

	return nil
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

	// 트랜잭션 시작
	tx, err := s.db.Writer.Begin()
	if err != nil {
		return 0, fmt.Errorf("트랜잭션 시작 실패: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := `INSERT INTO records (zone_id, name, type, content, ttl, priority, enabled)
	          VALUES (?, ?, ?, ?, ?, ?, ?)`

	result, err := tx.Exec(query,
		record.ZoneID,
		record.Name,
		record.Type,
		record.Content,
		record.TTL,
		record.Priority,
		record.Enabled,
	)

	if err != nil {
		return 0, fmt.Errorf("record 생성 실패: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("record ID 조회 실패: %w", err)
	}

	// 동기화 버전 증가
	_, err = tx.Exec(`UPDATE sync_state SET last_sync_version = last_sync_version + 1 WHERE id = 1`)
	if err != nil {
		return 0, fmt.Errorf("동기화 버전 업데이트 실패: %w", err)
	}

	// 트랜잭션 커밋
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("트랜잭션 커밋 실패: %w", err)
	}

	// 해당 Zone의 캐시 무효화
	s.cache.Invalidate(record.ZoneID)

	return id, nil
}

// UpdateRecord는 Record를 업데이트합니다
func (s *RecordStorage) UpdateRecord(record *model.Record) error {
	// 트랜잭션 시작
	tx, err := s.db.Writer.Begin()
	if err != nil {
		return fmt.Errorf("트랜잭션 시작 실패: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := `UPDATE records
	          SET zone_id = ?, name = ?, type = ?, content = ?, ttl = ?, priority = ?, enabled = ?,
	              updated_at = CURRENT_TIMESTAMP
	          WHERE id = ?`

	result, err := tx.Exec(query,
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
		return fmt.Errorf("record 업데이트 실패: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("record를 찾을 수 없습니다")
	}

	// 동기화 버전 증가
	_, err = tx.Exec(`UPDATE sync_state SET last_sync_version = last_sync_version + 1 WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("동기화 버전 업데이트 실패: %w", err)
	}

	// 트랜잭션 커밋
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("트랜잭션 커밋 실패: %w", err)
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
		return fmt.Errorf("record를 찾을 수 없습니다")
	}

	// 트랜잭션 시작
	tx, err := s.db.Writer.Begin()
	if err != nil {
		return fmt.Errorf("트랜잭션 시작 실패: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.Exec("DELETE FROM records WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("record 삭제 실패: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("record를 찾을 수 없습니다")
	}

	// 동기화 버전 증가
	_, err = tx.Exec(`UPDATE sync_state SET last_sync_version = last_sync_version + 1 WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("동기화 버전 업데이트 실패: %w", err)
	}

	// 트랜잭션 커밋
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("트랜잭션 커밋 실패: %w", err)
	}

	// 해당 Zone의 캐시 무효화
	s.cache.Invalidate(record.ZoneID)

	return nil
}
