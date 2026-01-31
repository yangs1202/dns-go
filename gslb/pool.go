package gslb

import (
	"database/sql"
	"dns-go/model"
	"dns-go/storage"
	"fmt"
	"sync"
	"time"
)

type PoolCache struct {
	mu           sync.RWMutex
	pools        map[int64][]*model.GSLBPool
	members      map[int64][]*model.GSLBMember
	poolExpiry   map[int64]time.Time
	memberExpiry map[int64]time.Time
	ttl          time.Duration
}

func NewPoolCache(ttl time.Duration) *PoolCache {
	return &PoolCache{
		pools:        make(map[int64][]*model.GSLBPool),
		members:      make(map[int64][]*model.GSLBMember),
		poolExpiry:   make(map[int64]time.Time),
		memberExpiry: make(map[int64]time.Time),
		ttl:          ttl,
	}
}

func (c *PoolCache) getPools(policyID int64) ([]*model.GSLBPool, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if exp, ok := c.poolExpiry[policyID]; !ok || time.Now().After(exp) {
		return nil, false
	}
	pools, ok := c.pools[policyID]
	return pools, ok
}

func (c *PoolCache) setPools(policyID int64, pools []*model.GSLBPool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pools[policyID] = pools
	c.poolExpiry[policyID] = time.Now().Add(c.ttl)
}

func (c *PoolCache) getMembers(poolID int64) ([]*model.GSLBMember, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if exp, ok := c.memberExpiry[poolID]; !ok || time.Now().After(exp) {
		return nil, false
	}
	members, ok := c.members[poolID]
	return members, ok
}

func (c *PoolCache) setMembers(poolID int64, members []*model.GSLBMember) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.members[poolID] = members
	c.memberExpiry[poolID] = time.Now().Add(c.ttl)
}

func (c *PoolCache) invalidate(policyID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pools, policyID)
	delete(c.poolExpiry, policyID)
}

func (c *PoolCache) invalidateMembers(poolID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.members, poolID)
	delete(c.memberExpiry, poolID)
}

type PoolStorage struct {
	db    *storage.Database
	cache *PoolCache
}

func NewPoolStorage(db *storage.Database) *PoolStorage {
	return &PoolStorage{
		db:    db,
		cache: NewPoolCache(5 * time.Minute),
	}
}

func (s *PoolStorage) GetPool(id int64) (*model.GSLBPool, error) {
	query := `SELECT id, policy_id, name, match_type, match_value, priority, fallback_pool
		FROM gslb_pools WHERE id = ?`
	var pool model.GSLBPool
	err := s.db.Reader.QueryRow(query, id).Scan(
		&pool.ID,
		&pool.PolicyID,
		&pool.Name,
		&pool.MatchType,
		&pool.MatchValue,
		&pool.Priority,
		&pool.FallbackPool,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("풀 조회 실패: %w", err)
	}
	return &pool, nil
}

func (s *PoolStorage) GetPoolsByPolicy(policyID int64) ([]*model.GSLBPool, error) {
	if pools, ok := s.cache.getPools(policyID); ok {
		return pools, nil
	}

	query := `SELECT id, policy_id, name, match_type, match_value, priority, fallback_pool
		FROM gslb_pools WHERE policy_id = ? ORDER BY priority ASC`
	rows, err := s.db.Reader.Query(query, policyID)
	if err != nil {
		return nil, fmt.Errorf("풀 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var pools []*model.GSLBPool
	for rows.Next() {
		var pool model.GSLBPool
		if err := rows.Scan(
			&pool.ID,
			&pool.PolicyID,
			&pool.Name,
			&pool.MatchType,
			&pool.MatchValue,
			&pool.Priority,
			&pool.FallbackPool,
		); err != nil {
			return nil, fmt.Errorf("풀 스캔 실패: %w", err)
		}
		pools = append(pools, &pool)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("풀 행 반복 실패: %w", err)
	}

	s.cache.setPools(policyID, pools)
	return pools, nil
}

func (s *PoolStorage) CreatePool(pool *model.GSLBPool) (int64, error) {
	query := `INSERT INTO gslb_pools (policy_id, name, match_type, match_value, priority, fallback_pool)
		VALUES (?, ?, ?, ?, ?, ?)`
	result, err := s.db.Writer.Exec(query,
		pool.PolicyID,
		pool.Name,
		pool.MatchType,
		pool.MatchValue,
		pool.Priority,
		pool.FallbackPool,
	)
	if err != nil {
		return 0, fmt.Errorf("풀 생성 실패: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("풀 ID 확인 실패: %w", err)
	}
	s.cache.invalidate(pool.PolicyID)
	return id, nil
}

func (s *PoolStorage) UpdatePool(pool *model.GSLBPool) error {
	existing, err := s.GetPool(pool.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("풀을 찾을 수 없습니다")
	}

	query := `UPDATE gslb_pools SET name = ?, match_type = ?, match_value = ?, priority = ?, fallback_pool = ?
		WHERE id = ?`
	result, err := s.db.Writer.Exec(query,
		pool.Name,
		pool.MatchType,
		pool.MatchValue,
		pool.Priority,
		pool.FallbackPool,
		pool.ID,
	)
	if err != nil {
		return fmt.Errorf("풀 업데이트 실패: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("풀을 찾을 수 없습니다")
	}
	s.cache.invalidate(existing.PolicyID)
	return nil
}

func (s *PoolStorage) DeletePool(id int64) error {
	pool, err := s.GetPool(id)
	if err != nil {
		return err
	}
	if pool == nil {
		return fmt.Errorf("풀을 찾을 수 없습니다")
	}
	result, err := s.db.Writer.Exec("DELETE FROM gslb_pools WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("풀 삭제 실패: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("풀을 찾을 수 없습니다")
	}
	s.cache.invalidate(pool.PolicyID)
	return nil
}

func (s *PoolStorage) GetMember(id int64) (*model.GSLBMember, error) {
	query := `SELECT id, pool_id, address, weight, enabled FROM gslb_members WHERE id = ?`
	var member model.GSLBMember
	err := s.db.Reader.QueryRow(query, id).Scan(
		&member.ID,
		&member.PoolID,
		&member.Address,
		&member.Weight,
		&member.Enabled,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("멤버 조회 실패: %w", err)
	}
	return &member, nil
}

func (s *PoolStorage) GetMembersByPool(poolID int64) ([]*model.GSLBMember, error) {
	if members, ok := s.cache.getMembers(poolID); ok {
		return members, nil
	}

	query := `SELECT id, pool_id, address, weight, enabled FROM gslb_members WHERE pool_id = ? ORDER BY id`
	rows, err := s.db.Reader.Query(query, poolID)
	if err != nil {
		return nil, fmt.Errorf("멤버 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var members []*model.GSLBMember
	for rows.Next() {
		var member model.GSLBMember
		if err := rows.Scan(
			&member.ID,
			&member.PoolID,
			&member.Address,
			&member.Weight,
			&member.Enabled,
		); err != nil {
			return nil, fmt.Errorf("멤버 스캔 실패: %w", err)
		}
		members = append(members, &member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("멤버 행 반복 실패: %w", err)
	}

	s.cache.setMembers(poolID, members)
	return members, nil
}

func (s *PoolStorage) CreateMember(member *model.GSLBMember) (int64, error) {
	query := `INSERT INTO gslb_members (pool_id, address, weight, enabled) VALUES (?, ?, ?, ?)`
	result, err := s.db.Writer.Exec(query,
		member.PoolID,
		member.Address,
		member.Weight,
		member.Enabled,
	)
	if err != nil {
		return 0, fmt.Errorf("멤버 생성 실패: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("멤버 ID 확인 실패: %w", err)
	}
	s.cache.invalidateMembers(member.PoolID)
	return id, nil
}

func (s *PoolStorage) UpdateMember(member *model.GSLBMember) error {
	query := `UPDATE gslb_members SET address = ?, weight = ?, enabled = ? WHERE id = ?`
	result, err := s.db.Writer.Exec(query,
		member.Address,
		member.Weight,
		member.Enabled,
		member.ID,
	)
	if err != nil {
		return fmt.Errorf("멤버 업데이트 실패: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("멤버를 찾을 수 없습니다")
	}
	s.cache.invalidateMembers(member.PoolID)
	return nil
}

func (s *PoolStorage) DeleteMember(id int64) error {
	member, err := s.GetMember(id)
	if err != nil {
		return err
	}
	if member == nil {
		return fmt.Errorf("멤버를 찾을 수 없습니다")
	}
	result, err := s.db.Writer.Exec("DELETE FROM gslb_members WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("멤버 삭제 실패: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("멤버를 찾을 수 없습니다")
	}
	s.cache.invalidateMembers(member.PoolID)
	return nil
}
