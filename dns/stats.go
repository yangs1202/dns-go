package dns

import "sync/atomic"

type QueryStats struct {
	Total        uint64
	L1Hits       uint64
	L1Misses     uint64
	UpstreamHits uint64
}

func NewQueryStats() *QueryStats {
	return &QueryStats{}
}

func (s *QueryStats) IncTotal() {
	atomic.AddUint64(&s.Total, 1)
}

func (s *QueryStats) IncL1Hit() {
	atomic.AddUint64(&s.L1Hits, 1)
}

func (s *QueryStats) IncL1Miss() {
	atomic.AddUint64(&s.L1Misses, 1)
}

func (s *QueryStats) IncUpstreamHit() {
	atomic.AddUint64(&s.UpstreamHits, 1)
}

func (s *QueryStats) Snapshot() QueryStats {
	return QueryStats{
		Total:        atomic.LoadUint64(&s.Total),
		L1Hits:       atomic.LoadUint64(&s.L1Hits),
		L1Misses:     atomic.LoadUint64(&s.L1Misses),
		UpstreamHits: atomic.LoadUint64(&s.UpstreamHits),
	}
}
