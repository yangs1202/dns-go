package gslb

import (
	"dns-go/model"
	"dns-go/storage"
	"net"
	"os"
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
