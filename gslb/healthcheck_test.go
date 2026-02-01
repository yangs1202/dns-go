package gslb

import (
	"dns-go/model"
	"dns-go/storage"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func setupHealthCheckTestDB(t *testing.T) (*storage.Database, func()) {
	path := "/tmp/test_hc_" + t.Name() + ".db"
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

func TestHealthCheckStorage_GetHealthCheck(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	policyID, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	hcID, err := hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:           policyID,
		CheckType:          "tcp",
		Target:             "443",
		IntervalSec:        10,
		TimeoutSec:         5,
		HealthyThreshold:   3,
		UnhealthyThreshold: 2,
		Enabled:            true,
	})
	if err != nil {
		t.Fatalf("create health check error: %v", err)
	}

	hc, err := hcStorage.GetHealthCheck(hcID)
	if err != nil {
		t.Fatalf("get health check error: %v", err)
	}
	if hc == nil {
		t.Fatalf("health check not found")
	}
	if hc.CheckType != "tcp" {
		t.Fatalf("expected type 'tcp', got '%s'", hc.CheckType)
	}
}

func TestHealthCheckStorage_GetHealthCheckNotFound(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	hcStorage := NewHealthCheckStorage(db)
	hc, err := hcStorage.GetHealthCheck(999)
	if err != nil {
		t.Fatalf("get health check error: %v", err)
	}
	if hc != nil {
		t.Fatalf("expected nil health check for non-existent ID")
	}
}

func TestHealthCheckStorage_GetHealthCheckByPolicy(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	policyID, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	_, err := hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:           policyID,
		CheckType:          "http",
		Target:             "/health",
		IntervalSec:        10,
		TimeoutSec:         5,
		HealthyThreshold:   3,
		UnhealthyThreshold: 2,
		Enabled:            true,
	})
	if err != nil {
		t.Fatalf("create health check error: %v", err)
	}

	hc, err := hcStorage.GetHealthCheckByPolicy(policyID)
	if err != nil {
		t.Fatalf("get health check by policy error: %v", err)
	}
	if hc == nil {
		t.Fatalf("health check not found")
	}
	if hc.PolicyID != policyID {
		t.Fatalf("expected policy ID %d, got %d", policyID, hc.PolicyID)
	}
}

func TestHealthCheckStorage_ListHealthChecks(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	policyID1, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test1",
		Domain:     "test1.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	policyID2, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test2",
		Domain:     "test2.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	_, err := hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:   policyID1,
		CheckType:  "tcp",
		Target:     "443",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create health check 1 error: %v", err)
	}

	_, err = hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:   policyID2,
		CheckType:  "http",
		Target:     "http://example.com/health",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create health check 2 error: %v", err)
	}

	checks, err := hcStorage.ListHealthChecks()
	if err != nil {
		t.Fatalf("list health checks error: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("expected 2 health checks, got %d", len(checks))
	}
}

func TestHealthCheckStorage_UpdateHealthCheck(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	policyID, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	hcID, _ := hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:  policyID,
		CheckType: "tcp",
		Target:    "443",
		Enabled:   true,
	})

	err := hcStorage.UpdateHealthCheck(&model.HealthCheck{
		ID:                 hcID,
		PolicyID:           policyID,
		CheckType:          "https",
		Target:             "https://example.com/health",
		IntervalSec:        20,
		TimeoutSec:         10,
		HealthyThreshold:   5,
		UnhealthyThreshold: 3,
		Enabled:            false,
	})
	if err != nil {
		t.Fatalf("update health check error: %v", err)
	}

	hc, err := hcStorage.GetHealthCheck(hcID)
	if err != nil {
		t.Fatalf("get health check error: %v", err)
	}
	if hc.CheckType != "https" {
		t.Fatalf("expected type 'https', got '%s'", hc.CheckType)
	}
	if hc.IntervalSec != 20 {
		t.Fatalf("expected interval 20, got %d", hc.IntervalSec)
	}
}

func TestHealthCheckStorage_UpdateHealthCheckNotFound(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	hcStorage := NewHealthCheckStorage(db)

	err := hcStorage.UpdateHealthCheck(&model.HealthCheck{
		ID:        999,
		CheckType: "tcp",
		Target:    "443",
		Enabled:   true,
	})
	if err == nil {
		t.Fatalf("expected error for non-existent health check")
	}
}

func TestHealthCheckStorage_DeleteHealthCheck(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	policyID, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	hcID, _ := hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:  policyID,
		CheckType: "tcp",
		Target:    "443",
		Enabled:   true,
	})

	err := hcStorage.DeleteHealthCheck(hcID)
	if err != nil {
		t.Fatalf("delete health check error: %v", err)
	}

	hc, err := hcStorage.GetHealthCheck(hcID)
	if err != nil {
		t.Fatalf("get health check error: %v", err)
	}
	if hc != nil {
		t.Fatalf("expected nil health check after deletion")
	}
}

func TestHealthCheckStorage_DeleteHealthCheckNotFound(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	hcStorage := NewHealthCheckStorage(db)

	err := hcStorage.DeleteHealthCheck(999)
	if err == nil {
		t.Fatalf("expected error for non-existent health check")
	}
}

func TestApplyDefaults(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	policyID, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	hcID, err := hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID: policyID,
		Target:   "443",
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("create health check error: %v", err)
	}

	hc, err := hcStorage.GetHealthCheck(hcID)
	if err != nil {
		t.Fatalf("get health check error: %v", err)
	}

	if hc.CheckType != "tcp" {
		t.Fatalf("expected default check type 'tcp', got '%s'", hc.CheckType)
	}
	if hc.IntervalSec != 10 {
		t.Fatalf("expected default interval 10, got %d", hc.IntervalSec)
	}
	if hc.TimeoutSec != 5 {
		t.Fatalf("expected default timeout 5, got %d", hc.TimeoutSec)
	}
	if hc.HealthyThreshold != 3 {
		t.Fatalf("expected default healthy threshold 3, got %d", hc.HealthyThreshold)
	}
	if hc.UnhealthyThreshold != 2 {
		t.Fatalf("expected default unhealthy threshold 2, got %d", hc.UnhealthyThreshold)
	}
}

func TestHealthCheckWorker_HTTPCheckWithFullURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Host 헤더 확인 (도메인이 올바르게 전달되는지)
		if r.Host == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// 서버 주소 추출 (127.0.0.1:포트)
	serverAddr := server.Listener.Addr().String()

	check := &model.HealthCheck{
		PolicyID:           1,
		CheckType:          "http",
		Target:             server.URL, // 전체 URL 사용 (http://127.0.0.1:포트)
		IntervalSec:        1,
		TimeoutSec:         5,
		HealthyThreshold:   2,
		UnhealthyThreshold: 2,
		Enabled:            true,
	}

	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: serverAddr, // 멤버 주소를 서버 주소로 설정
		Weight:  100,
		Enabled: true,
	}

	pool := &model.GSLBPool{ID: 1, Name: "testpool"}

	worker.runCheck(check, member, pool)

	status := worker.getStatus(1)
	if status.ConsecutiveOKs != 1 {
		t.Fatalf("expected 1 consecutive OK, got %d (error: %s)", status.ConsecutiveOKs, status.LastError)
	}

	worker.runCheck(check, member, pool)
	status = worker.getStatus(1)
	if status.ConsecutiveOKs != 2 {
		t.Fatalf("expected 2 consecutive OKs, got %d", status.ConsecutiveOKs)
	}
	if !status.Healthy {
		t.Fatalf("expected healthy status after reaching threshold")
	}
}

func TestHealthCheckWorker_HTTPCheckWithPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// 서버 주소에서 호스트 추출
	// httptest 서버는 127.0.0.1:port 형식
	serverHost := server.Listener.Addr().String()

	check := &model.HealthCheck{
		PolicyID:           1,
		CheckType:          "http",
		Target:             "/health", // 경로만 지정
		IntervalSec:        1,
		TimeoutSec:         5,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}

	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: serverHost, // 멤버 IP로 서버 주소 사용
		Weight:  100,
		Enabled: true,
	}

	pool := &model.GSLBPool{ID: 1, Name: "testpool"}

	worker.runCheck(check, member, pool)

	status := worker.getStatus(1)
	if status.ConsecutiveOKs != 1 {
		t.Fatalf("expected 1 consecutive OK, got %d (error: %s)", status.ConsecutiveOKs, status.LastError)
	}
}

func TestHealthCheckWorker_HTTPCheckFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	serverAddr := server.Listener.Addr().String()

	check := &model.HealthCheck{
		PolicyID:           1,
		CheckType:          "http",
		Target:             server.URL,
		IntervalSec:        1,
		TimeoutSec:         5,
		HealthyThreshold:   2,
		UnhealthyThreshold: 2,
		Enabled:            true,
	}

	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: serverAddr,
		Weight:  100,
		Enabled: true,
	}

	pool := &model.GSLBPool{ID: 1, Name: "testpool"}

	worker.runCheck(check, member, pool)
	worker.runCheck(check, member, pool)

	status := worker.getStatus(1)
	if status.ConsecutiveFails != 2 {
		t.Fatalf("expected 2 consecutive fails, got %d", status.ConsecutiveFails)
	}
	if status.Healthy {
		t.Fatalf("expected unhealthy status after reaching threshold")
	}
}

func TestHealthCheckWorker_StartStop(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}

	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	worker.Start()
	time.Sleep(100 * time.Millisecond)
	worker.Stop()
}

func TestHealthCheckWorker_TCPCheck(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	check := &model.HealthCheck{
		PolicyID:           1,
		CheckType:          "tcp",
		Target:             "8.8.8.8:53", // 전체 주소 지정
		IntervalSec:        1,
		TimeoutSec:         2,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}

	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: "8.8.8.8",
		Weight:  100,
		Enabled: true,
	}

	pool := &model.GSLBPool{ID: 1, Name: "testpool"}

	worker.runCheck(check, member, pool)

	status := worker.getStatus(1)
	if status.ConsecutiveOKs != 1 {
		t.Fatalf("expected 1 consecutive OK for TCP check, got %d", status.ConsecutiveOKs)
	}
}

func TestHealthCheckWorker_TCPCheckWithPort(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	check := &model.HealthCheck{
		PolicyID:           1,
		CheckType:          "tcp",
		Target:             "53", // 포트만 지정
		IntervalSec:        1,
		TimeoutSec:         2,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}

	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: "8.8.8.8",
		Weight:  100,
		Enabled: true,
	}

	pool := &model.GSLBPool{ID: 1, Name: "testpool"}

	worker.runCheck(check, member, pool)

	status := worker.getStatus(1)
	if status.ConsecutiveOKs != 1 {
		t.Fatalf("expected 1 consecutive OK for TCP check, got %d", status.ConsecutiveOKs)
	}
}

func TestHealthCheckWorker_TCPCheckFailure(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	check := &model.HealthCheck{
		PolicyID:           1,
		CheckType:          "tcp",
		Target:             "9999",
		IntervalSec:        1,
		TimeoutSec:         1,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}

	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: "192.0.2.1",
		Weight:  100,
		Enabled: true,
	}

	pool := &model.GSLBPool{ID: 1, Name: "testpool"}

	worker.runCheck(check, member, pool)

	status := worker.getStatus(1)
	if status.ConsecutiveFails != 1 {
		t.Fatalf("expected 1 consecutive fail for unreachable TCP, got %d", status.ConsecutiveFails)
	}
	if status.LastError == "" {
		t.Fatalf("expected error message for failed TCP check")
	}
}

func TestHealthCheckWorker_HTTPSCheck(t *testing.T) {
	// HTTPS 테스트 서버 생성
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	serverAddr := server.Listener.Addr().String()

	check := &model.HealthCheck{
		PolicyID:           1,
		CheckType:          "https",
		Target:             server.URL, // https://127.0.0.1:포트
		IntervalSec:        1,
		TimeoutSec:         5,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}

	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: serverAddr,
		Weight:  100,
		Enabled: true,
	}

	pool := &model.GSLBPool{ID: 1, Name: "testpool"}

	worker.runCheck(check, member, pool)

	status := worker.getStatus(1)
	if status.ConsecutiveOKs != 1 {
		t.Fatalf("expected 1 consecutive OK for HTTPS check, got %d (error: %s)", status.ConsecutiveOKs, status.LastError)
	}
}

func TestHealthCheckWorker_GetStatusInitial(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	status := worker.getStatus(999)
	if !status.Healthy {
		t.Fatalf("expected initial status to be healthy")
	}
	if status.ConsecutiveOKs != 0 {
		t.Fatalf("expected 0 consecutive OKs for new member")
	}
}

func TestHealthCheckWorker_StartWithEnabledChecks(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)

	policyID, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "test",
		Domain:     "test.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	poolID, _ := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "testpool",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})

	_, _ = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "8.8.8.8",
		Weight:  100,
		Enabled: true,
	})

	_, err := hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:           policyID,
		CheckType:          "tcp",
		Target:             "8.8.8.8:53",
		IntervalSec:        1,
		TimeoutSec:         2,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	})
	if err != nil {
		t.Fatalf("create health check error: %v", err)
	}

	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	worker.Start()
	time.Sleep(200 * time.Millisecond)
	worker.Stop()
}
