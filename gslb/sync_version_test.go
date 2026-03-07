package gslb

import (
	"dns-go/model"
	"dns-go/storage"
	"testing"
)

func getSyncVersionForTest(t *testing.T, db *storage.Database) int64 {
	t.Helper()

	var version int64
	if err := db.Reader.QueryRow("SELECT last_sync_version FROM sync_state WHERE id = 1").Scan(&version); err != nil {
		t.Fatalf("sync version 조회 실패: %v", err)
	}
	return version
}

func TestPolicyStorage_IncrementsSyncVersionOnMutation(t *testing.T) {
	db, cleanup := setupPolicyTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)

	initialVersion := getSyncVersionForTest(t, db)

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
	if version := getSyncVersionForTest(t, db); version != initialVersion+1 {
		t.Fatalf("expected sync version %d after create, got %d", initialVersion+1, version)
	}

	if err := policyStorage.UpdatePolicy(&model.GSLBPolicy{
		ID:         policyID,
		Name:       "updated",
		Domain:     "updated.example.com.",
		RecordType: "AAAA",
		TTL:        120,
		Enabled:    false,
	}); err != nil {
		t.Fatalf("update policy error: %v", err)
	}
	if version := getSyncVersionForTest(t, db); version != initialVersion+2 {
		t.Fatalf("expected sync version %d after update, got %d", initialVersion+2, version)
	}

	if err := policyStorage.DeletePolicy(policyID); err != nil {
		t.Fatalf("delete policy error: %v", err)
	}
	if version := getSyncVersionForTest(t, db); version != initialVersion+3 {
		t.Fatalf("expected sync version %d after delete, got %d", initialVersion+3, version)
	}
}

func TestPoolStorage_IncrementsSyncVersionOnMutation(t *testing.T) {
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

	initialVersion := getSyncVersionForTest(t, db)

	poolID, err := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "pool",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})
	if err != nil {
		t.Fatalf("create pool error: %v", err)
	}
	if version := getSyncVersionForTest(t, db); version != initialVersion+1 {
		t.Fatalf("expected sync version %d after pool create, got %d", initialVersion+1, version)
	}

	if err := poolStorage.UpdatePool(&model.GSLBPool{
		ID:           poolID,
		Name:         "updated-pool",
		MatchType:    "cidr",
		MatchValue:   "10.0.0.0/8",
		Priority:     10,
		FallbackPool: false,
	}); err != nil {
		t.Fatalf("update pool error: %v", err)
	}
	if version := getSyncVersionForTest(t, db); version != initialVersion+2 {
		t.Fatalf("expected sync version %d after pool update, got %d", initialVersion+2, version)
	}

	memberID, err := poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.10",
		Weight:  100,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create member error: %v", err)
	}
	if version := getSyncVersionForTest(t, db); version != initialVersion+3 {
		t.Fatalf("expected sync version %d after member create, got %d", initialVersion+3, version)
	}

	if err := poolStorage.UpdateMember(&model.GSLBMember{
		ID:      memberID,
		PoolID:  poolID,
		Address: "192.0.2.11",
		Weight:  50,
		Enabled: true,
	}); err != nil {
		t.Fatalf("update member error: %v", err)
	}
	if version := getSyncVersionForTest(t, db); version != initialVersion+4 {
		t.Fatalf("expected sync version %d after member update, got %d", initialVersion+4, version)
	}

	if err := poolStorage.DeleteMember(memberID); err != nil {
		t.Fatalf("delete member error: %v", err)
	}
	if version := getSyncVersionForTest(t, db); version != initialVersion+5 {
		t.Fatalf("expected sync version %d after member delete, got %d", initialVersion+5, version)
	}

	if err := poolStorage.DeletePool(poolID); err != nil {
		t.Fatalf("delete pool error: %v", err)
	}
	if version := getSyncVersionForTest(t, db); version != initialVersion+6 {
		t.Fatalf("expected sync version %d after pool delete, got %d", initialVersion+6, version)
	}
}

func TestHealthCheckStorage_IncrementsSyncVersionOnMutation(t *testing.T) {
	db, cleanup := setupPolicyTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	healthCheckStorage := NewHealthCheckStorage(db)

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

	initialVersion := getSyncVersionForTest(t, db)

	checkID, err := healthCheckStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID: policyID,
		Target:   "192.0.2.10:80",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("create health check error: %v", err)
	}
	if version := getSyncVersionForTest(t, db); version != initialVersion+1 {
		t.Fatalf("expected sync version %d after health check create, got %d", initialVersion+1, version)
	}

	if err := healthCheckStorage.UpdateHealthCheck(&model.HealthCheck{
		ID:                 checkID,
		PolicyID:           policyID,
		CheckType:          "http",
		Target:             "https://192.0.2.10/healthz",
		IntervalSec:        15,
		TimeoutSec:         3,
		HealthyThreshold:   2,
		UnhealthyThreshold: 2,
		ExpectedCodes:      []int{200},
		Enabled:            true,
	}); err != nil {
		t.Fatalf("update health check error: %v", err)
	}
	if version := getSyncVersionForTest(t, db); version != initialVersion+2 {
		t.Fatalf("expected sync version %d after health check update, got %d", initialVersion+2, version)
	}

	if err := healthCheckStorage.DeleteHealthCheck(checkID); err != nil {
		t.Fatalf("delete health check error: %v", err)
	}
	if version := getSyncVersionForTest(t, db); version != initialVersion+3 {
		t.Fatalf("expected sync version %d after health check delete, got %d", initialVersion+3, version)
	}
}
