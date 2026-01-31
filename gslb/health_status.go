package gslb

import "time"

type HealthStatus struct {
	Healthy          bool
	ConsecutiveFails int
	ConsecutiveOKs   int
	LastCheck        time.Time
	LastError        string
}
