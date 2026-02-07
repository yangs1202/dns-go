package gslb

import (
	"database/sql"
	"dns-go/model"
	"dns-go/storage"
	"fmt"
	"strings"
	"sync"
	"time"
)

type PolicyCache struct {
	mu       sync.RWMutex
	policies map[string]*model.GSLBPolicy
	expiry   time.Time
	ttl      time.Duration
}

func NewPolicyCache(ttl time.Duration) *PolicyCache {
	return &PolicyCache{
		policies: make(map[string]*model.GSLBPolicy),
		ttl:      ttl,
	}
}

func (c *PolicyCache) Get(key string) (*model.GSLBPolicy, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Now().After(c.expiry) {
		return nil, false
	}
	policy, ok := c.policies[key]
	return policy, ok
}

func (c *PolicyCache) Set(policies []*model.GSLBPolicy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.policies = make(map[string]*model.GSLBPolicy)
	for _, policy := range policies {
		key := cachePolicyKey(policy.Domain, policy.RecordType)
		c.policies[key] = policy
	}
	c.expiry = time.Now().Add(c.ttl)
}

func (c *PolicyCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.policies = make(map[string]*model.GSLBPolicy)
	c.expiry = time.Time{}
}

type PolicyStorage struct {
	db    *storage.Database
	cache *PolicyCache
}

func NewPolicyStorage(db *storage.Database) *PolicyStorage {
	return &PolicyStorage{
		db:    db,
		cache: NewPolicyCache(5 * time.Minute),
	}
}

func (s *PolicyStorage) GetPolicy(id int64) (*model.GSLBPolicy, error) {
	query := `SELECT id, name, domain, record_type, ttl, enabled, created_at FROM gslb_policies WHERE id = ?`
	var policy model.GSLBPolicy
	err := s.db.Reader.QueryRow(query, id).Scan(
		&policy.ID,
		&policy.Name,
		&policy.Domain,
		&policy.RecordType,
		&policy.TTL,
		&policy.Enabled,
		&policy.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("정책 조회 실패: %w", err)
	}
	return &policy, nil
}

func (s *PolicyStorage) GetPolicyByDomain(domain, recordType string) (*model.GSLBPolicy, error) {
	key := cachePolicyKey(domain, recordType)
	if policy, ok := s.cache.Get(key); ok {
		return policy, nil
	}

	query := `SELECT id, name, domain, record_type, ttl, enabled, created_at
		FROM gslb_policies WHERE domain = ? AND record_type = ? AND enabled = 1`
	var policy model.GSLBPolicy
	err := s.db.Reader.QueryRow(query, domain, recordType).Scan(
		&policy.ID,
		&policy.Name,
		&policy.Domain,
		&policy.RecordType,
		&policy.TTL,
		&policy.Enabled,
		&policy.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("정책 조회 실패: %w", err)
	}
	return &policy, nil
}

func (s *PolicyStorage) ListPolicies() ([]*model.GSLBPolicy, error) {
	query := `SELECT id, name, domain, record_type, ttl, enabled, created_at FROM gslb_policies ORDER BY id`
	rows, err := s.db.Reader.Query(query)
	if err != nil {
		return nil, fmt.Errorf("정책 목록 조회 실패: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var policies []*model.GSLBPolicy
	for rows.Next() {
		var policy model.GSLBPolicy
		if err := rows.Scan(
			&policy.ID,
			&policy.Name,
			&policy.Domain,
			&policy.RecordType,
			&policy.TTL,
			&policy.Enabled,
			&policy.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("정책 스캔 실패: %w", err)
		}
		policies = append(policies, &policy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("정책 행 반복 실패: %w", err)
	}

	s.cache.Set(policies)
	return policies, nil
}

func (s *PolicyStorage) CreatePolicy(policy *model.GSLBPolicy) (int64, error) {
	if policy.RecordType == "" {
		policy.RecordType = "A"
	}
	query := `INSERT INTO gslb_policies (name, domain, record_type, ttl, enabled) VALUES (?, ?, ?, ?, ?)`
	result, err := s.db.Writer.Exec(query,
		policy.Name,
		policy.Domain,
		strings.ToUpper(policy.RecordType),
		policy.TTL,
		policy.Enabled,
	)
	if err != nil {
		return 0, fmt.Errorf("정책 생성 실패: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("정책 ID 확인 실패: %w", err)
	}
	s.cache.Invalidate()
	return id, nil
}

func (s *PolicyStorage) UpdatePolicy(policy *model.GSLBPolicy) error {
	query := `UPDATE gslb_policies SET name = ?, domain = ?, record_type = ?, ttl = ?, enabled = ? WHERE id = ?`
	result, err := s.db.Writer.Exec(query,
		policy.Name,
		policy.Domain,
		strings.ToUpper(policy.RecordType),
		policy.TTL,
		policy.Enabled,
		policy.ID,
	)
	if err != nil {
		return fmt.Errorf("정책 업데이트 실패: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("정책을 찾을 수 없습니다")
	}
	s.cache.Invalidate()
	return nil
}

func (s *PolicyStorage) DeletePolicy(id int64) error {
	result, err := s.db.Writer.Exec("DELETE FROM gslb_policies WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("정책 삭제 실패: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("정책을 찾을 수 없습니다")
	}
	s.cache.Invalidate()
	return nil
}

func cachePolicyKey(domain, recordType string) string {
	return strings.ToLower(domain) + ":" + strings.ToUpper(recordType)
}
