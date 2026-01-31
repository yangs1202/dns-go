package dns

import (
	"dns-go/model"
	"dns-go/storage"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDB는 테스트용 데이터베이스를 설정합니다
func setupTestDB(t *testing.T) (*storage.Database, *storage.UpstreamStorage) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := storage.NewDatabase(dbPath)
	require.NoError(t, err)

	t.Cleanup(func() {
		db.Close()
	})

	upstreamStorage := storage.NewUpstreamStorage(db)

	return db, upstreamStorage
}

// createTestServer는 테스트용 업스트림 서버를 생성합니다
func createTestServer(t *testing.T, storage *storage.UpstreamStorage, name, address, protocol string, priority int64, enabled bool) *model.UpstreamServer {
	server := &model.UpstreamServer{
		Name:     name,
		Address:  address,
		Protocol: protocol,
		Priority: priority,
		Enabled:  enabled,
	}

	id, err := storage.CreateUpstreamServer(server)
	require.NoError(t, err)

	server.ID = id
	return server
}

// startTestDNSServer는 테스트용 DNS 서버를 시작합니다
func startTestDNSServer(t *testing.T, network string, address string) *dns.Server {
	server := &dns.Server{
		Addr: address,
		Net:  network,
	}

	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(r)

		// A 레코드 응답
		if len(r.Question) > 0 && r.Question[0].Qtype == dns.TypeA {
			rr, _ := dns.NewRR(fmt.Sprintf("%s 300 IN A 1.2.3.4", r.Question[0].Name))
			m.Answer = append(m.Answer, rr)
		}

		w.WriteMsg(m)
	})

	go func() {
		err := server.ListenAndServe()
		if err != nil {
			t.Logf("DNS 서버 실행 실패: %v", err)
		}
	}()

	// 서버가 시작될 때까지 대기
	time.Sleep(100 * time.Millisecond)

	return server
}

// TestForward_UDP는 UDP 프로토콜로 기본 포워딩을 테스트합니다
func TestForward_UDP(t *testing.T) {
	// 테스트 DB 및 스토리지 설정
	db, upstreamStorage := setupTestDB(t)
	defer db.Close()

	// 테스트 DNS 서버 시작
	testServer := startTestDNSServer(t, "udp", "127.0.0.1:15353")
	defer testServer.Shutdown()

	// 업스트림 서버 생성
	createTestServer(t, upstreamStorage, "Test UDP", "127.0.0.1:15353", "udp", 1, true)

	// Resolver 생성
	resolver := NewResolver(upstreamStorage, 2*time.Second)

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	// Forward 실행
	resp, err := resolver.Forward(req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// 응답 검증
	assert.Equal(t, 1, len(resp.Answer))
	assert.Equal(t, "example.com.", resp.Question[0].Name)
}

// TestForward_TCP는 TCP 프로토콜로 포워딩을 테스트합니다
func TestForward_TCP(t *testing.T) {
	// 테스트 DB 및 스토리지 설정
	db, upstreamStorage := setupTestDB(t)
	defer db.Close()

	// 테스트 DNS 서버 시작 (TCP)
	testServer := startTestDNSServer(t, "tcp", "127.0.0.1:15354")
	defer testServer.Shutdown()

	// 업스트림 서버 생성
	createTestServer(t, upstreamStorage, "Test TCP", "127.0.0.1:15354", "tcp", 1, true)

	// Resolver 생성
	resolver := NewResolver(upstreamStorage, 2*time.Second)

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	// Forward 실행
	resp, err := resolver.Forward(req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// 응답 검증
	assert.Equal(t, 1, len(resp.Answer))
}

// TestForward_Priority는 우선순위 기반 서버 선택을 테스트합니다
func TestForward_Priority(t *testing.T) {
	// 테스트 DB 및 스토리지 설정
	db, upstreamStorage := setupTestDB(t)
	defer db.Close()

	// 테스트 DNS 서버 시작 (우선순위 높은 서버)
	testServer1 := startTestDNSServer(t, "udp", "127.0.0.1:15355")
	defer testServer1.Shutdown()

	// 테스트 DNS 서버 시작 (우선순위 낮은 서버)
	testServer2 := startTestDNSServer(t, "udp", "127.0.0.1:15356")
	defer testServer2.Shutdown()

	// 업스트림 서버 생성 (우선순위가 낮은 것이 먼저)
	createTestServer(t, upstreamStorage, "Low Priority", "127.0.0.1:15356", "udp", 10, true)
	createTestServer(t, upstreamStorage, "High Priority", "127.0.0.1:15355", "udp", 1, true)

	// Resolver 생성
	resolver := NewResolver(upstreamStorage, 2*time.Second)

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	// Forward 실행 - 우선순위 높은 서버(priority 1)가 먼저 사용되어야 함
	resp, err := resolver.Forward(req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// 응답 검증
	assert.Equal(t, 1, len(resp.Answer))
}

// TestForward_Fallback는 첫 번째 서버 실패 시 다음 서버로 폴백을 테스트합니다
func TestForward_Fallback(t *testing.T) {
	// 테스트 DB 및 스토리지 설정
	db, upstreamStorage := setupTestDB(t)
	defer db.Close()

	// 테스트 DNS 서버 시작 (두 번째 서버만)
	testServer := startTestDNSServer(t, "udp", "127.0.0.1:15358")
	defer testServer.Shutdown()

	// 업스트림 서버 생성
	createTestServer(t, upstreamStorage, "Failing Server", "127.0.0.1:15357", "udp", 1, true)  // 존재하지 않는 서버
	createTestServer(t, upstreamStorage, "Working Server", "127.0.0.1:15358", "udp", 2, true) // 정상 서버

	// Resolver 생성
	resolver := NewResolver(upstreamStorage, 500*time.Millisecond)

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	// Forward 실행 - 첫 번째 서버 실패 후 두 번째 서버로 폴백
	resp, err := resolver.Forward(req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// 응답 검증
	assert.Equal(t, 1, len(resp.Answer))
}

// TestForward_AllServersFail는 모든 서버 실패 시 에러를 테스트합니다
func TestForward_AllServersFail(t *testing.T) {
	// 테스트 DB 및 스토리지 설정
	db, upstreamStorage := setupTestDB(t)
	defer db.Close()

	// 업스트림 서버 생성 (모두 존재하지 않는 서버)
	createTestServer(t, upstreamStorage, "Failing Server 1", "127.0.0.1:15359", "udp", 1, true)
	createTestServer(t, upstreamStorage, "Failing Server 2", "127.0.0.1:15360", "udp", 2, true)

	// Resolver 생성
	resolver := NewResolver(upstreamStorage, 500*time.Millisecond)

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	// Forward 실행 - 모든 서버 실패
	resp, err := resolver.Forward(req)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "모든 업스트림 서버 실패")
}

// TestForward_Timeout는 타임아웃을 테스트합니다
func TestForward_Timeout(t *testing.T) {
	// 테스트 DB 및 스토리지 설정
	db, upstreamStorage := setupTestDB(t)
	defer db.Close()

	// 응답하지 않는 서버 시뮬레이션 (잘못된 주소)
	createTestServer(t, upstreamStorage, "Timeout Server", "192.0.2.1:53", "udp", 1, true)

	// Resolver 생성 (짧은 타임아웃)
	resolver := NewResolver(upstreamStorage, 100*time.Millisecond)

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	// Forward 실행 - 타임아웃 발생
	start := time.Now()
	resp, err := resolver.Forward(req)
	duration := time.Since(start)

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Less(t, duration, 500*time.Millisecond) // 타임아웃이 설정대로 작동하는지 확인
}

// TestForwardToServer_UDP는 UDP 프로토콜로 단일 서버 포워딩을 테스트합니다
func TestForwardToServer_UDP(t *testing.T) {
	// 테스트 DB 및 스토리지 설정
	db, upstreamStorage := setupTestDB(t)
	defer db.Close()

	// 테스트 DNS 서버 시작
	testServer := startTestDNSServer(t, "udp", "127.0.0.1:15361")
	defer testServer.Shutdown()

	// Resolver 생성
	resolver := NewResolver(upstreamStorage, 2*time.Second)

	// 업스트림 서버 모델 생성
	server := &model.UpstreamServer{
		Name:     "Test Server",
		Address:  "127.0.0.1:15361",
		Protocol: "udp",
	}

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	// forwardToServer 실행
	resp, err := resolver.forwardToServer(server, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// 응답 검증
	assert.Equal(t, 1, len(resp.Answer))
}

// TestForwardToServer_TCP는 TCP 프로토콜로 단일 서버 포워딩을 테스트합니다
func TestForwardToServer_TCP(t *testing.T) {
	// 테스트 DB 및 스토리지 설정
	db, upstreamStorage := setupTestDB(t)
	defer db.Close()

	// 테스트 DNS 서버 시작
	testServer := startTestDNSServer(t, "tcp", "127.0.0.1:15362")
	defer testServer.Shutdown()

	// Resolver 생성
	resolver := NewResolver(upstreamStorage, 2*time.Second)

	// 업스트림 서버 모델 생성
	server := &model.UpstreamServer{
		Name:     "Test Server",
		Address:  "127.0.0.1:15362",
		Protocol: "tcp",
	}

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	// forwardToServer 실행
	resp, err := resolver.forwardToServer(server, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// 응답 검증
	assert.Equal(t, 1, len(resp.Answer))
}

// TestForwardToServer_UnsupportedProtocol는 지원하지 않는 프로토콜을 테스트합니다
func TestForwardToServer_UnsupportedProtocol(t *testing.T) {
	// 테스트 DB 및 스토리지 설정
	db, upstreamStorage := setupTestDB(t)
	defer db.Close()

	// Resolver 생성
	resolver := NewResolver(upstreamStorage, 2*time.Second)

	// 업스트림 서버 모델 생성 (잘못된 프로토콜)
	server := &model.UpstreamServer{
		Name:     "Test Server",
		Address:  "127.0.0.1:53",
		Protocol: "http",
	}

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	// forwardToServer 실행 - 에러 발생
	resp, err := resolver.forwardToServer(server, req)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "지원하지 않는 프로토콜")
}

// TestForward_DisabledServersNotUsed는 비활성화된 서버가 사용되지 않는지 테스트합니다
func TestForward_DisabledServersNotUsed(t *testing.T) {
	// 테스트 DB 및 스토리지 설정
	db, upstreamStorage := setupTestDB(t)
	defer db.Close()

	// 테스트 DNS 서버 시작
	testServer := startTestDNSServer(t, "udp", "127.0.0.1:15363")
	defer testServer.Shutdown()

	// 업스트림 서버 생성
	createTestServer(t, upstreamStorage, "Disabled Server", "127.0.0.1:15364", "udp", 1, false) // 비활성화
	createTestServer(t, upstreamStorage, "Enabled Server", "127.0.0.1:15363", "udp", 2, true)   // 활성화

	// Resolver 생성
	resolver := NewResolver(upstreamStorage, 2*time.Second)

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	// Forward 실행 - 활성화된 서버만 사용
	resp, err := resolver.Forward(req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// 응답 검증
	assert.Equal(t, 1, len(resp.Answer))
}

// TestForward_NoEnabledServers는 활성화된 서버가 없을 때 에러를 테스트합니다
func TestForward_NoEnabledServers(t *testing.T) {
	// 테스트 DB 및 스토리지 설정
	db, upstreamStorage := setupTestDB(t)
	defer db.Close()

	// 업스트림 서버 생성 (모두 비활성화)
	createTestServer(t, upstreamStorage, "Disabled Server 1", "127.0.0.1:53", "udp", 1, false)
	createTestServer(t, upstreamStorage, "Disabled Server 2", "8.8.8.8:53", "udp", 2, false)

	// Resolver 생성
	resolver := NewResolver(upstreamStorage, 2*time.Second)

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	// Forward 실행 - 활성화된 서버가 없음
	resp, err := resolver.Forward(req)
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "활성화된 업스트림 서버가 없습니다")
}
