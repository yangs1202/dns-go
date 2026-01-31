package model

import "time"

// CacheSettings는 캐시 설정을 나타냅니다
type CacheSettings struct {
	ID             int64     `json:"id"`              // Singleton (항상 1)
	Enabled        bool      `json:"enabled"`
	MaxSize        int64     `json:"max_size"`        // 최대 캐시 항목 수
	DefaultTTL     int64     `json:"default_ttl"`     // 기본 TTL (초)
	MinTTL         int64     `json:"min_ttl"`         // 최소 TTL (초)
	MaxTTL         int64     `json:"max_ttl"`         // 최대 TTL (초)
	NegativeTTL    int64     `json:"negative_ttl"`    // NXDOMAIN 캐시 TTL (초)
	PrefetchTrigger float64  `json:"prefetch_trigger"` // TTL의 N% 시점에 백그라운드 갱신
	UpdatedAt      time.Time `json:"updated_at"`
}
