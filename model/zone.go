package model

import "time"

// Zone은 DNS Zone을 나타냅니다
type Zone struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`           // "example.com."
	SOAMname      string    `json:"soa_mname"`      // Primary NS
	SOARname      string    `json:"soa_rname"`      // Admin email
	SOASerial     int64     `json:"soa_serial"`     // Serial number
	SOARefresh    int64     `json:"soa_refresh"`    // Refresh interval
	SOARetry      int64     `json:"soa_retry"`      // Retry interval
	SOAExpire     int64     `json:"soa_expire"`     // Expire time
	SOAMinimum    int64     `json:"soa_minimum"`    // Minimum TTL
	Enabled       bool      `json:"enabled"`
	AllowFallback bool      `json:"allow_fallback"` // Record 없을 때 Upstream Fallback 허용
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
