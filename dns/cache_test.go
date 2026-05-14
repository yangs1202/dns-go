package dns

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// Helper function to create a test DNS RR
func createTestRR(domain string, ip string) dns.RR {
	rr := &dns.A{
		Hdr: dns.RR_Header{
			Name:   domain,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    300,
		},
		A: net.ParseIP(ip),
	}
	return rr
}

// TestGetSetBasic tests basic Get/Set operations
func TestGetSetBasic(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	domain := "example.com."
	qtype := "A"
	rrs := []dns.RR{createTestRR(domain, "1.2.3.4")}

	// Initially cache should be empty
	_, found := cache.Get(domain, qtype)
	if found {
		t.Error("Expected cache miss, got hit")
	}

	// Set cache entry
	cache.Set(domain, qtype, rrs, 300, false)

	// Now it should be found
	entry, found := cache.Get(domain, qtype)
	if !found {
		t.Fatal("Expected cache hit, got miss")
	}

	if len(entry.RRs) != 1 {
		t.Errorf("Expected 1 RR, got %d", len(entry.RRs))
	}

	if entry.IsNegative {
		t.Error("Expected non-negative entry")
	}

	// Check stats
	stats := cache.GetStats()
	if stats.Hits != 1 {
		t.Errorf("Expected 1 hit, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.Misses)
	}
	if stats.Size != 1 {
		t.Errorf("Expected size 1, got %d", stats.Size)
	}
}

func TestSetReplacesExistingEntry(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	domain := "replace.example."
	qtype := "A"
	cache.Set(domain, qtype, []dns.RR{createTestRR(domain, "1.2.3.4")}, 300, false)
	cache.Set(domain, qtype, []dns.RR{createTestRR(domain, "5.6.7.8")}, 120, false)

	entry, found := cache.Get(domain, qtype)
	if !found {
		t.Fatal("Expected cache hit after replacement")
	}
	if cache.Size() != 1 {
		t.Fatalf("Expected replacement to keep size 1, got %d", cache.Size())
	}
	a, ok := entry.RRs[0].(*dns.A)
	if !ok {
		t.Fatalf("Expected A record, got %T", entry.RRs[0])
	}
	if got := a.A.String(); got != "5.6.7.8" {
		t.Fatalf("Expected replacement IP 5.6.7.8, got %s", got)
	}
}

func TestSetConcurrentSameKeyKeepsSizeStable(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	domain := "same-key.example."
	qtype := "A"
	rrs := []dns.RR{createTestRR(domain, "192.0.2.10")}

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Set(domain, qtype, rrs, 300, false)
		}()
	}
	wg.Wait()

	if cache.Size() != 1 {
		t.Fatalf("Expected one logical cache entry, got size %d", cache.Size())
	}
	stats := cache.GetStats()
	if stats.Size != 1 {
		t.Fatalf("Expected stats size 1, got %d", stats.Size)
	}
	if _, ok := cache.Get(domain, qtype); !ok {
		t.Fatal("Expected cache entry to be readable")
	}
}

// TestTTLExpiry tests that entries expire after TTL
func TestTTLExpiry(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	domain := "example.com."
	qtype := "A"
	rrs := []dns.RR{createTestRR(domain, "1.2.3.4")}

	// Set with 1 second TTL
	cache.Set(domain, qtype, rrs, 1, false)

	// Should be found immediately
	_, found := cache.Get(domain, qtype)
	if !found {
		t.Fatal("Expected cache hit")
	}

	// Wait for expiry
	time.Sleep(1100 * time.Millisecond)

	// Should now be expired
	_, found = cache.Get(domain, qtype)
	if found {
		t.Error("Expected cache miss after expiry")
	}

	// Size should be 0 after expiry
	if cache.Size() != 0 {
		t.Errorf("Expected size 0 after expiry, got %d", cache.Size())
	}
}

// TestNegativeCaching tests NXDOMAIN caching
func TestNegativeCaching(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	domain := "nonexistent.com."
	qtype := "A"

	// Set negative cache entry
	cache.Set(domain, qtype, nil, 0, true)

	// Should be found
	entry, found := cache.Get(domain, qtype)
	if !found {
		t.Fatal("Expected cache hit for negative entry")
	}

	if !entry.IsNegative {
		t.Error("Expected negative entry")
	}

	if len(entry.RRs) > 0 {
		t.Error("Expected no RRs for negative entry")
	}

	// Check that it uses negative TTL
	expectedExpiry := time.Now().Add(60 * time.Second)
	if entry.Expiry.Before(expectedExpiry.Add(-5*time.Second)) ||
		entry.Expiry.After(expectedExpiry.Add(5*time.Second)) {
		t.Errorf("Expected expiry around %v, got %v", expectedExpiry, entry.Expiry)
	}
}

// TestPrefetchTrigger tests that prefetch is triggered at the right time
func TestPrefetchTrigger(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	prefetchCalled := make(chan bool, 1)
	prefetchDomain := ""
	prefetchQtype := ""

	cache.SetPrefetchFunc(func(domain, qtype string) {
		prefetchDomain = domain
		prefetchQtype = qtype
		prefetchCalled <- true
	})

	domain := "example.com."
	qtype := "A"
	rrs := []dns.RR{createTestRR(domain, "1.2.3.4")}

	// Set with 3 second TTL (prefetch at 2.7s with 0.9 trigger)
	cache.Set(domain, qtype, rrs, 3, false)

	// Get immediately - should not trigger prefetch
	_, found := cache.Get(domain, qtype)
	if !found {
		t.Fatal("Expected cache hit")
	}

	select {
	case <-prefetchCalled:
		t.Error("Prefetch should not be triggered immediately")
	case <-time.After(100 * time.Millisecond):
		// Expected - no prefetch yet
	}

	// Wait past prefetch time (90% of 3s = 2.7s)
	time.Sleep(2800 * time.Millisecond)

	// Get again - should trigger prefetch
	cache.Get(domain, qtype)

	select {
	case <-prefetchCalled:
		if prefetchDomain != domain {
			t.Errorf("Expected prefetch domain %s, got %s", domain, prefetchDomain)
		}
		if prefetchQtype != qtype {
			t.Errorf("Expected prefetch qtype %s, got %s", qtype, prefetchQtype)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Prefetch was not triggered")
	}
}

// TestLRUEviction tests that oldest entries are evicted when cache is full
func TestLRUEviction(t *testing.T) {
	cache := NewDNSCache(3, 300, 60, 0.9) // Small cache size
	defer cache.Stop()

	domain1 := "example1.com."
	domain2 := "example2.com."
	domain3 := "example3.com."
	domain4 := "example4.com."
	qtype := "A"

	rrs := []dns.RR{createTestRR("test.com.", "1.2.3.4")}

	// Fill cache to capacity
	cache.Set(domain1, qtype, rrs, 300, false)
	time.Sleep(10 * time.Millisecond)
	cache.Set(domain2, qtype, rrs, 300, false)
	time.Sleep(10 * time.Millisecond)
	cache.Set(domain3, qtype, rrs, 300, false)

	if cache.Size() != 3 {
		t.Errorf("Expected size 3, got %d", cache.Size())
	}

	// Access domain2 to make it more recent
	cache.Get(domain2, qtype)
	time.Sleep(10 * time.Millisecond)

	// Add domain4 - should evict domain1 (oldest)
	cache.Set(domain4, qtype, rrs, 300, false)

	if cache.Size() != 3 {
		t.Errorf("Expected size 3 after eviction, got %d", cache.Size())
	}

	// domain1 should be evicted
	_, found := cache.Get(domain1, qtype)
	if found {
		t.Error("Expected domain1 to be evicted")
	}

	// domain2, domain3, domain4 should still be present
	_, found = cache.Get(domain2, qtype)
	if !found {
		t.Error("Expected domain2 to be present")
	}

	_, found = cache.Get(domain3, qtype)
	if !found {
		t.Error("Expected domain3 to be present")
	}

	_, found = cache.Get(domain4, qtype)
	if !found {
		t.Error("Expected domain4 to be present")
	}

	// Check eviction stats
	stats := cache.GetStats()
	if stats.Evictions != 1 {
		t.Errorf("Expected 1 eviction, got %d", stats.Evictions)
	}
}

// TestConcurrency tests concurrent access to the cache
func TestConcurrency(t *testing.T) {
	cache := NewDNSCache(1000, 300, 60, 0.9)
	defer cache.Stop()

	var wg sync.WaitGroup
	numGoroutines := 50
	numOperations := 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				domain := "example.com."
				qtype := "A"
				rrs := []dns.RR{createTestRR(domain, "1.2.3.4")}
				cache.Set(domain, qtype, rrs, 300, false)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				domain := "example.com."
				qtype := "A"
				cache.Get(domain, qtype)
			}
		}(i)
	}

	wg.Wait()

	// Cache should still be consistent
	stats := cache.GetStats()
	if stats.Size == 0 {
		t.Error("Expected non-zero cache size")
	}
}

// TestDelete tests deleting all entries for a domain
func TestDelete(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	domain := "example.com."
	rrs := []dns.RR{createTestRR(domain, "1.2.3.4")}

	// Set multiple query types for the same domain
	cache.Set(domain, "A", rrs, 300, false)
	cache.Set(domain, "AAAA", rrs, 300, false)
	cache.Set(domain, "MX", rrs, 300, false)

	// Set different domain
	cache.Set("other.com.", "A", rrs, 300, false)

	if cache.Size() != 4 {
		t.Errorf("Expected size 4, got %d", cache.Size())
	}

	// Delete all entries for example.com
	cache.Delete(domain)

	// example.com entries should be gone
	_, found := cache.Get(domain, "A")
	if found {
		t.Error("Expected A record to be deleted")
	}

	_, found = cache.Get(domain, "AAAA")
	if found {
		t.Error("Expected AAAA record to be deleted")
	}

	_, found = cache.Get(domain, "MX")
	if found {
		t.Error("Expected MX record to be deleted")
	}

	// other.com should still be present
	_, found = cache.Get("other.com.", "A")
	if !found {
		t.Error("Expected other.com to still be present")
	}

	if cache.Size() != 1 {
		t.Errorf("Expected size 1 after delete, got %d", cache.Size())
	}
}

// TestClear tests clearing the entire cache
func TestClear(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	rrs := []dns.RR{createTestRR("test.com.", "1.2.3.4")}

	// Add multiple entries
	cache.Set("example1.com.", "A", rrs, 300, false)
	cache.Set("example2.com.", "A", rrs, 300, false)
	cache.Set("example3.com.", "A", rrs, 300, false)

	if cache.Size() != 3 {
		t.Errorf("Expected size 3, got %d", cache.Size())
	}

	// Clear cache
	cache.Clear()

	if cache.Size() != 0 {
		t.Errorf("Expected size 0 after clear, got %d", cache.Size())
	}

	// All entries should be gone
	_, found := cache.Get("example1.com.", "A")
	if found {
		t.Error("Expected cache to be empty")
	}

	stats := cache.GetStats()
	if stats.Size != 0 {
		t.Errorf("Expected stats size 0, got %d", stats.Size)
	}
}

// TestStats tests cache statistics tracking
func TestStats(t *testing.T) {
	cache := NewDNSCache(2, 300, 60, 0.9)
	defer cache.Stop()

	domain := "example.com."
	qtype := "A"
	rrs := []dns.RR{createTestRR(domain, "1.2.3.4")}

	// Initial miss
	cache.Get(domain, qtype)

	stats := cache.GetStats()
	if stats.Hits != 0 {
		t.Errorf("Expected 0 hits, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.Misses)
	}

	// Add entry
	cache.Set(domain, qtype, rrs, 300, false)

	// Hit
	cache.Get(domain, qtype)
	cache.Get(domain, qtype)

	stats = cache.GetStats()
	if stats.Hits != 2 {
		t.Errorf("Expected 2 hits, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.Misses)
	}
	if stats.Size != 1 {
		t.Errorf("Expected size 1, got %d", stats.Size)
	}

	// Trigger eviction
	cache.Set("example2.com.", "A", rrs, 300, false)
	cache.Set("example3.com.", "A", rrs, 300, false)

	stats = cache.GetStats()
	if stats.Evictions != 1 {
		t.Errorf("Expected 1 eviction, got %d", stats.Evictions)
	}
	if stats.Size != 2 {
		t.Errorf("Expected size 2, got %d", stats.Size)
	}
}

// TestDefaultTTL tests that default TTL is used when TTL is 0
func TestDefaultTTL(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	domain := "example.com."
	qtype := "A"
	rrs := []dns.RR{createTestRR(domain, "1.2.3.4")}

	// Set with TTL 0 - should use default
	cache.Set(domain, qtype, rrs, 0, false)

	entry, found := cache.Get(domain, qtype)
	if !found {
		t.Fatal("Expected cache hit")
	}

	// Check that expiry is around default TTL (300s)
	expectedExpiry := time.Now().Add(300 * time.Second)
	if entry.Expiry.Before(expectedExpiry.Add(-5*time.Second)) ||
		entry.Expiry.After(expectedExpiry.Add(5*time.Second)) {
		t.Errorf("Expected expiry around %v, got %v", expectedExpiry, entry.Expiry)
	}
}

// TestPrefetchNotTriggeredForNegative tests that prefetch is not triggered for negative entries
func TestPrefetchNotTriggeredForNegative(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	prefetchCalled := make(chan bool, 1)

	cache.SetPrefetchFunc(func(domain, qtype string) {
		prefetchCalled <- true
	})

	domain := "nonexistent.com."
	qtype := "A"

	// Set negative entry with short TTL
	cache.Set(domain, qtype, nil, 1, true)

	// Wait past prefetch time
	time.Sleep(1100 * time.Millisecond)

	// Get - should not trigger prefetch for negative entry
	cache.Get(domain, qtype)

	select {
	case <-prefetchCalled:
		t.Error("Prefetch should not be triggered for negative entries")
	case <-time.After(200 * time.Millisecond):
		// Expected - no prefetch for negative
	}
}

// TestCacheKeyFormat tests that cache keys are properly formatted
func TestCacheKeyFormat(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	domain := "example.com."
	rrs := []dns.RR{createTestRR(domain, "1.2.3.4")}

	// Set different query types
	cache.Set(domain, "A", rrs, 300, false)
	cache.Set(domain, "AAAA", rrs, 300, false)

	// Both should be retrievable independently
	_, foundA := cache.Get(domain, "A")
	_, foundAAAA := cache.Get(domain, "AAAA")

	if !foundA {
		t.Error("Expected to find A record")
	}
	if !foundAAAA {
		t.Error("Expected to find AAAA record")
	}

	if cache.Size() != 2 {
		t.Errorf("Expected size 2, got %d", cache.Size())
	}
}

// TestCleanupExpired tests the background cleanup of expired entries
func TestCleanupExpired(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	domain := "example.com."
	qtype := "A"
	rrs := []dns.RR{createTestRR(domain, "1.2.3.4")}

	// Set with very short TTL
	cache.Set(domain, qtype, rrs, 1, false)

	if cache.Size() != 1 {
		t.Errorf("Expected size 1, got %d", cache.Size())
	}

	// Wait for expiry + cleanup cycle
	// The cleanup runs every minute, but entry expires in 1 second
	// We test immediate expiry detection in Get instead
	time.Sleep(1100 * time.Millisecond)

	// Get should detect expiry and clean up
	_, found := cache.Get(domain, qtype)
	if found {
		t.Error("Expected entry to be expired")
	}

	if cache.Size() != 0 {
		t.Errorf("Expected size 0 after expiry, got %d", cache.Size())
	}
}

// BenchmarkCacheSet benchmarks cache Set operations
func BenchmarkCacheSet(b *testing.B) {
	cache := NewDNSCache(10000, 300, 60, 0.9)
	defer cache.Stop()
	rrs := []dns.RR{createTestRR("example.com.", "1.2.3.4")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		domain := "example.com."
		cache.Set(domain, "A", rrs, 300, false)
	}
}

// BenchmarkCacheGet benchmarks cache Get operations
func BenchmarkCacheGet(b *testing.B) {
	cache := NewDNSCache(10000, 300, 60, 0.9)
	defer cache.Stop()
	domain := "example.com."
	rrs := []dns.RR{createTestRR(domain, "1.2.3.4")}
	cache.Set(domain, "A", rrs, 300, false)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(domain, "A")
	}
}

// BenchmarkCacheConcurrent benchmarks concurrent cache operations
func BenchmarkCacheConcurrent(b *testing.B) {
	cache := NewDNSCache(10000, 300, 60, 0.9)
	defer cache.Stop()
	domain := "example.com."
	rrs := []dns.RR{createTestRR(domain, "1.2.3.4")}
	cache.Set(domain, "A", rrs, 300, false)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cache.Get(domain, "A")
		}
	})
}

// TestAtomicOperations tests that atomic operations work correctly
func TestAtomicOperations(t *testing.T) {
	cache := NewDNSCache(100, 300, 60, 0.9)
	defer cache.Stop()

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent increments to stats
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			atomic.AddUint64(&cache.stats.Hits, 1)
		}()
	}

	wg.Wait()

	stats := cache.GetStats()
	if stats.Hits != uint64(numGoroutines) {
		t.Errorf("Expected %d hits, got %d", numGoroutines, stats.Hits)
	}
}
