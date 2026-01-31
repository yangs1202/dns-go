package storage

import (
	"database/sql"
	"dns-go/model"
	"fmt"
	"sync"
	"time"
)

// UpstreamCache는 UpstreamServer 조회 결과를 캐싱합니다 (L2 캐시, 10분 TTL)
type UpstreamCache struct {
	mu      sync.RWMutex
	servers []*model.UpstreamServer // priority 오름차순 정렬된 목록
	expiry  time.Time
	ttl     time.Duration
}

// NewUpstreamCache는 새로운 UpstreamServer 캐시를 생성합니다
func NewUpstreamCache(ttl time.Duration) *UpstreamCache {
	return &UpstreamCache{
		servers: make([]*model.UpstreamServer, 0),
		ttl:     ttl,
	}
}

// Get은 캐시에서 UpstreamServer 목록을 조회합니다
func (c *UpstreamCache) Get() ([]*model.UpstreamServer, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if time.Now().After(c.expiry) {
		return nil, false
	}

	return c.servers, true
}

// Set은 UpstreamServer 목록을 캐시에 저장합니다
func (c *UpstreamCache) Set(servers []*model.UpstreamServer) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.servers = servers
	c.expiry = time.Now().Add(c.ttl)
}

// Invalidate는 캐시를 무효화합니다
func (c *UpstreamCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.servers = make([]*model.UpstreamServer, 0)
	c.expiry = time.Time{}
}

// UpstreamStorage는 UpstreamServer 저장소입니다
type UpstreamStorage struct {
	db    *Database
	cache *UpstreamCache
}

// NewUpstreamStorage는 새로운 UpstreamServer 저장소를 생성합니다
func NewUpstreamStorage(db *Database) *UpstreamStorage {
	return &UpstreamStorage{
		db:    db,
		cache: NewUpstreamCache(10 * time.Minute), // 10분 TTL
	}
}

// GetUpstreamServer는 ID로 UpstreamServer를 조회합니다
func (s *UpstreamStorage) GetUpstreamServer(id int64) (*model.UpstreamServer, error) {
	query := `SELECT id, name, address, protocol, priority, enabled, created_at, updated_at
	          FROM upstream_servers WHERE id = ?`

	var server model.UpstreamServer
	err := s.db.Reader.QueryRow(query, id).Scan(
		&server.ID,
		&server.Name,
		&server.Address,
		&server.Protocol,
		&server.Priority,
		&server.Enabled,
		&server.CreatedAt,
		&server.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("UpstreamServer 조회 실패: %w", err)
	}

	return &server, nil
}

// ListUpstreamServers는 모든 UpstreamServer를 조회합니다 (L2 캐시 활용, priority 오름차순 정렬)
func (s *UpstreamStorage) ListUpstreamServers() ([]*model.UpstreamServer, error) {
	// L2 캐시 확인
	if servers, ok := s.cache.Get(); ok {
		return servers, nil
	}

	// DB 조회
	query := `SELECT id, name, address, protocol, priority, enabled, created_at, updated_at
	          FROM upstream_servers ORDER BY priority ASC`

	rows, err := s.db.Reader.Query(query)
	if err != nil {
		return nil, fmt.Errorf("UpstreamServer 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var servers []*model.UpstreamServer
	for rows.Next() {
		var server model.UpstreamServer
		err := rows.Scan(
			&server.ID,
			&server.Name,
			&server.Address,
			&server.Protocol,
			&server.Priority,
			&server.Enabled,
			&server.CreatedAt,
			&server.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("UpstreamServer 스캔 실패: %w", err)
		}
		servers = append(servers, &server)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("UpstreamServer 행 반복 실패: %w", err)
	}

	// 캐시 업데이트
	s.cache.Set(servers)

	return servers, nil
}

// ListEnabledUpstreamServers는 활성화된 UpstreamServer만 조회합니다 (L2 캐시 활용)
func (s *UpstreamStorage) ListEnabledUpstreamServers() ([]*model.UpstreamServer, error) {
	// L2 캐시 확인
	if servers, ok := s.cache.Get(); ok {
		// 활성화된 서버만 필터링
		enabled := make([]*model.UpstreamServer, 0)
		for _, server := range servers {
			if server.Enabled {
				enabled = append(enabled, server)
			}
		}
		return enabled, nil
	}

	// DB 조회
	query := `SELECT id, name, address, protocol, priority, enabled, created_at, updated_at
	          FROM upstream_servers WHERE enabled = 1 ORDER BY priority ASC`

	rows, err := s.db.Reader.Query(query)
	if err != nil {
		return nil, fmt.Errorf("활성화된 UpstreamServer 목록 조회 실패: %w", err)
	}
	defer rows.Close()

	var servers []*model.UpstreamServer
	for rows.Next() {
		var server model.UpstreamServer
		err := rows.Scan(
			&server.ID,
			&server.Name,
			&server.Address,
			&server.Protocol,
			&server.Priority,
			&server.Enabled,
			&server.CreatedAt,
			&server.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("UpstreamServer 스캔 실패: %w", err)
		}
		servers = append(servers, &server)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("UpstreamServer 행 반복 실패: %w", err)
	}

	return servers, nil
}

// CreateUpstreamServer는 새로운 UpstreamServer를 생성합니다 (캐시 무효화)
func (s *UpstreamStorage) CreateUpstreamServer(server *model.UpstreamServer) (int64, error) {
	query := `INSERT INTO upstream_servers (name, address, protocol, priority, enabled)
	          VALUES (?, ?, ?, ?, ?)`

	result, err := s.db.Writer.Exec(query,
		server.Name,
		server.Address,
		server.Protocol,
		server.Priority,
		server.Enabled,
	)

	if err != nil {
		return 0, fmt.Errorf("UpstreamServer 생성 실패: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("UpstreamServer ID 조회 실패: %w", err)
	}

	// 캐시 무효화
	s.cache.Invalidate()

	return id, nil
}

// UpdateUpstreamServer는 UpstreamServer를 업데이트합니다 (캐시 무효화)
func (s *UpstreamStorage) UpdateUpstreamServer(server *model.UpstreamServer) error {
	query := `UPDATE upstream_servers
	          SET name = ?, address = ?, protocol = ?, priority = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
	          WHERE id = ?`

	result, err := s.db.Writer.Exec(query,
		server.Name,
		server.Address,
		server.Protocol,
		server.Priority,
		server.Enabled,
		server.ID,
	)

	if err != nil {
		return fmt.Errorf("UpstreamServer 업데이트 실패: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("UpstreamServer를 찾을 수 없습니다")
	}

	// 캐시 무효화
	s.cache.Invalidate()

	return nil
}

// DeleteUpstreamServer는 UpstreamServer를 삭제합니다 (캐시 무효화)
func (s *UpstreamStorage) DeleteUpstreamServer(id int64) error {
	result, err := s.db.Writer.Exec("DELETE FROM upstream_servers WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("UpstreamServer 삭제 실패: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("영향받은 행 확인 실패: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("UpstreamServer를 찾을 수 없습니다")
	}

	// 캐시 무효화
	s.cache.Invalidate()

	return nil
}
