package gslb

import (
	"dns-go/model"
	"dns-go/storage"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// setupClosedDB creates a DB, initializes schema, then closes it to force errors
func setupClosedDB(t *testing.T) *storage.Database {
	path := "/tmp/test_closed_" + strings.ReplaceAll(t.Name(), "/", "_") + ".db"
	_ = os.Remove(path)
	db, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatalf("db init error: %v", err)
	}
	// Close both connections to force errors on subsequent operations
	_ = db.Close()
	_ = os.Remove(path)
	return db
}

func TestHealthCheckStorage_ErrorPaths(t *testing.T) {
	db := setupClosedDB(t)
	hcStorage := NewHealthCheckStorage(db)

	// GetHealthCheck on closed DB
	_, err := hcStorage.GetHealthCheck(1)
	if err == nil {
		t.Fatalf("expected error on closed DB GetHealthCheck")
	}

	// GetHealthCheckByPolicy on closed DB
	_, err = hcStorage.GetHealthCheckByPolicy(1)
	if err == nil {
		t.Fatalf("expected error on closed DB GetHealthCheckByPolicy")
	}

	// ListHealthChecks on closed DB
	_, err = hcStorage.ListHealthChecks()
	if err == nil {
		t.Fatalf("expected error on closed DB ListHealthChecks")
	}

	// CreateHealthCheck on closed DB
	_, err = hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:  1,
		CheckType: "tcp",
		Target:    "53",
		Enabled:   true,
	})
	if err == nil {
		t.Fatalf("expected error on closed DB CreateHealthCheck")
	}

	// UpdateHealthCheck on closed DB
	err = hcStorage.UpdateHealthCheck(&model.HealthCheck{
		ID:        1,
		CheckType: "tcp",
		Target:    "80",
		Enabled:   true,
	})
	if err == nil {
		t.Fatalf("expected error on closed DB UpdateHealthCheck")
	}

	// DeleteHealthCheck on closed DB
	err = hcStorage.DeleteHealthCheck(1)
	if err == nil {
		t.Fatalf("expected error on closed DB DeleteHealthCheck")
	}
}

func TestPolicyStorage_ErrorPaths(t *testing.T) {
	db := setupClosedDB(t)
	policyStorage := NewPolicyStorage(db)

	// GetPolicy on closed DB
	_, err := policyStorage.GetPolicy(1)
	if err == nil {
		t.Fatalf("expected error on closed DB GetPolicy")
	}

	// GetPolicyByDomain on closed DB
	_, err = policyStorage.GetPolicyByDomain("test.com.", "A")
	if err == nil {
		t.Fatalf("expected error on closed DB GetPolicyByDomain")
	}

	// ListPolicies on closed DB
	_, err = policyStorage.ListPolicies()
	if err == nil {
		t.Fatalf("expected error on closed DB ListPolicies")
	}

	// CreatePolicy on closed DB
	_, err = policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err == nil {
		t.Fatalf("expected error on closed DB CreatePolicy")
	}

	// UpdatePolicy on closed DB
	err = policyStorage.UpdatePolicy(&model.GSLBPolicy{
		ID:         1,
		Name:       "test",
		Domain:     "test.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err == nil {
		t.Fatalf("expected error on closed DB UpdatePolicy")
	}

	// DeletePolicy on closed DB
	err = policyStorage.DeletePolicy(1)
	if err == nil {
		t.Fatalf("expected error on closed DB DeletePolicy")
	}
}

func TestPoolStorage_ErrorPaths(t *testing.T) {
	db := setupClosedDB(t)
	poolStorage := NewPoolStorage(db)

	// GetPool on closed DB
	_, err := poolStorage.GetPool(1)
	if err == nil {
		t.Fatalf("expected error on closed DB GetPool")
	}

	// GetPoolsByPolicy on closed DB
	_, err = poolStorage.GetPoolsByPolicy(1)
	if err == nil {
		t.Fatalf("expected error on closed DB GetPoolsByPolicy")
	}

	// CreatePool on closed DB
	_, err = poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:   1,
		Name:       "test",
		MatchType:  "default",
		MatchValue: "*",
	})
	if err == nil {
		t.Fatalf("expected error on closed DB CreatePool")
	}

	// UpdatePool on closed DB - this calls GetPool first, which will error
	err = poolStorage.UpdatePool(&model.GSLBPool{
		ID:         1,
		Name:       "test",
		MatchType:  "default",
		MatchValue: "*",
	})
	if err == nil {
		t.Fatalf("expected error on closed DB UpdatePool")
	}

	// DeletePool on closed DB
	err = poolStorage.DeletePool(1)
	if err == nil {
		t.Fatalf("expected error on closed DB DeletePool")
	}

	// GetMember on closed DB
	_, err = poolStorage.GetMember(1)
	if err == nil {
		t.Fatalf("expected error on closed DB GetMember")
	}

	// GetMembersByPool on closed DB
	_, err = poolStorage.GetMembersByPool(1)
	if err == nil {
		t.Fatalf("expected error on closed DB GetMembersByPool")
	}

	// CreateMember on closed DB
	_, err = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  1,
		Address: "10.0.0.1",
		Weight:  100,
		Enabled: true,
	})
	if err == nil {
		t.Fatalf("expected error on closed DB CreateMember")
	}

	// UpdateMember on closed DB
	err = poolStorage.UpdateMember(&model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: "10.0.0.1",
		Weight:  100,
		Enabled: true,
	})
	if err == nil {
		t.Fatalf("expected error on closed DB UpdateMember")
	}

	// DeleteMember on closed DB
	err = poolStorage.DeleteMember(1)
	if err == nil {
		t.Fatalf("expected error on closed DB DeleteMember")
	}
}

func TestHealthCheckWorker_StartWithClosedDB(t *testing.T) {
	db := setupClosedDB(t)
	hcStorage := NewHealthCheckStorage(db)
	poolStorage := NewPoolStorage(db)
	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// Start should handle ListHealthChecks error gracefully
	worker.Start()
	worker.Stop()
}

func TestHealthCheckWorker_RestartWithClosedDB(t *testing.T) {
	db := setupClosedDB(t)
	hcStorage := NewHealthCheckStorage(db)
	poolStorage := NewPoolStorage(db)
	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// Restart should handle ListHealthChecks error gracefully
	worker.Restart()
	time.Sleep(600 * time.Millisecond)
	worker.Stop()
}

func TestEngine_ResolveWithClosedDB(t *testing.T) {
	db := setupClosedDB(t)
	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	engine := NewEngine(policyStorage, poolStorage, nil, nil)

	// Should return error from GetPolicyByDomain
	_, _, err := engine.Resolve("test.example.com.", "A", nil)
	if err == nil {
		t.Fatalf("expected error from Resolve with closed DB")
	}
}

func TestEngine_ResolveGetPoolsError(t *testing.T) {
	// Need a valid policy but closed pool DB to get GetPoolsByPolicy error
	path := "/tmp/test_poolerr_" + strings.ReplaceAll(t.Name(), "/", "_") + ".db"
	_ = os.Remove(path)
	db, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatalf("db init error: %v", err)
	}
	defer os.Remove(path)

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)

	// Create a policy while DB is open
	_, err = policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "poolerr.example.com.",
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create policy error: %v", err)
	}

	// Close DB to force GetPoolsByPolicy error
	_ = db.Close()

	engine := NewEngine(policyStorage, poolStorage, nil, nil)

	// The policy cache might have it but pool query will fail
	// Invalidate policy cache first to force DB read (which will also fail)
	policyStorage.cache.Invalidate()

	_, _, err = engine.Resolve("poolerr.example.com.", "A", nil)
	if err == nil {
		t.Fatalf("expected error from Resolve with closed DB for pools")
	}
}

func TestHealthCheckWorker_RunPolicyCheckGetPoolsError(t *testing.T) {
	path := "/tmp/test_rpcerr_" + strings.ReplaceAll(t.Name(), "/", "_") + ".db"
	_ = os.Remove(path)
	db, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatalf("db init error: %v", err)
	}
	defer os.Remove(path)

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// Close DB to force GetPoolsByPolicy error
	_ = db.Close()

	check := &model.HealthCheck{
		ID:                 1,
		PolicyID:           1,
		CheckType:          "tcp",
		Target:             "53",
		IntervalSec:        1,
		TimeoutSec:         1,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}

	// Should handle error gracefully (log and return)
	worker.runPolicyCheck(check)
}
