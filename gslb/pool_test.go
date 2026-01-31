package gslb

import (
	"dns-go/model"
	"dns-go/storage"
	"os"
	"testing"
	"time"
)

func setupPoolTestDB(t *testing.T) (*storage.Database, func()) {
	path := "/tmp/test_pool_" + t.Name() + ".db"
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

func TestPoolStorage_GetPool(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "testpool",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	pool, err := poolStorage.GetPool(poolID)
	if err != nil {
		t.Fatalf("get pool error: %v", err)
	}
	if pool == nil {
		t.Fatalf("pool not found")
	}
	if pool.Name != "testpool" {
		t.Fatalf("expected name 'testpool', got '%s'", pool.Name)
	}
}

func TestPoolStorage_GetPoolNotFound(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	pool, err := poolStorage.GetPool(999)
	if err != nil {
		t.Fatalf("get pool error: %v", err)
	}
	if pool != nil {
		t.Fatalf("expected nil pool for non-existent ID")
	}
}

func TestPoolStorage_UpdatePool(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "original",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	err = poolStorage.UpdatePool(&model.GSLBPool{
		ID:           poolID,
		Name:         "updated",
		MatchType:    "cidr",
		MatchValue:   "10.0.0.0/8",
		Priority:     5,
		FallbackPool: false,
	})
	if err != nil {
		t.Fatalf("update pool error: %v", err)
	}

	pool, err := poolStorage.GetPool(poolID)
	if err != nil {
		t.Fatalf("get pool error: %v", err)
	}
	if pool.Name != "updated" {
		t.Fatalf("expected name 'updated', got '%s'", pool.Name)
	}
	if pool.Priority != 5 {
		t.Fatalf("expected priority 5, got %d", pool.Priority)
	}
}

func TestPoolStorage_UpdatePoolNotFound(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)

	err := poolStorage.UpdatePool(&model.GSLBPool{
		ID:           999,
		Name:         "notfound",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: false,
	})
	if err == nil {
		t.Fatalf("expected error for non-existent pool")
	}
}

func TestPoolStorage_DeletePool(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "delete-me",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	err = poolStorage.DeletePool(poolID)
	if err != nil {
		t.Fatalf("delete pool error: %v", err)
	}

	pool, err := poolStorage.GetPool(poolID)
	if err != nil {
		t.Fatalf("get pool error: %v", err)
	}
	if pool != nil {
		t.Fatalf("expected nil pool after deletion")
	}
}

func TestPoolStorage_DeletePoolNotFound(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)

	err := poolStorage.DeletePool(999)
	if err == nil {
		t.Fatalf("expected error for non-existent pool")
	}
}

func TestPoolStorage_GetMember(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "testpool",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	memberID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.1",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	member, err := poolStorage.GetMember(memberID)
	if err != nil {
		t.Fatalf("get member error: %v", err)
	}
	if member == nil {
		t.Fatalf("member not found")
	}
	if member.Address != "192.0.2.1" {
		t.Fatalf("expected address '192.0.2.1', got '%s'", member.Address)
	}
}

func TestPoolStorage_GetMemberNotFound(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	member, err := poolStorage.GetMember(999)
	if err != nil {
		t.Fatalf("get member error: %v", err)
	}
	if member != nil {
		t.Fatalf("expected nil member for non-existent ID")
	}
}

func TestPoolStorage_UpdateMember(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "testpool",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	memberID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.1",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	err = poolStorage.UpdateMember(&model.GSLBMember{
		ID:      memberID,
		PoolID:  poolID,
		Address: "192.0.2.99",
		Weight:  50,
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("update member error: %v", err)
	}

	member, err := poolStorage.GetMember(memberID)
	if err != nil {
		t.Fatalf("get member error: %v", err)
	}
	if member.Address != "192.0.2.99" {
		t.Fatalf("expected address '192.0.2.99', got '%s'", member.Address)
	}
	if member.Weight != 50 {
		t.Fatalf("expected weight 50, got %d", member.Weight)
	}
	if member.Enabled {
		t.Fatalf("expected disabled member")
	}
}

func TestPoolStorage_UpdateMemberNotFound(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)

	err := poolStorage.UpdateMember(&model.GSLBMember{
		ID:      999,
		PoolID:  1,
		Address: "192.0.2.1",
		Weight:  100,
		Enabled: true,
	})
	if err == nil {
		t.Fatalf("expected error for non-existent member")
	}
}

func TestPoolStorage_DeleteMember(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	policyID, err := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "testpool",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}

	memberID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.1",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}

	err = poolStorage.DeleteMember(memberID)
	if err != nil {
		t.Fatalf("delete member error: %v", err)
	}

	member, err := poolStorage.GetMember(memberID)
	if err != nil {
		t.Fatalf("get member error: %v", err)
	}
	if member != nil {
		t.Fatalf("expected nil member after deletion")
	}
}

func TestPoolStorage_DeleteMemberNotFound(t *testing.T) {
	db, cleanup := setupPoolTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)

	err := poolStorage.DeleteMember(999)
	if err == nil {
		t.Fatalf("expected error for non-existent member")
	}
}

func TestPoolCache_GetPoolsExpired(t *testing.T) {
	cache := NewPoolCache(100 * time.Millisecond)
	cache.setPools(1, []*model.GSLBPool{
		{ID: 1, Name: "pool1"},
	})

	time.Sleep(150 * time.Millisecond)

	pools, ok := cache.getPools(1)
	if ok {
		t.Fatalf("expected cache miss after expiry, got pools: %v", pools)
	}
}

func TestPoolCache_GetMembersExpired(t *testing.T) {
	cache := NewPoolCache(100 * time.Millisecond)
	cache.setMembers(1, []*model.GSLBMember{
		{ID: 1, Address: "192.0.2.1"},
	})

	time.Sleep(150 * time.Millisecond)

	members, ok := cache.getMembers(1)
	if ok {
		t.Fatalf("expected cache miss after expiry, got members: %v", members)
	}
}
