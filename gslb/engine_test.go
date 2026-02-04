package gslb

import (
	"dns-go/model"
	"dns-go/storage"
	"net"
	"os"
	"sync"
	"testing"
)

func setupTestDB(t *testing.T) (*storage.Database, func()) {
	path := "/tmp/test_gslb_" + t.Name() + ".db"
	_ = os.Remove(path)
	db, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatalf("db init error: %v", err)
	}
	cleanup := func() {
		_ = db.Close()
		_ = os.Remove(path)
	}
	return db, cleanup
}

func TestResolveDefaultPool(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "app.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "default",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.10",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	ips, ttl, err := engine.Resolve("app.example.com.", "A", net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if ttl != 30 {
		t.Fatalf("expected ttl 30, got %d", ttl)
	}
	if len(ips) != 1 || ips[0].String() != "192.0.2.10" {
		t.Fatalf("unexpected ips: %v", ips)
	}
}

func TestResolveCIDRPool(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "cidr",
		Domain:     "internal.example.com.",
		RecordType: "A",
		TTL:        10,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "internal",
		MatchType:    "cidr",
		MatchValue:   "10.0.0.0/8",
		Priority:     0,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "10.1.1.1",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	ips, _, err := engine.Resolve("internal.example.com.", "A", net.ParseIP("10.2.3.4"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "10.1.1.1" {
		t.Fatalf("unexpected ips: %v", ips)
	}
}

func TestResolveWeightedSelection(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "weighted",
		Domain:     "lb.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "default",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.1",
		Weight:  50,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.2",
		Weight:  50,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	ips, ttl, err := engine.Resolve("lb.example.com.", "A", net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if ttl != 60 {
		t.Fatalf("expected ttl 60, got %d", ttl)
	}
	if len(ips) != 1 {
		t.Fatalf("expected 1 ip, got %d", len(ips))
	}
}

func TestResolveDisabledMember(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "disabled.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "default",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.10",
		Weight:  100,
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	ips, _, err := engine.Resolve("disabled.example.com.", "A", net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 0 {
		t.Fatalf("expected no ips for disabled member, got %v", ips)
	}
}

func TestResolveNoPolicy(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	ips, ttl, err := engine.Resolve("notfound.example.com.", "A", net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if ttl != 0 {
		t.Fatalf("expected ttl 0, got %d", ttl)
	}
	if len(ips) != 0 {
		t.Fatalf("expected no ips, got %v", ips)
	}
}

func TestResolveNilEngine(t *testing.T) {
	var engine *Engine
	ips, ttl, err := engine.Resolve("test.example.com.", "A", net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if ttl != 0 || len(ips) != 0 {
		t.Fatalf("expected empty result for nil engine")
	}
}

func TestResolveInvalidMemberIP(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "invalid.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "default",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "invalid-ip",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	_, _, err = engine.Resolve("invalid.example.com.", "A", net.ParseIP("203.0.113.1"))
	if err == nil {
		t.Fatalf("expected error for invalid IP")
	}
}

func TestMatchPoolGeoCountryNilGeoIP(t *testing.T) {
	// When geoip is nil, geo_country should return false
	engine := &Engine{geoip: nil}
	pool := &model.GSLBPool{
		MatchType:  "geo_country",
		MatchValue: "KR",
	}
	result := engine.matchPool(pool, net.ParseIP("8.8.8.8"))
	if result {
		t.Fatalf("geo_country should return false when geoip is nil")
	}
}

func TestMatchPoolGeoContinentNilGeoIP(t *testing.T) {
	// When geoip is nil, geo_continent should return false
	engine := &Engine{geoip: nil}
	pool := &model.GSLBPool{
		MatchType:  "geo_continent",
		MatchValue: "AS",
	}
	result := engine.matchPool(pool, net.ParseIP("8.8.8.8"))
	if result {
		t.Fatalf("geo_continent should return false when geoip is nil")
	}
}

func TestMatchPoolGeoCountryNilClientIP(t *testing.T) {
	// When clientIP is nil, geo_country should return false
	engine := &Engine{geoip: &GeoIPResolver{reader: nil}}
	pool := &model.GSLBPool{
		MatchType:  "geo_country",
		MatchValue: "KR",
	}
	result := engine.matchPool(pool, nil)
	if result {
		t.Fatalf("geo_country should return false when clientIP is nil")
	}
}

func TestMatchPoolGeoContinentNilClientIP(t *testing.T) {
	// When clientIP is nil, geo_continent should return false
	engine := &Engine{geoip: &GeoIPResolver{reader: nil}}
	pool := &model.GSLBPool{
		MatchType:  "geo_continent",
		MatchValue: "AS",
	}
	result := engine.matchPool(pool, nil)
	if result {
		t.Fatalf("geo_continent should return false when clientIP is nil")
	}
}

func TestMatchPoolGeoCountryResolverError(t *testing.T) {
	// GeoIP resolver with nil reader returns error on Country()
	// This exercises the error branch in geo_country
	engine := &Engine{geoip: &GeoIPResolver{reader: nil}}
	pool := &model.GSLBPool{
		MatchType:  "geo_country",
		MatchValue: "KR",
	}
	result := engine.matchPool(pool, net.ParseIP("8.8.8.8"))
	if result {
		t.Fatalf("geo_country should return false when geoip.Country returns error")
	}
}

func TestMatchPoolGeoContinentResolverError(t *testing.T) {
	// GeoIP resolver with nil reader returns error on Country()
	// This exercises the error branch in geo_continent
	engine := &Engine{geoip: &GeoIPResolver{reader: nil}}
	pool := &model.GSLBPool{
		MatchType:  "geo_continent",
		MatchValue: "AS",
	}
	result := engine.matchPool(pool, net.ParseIP("8.8.8.8"))
	if result {
		t.Fatalf("geo_continent should return false when geoip.Country returns error")
	}
}

func TestMatchPoolNilPool(t *testing.T) {
	engine := &Engine{}
	result := engine.matchPool(nil, net.ParseIP("8.8.8.8"))
	if result {
		t.Fatalf("matchPool should return false for nil pool")
	}
}

func TestMatchPoolCIDRNilClientIP(t *testing.T) {
	engine := &Engine{}
	pool := &model.GSLBPool{
		MatchType:  "cidr",
		MatchValue: "10.0.0.0/8",
	}
	result := engine.matchPool(pool, nil)
	if result {
		t.Fatalf("cidr should return false for nil clientIP")
	}
}

func TestResolveGeoCountryFallbackIntegration(t *testing.T) {
	// Integration test: geo_country pool with nil geoip should fall through to fallback
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "geo-test",
		Domain:     "geo.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	// geo_country pool (won't match because geoip is nil)
	geoPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "korea-pool",
		MatchType:    "geo_country",
		MatchValue:   "KR",
		Priority:     0,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create geo pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  geoPoolID,
		Address: "10.0.0.1",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create geo member error: %v", err)
	}

	// Fallback pool
	fallbackPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "fallback",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     100,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create fallback pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackPoolID,
		Address: "192.0.2.100",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create fallback member error: %v", err)
	}

	// nil geoip -> geo_country can't match -> fallback
	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	ips, _, err := engine.Resolve("geo.example.com.", "A", net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "192.0.2.100" {
		t.Fatalf("expected fallback ip 192.0.2.100, got %v", ips)
	}
}

func TestResolveGeoContinentFallbackIntegration(t *testing.T) {
	// Integration test: geo_continent pool with nil geoip should fall through to fallback
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "geocont-test",
		Domain:     "geocont.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	// geo_continent pool
	geoPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "asia-pool",
		MatchType:    "geo_continent",
		MatchValue:   "AS",
		Priority:     0,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create geo pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  geoPoolID,
		Address: "10.0.0.2",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create geo member error: %v", err)
	}

	// Fallback pool
	fallbackPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "fallback",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     100,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create fallback pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackPoolID,
		Address: "192.0.2.200",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create fallback member error: %v", err)
	}

	// nil geoip -> geo_continent can't match -> fallback
	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	ips, _, err := engine.Resolve("geocont.example.com.", "A", net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "192.0.2.200" {
		t.Fatalf("expected fallback ip 192.0.2.200, got %v", ips)
	}
}

func TestResolveNoMatchNoFallback(t *testing.T) {
	// When no pool matches and there's no fallback, should return nil
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "nomatch",
		Domain:     "nomatch.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	// CIDR pool that won't match the client IP, and NOT marked as fallback
	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "specific-cidr",
		MatchType:    "cidr",
		MatchValue:   "172.16.0.0/12",
		Priority:     0,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "172.16.0.1",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	// Client IP 8.8.8.8 doesn't match 172.16.0.0/12, and there's no fallback
	ips, _, err := engine.Resolve("nomatch.example.com.", "A", net.ParseIP("8.8.8.8"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 0 {
		t.Fatalf("expected no ips when no match and no fallback, got %v", ips)
	}
}

func TestResolveWeightedSelectEdgeCases(t *testing.T) {
	// Test the weightedSelect function's edge cases
	engine := &Engine{healthStatus: &sync.Map{}}

	// Test with empty members
	result := engine.weightedSelect(nil)
	if result != nil {
		t.Fatalf("expected nil for empty members, got %v", result)
	}

	result = engine.weightedSelect([]*model.GSLBMember{})
	if result != nil {
		t.Fatalf("expected nil for empty slice, got %v", result)
	}

	// Test with all zero-weight members
	members := []*model.GSLBMember{
		{ID: 1, Address: "10.0.0.1", Weight: 0, Enabled: true},
		{ID: 2, Address: "10.0.0.2", Weight: 0, Enabled: true},
	}
	result = engine.weightedSelect(members)
	if result == nil {
		t.Fatalf("expected non-nil for zero-weight members (should return first)")
	}
	if result.Address != "10.0.0.1" {
		t.Fatalf("expected first member for zero-weight, got %s", result.Address)
	}

	// Test with negative weight members (treated as 0)
	members2 := []*model.GSLBMember{
		{ID: 1, Address: "10.0.0.1", Weight: -10, Enabled: true},
		{ID: 2, Address: "10.0.0.2", Weight: -5, Enabled: true},
	}
	result = engine.weightedSelect(members2)
	if result == nil {
		t.Fatalf("expected non-nil for negative-weight members")
	}

	// Test with mixed positive and zero/negative weights
	// This covers lines 224-225 (skip negative weights in the selection loop)
	members3 := []*model.GSLBMember{
		{ID: 1, Address: "10.0.0.1", Weight: -10, Enabled: true},
		{ID: 2, Address: "10.0.0.2", Weight: 0, Enabled: true},
		{ID: 3, Address: "10.0.0.3", Weight: 100, Enabled: true},
	}
	result = engine.weightedSelect(members3)
	if result == nil {
		t.Fatalf("expected non-nil for mixed-weight members")
	}
	// The only member with positive weight is 10.0.0.3
	if result.Address != "10.0.0.3" {
		t.Fatalf("expected member with positive weight, got %s", result.Address)
	}
}

func TestMatchPoolGeoCountryWithResolverIntegration(t *testing.T) {
	// Test with non-nil GeoIPResolver that has nil reader
	// This exercises the geo_country code path where geoip is non-nil but returns error
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "geo-err",
		Domain:     "geoerr.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	geoPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "geo-pool",
		MatchType:    "geo_country",
		MatchValue:   "US",
		Priority:     0,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}
	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  geoPoolID,
		Address: "10.0.0.1",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	fallbackPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "fb",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     100,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create fallback pool error: %v", err)
	}
	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackPoolID,
		Address: "192.0.2.1",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create fallback member error: %v", err)
	}

	// Use GeoIPResolver with nil reader - Country() will return error
	geoip := &GeoIPResolver{reader: nil}
	engine := NewEngine(policyStorage, poolStorage, geoip, nil)

	ips, _, err := engine.Resolve("geoerr.example.com.", "A", net.ParseIP("8.8.8.8"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "192.0.2.1" {
		t.Fatalf("expected fallback ip due to geo error, got %v", ips)
	}
}

func TestMatchPoolGeoContinentWithResolverIntegration(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "geocont-err",
		Domain:     "geoconterr.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	geoPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "geo-pool",
		MatchType:    "geo_continent",
		MatchValue:   "EU",
		Priority:     0,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}
	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  geoPoolID,
		Address: "10.0.0.2",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	fallbackPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "fb",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     100,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create fallback pool error: %v", err)
	}
	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackPoolID,
		Address: "192.0.2.2",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create fallback member error: %v", err)
	}

	geoip := &GeoIPResolver{reader: nil}
	engine := NewEngine(policyStorage, poolStorage, geoip, nil)

	ips, _, err := engine.Resolve("geoconterr.example.com.", "A", net.ParseIP("8.8.8.8"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "192.0.2.2" {
		t.Fatalf("expected fallback ip due to geo error, got %v", ips)
	}
}

func TestResolveZeroWeightMembers(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "zero.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "default",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.10",
		Weight:  0,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	ips, _, err := engine.Resolve("zero.example.com.", "A", net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "192.0.2.10" {
		t.Fatalf("unexpected ips: %v", ips)
	}
}

func TestMatchPoolInvalidCIDR(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "cidr.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	_, err = poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "badcidr",
		MatchType:    "cidr",
		MatchValue:   "invalid-cidr",
		Priority:     0,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	fallbackID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "fallback",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     10,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create fallback pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackID,
		Address: "192.0.2.100",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	ips, _, err := engine.Resolve("cidr.example.com.", "A", net.ParseIP("10.0.0.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "192.0.2.100" {
		t.Fatalf("expected fallback ip, got %v", ips)
	}
}

func TestMatchPoolNilClientIP(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	_, err = poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "cidr",
		MatchType:    "cidr",
		MatchValue:   "10.0.0.0/8",
		Priority:     0,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	fallbackID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "fallback",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     10,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create fallback pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackID,
		Address: "192.0.2.100",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	ips, _, err := engine.Resolve("test.example.com.", "A", nil)
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "192.0.2.100" {
		t.Fatalf("expected fallback ip for nil client IP, got %v", ips)
	}
}

func TestMatchPoolUnknownType(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	_, err = poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "unknown",
		MatchType:    "unknown_type",
		MatchValue:   "test",
		Priority:     0,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	fallbackID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "fallback",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     10,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create fallback pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackID,
		Address: "192.0.2.100",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)
	ips, _, err := engine.Resolve("test.example.com.", "A", net.ParseIP("10.0.0.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "192.0.2.100" {
		t.Fatalf("expected fallback ip for unknown match type, got %v", ips)
	}
}

func TestResolveAllMembersUnhealthy(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "allfailed.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "default",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	member1ID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.10",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member1 error: %v", err)
	}

	member2ID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.20",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member2 error: %v", err)
	}

	member3ID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.30",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member3 error: %v", err)
	}

	// 모든 멤버를 unhealthy로 설정
	healthStatus := &sync.Map{}
	healthStatus.Store(member1ID, HealthStatus{Healthy: false})
	healthStatus.Store(member2ID, HealthStatus{Healthy: false})
	healthStatus.Store(member3ID, HealthStatus{Healthy: false})

	engine := NewEngine(policyStorage, poolStorage, nil, healthStatus)
	ips, ttl, err := engine.Resolve("allfailed.example.com.", "A", net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if ttl != 30 {
		t.Fatalf("expected ttl 30, got %d", ttl)
	}

	// 모든 멤버가 실패했으므로 3개의 IP가 모두 반환되어야 함
	if len(ips) != 3 {
		t.Fatalf("expected 3 ips when all members are unhealthy, got %d", len(ips))
	}

	// 반환된 IP들 확인
	expectedIPs := map[string]bool{
		"192.0.2.10": true,
		"192.0.2.20": true,
		"192.0.2.30": true,
	}
	for _, ip := range ips {
		if !expectedIPs[ip.String()] {
			t.Fatalf("unexpected ip in response: %s", ip.String())
		}
		delete(expectedIPs, ip.String())
	}
	if len(expectedIPs) > 0 {
		t.Fatalf("missing ips in response: %v", expectedIPs)
	}
}

func TestResolvePartiallyHealthy(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "partial.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "default",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	member1ID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.10",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member1 error: %v", err)
	}

	member2ID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.20",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member2 error: %v", err)
	}

	// member1만 unhealthy로 설정
	healthStatus := &sync.Map{}
	healthStatus.Store(member1ID, HealthStatus{Healthy: false})
	healthStatus.Store(member2ID, HealthStatus{Healthy: true})

	engine := NewEngine(policyStorage, poolStorage, nil, healthStatus)
	ips, ttl, err := engine.Resolve("partial.example.com.", "A", net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if ttl != 30 {
		t.Fatalf("expected ttl 30, got %d", ttl)
	}

	// healthy한 멤버가 있으므로 1개의 IP만 반환되어야 함
	if len(ips) != 1 {
		t.Fatalf("expected 1 ip when some members are healthy, got %d", len(ips))
	}

	// healthy한 멤버의 IP가 반환되어야 함
	if ips[0].String() != "192.0.2.20" {
		t.Fatalf("expected healthy member IP 192.0.2.20, got %s", ips[0].String())
	}
}

func TestResolveCIDRWithFallback(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "multi.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	// CIDR pool (priority 0)
	cidrPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "cidr-pool",
		MatchType:    "cidr",
		MatchValue:   "10.97.0.0/16",
		Priority:     0,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("create cidr pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  cidrPoolID,
		Address: "192.168.1.100",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create cidr member error: %v", err)
	}

	// Fallback pool (priority 100)
	fallbackPoolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "fallback-pool",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     100,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create fallback pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  fallbackPoolID,
		Address: "203.0.113.1",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create fallback member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)

	// 10.97.11.18은 CIDR pool에 매칭되어야 함
	ips, ttl, err := engine.Resolve("multi.example.com.", "A", net.ParseIP("10.97.11.18"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if ttl != 30 {
		t.Fatalf("expected ttl 30, got %d", ttl)
	}
	if len(ips) != 1 {
		t.Fatalf("expected 1 ip, got %d", len(ips))
	}
	if ips[0].String() != "192.168.1.100" {
		t.Fatalf("expected CIDR pool IP 192.168.1.100, got %s", ips[0].String())
	}

	// 8.8.8.8은 CIDR pool에 매칭되지 않아 fallback으로 가야 함
	ips, ttl, err = engine.Resolve("multi.example.com.", "A", net.ParseIP("8.8.8.8"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if ttl != 30 {
		t.Fatalf("expected ttl 30, got %d", ttl)
	}
	if len(ips) != 1 {
		t.Fatalf("expected 1 ip, got %d", len(ips))
	}
	if ips[0].String() != "203.0.113.1" {
		t.Fatalf("expected fallback pool IP 203.0.113.1, got %s", ips[0].String())
	}
}

func TestHasDomain(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	// A 타입 policy만 생성 (AAAA는 없음)
	_, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "lb.gslb.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)

	// A policy가 있는 도메인 → true
	if !engine.HasDomain("lb.gslb.example.com.") {
		t.Error("expected HasDomain to return true for domain with A policy")
	}

	// policy가 없는 도메인 → false
	if engine.HasDomain("unknown.example.com.") {
		t.Error("expected HasDomain to return false for unknown domain")
	}

	// nil engine → false
	var nilEngine *Engine
	if nilEngine.HasDomain("lb.gslb.example.com.") {
		t.Error("expected HasDomain to return false for nil engine")
	}
}

func TestResolveAAAA_NoIPv6_ReturnsEmpty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	// A 타입 policy만 생성 (IPv4 멤버만 존재)
	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "lb.gslb.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "default",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "10.96.50.21",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	engine := NewEngine(policyStorage, poolStorage, nil, nil)

	// AAAA 쿼리 → AAAA policy가 없으므로 빈 결과
	ips, _, err := engine.Resolve("lb.gslb.example.com.", "AAAA", net.ParseIP("203.0.113.1"))
	if err != nil {
		t.Fatalf("resolve error: %v", err)
	}
	if len(ips) != 0 {
		t.Fatalf("expected no IPs for AAAA query without AAAA policy, got %v", ips)
	}

	// 하지만 HasDomain은 true여야 함 (A policy가 존재하므로)
	if !engine.HasDomain("lb.gslb.example.com.") {
		t.Error("HasDomain should return true even when only A policy exists")
	}
}
