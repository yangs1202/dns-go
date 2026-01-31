package storage

import (
	"database/sql"
	"dns-go/model"
	"fmt"
	"strings"
	"sync"
	"time"
)

type AdblockCache struct {
	mu      sync.RWMutex
	sources []*model.AdblockSource
	expiry  time.Time
	ttl     time.Duration
}

func NewAdblockCache(ttl time.Duration) *AdblockCache {
	return &AdblockCache{ttl: ttl}
}

func (c *AdblockCache) Get() ([]*model.AdblockSource, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Now().After(c.expiry) {
		return nil, false
	}
	return c.sources, true
}

func (c *AdblockCache) Set(sources []*model.AdblockSource) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sources = sources
	c.expiry = time.Now().Add(c.ttl)
}

func (c *AdblockCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sources = nil
	c.expiry = time.Time{}
}

type AdblockStorage struct {
	db    *Database
	cache *AdblockCache
}

func NewAdblockStorage(db *Database) *AdblockStorage {
	return &AdblockStorage{
		db:    db,
		cache: NewAdblockCache(10 * time.Minute),
	}
}

func (s *AdblockStorage) GetAdblockSource(id int64) (*model.AdblockSource, error) {
	query := `SELECT id, name, url, enabled, last_sync, last_modified, rule_count, created_at, updated_at
		FROM adblock_sources WHERE id = ?`
	var src model.AdblockSource
	err := s.db.Reader.QueryRow(query, id).Scan(
		&src.ID,
		&src.Name,
		&src.URL,
		&src.Enabled,
		&src.LastSync,
		&src.LastModified,
		&src.RuleCount,
		&src.CreatedAt,
		&src.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("adblock source 조회 실패: %w", err)
	}
	return &src, nil
}

func (s *AdblockStorage) ListAdblockSources() ([]*model.AdblockSource, error) {
	query := `SELECT id, name, url, enabled, last_sync, last_modified, rule_count, created_at, updated_at
		FROM adblock_sources ORDER BY id`
	rows, err := s.db.Reader.Query(query)
	if err != nil {
		return nil, fmt.Errorf("adblock source 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var sources []*model.AdblockSource
	for rows.Next() {
		var src model.AdblockSource
		if err := rows.Scan(
			&src.ID,
			&src.Name,
			&src.URL,
			&src.Enabled,
			&src.LastSync,
			&src.LastModified,
			&src.RuleCount,
			&src.CreatedAt,
			&src.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("adblock source 스캔 실패: %w", err)
		}
		sources = append(sources, &src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adblock source 행 반복 실패: %w", err)
	}

	s.cache.Set(sources)
	return sources, nil
}

func (s *AdblockStorage) GetEnabledAdblockSources() ([]*model.AdblockSource, error) {
	if sources, ok := s.cache.Get(); ok {
		enabled := make([]*model.AdblockSource, 0)
		for _, src := range sources {
			if src.Enabled {
				enabled = append(enabled, src)
			}
		}
		return enabled, nil
	}

	query := `SELECT id, name, url, enabled, last_sync, last_modified, rule_count, created_at, updated_at
		FROM adblock_sources WHERE enabled = 1 ORDER BY id`
	rows, err := s.db.Reader.Query(query)
	if err != nil {
		return nil, fmt.Errorf("활성 adblock source 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var sources []*model.AdblockSource
	for rows.Next() {
		var src model.AdblockSource
		if err := rows.Scan(
			&src.ID,
			&src.Name,
			&src.URL,
			&src.Enabled,
			&src.LastSync,
			&src.LastModified,
			&src.RuleCount,
			&src.CreatedAt,
			&src.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("adblock source 스캔 실패: %w", err)
		}
		sources = append(sources, &src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("adblock source 행 반복 실패: %w", err)
	}

	return sources, nil
}

func (s *AdblockStorage) CreateAdblockSource(source *model.AdblockSource) (int64, error) {
	query := `INSERT INTO adblock_sources (name, url, enabled) VALUES (?, ?, ?)`
	result, err := s.db.Writer.Exec(query, source.Name, source.URL, source.Enabled)
	if err != nil {
		return 0, fmt.Errorf("adblock source 생성 실패: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("adblock source ID 확인 실패: %w", err)
	}
	s.cache.Invalidate()
	return id, nil
}

func (s *AdblockStorage) UpdateAdblockSource(source *model.AdblockSource) error {
	query := `UPDATE adblock_sources SET name = ?, url = ?, enabled = ?, last_sync = ?, last_modified = ?, rule_count = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`
	result, err := s.db.Writer.Exec(query,
		source.Name,
		source.URL,
		source.Enabled,
		source.LastSync,
		source.LastModified,
		source.RuleCount,
		source.ID,
	)
	if err != nil {
		return fmt.Errorf("adblock source 업데이트 실패: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("adblock source를 찾을 수 없습니다")
	}
	s.cache.Invalidate()
	return nil
}

func (s *AdblockStorage) DeleteAdblockSource(id int64) error {
	result, err := s.db.Writer.Exec("DELETE FROM adblock_sources WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("adblock source 삭제 실패: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("adblock source를 찾을 수 없습니다")
	}
	s.cache.Invalidate()
	return nil
}

func (s *AdblockStorage) AddBlockedDomain(sourceID int64, domain string) error {
	domain = normalizeDomain(domain)
	query := `INSERT OR IGNORE INTO adblock_domains (domain, source_id) VALUES (?, ?)`
	_, err := s.db.Writer.Exec(query, domain, sourceID)
	if err != nil {
		return fmt.Errorf("차단 도메인 추가 실패: %w", err)
	}
	return nil
}

func (s *AdblockStorage) RemoveBlockedDomains(sourceID int64) error {
	_, err := s.db.Writer.Exec("DELETE FROM adblock_domains WHERE source_id = ?", sourceID)
	if err != nil {
		return fmt.Errorf("차단 도메인 삭제 실패: %w", err)
	}
	return nil
}

func (s *AdblockStorage) IsBlocked(domain string) (bool, error) {
	domain = normalizeDomain(domain)
	var id int64
	err := s.db.Reader.QueryRow("SELECT id FROM adblock_domains WHERE domain = ?", domain).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("차단 조회 실패: %w", err)
	}
	return true, nil
}

func (s *AdblockStorage) GetBlockedDomainCount() (int64, error) {
	var count int64
	if err := s.db.Reader.QueryRow("SELECT COUNT(*) FROM adblock_domains").Scan(&count); err != nil {
		return 0, fmt.Errorf("차단 도메인 수 조회 실패: %w", err)
	}
	return count, nil
}

func (s *AdblockStorage) ListBlockedDomains() ([]string, error) {
	rows, err := s.db.Reader.Query("SELECT domain FROM adblock_domains")
	if err != nil {
		return nil, fmt.Errorf("차단 도메인 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var domains []string
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			return nil, fmt.Errorf("차단 도메인 스캔 실패: %w", err)
		}
		domains = append(domains, domain)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("차단 도메인 행 반복 실패: %w", err)
	}
	return domains, nil
}

func (s *AdblockStorage) RecordBlockedQuery(domain, clientIP string) error {
	domain = normalizeDomain(domain)
	query := `INSERT INTO adblock_stats (blocked_domain, client_ip) VALUES (?, ?)`
	_, err := s.db.Writer.Exec(query, domain, clientIP)
	if err != nil {
		return fmt.Errorf("차단 통계 기록 실패: %w", err)
	}
	return nil
}

type BlockedStat struct {
	Domain string `json:"domain"`
	Count  int64  `json:"count"`
}

func (s *AdblockStorage) GetBlockedStats(limit int) ([]*BlockedStat, error) {
	if limit <= 0 {
		limit = 10
	}
	query := `SELECT blocked_domain, COUNT(*) as cnt FROM adblock_stats
		GROUP BY blocked_domain ORDER BY cnt DESC LIMIT ?`
	rows, err := s.db.Reader.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("차단 통계 조회 실패: %w", err)
	}
	defer rows.Close()

	var stats []*BlockedStat
	for rows.Next() {
		var stat BlockedStat
		if err := rows.Scan(&stat.Domain, &stat.Count); err != nil {
			return nil, fmt.Errorf("차단 통계 스캔 실패: %w", err)
		}
		stats = append(stats, &stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("차단 통계 행 반복 실패: %w", err)
	}
	return stats, nil
}

func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	domain = strings.ToLower(domain)
	domain = strings.TrimSuffix(domain, ".")
	return domain
}
