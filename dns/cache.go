package dns

import (
	"dns-go/metrics"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// DNSCacheEntry represents a cached DNS response
type DNSCacheEntry struct {
	RRs               []dns.RR  // DNS 응답 레코드
	Expiry            time.Time // 만료 시간
	PrefetchTime      time.Time // Prefetch 트리거 시간 (TTL의 90%)
	IsNegative        bool      // NXDOMAIN 여부
	prefetchTriggered uint32    // Atomic flag for prefetch (0 = not triggered, 1 = triggered)
}

// IsExpired checks if the cache entry has expired
func (e *DNSCacheEntry) IsExpired() bool {
	return time.Now().After(e.Expiry)
}

// ShouldPrefetch checks if the entry should trigger a prefetch
func (e *DNSCacheEntry) ShouldPrefetch() bool {
	return time.Now().After(e.PrefetchTime)
}

// CacheStats tracks cache performance statistics
type CacheStats struct {
	Hits      uint64 // 캐시 히트 수 (atomic)
	Misses    uint64 // 캐시 미스 수 (atomic)
	Evictions uint64 // LRU 제거 수 (atomic)
	Size      uint64 // 현재 캐시 항목 수 (atomic)
}

// cacheItem wraps a cache entry with metadata for LRU
type cacheItem struct {
	entry      *DNSCacheEntry
	key        string
	lastAccess time.Time
}

type prefetchCallback struct {
	fn func(domain, qtype string)
}

// DNSCache implements an L1 cache for DNS responses
type DNSCache struct {
	entries         sync.Map              // key: "domain:qtype" (예: "example.com.:A")
	maxSize         int64                 // 최대 캐시 항목 수
	defaultTTL      int64                 // 기본 TTL (초)
	negativeTTL     int64                 // Negative 캐시 TTL (초)
	prefetchTrigger float64               // Prefetch 트리거 비율 (0.9 = 90%)
	stats           CacheStats            // 히트율 통계
	prefetchFn      atomic.Value          // stores func(domain, qtype string)
	mu              sync.RWMutex          // items 맵 보호용
	items           map[string]*cacheItem // LRU 추적용
	stopCh          chan struct{}         // cleanup goroutine 중지용
	doneCh          chan struct{}         // cleanup goroutine 종료 대기용
	stopOnce        sync.Once             // Stop이 한 번만 실행되도록 보장
}

// NewDNSCache creates a new DNS cache
func NewDNSCache(maxSize int64, defaultTTL, negativeTTL int64, prefetchTrigger float64) *DNSCache {
	cache := &DNSCache{
		maxSize:         maxSize,
		defaultTTL:      defaultTTL,
		negativeTTL:     negativeTTL,
		prefetchTrigger: prefetchTrigger,
		items:           make(map[string]*cacheItem),
		stopCh:          make(chan struct{}),
		doneCh:          make(chan struct{}),
	}

	// Start background cleanup goroutine
	go cache.cleanupExpired()

	return cache
}

// SetPrefetchFunc sets the prefetch callback function
func (c *DNSCache) SetPrefetchFunc(fn func(domain, qtype string)) {
	c.prefetchFn.Store(prefetchCallback{fn: fn})
}

// Stop gracefully stops the cache cleanup goroutine
func (c *DNSCache) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
		<-c.doneCh
	})
}

// Get retrieves a cached DNS entry
func (c *DNSCache) Get(domain, qtype string) (*DNSCacheEntry, bool) {
	key := makeKey(domain, qtype)

	for {
		value, ok := c.entries.Load(key)
		if !ok {
			atomic.AddUint64(&c.stats.Misses, 1)
			metrics.CacheMissesTotal.Inc()
			return nil, false
		}

		entry := value.(*DNSCacheEntry)

		// Check if expired. Delete only the same entry we loaded so a
		// concurrent refresh cannot be removed by an older reader.
		if entry.IsExpired() {
			c.mu.Lock()
			current, exists := c.entries.Load(key)
			if exists && current == value {
				c.entries.Delete(key)
				delete(c.items, key)
				atomic.AddUint64(&c.stats.Size, ^uint64(0)) // Decrement
				metrics.CacheSize.Set(float64(atomic.LoadUint64(&c.stats.Size)))
				c.mu.Unlock()
				atomic.AddUint64(&c.stats.Misses, 1)
				metrics.CacheMissesTotal.Inc()
				return nil, false
			}
			c.mu.Unlock()
			continue
		}

		// Update last access time for LRU
		c.mu.Lock()
		if item, exists := c.items[key]; exists && item.entry == entry {
			item.lastAccess = time.Now()
		}
		c.mu.Unlock()

		atomic.AddUint64(&c.stats.Hits, 1)
		metrics.CacheHitsTotal.Inc()

		// Check if prefetch should be triggered
		c.checkPrefetch(entry, domain, qtype)

		return entry, true
	}
}

// Set stores a DNS response in the cache
func (c *DNSCache) Set(domain, qtype string, rrs []dns.RR, ttl int64, isNegative bool) {
	key := makeKey(domain, qtype)

	// Determine TTL
	effectiveTTL := ttl
	if effectiveTTL == 0 {
		if isNegative {
			effectiveTTL = c.negativeTTL
		} else {
			effectiveTTL = c.defaultTTL
		}
	}

	now := time.Now()
	duration := time.Duration(effectiveTTL) * time.Second
	prefetchSeconds := float64(effectiveTTL) * c.prefetchTrigger
	prefetchDuration := time.Duration(prefetchSeconds * float64(time.Second))

	entry := &DNSCacheEntry{
		RRs:          rrs,
		Expiry:       now.Add(duration),
		PrefetchTime: now.Add(prefetchDuration),
		IsNegative:   isNegative,
	}

	c.mu.Lock()
	_, exists := c.entries.Load(key)

	// Check if we need to evict before adding a new entry.
	if !exists && int64(len(c.items)) >= c.maxSize {
		c.evictOldestLocked()
	}

	c.entries.Store(key, entry)
	c.items[key] = &cacheItem{
		entry:      entry,
		key:        key,
		lastAccess: now,
	}

	// Increment size only if it's a new entry
	if !exists {
		atomic.AddUint64(&c.stats.Size, 1)
		metrics.CacheSize.Set(float64(atomic.LoadUint64(&c.stats.Size)))
	}
	c.mu.Unlock()
}

// Delete removes all cache entries for a specific domain
func (c *DNSCache) Delete(domain string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Find all keys matching the domain
	keysToDelete := make([]string, 0)
	for key := range c.items {
		// Keys are in format "domain:qtype"
		// Match if key starts with domain
		if len(key) > len(domain) && key[:len(domain)] == domain && key[len(domain)] == ':' {
			keysToDelete = append(keysToDelete, key)
		}
	}

	// Delete matching entries
	for _, key := range keysToDelete {
		c.entries.Delete(key)
		delete(c.items, key)
		atomic.AddUint64(&c.stats.Size, ^uint64(0)) // Decrement
	}
	if len(keysToDelete) > 0 {
		metrics.CacheSize.Set(float64(atomic.LoadUint64(&c.stats.Size)))
	}
}

// Clear removes all entries from the cache
func (c *DNSCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Clear sync.Map
	c.entries.Range(func(key, value interface{}) bool {
		c.entries.Delete(key)
		return true
	})

	// Clear items map
	c.items = make(map[string]*cacheItem)

	// Reset size
	atomic.StoreUint64(&c.stats.Size, 0)
	metrics.CacheSize.Set(0)
}

// GetStats returns a copy of the cache statistics
func (c *DNSCache) GetStats() CacheStats {
	return CacheStats{
		Hits:      atomic.LoadUint64(&c.stats.Hits),
		Misses:    atomic.LoadUint64(&c.stats.Misses),
		Evictions: atomic.LoadUint64(&c.stats.Evictions),
		Size:      atomic.LoadUint64(&c.stats.Size),
	}
}

// Size returns the current number of cache entries
func (c *DNSCache) Size() int {
	return int(atomic.LoadUint64(&c.stats.Size))
}

// checkPrefetch checks if prefetch should be triggered for an entry
func (c *DNSCache) checkPrefetch(entry *DNSCacheEntry, domain, qtype string) {
	value := c.prefetchFn.Load()
	if value == nil {
		return
	}
	callback, ok := value.(prefetchCallback)
	if !ok || callback.fn == nil {
		return
	}

	if entry.ShouldPrefetch() && !entry.IsNegative {
		// Use atomic CAS to ensure prefetch is only triggered once
		if atomic.CompareAndSwapUint32(&entry.prefetchTriggered, 0, 1) {
			metrics.CachePrefetchTotal.Inc()
			// Trigger prefetch asynchronously
			go callback.fn(domain, qtype)
		}
	}
}

func (c *DNSCache) evictOldestLocked() {
	if len(c.items) == 0 {
		return
	}

	// Find the oldest item
	var oldestKey string
	var oldestTime time.Time
	first := true

	for key, item := range c.items {
		if first || item.lastAccess.Before(oldestTime) {
			oldestKey = key
			oldestTime = item.lastAccess
			first = false
		}
	}

	// Remove the oldest item
	if oldestKey != "" {
		c.entries.Delete(oldestKey)
		delete(c.items, oldestKey)
		atomic.AddUint64(&c.stats.Size, ^uint64(0)) // Decrement
		atomic.AddUint64(&c.stats.Evictions, 1)
		metrics.CacheEvictionsTotal.Inc()
		metrics.CacheSize.Set(float64(atomic.LoadUint64(&c.stats.Size)))
	}
}

// cleanupExpired periodically removes expired entries
func (c *DNSCache) cleanupExpired() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	defer close(c.doneCh)

	for {
		select {
		case <-ticker.C:
			c.removeExpired()
		case <-c.stopCh:
			return
		}
	}
}

// removeExpired removes all expired entries from the cache
func (c *DNSCache) removeExpired() {
	c.mu.Lock()
	expiredKeys := make([]string, 0)

	for key, item := range c.items {
		if item.entry.IsExpired() {
			expiredKeys = append(expiredKeys, key)
		}
	}

	for _, key := range expiredKeys {
		c.entries.Delete(key)
		delete(c.items, key)
		atomic.AddUint64(&c.stats.Size, ^uint64(0)) // Decrement
	}
	if len(expiredKeys) > 0 {
		metrics.CacheSize.Set(float64(atomic.LoadUint64(&c.stats.Size)))
	}
	c.mu.Unlock()
}

// makeKey creates a cache key from domain and query type
func makeKey(domain, qtype string) string {
	return domain + ":" + qtype
}
