package model

import "time"

// Record는 DNS 레코드를 나타냅니다
type Record struct {
	ID        int64     `json:"id"`
	ZoneID    int64     `json:"zone_id"`
	Name      string    `json:"name"`     // "www.example.com."
	Type      string    `json:"type"`     // "A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "PTR", "CAA"
	Content   string    `json:"content"`  // 레코드 값
	TTL       int64     `json:"ttl"`      // Time to live
	Priority  int64     `json:"priority"` // MX, SRV 우선순위
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
