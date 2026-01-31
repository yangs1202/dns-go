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
