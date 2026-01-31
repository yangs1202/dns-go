package model

import (
	"database/sql"
	"time"
)

// AdblockSource는 광고차단 필터 소스를 나타냅니다
type AdblockSource struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`          // "AdGuard DNS Filter"
	URL          string         `json:"url"`           // 필터 파일 URL
	Enabled      bool           `json:"enabled"`
	LastSync     sql.NullTime   `json:"last_sync"`
	LastModified sql.NullString `json:"last_modified"` // ETag 또는 Last-Modified 헤더
	RuleCount    int64          `json:"rule_count"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// AdblockDomain은 차단 도메인을 나타냅니다
type AdblockDomain struct {
	ID       int64     `json:"id"`
	Domain   string    `json:"domain"`   // "doubleclick.net" (정규화)
	SourceID int64     `json:"source_id"`
	AddedAt  time.Time `json:"added_at"`
}
