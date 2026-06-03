package gslb

import (
	"dns-go/model"
	"dns-go/storage"
	"net"
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

func startTCPTestServer(t *testing.T) (host string, port string, cleanup func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcp listen error: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	host, port, err = net.SplitHostPort(ln.Addr().String())
	if err != nil {
		_ = ln.Close()
		t.Fatalf("split host port error: %v", err)
	}

	cleanup = func() {
		_ = ln.Close()
	}
	return host, port, cleanup
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
		PolicyID:  policyID1,
		CheckType: "tcp",
		Target:    "443",
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("create health check 1 error: %v", err)
	}

	_, err = hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:  policyID2,
		CheckType: "http",
		Target:    "http://example.com/health",
		Enabled:   true,
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
	host, port, closeTCP := startTCPTestServer(t)
	defer closeTCP()

	check := &model.HealthCheck{
		PolicyID:           1,
		CheckType:          "tcp",
		Target:             net.JoinHostPort(host, port), // host:port 지정
		IntervalSec:        1,
		TimeoutSec:         2,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}

	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: host,
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
	host, port, closeTCP := startTCPTestServer(t)
	defer closeTCP()

	check := &model.HealthCheck{
		PolicyID:           1,
		CheckType:          "tcp",
		Target:             port, // 포트만 지정
		IntervalSec:        1,
		TimeoutSec:         2,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}

	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: host,
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
	host, port, closeTCP := startTCPTestServer(t)
	defer closeTCP()

	_, _ = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: host,
		Weight:  100,
		Enabled: true,
	})

	_, err := hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:           policyID,
		CheckType:          "tcp",
		Target:             net.JoinHostPort(host, port),
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

func TestHealthCheckWorker_AddCheck(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}

	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)
	// Keep stopCh open so the runCheckLoop can operate
	defer worker.Stop()

	// Test 1: Adding a disabled check should be a no-op
	disabledCheck := &model.HealthCheck{
		ID:          100,
		PolicyID:    1,
		CheckType:   "tcp",
		Target:      "53",
		IntervalSec: 60,
		TimeoutSec:  5,
		Enabled:     false,
	}
	worker.AddCheck(disabledCheck)

	// Verify the disabled check did not start a runner
	if _, exists := worker.runners.Load(int64(100)); exists {
		t.Fatalf("disabled check should not be registered as a runner")
	}

	// Test 2: Adding an enabled check should start a runner
	enabledCheck := &model.HealthCheck{
		ID:                 200,
		PolicyID:           1,
		CheckType:          "tcp",
		Target:             "53",
		IntervalSec:        60,
		TimeoutSec:         1,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}
	worker.AddCheck(enabledCheck)
	time.Sleep(150 * time.Millisecond)

	// Verify the runner is registered
	if _, exists := worker.runners.Load(int64(200)); !exists {
		t.Fatalf("enabled check should be registered as a runner")
	}

	// Test 3: Adding the same check again should be a no-op (already running)
	worker.AddCheck(enabledCheck)
	time.Sleep(50 * time.Millisecond)
}

func TestHealthCheckWorker_RemoveCheck(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}

	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)
	defer worker.Stop()

	// Add a check first
	check := &model.HealthCheck{
		ID:                 300,
		PolicyID:           1,
		CheckType:          "tcp",
		Target:             "53",
		IntervalSec:        60,
		TimeoutSec:         1,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}
	worker.AddCheck(check)
	time.Sleep(150 * time.Millisecond)

	// Verify it is running
	if _, exists := worker.runners.Load(int64(300)); !exists {
		t.Fatalf("check should be registered before removal")
	}

	// Remove it
	worker.RemoveCheck(300)
	time.Sleep(150 * time.Millisecond)

	// Verify it is gone
	if _, exists := worker.runners.Load(int64(300)); exists {
		t.Fatalf("check should be removed after RemoveCheck")
	}

	// Removing a non-existent check should not panic
	worker.RemoveCheck(999)
}

func TestHealthCheckWorker_UpdateCheck(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}

	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)
	defer worker.Stop()

	// Add a check
	check := &model.HealthCheck{
		ID:                 400,
		PolicyID:           1,
		CheckType:          "tcp",
		Target:             "53",
		IntervalSec:        60,
		TimeoutSec:         1,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}
	worker.AddCheck(check)
	time.Sleep(150 * time.Millisecond)

	if _, exists := worker.runners.Load(int64(400)); !exists {
		t.Fatalf("check should be running before update")
	}

	// Update the check (removes and re-adds)
	updatedCheck := &model.HealthCheck{
		ID:                 400,
		PolicyID:           1,
		CheckType:          "tcp",
		Target:             "80",
		IntervalSec:        60,
		TimeoutSec:         1,
		HealthyThreshold:   2,
		UnhealthyThreshold: 2,
		Enabled:            true,
	}
	worker.UpdateCheck(updatedCheck)
	time.Sleep(300 * time.Millisecond)

	// Verify the runner is still registered after update
	if _, exists := worker.runners.Load(int64(400)); !exists {
		t.Fatalf("check should be running after update")
	}
}

func TestHealthCheckWorker_Restart(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}

	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// Create a policy and health check in DB
	policyID, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "restart-test",
		Domain:     "restart.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	poolID, _ := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "default",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})

	_, _ = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: "192.0.2.1",
		Weight:  100,
		Enabled: true,
	})

	_, err := hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:           policyID,
		CheckType:          "tcp",
		Target:             "53",
		IntervalSec:        60,
		TimeoutSec:         1,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	})
	if err != nil {
		t.Fatalf("create health check error: %v", err)
	}

	// Also create a disabled check (should not be started on restart)
	_, err = hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:           policyID,
		CheckType:          "tcp",
		Target:             "80",
		IntervalSec:        60,
		TimeoutSec:         1,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            false,
	})
	if err != nil {
		t.Fatalf("create disabled health check error: %v", err)
	}

	// Start, then restart
	worker.Start()
	time.Sleep(200 * time.Millisecond)

	worker.Restart()
	time.Sleep(700 * time.Millisecond)

	worker.Stop()
}

func TestHealthCheckWorker_RunPolicyCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}

	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// Create a policy with pools and members
	policyID, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "rpc-test",
		Domain:     "rpc.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	poolID, _ := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "pool1",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: true,
	})

	serverAddr := server.Listener.Addr().String()

	// Add enabled member
	_, _ = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: serverAddr,
		Weight:  100,
		Enabled: true,
	})

	// Add disabled member (should be skipped)
	_, _ = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  poolID,
		Address: serverAddr,
		Weight:  50,
		Enabled: false,
	})

	check := &model.HealthCheck{
		ID:                 1,
		PolicyID:           policyID,
		CheckType:          "http",
		Target:             server.URL,
		IntervalSec:        1,
		TimeoutSec:         5,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}

	// Run the policy check - this should check all enabled members in all pools
	worker.runPolicyCheck(check)

	// The first enabled member should have a health status entry
	// We do not know exact member IDs, so just verify the method ran without error
}

func TestHealthCheckWorker_RunPolicyCheckPoolError(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}

	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// Use a policy ID that has no pools (should log and return gracefully)
	check := &model.HealthCheck{
		ID:                 1,
		PolicyID:           9999,
		CheckType:          "tcp",
		Target:             "53",
		IntervalSec:        1,
		TimeoutSec:         1,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}

	// Should not panic
	worker.runPolicyCheck(check)
}

func TestHealthCheckWorker_ProbeHTTPPathWithoutSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
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

	serverAddr := server.Listener.Addr().String()

	// Test probe with path that does NOT start with /
	check := &model.HealthCheck{
		PolicyID:   1,
		CheckType:  "http",
		Target:     "healthz", // no leading slash
		TimeoutSec: 5,
	}
	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: serverAddr,
		Weight:  100,
		Enabled: true,
	}

	err := worker.probe(check, member)
	if err != nil {
		t.Fatalf("expected successful probe with path 'healthz', got: %v", err)
	}
}

func TestHealthCheckWorker_ProbeHTTPSWithPath(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	serverAddr := server.Listener.Addr().String()

	// Test probe with https and path only
	check := &model.HealthCheck{
		PolicyID:   1,
		CheckType:  "https",
		Target:     "/health",
		TimeoutSec: 5,
	}
	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: serverAddr,
		Weight:  100,
		Enabled: true,
	}

	err := worker.probe(check, member)
	if err != nil {
		t.Fatalf("expected successful HTTPS probe with path, got: %v", err)
	}
}

func TestBuildHealthCheckRequestURLPathTargetPreservesQueryAndIPv6(t *testing.T) {
	check := &model.HealthCheck{
		CheckType: "http",
		Target:    "healthz?ready=1",
	}
	member := &model.GSLBMember{
		Address: "2001:db8::1",
	}

	requestURL, originalHost, _, err := buildHealthCheckRequestURL(check, member)
	if err != nil {
		t.Fatalf("expected request URL build to succeed, got: %v", err)
	}

	if requestURL.Scheme != "http" {
		t.Fatalf("expected http scheme, got %q", requestURL.Scheme)
	}
	if requestURL.Host != "[2001:db8::1]:80" {
		t.Fatalf("expected bracketed IPv6 host with default port, got %q", requestURL.Host)
	}
	if requestURL.Path != "/healthz" {
		t.Fatalf("expected /healthz path, got %q", requestURL.Path)
	}
	if requestURL.RawQuery != "ready=1" {
		t.Fatalf("expected query string to be preserved, got %q", requestURL.RawQuery)
	}
	if originalHost != "2001:db8::1" {
		t.Fatalf("expected original host to preserve member address, got %q", originalHost)
	}
}

func TestHealthCheckWorker_ProbeDefaultType(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)
	host, port, closeTCP := startTCPTestServer(t)
	defer closeTCP()

	// Test with an unknown check type (falls through to default TCP behavior)
	check := &model.HealthCheck{
		PolicyID:   1,
		CheckType:  "unknown_type",
		Target:     port,
		TimeoutSec: 2,
	}
	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: host,
		Weight:  100,
		Enabled: true,
	}

	err := worker.probe(check, member)
	if err != nil {
		t.Fatalf("expected successful probe with default TCP behavior, got: %v", err)
	}
}

func TestHealthCheckWorker_ProbeDefaultTypeWithHostPort(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)
	host, port, closeTCP := startTCPTestServer(t)
	defer closeTCP()

	// Test the default type with "host:port" format target
	check := &model.HealthCheck{
		PolicyID:   1,
		CheckType:  "something_else",
		Target:     net.JoinHostPort(host, port),
		TimeoutSec: 2,
	}
	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: host,
		Weight:  100,
		Enabled: true,
	}

	err := worker.probe(check, member)
	if err != nil {
		t.Fatalf("expected successful probe with default type (host:port), got: %v", err)
	}
}

func TestHealthCheckWorker_ProbeHTTPConnectionRefused(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// Test probe against an unreachable HTTP endpoint
	check := &model.HealthCheck{
		PolicyID:   1,
		CheckType:  "http",
		Target:     "/health",
		TimeoutSec: 1,
	}
	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: "192.0.2.1",
		Weight:  100,
		Enabled: true,
	}

	err := worker.probe(check, member)
	if err == nil {
		t.Fatalf("expected error for unreachable HTTP endpoint")
	}
}

func TestHealthCheckWorker_RunCheckStateTransitions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	pool := &model.GSLBPool{ID: 1, Name: "testpool"}
	member := &model.GSLBMember{
		ID:      10,
		PoolID:  1,
		Address: serverAddr,
		Weight:  100,
		Enabled: true,
	}

	// Start as unhealthy and recover
	healthStatus.Store(int64(10), HealthStatus{Healthy: false, ConsecutiveFails: 3})

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

	// First OK: ConsecutiveOKs=1, not yet healthy
	worker.runCheck(check, member, pool)
	status := worker.getStatus(10)
	if status.ConsecutiveOKs != 1 {
		t.Fatalf("expected 1 OK, got %d", status.ConsecutiveOKs)
	}
	if status.Healthy {
		t.Fatalf("should not be healthy yet (threshold=2)")
	}

	// Second OK: ConsecutiveOKs=2, now healthy (state transition: unhealthy -> healthy)
	worker.runCheck(check, member, pool)
	status = worker.getStatus(10)
	if !status.Healthy {
		t.Fatalf("should be healthy after reaching threshold")
	}
	if status.ConsecutiveFails != 0 {
		t.Fatalf("consecutive fails should be reset, got %d", status.ConsecutiveFails)
	}
}

func TestHealthCheckWorker_StartWithDisabledChecksOnly(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}

	policyID, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "disabled-only",
		Domain:     "disabled-only.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	_, _ = hcStorage.CreateHealthCheck(&model.HealthCheck{
		PolicyID:           policyID,
		CheckType:          "tcp",
		Target:             "53",
		IntervalSec:        60,
		TimeoutSec:         1,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            false, // disabled
	})

	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)
	worker.Start()
	time.Sleep(100 * time.Millisecond)
	worker.Stop()
}

func TestHealthCheckWorker_ProbeHTTPMemberAddressWithPort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	// Member address already contains port (host:port format)
	check := &model.HealthCheck{
		PolicyID:   1,
		CheckType:  "http",
		Target:     "/",
		TimeoutSec: 5,
	}
	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: serverAddr, // already "127.0.0.1:PORT"
		Weight:  100,
		Enabled: true,
	}

	err := worker.probe(check, member)
	if err != nil {
		t.Fatalf("expected successful probe with member address containing port, got: %v", err)
	}
}

func TestHealthCheckWorker_ProbeHTTPSDefaultPort(t *testing.T) {
	// Tests the HTTPS default port path (port == "" && scheme == "https" -> port = "443")
	// This will fail connection but exercises the port selection code path
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// Full HTTPS URL without explicit port - exercises the "port = 443" branch
	check := &model.HealthCheck{
		PolicyID:   1,
		CheckType:  "https",
		Target:     "https://example.com/health",
		TimeoutSec: 1,
	}
	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: "192.0.2.1", // Will fail connection, but code path is tested
		Weight:  100,
		Enabled: true,
	}

	err := worker.probe(check, member)
	// We expect an error (connection refused/timeout) but the code path for
	// HTTPS default port selection should have been exercised
	if err == nil {
		t.Fatalf("expected error for unreachable HTTPS endpoint, got nil")
	}
}

func TestHealthCheckWorker_ProbeHTTPDefaultPort(t *testing.T) {
	// Tests the HTTP default port path (port == "" && scheme != "https" -> port = "80")
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// Full HTTP URL without explicit port
	check := &model.HealthCheck{
		PolicyID:   1,
		CheckType:  "http",
		Target:     "http://example.com/health",
		TimeoutSec: 1,
	}
	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: "192.0.2.1",
		Weight:  100,
		Enabled: true,
	}

	err := worker.probe(check, member)
	if err == nil {
		t.Fatalf("expected error for unreachable HTTP endpoint, got nil")
	}
}

func TestHealthCheckWorker_ProbeDefaultTypeFailure(t *testing.T) {
	// Tests the error return branch of the default (non-tcp, non-http) type
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	check := &model.HealthCheck{
		PolicyID:   1,
		CheckType:  "something_custom",
		Target:     "9999",
		TimeoutSec: 1,
	}
	member := &model.GSLBMember{
		ID:      1,
		PoolID:  1,
		Address: "192.0.2.1", // unreachable
		Weight:  100,
		Enabled: true,
	}

	err := worker.probe(check, member)
	if err == nil {
		t.Fatalf("expected error for unreachable endpoint with default type, got nil")
	}
}

func TestHealthCheckWorker_RunPolicyCheckWithMultiplePools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	policyStorage := NewPolicyStorage(db)
	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}
	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// Create a real policy with multiple pools
	policyID, _ := policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       "multi-pool",
		Domain:     "multi.example.com.",
		RecordType: "A",
		TTL:        60,
		Enabled:    true,
	})

	serverAddr := server.Listener.Addr().String()

	// Pool 1
	pool1ID, _ := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "pool1",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     0,
		FallbackPool: false,
	})
	_, _ = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  pool1ID,
		Address: serverAddr,
		Weight:  100,
		Enabled: true,
	})

	// Pool 2
	pool2ID, _ := poolStorage.CreatePool(&model.GSLBPool{
		PolicyID:     policyID,
		Name:         "pool2",
		MatchType:    "default",
		MatchValue:   "*",
		Priority:     10,
		FallbackPool: true,
	})
	_, _ = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  pool2ID,
		Address: serverAddr,
		Weight:  100,
		Enabled: true,
	})
	// Also add a disabled member in pool2
	_, _ = poolStorage.CreateMember(&model.GSLBMember{
		PoolID:  pool2ID,
		Address: serverAddr,
		Weight:  50,
		Enabled: false,
	})

	check := &model.HealthCheck{
		ID:                 1,
		PolicyID:           policyID,
		CheckType:          "http",
		Target:             server.URL,
		IntervalSec:        1,
		TimeoutSec:         5,
		HealthyThreshold:   1,
		UnhealthyThreshold: 1,
		Enabled:            true,
	}

	// This exercises the full runPolicyCheck: iterates all pools, all members, skips disabled
	worker.runPolicyCheck(check)
}

func TestHealthCheckWorker_RestartWithNoChecks(t *testing.T) {
	db, cleanup := setupHealthCheckTestDB(t)
	defer cleanup()

	poolStorage := NewPoolStorage(db)
	hcStorage := NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}

	worker := NewHealthCheckWorker(hcStorage, poolStorage, healthStatus)

	// Restart with no checks in DB - should handle gracefully
	worker.Restart()
	time.Sleep(600 * time.Millisecond)
	worker.Stop()
}
