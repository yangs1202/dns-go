package gslb

import (
	"dns-go/model"
	"dns-go/storage"
	"os"
	"testing"
	"time"
)

func setupPolicyTestDB(t *testing.T) (*storage.Database, func()) {
	path := "/tmp/test_policy_" + t.Name() + ".db"
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

func TestPolicyStorage_GetPolicy(t *testing.T) {
	db, cleanup := setupPolicyTestDB(t)
	defer cleanup()

	storage := NewPolicyStorage(db)

	id, err := storage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	policy, err := storage.GetPolicy(id)
	if err != nil {
		t.Fatalf("get policy error: %v", err)
	}
	if policy == nil {
		t.Fatalf("policy not found")
	}
	if policy.Name != "test" {
		t.Fatalf("expected name 'test', got '%s'", policy.Name)
	}
	if policy.Domain != "test.example.com." {
		t.Fatalf("expected domain 'test.example.com.', got '%s'", policy.Domain)
	}
}

func TestPolicyStorage_GetPolicyNotFound(t *testing.T) {
	db, cleanup := setupPolicyTestDB(t)
	defer cleanup()

	storage := NewPolicyStorage(db)
	policy, err := storage.GetPolicy(999)
	if err != nil {
		t.Fatalf("get policy error: %v", err)
	}
	if policy != nil {
		t.Fatalf("expected nil policy for non-existent ID")
	}
}

func TestPolicyStorage_ListPolicies(t *testing.T) {
	db, cleanup := setupPolicyTestDB(t)
	defer cleanup()

	storage := NewPolicyStorage(db)

	_, err := storage.CreatePolicy(&model.GSLBPolicy{
		Name:       "policy1",
		Domain:     "test1.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	_, err = storage.CreatePolicy(&model.GSLBPolicy{
		Name:       "policy2",
		Domain:     "test2.example.com.",
		RecordType: "AAAA",
		TTL:        30,
		Enabled:    false,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	policies, err := storage.ListPolicies()
	if err != nil {
		t.Fatalf("list policies error: %v", err)
	}
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(policies))
	}
}

func TestPolicyStorage_UpdatePolicy(t *testing.T) {
	db, cleanup := setupPolicyTestDB(t)
	defer cleanup()

	storage := NewPolicyStorage(db)

	id, err := storage.CreatePolicy(&model.GSLBPolicy{
		Name:       "original",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	err = storage.UpdatePolicy(&model.GSLBPolicy{
		ID:         id,
		Name:       "updated",
		Domain:     "updated.example.com.",
		RecordType: "AAAA",
		TTL:        120,
		Enabled:    false,
	})
	if err != nil {
		t.Fatalf("update policy error: %v", err)
	}

	policy, err := storage.GetPolicy(id)
	if err != nil {
		t.Fatalf("get policy error: %v", err)
	}
	if policy.Name != "updated" {
		t.Fatalf("expected name 'updated', got '%s'", policy.Name)
	}
	if policy.TTL != 120 {
		t.Fatalf("expected ttl 120, got %d", policy.TTL)
	}
}

func TestPolicyStorage_UpdatePolicyNotFound(t *testing.T) {
	db, cleanup := setupPolicyTestDB(t)
	defer cleanup()

	storage := NewPolicyStorage(db)

	err := storage.UpdatePolicy(&model.GSLBPolicy{
		ID:         999,
		Name:       "notfound",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err == nil {
		t.Fatalf("expected error for non-existent policy")
	}
}

func TestPolicyStorage_DeletePolicy(t *testing.T) {
	db, cleanup := setupPolicyTestDB(t)
	defer cleanup()

	storage := NewPolicyStorage(db)

	id, err := storage.CreatePolicy(&model.GSLBPolicy{
		Name:       "delete-me",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	err = storage.DeletePolicy(id)
	if err != nil {
		t.Fatalf("delete policy error: %v", err)
	}

	policy, err := storage.GetPolicy(id)
	if err != nil {
		t.Fatalf("get policy error: %v", err)
	}
	if policy != nil {
		t.Fatalf("expected nil policy after deletion")
	}
}

func TestPolicyStorage_DeletePolicyNotFound(t *testing.T) {
	db, cleanup := setupPolicyTestDB(t)
	defer cleanup()

	storage := NewPolicyStorage(db)

	err := storage.DeletePolicy(999)
	if err == nil {
		t.Fatalf("expected error for non-existent policy")
	}
}

func TestPolicyCache_GetExpired(t *testing.T) {
	cache := NewPolicyCache(100 * time.Millisecond)
	cache.Set([]*model.GSLBPolicy{
		{
			ID:         1,
			Domain:     "test.example.com.",
			RecordType: "A",
		},
	})

	time.Sleep(150 * time.Millisecond)

	policy, ok := cache.Get("test.example.com.:A")
	if ok {
		t.Fatalf("expected cache miss after expiry, got policy: %v", policy)
	}
}

func TestPolicyCache_Set(t *testing.T) {
	cache := NewPolicyCache(5 * time.Minute)
	cache.Set([]*model.GSLBPolicy{
		{
			ID:         1,
			Domain:     "test.example.com.",
			RecordType: "A",
		},
		{
			ID:         2,
			Domain:     "test.example.com.",
			RecordType: "AAAA",
		},
	})

	policy1, ok1 := cache.Get("test.example.com.:A")
	if !ok1 || policy1.ID != 1 {
		t.Fatalf("expected policy 1")
	}

	policy2, ok2 := cache.Get("test.example.com.:AAAA")
	if !ok2 || policy2.ID != 2 {
		t.Fatalf("expected policy 2")
	}
}

func TestPolicyStorage_CreatePolicyDefaultRecordType(t *testing.T) {
	db, cleanup := setupPolicyTestDB(t)
	defer cleanup()

	storage := NewPolicyStorage(db)

	id, err := storage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "",
		TTL:        60,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	policy, err := storage.GetPolicy(id)
	if err != nil {
		t.Fatalf("get policy error: %v", err)
	}
	if policy.RecordType != "A" {
		t.Fatalf("expected default record type 'A', got '%s'", policy.RecordType)
	}
}
