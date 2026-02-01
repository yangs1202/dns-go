package gslb

import "time"

type HealthStatus struct {
	Healthy          bool      `json:"healthy"`
	ConsecutiveFails int       `json:"consecutiveFails"`
	ConsecutiveOKs   int       `json:"consecutiveOKs"`
	LastCheck        time.Time `json:"lastCheck"`
	LastError        string    `json:"lastError"`
}
