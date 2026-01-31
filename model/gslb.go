package model

import "time"

// GSLBPolicy는 GSLB 정책을 나타냅니다
type GSLBPolicy struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`        // 정책 이름
	Domain     string    `json:"domain"`      // 대상 도메인
	RecordType string    `json:"record_type"` // A 또는 AAAA
	TTL        int64     `json:"ttl"`         // 짧은 TTL (GSLB용)
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

// GSLBPool은 GSLB 풀을 나타냅니다
type GSLBPool struct {
	ID           int64  `json:"id"`
	PolicyID     int64  `json:"policy_id"`
	Name         string `json:"name"`         // "korea-pool", "default-pool"
	MatchType    string `json:"match_type"`   // "cidr", "geo_country", "geo_continent", "default"
	MatchValue   string `json:"match_value"`  // "10.0.0.0/8", "KR", "AS", "*"
	Priority     int64  `json:"priority"`     // 낮을수록 먼저 매칭
	FallbackPool bool   `json:"fallback_pool"` // 폴백 풀 여부
}

// GSLBMember는 GSLB 풀 멤버를 나타냅니다
type GSLBMember struct {
	ID      int64  `json:"id"`
	PoolID  int64  `json:"pool_id"`
	Address string `json:"address"` // "1.2.3.4" 또는 "2001:db8::1"
	Weight  int64  `json:"weight"`  // 가중치 (0-100)
	Enabled bool   `json:"enabled"`
}

// HealthCheck는 헬스체크 설정을 나타냅니다
type HealthCheck struct {
	ID                 int64  `json:"id"`
	MemberID           int64  `json:"member_id"`
	CheckType          string `json:"check_type"`           // "http", "https", "tcp"
	Target             string `json:"target"`               // "http://1.2.3.4:80/health" 또는 "1.2.3.4:80"
	IntervalSec        int64  `json:"interval_sec"`         // 체크 간격
	TimeoutSec         int64  `json:"timeout_sec"`          // 타임아웃
	HealthyThreshold   int64  `json:"healthy_threshold"`    // 정상 판정 임계값
	UnhealthyThreshold int64  `json:"unhealthy_threshold"`  // 비정상 판정 임계값
	Enabled            bool   `json:"enabled"`
}
