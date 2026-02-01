package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "dns"

var (
	// DNS 쿼리
	QueriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "queries_total",
		Help:      "Total number of DNS queries received",
	}, []string{"qtype", "rcode"})

	QueriesBlockedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "queries_blocked_total",
		Help:      "Total number of DNS queries blocked by adblock filter",
	})

	QueriesForwardedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "queries_forwarded_total",
		Help:      "Total number of DNS queries forwarded to upstream",
	})

	// DNS 응답 시간
	QueryDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "query_duration_seconds",
		Help:      "DNS query processing duration in seconds",
		Buckets:   []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
	}, []string{"source"}) // cache, zone, upstream, adblock, gslb

	// 캐시
	CacheSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "cache_size",
		Help:      "Current number of entries in DNS cache",
	})

	CacheHitsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cache_hits_total",
		Help:      "Total number of DNS cache hits",
	})

	CacheMissesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cache_misses_total",
		Help:      "Total number of DNS cache misses",
	})

	CacheEvictionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cache_evictions_total",
		Help:      "Total number of DNS cache LRU evictions",
	})

	CachePrefetchTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "cache_prefetch_total",
		Help:      "Total number of DNS cache prefetch triggers",
	})

	// Adblock
	AdblockRulesTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "adblock_rules_total",
		Help:      "Total number of adblock rules loaded",
	})

	AdblockBlockedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "adblock_blocked_total",
		Help:      "Total number of queries blocked by adblock",
	})

	AdblockLastSyncTimestamp = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "adblock_last_sync_timestamp",
		Help:      "Unix timestamp of last adblock sync",
	})

	AdblockSourceRules = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "adblock_source_rules",
		Help:      "Number of rules per adblock source",
	}, []string{"source_id", "source_name"})

	AdblockSourceLastSync = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "adblock_source_last_sync",
		Help:      "Unix timestamp of last sync per adblock source",
	}, []string{"source_id", "source_name"})

	// 업스트림
	UpstreamRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "upstream_requests_total",
		Help:      "Total number of upstream DNS requests",
	}, []string{"server_id", "server_name", "status"})

	UpstreamDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "upstream_duration_seconds",
		Help:      "Upstream DNS request duration in seconds",
		Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5},
	}, []string{"server_id", "server_name"})

	// GSLB
	GSLBQueriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "gslb_queries_total",
		Help:      "Total number of GSLB resolved queries",
	}, []string{"policy_id", "policy_name"})

	GSLBHealthStatus = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "gslb_health_status",
		Help:      "GSLB member health status (1=healthy, 0=unhealthy)",
	}, []string{"member_id", "member_address", "pool_id", "pool_name"})
)

func init() {
	prometheus.MustRegister(
		// DNS 쿼리
		QueriesTotal,
		QueriesBlockedTotal,
		QueriesForwardedTotal,
		QueryDurationSeconds,

		// 캐시
		CacheSize,
		CacheHitsTotal,
		CacheMissesTotal,
		CacheEvictionsTotal,
		CachePrefetchTotal,

		// Adblock
		AdblockRulesTotal,
		AdblockBlockedTotal,
		AdblockLastSyncTimestamp,
		AdblockSourceRules,
		AdblockSourceLastSync,

		// 업스트림
		UpstreamRequestsTotal,
		UpstreamDurationSeconds,

		// GSLB
		GSLBQueriesTotal,
		GSLBHealthStatus,
	)
}
