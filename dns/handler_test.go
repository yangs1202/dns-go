package dns

import (
	"dns-go/model"
	"dns-go/storage"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// setupTestHandler는 테스트용 핸들러를 생성합니다
func setupTestHandler(t *testing.T) (*Handler, *storage.Database, func()) {
	// 임시 데이터베이스 생성
	dbPath := "/tmp/test_handler_" + t.Name() + ".db"
	_ = os.Remove(dbPath)

	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("데이터베이스 생성 실패: %v", err)
	}

	// Storage 생성
	zoneStorage := storage.NewZoneStorage(db)
	recordStorage := storage.NewRecordStorage(db)
	upstreamStorage := storage.NewUpstreamStorage(db)

	// Resolver 생성
	resolver := NewResolver(upstreamStorage, 5*time.Second)

	// Handler 생성
	stats := NewQueryStats()
	handler, err := NewHandler(zoneStorage, recordStorage, resolver, db, stats, nil, nil, nil, "0.0.0.0", "test-server", "DNS-Go Test v1.0", nil)
	if err != nil {
		t.Fatalf("핸들러 생성 실패: %v", err)
	}

	// Cleanup 함수
	cleanup := func() {
		handler.Stop()
		_ = db.Close()
		_ = os.Remove(dbPath)
	}

	return handler, db, cleanup
}

// mockResponseWriter는 테스트용 ResponseWriter입니다
type mockResponseWriter struct {
	msg      *dns.Msg
	written  bool
	remoteIP string
}

func (m *mockResponseWriter) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53}
}

func (m *mockResponseWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP(m.remoteIP), Port: 53}
}

func (m *mockResponseWriter) WriteMsg(msg *dns.Msg) error {
	m.msg = msg
	m.written = true
	return nil
}

func (m *mockResponseWriter) Write([]byte) (int, error) {
	return 0, nil
}

func (m *mockResponseWriter) Close() error {
	return nil
}

func (m *mockResponseWriter) TsigStatus() error {
	return nil
}

func (m *mockResponseWriter) TsigTimersOnly(bool) {}

func (m *mockResponseWriter) Hijack() {}

func newMockWriter(remoteIP string) *mockResponseWriter {
	return &mockResponseWriter{remoteIP: remoteIP}
}

// TestServeDNS_L1CacheHit은 L1 캐시 히트를 테스트합니다
func TestServeDNS_L1CacheHit(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// L1 캐시에 미리 저장
	rrs := []dns.RR{
		&dns.A{
			Hdr: dns.RR_Header{
				Name:   "test.example.com.",
				Rrtype: dns.TypeA,
				Class:  dns.ClassINET,
				Ttl:    300,
			},
			A: net.ParseIP("192.0.2.1"),
		},
	}
	handler.cache.Set("test.example.com.", "A", rrs, 300, false)

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("test.example.com.", dns.TypeA)

	// 쿼리 실행
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	// 검증
	if !w.written {
		t.Fatal("응답이 작성되지 않음")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("예상 Rcode: %d, 실제: %d", dns.RcodeSuccess, w.msg.Rcode)
	}

	if len(w.msg.Answer) != 1 {
		t.Errorf("예상 Answer 수: 1, 실제: %d", len(w.msg.Answer))
	}

	// 캐시 히트 확인
	stats := handler.cache.GetStats()
	if stats.Hits != 1 {
		t.Errorf("예상 캐시 히트: 1, 실제: %d", stats.Hits)
	}
}

// TestServeDNS_L2CacheHit은 Zone + Record 조회를 테스트합니다
func TestServeDNS_L2CacheHit(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone 생성
	zone := &model.Zone{
		Name:       "example.com.",
		SOAMname:   "ns1.example.com.",
		SOARname:   "admin.example.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}

	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone 생성 실패: %v", err)
	}

	// Record 생성
	record := &model.Record{
		ZoneID:  zoneID,
		Name:    "test.example.com.",
		Type:    "A",
		Content: "192.0.2.10",
		TTL:     300,
		Enabled: true,
	}

	_, err = handler.recordStorage.CreateRecord(record)
	if err != nil {
		t.Fatalf("Record 생성 실패: %v", err)
	}

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("test.example.com.", dns.TypeA)

	// 쿼리 실행
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	// 검증
	if !w.written {
		t.Fatal("응답이 작성되지 않음")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("예상 Rcode: %d, 실제: %d", dns.RcodeSuccess, w.msg.Rcode)
	}

	if len(w.msg.Answer) != 1 {
		t.Errorf("예상 Answer 수: 1, 실제: %d", len(w.msg.Answer))
	}

	// A 레코드 확인
	if a, ok := w.msg.Answer[0].(*dns.A); ok {
		if a.A.String() != "192.0.2.10" {
			t.Errorf("예상 IP: 192.0.2.10, 실제: %s", a.A.String())
		}
	} else {
		t.Error("A 레코드가 아님")
	}

	// L1 캐시에 저장되었는지 확인
	if entry, ok := handler.cache.Get("test.example.com.", "A"); !ok {
		t.Error("L1 캐시에 저장되지 않음")
	} else if entry.IsNegative {
		t.Error("Negative 캐시로 잘못 저장됨")
	}

	// 캐시 통계 확인
	stats := handler.cache.GetStats()
	if stats.Misses != 1 {
		t.Errorf("예상 캐시 미스: 1, 실제: %d", stats.Misses)
	}
	if stats.Size != 1 {
		t.Errorf("예상 캐시 크기: 1, 실제: %d", stats.Size)
	}

	// 쿼리 재실행으로 캐시 히트 확인
	oldHits := stats.Hits
	w2 := newMockWriter("192.0.2.100")
	handler.ServeDNS(w2, req)

	stats2 := handler.cache.GetStats()
	newHits := stats2.Hits - oldHits
	if newHits != 1 {
		t.Errorf("예상 캐시 히트 증가: 1, 실제: %d", newHits)
	}
}

// TestServeDNS_CNAMEChainMultiHop은 다단 CNAME 체인 해석을 테스트합니다.
func TestServeDNS_CNAMEChainMultiHop(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:          "example.com.",
		Enabled:       true,
		AllowFallback: false,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone 생성 실패: %v", err)
	}

	records := []*model.Record{
		{ZoneID: zoneID, Name: "noti.example.com.", Type: "CNAME", Content: "lb.example.com.", TTL: 300, Enabled: true},
		{ZoneID: zoneID, Name: "lb.example.com.", Type: "CNAME", Content: "final.example.com.", TTL: 300, Enabled: true},
		{ZoneID: zoneID, Name: "final.example.com.", Type: "A", Content: "192.0.2.77", TTL: 60, Enabled: true},
	}
	for _, record := range records {
		if _, err := handler.recordStorage.CreateRecord(record); err != nil {
			t.Fatalf("Record 생성 실패: %v", err)
		}
	}

	req := new(dns.Msg)
	req.SetQuestion("noti.example.com.", dns.TypeA)

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if w.msg == nil {
		t.Fatal("응답이 없습니다")
	}
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("예상 Rcode: %d, 실제: %d", dns.RcodeSuccess, w.msg.Rcode)
	}
	if len(w.msg.Answer) != 3 {
		t.Fatalf("예상 Answer 수: 3 (CNAME, CNAME, A), 실제: %d", len(w.msg.Answer))
	}

	if _, ok := w.msg.Answer[0].(*dns.CNAME); !ok {
		t.Fatalf("첫 번째 응답은 CNAME이어야 합니다: %T", w.msg.Answer[0])
	}
	if _, ok := w.msg.Answer[1].(*dns.CNAME); !ok {
		t.Fatalf("두 번째 응답은 CNAME이어야 합니다: %T", w.msg.Answer[1])
	}
	a, ok := w.msg.Answer[2].(*dns.A)
	if !ok {
		t.Fatalf("세 번째 응답은 A여야 합니다: %T", w.msg.Answer[2])
	}
	if a.A.String() != "192.0.2.77" {
		t.Fatalf("예상 IP: 192.0.2.77, 실제: %s", a.A.String())
	}
}

// TestServeDNS_CNAMEChainLoop은 CNAME 순환 참조 시 무한 루프 없이 응답하는지 테스트합니다.
func TestServeDNS_CNAMEChainLoop(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	zone := &model.Zone{
		Name:          "example.com.",
		Enabled:       true,
		AllowFallback: false,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone 생성 실패: %v", err)
	}

	records := []*model.Record{
		{ZoneID: zoneID, Name: "a.example.com.", Type: "CNAME", Content: "b.example.com.", TTL: 300, Enabled: true},
		{ZoneID: zoneID, Name: "b.example.com.", Type: "CNAME", Content: "a.example.com.", TTL: 300, Enabled: true},
	}
	for _, record := range records {
		if _, err := handler.recordStorage.CreateRecord(record); err != nil {
			t.Fatalf("Record 생성 실패: %v", err)
		}
	}

	req := new(dns.Msg)
	req.SetQuestion("a.example.com.", dns.TypeA)

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if w.msg == nil {
		t.Fatal("응답이 없습니다")
	}
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("예상 Rcode: %d, 실제: %d", dns.RcodeSuccess, w.msg.Rcode)
	}
	if len(w.msg.Answer) == 0 {
		t.Fatal("CNAME 응답이 없습니다")
	}
	if len(w.msg.Answer) > maxCNAMEChainDepth {
		t.Fatalf("CNAME 체인 depth 제한 초과: %d", len(w.msg.Answer))
	}
}

// TestServeDNS_CNAMEChainCrossZone은 크로스 Zone CNAME 해석을 테스트합니다
func TestServeDNS_CNAMEChainCrossZone(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone 1: decoverstudio.com
	zone1 := &model.Zone{
		Name:          "decoverstudio.com.",
		Enabled:       true,
		AllowFallback: false,
	}
	zoneID1, err := handler.zoneStorage.CreateZone(zone1)
	if err != nil {
		t.Fatalf("Zone 1 생성 실패: %v", err)
	}

	// Zone 2: yangs.sh
	zone2 := &model.Zone{
		Name:          "yangs.sh.",
		Enabled:       true,
		AllowFallback: false,
	}
	zoneID2, err := handler.zoneStorage.CreateZone(zone2)
	if err != nil {
		t.Fatalf("Zone 2 생성 실패: %v", err)
	}

	// Zone 1의 레코드: dev-ins-api.decoverstudio.com. -> lb.yangs.sh.
	record1 := &model.Record{
		ZoneID:  zoneID1,
		Name:    "dev-ins-api.decoverstudio.com.",
		Type:    "CNAME",
		Content: "lb.yangs.sh.",
		TTL:     300,
		Enabled: true,
	}
	if _, err := handler.recordStorage.CreateRecord(record1); err != nil {
		t.Fatalf("CNAME 레코드 생성 실패: %v", err)
	}

	// Zone 2의 레코드: lb.yangs.sh. -> A 10.97.11.110
	record2 := &model.Record{
		ZoneID:  zoneID2,
		Name:    "lb.yangs.sh.",
		Type:    "A",
		Content: "10.97.11.110",
		TTL:     60,
		Enabled: true,
	}
	if _, err := handler.recordStorage.CreateRecord(record2); err != nil {
		t.Fatalf("A 레코드 생성 실패: %v", err)
	}

	// dev-ins-api.decoverstudio.com에 대한 A 쿼리
	req := new(dns.Msg)
	req.SetQuestion("dev-ins-api.decoverstudio.com.", dns.TypeA)

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if w.msg == nil {
		t.Fatal("응답이 없습니다")
	}
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("예상 Rcode: %d, 실제: %d", dns.RcodeSuccess, w.msg.Rcode)
	}

	// CNAME + A 레코드 모두 반환되어야 함
	if len(w.msg.Answer) != 2 {
		t.Fatalf("예상 Answer 수: 2 (CNAME, A), 실제: %d", len(w.msg.Answer))
	}

	// 첫 번째는 CNAME이어야 함
	cname, ok := w.msg.Answer[0].(*dns.CNAME)
	if !ok {
		t.Fatalf("첫 번째 응답은 CNAME이어야 합니다: %T", w.msg.Answer[0])
	}
	if cname.Target != "lb.yangs.sh." {
		t.Fatalf("예상 CNAME 타겟: lb.yangs.sh., 실제: %s", cname.Target)
	}

	// 두 번째는 A 레코드여야 함
	a, ok := w.msg.Answer[1].(*dns.A)
	if !ok {
		t.Fatalf("두 번째 응답은 A여야 합니다: %T", w.msg.Answer[1])
	}
	if a.A.String() != "10.97.11.110" {
		t.Fatalf("예상 IP: 10.97.11.110, 실제: %s", a.A.String())
	}
}

// TestServeDNS_CNAMEChainCrossZoneUpstream은 업스트림 포워딩을 통한 크로스 Zone CNAME 해석을 테스트합니다
func TestServeDNS_CNAMEChainCrossZoneUpstream(t *testing.T) {
	t.Skip("실제 업스트림 DNS 서버가 필요하므로 스킵")

	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	// 업스트림 서버 추가
	upstream := &model.UpstreamServer{
		Name:     "Google DNS",
		Address:  "8.8.8.8:53",
		Protocol: "udp",
		Priority: 1,
		Enabled:  true,
	}
	_, err := db.Writer.Exec(
		`INSERT INTO upstream_servers (name, address, protocol, priority, enabled) VALUES (?, ?, ?, ?, ?)`,
		upstream.Name, upstream.Address, upstream.Protocol, upstream.Priority, upstream.Enabled,
	)
	if err != nil {
		t.Fatalf("업스트림 서버 추가 실패: %v", err)
	}

	// Zone 생성
	zone := &model.Zone{
		Name:          "example.com.",
		Enabled:       true,
		AllowFallback: true,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone 생성 실패: %v", err)
	}

	// CNAME 레코드: api.example.com. -> www.google.com.
	record := &model.Record{
		ZoneID:  zoneID,
		Name:    "api.example.com.",
		Type:    "CNAME",
		Content: "www.google.com.",
		TTL:     300,
		Enabled: true,
	}
	if _, err := handler.recordStorage.CreateRecord(record); err != nil {
		t.Fatalf("CNAME 레코드 생성 실패: %v", err)
	}

	// api.example.com에 대한 A 쿼리 (www.google.com은 업스트림에서 해석됨)
	req := new(dns.Msg)
	req.SetQuestion("api.example.com.", dns.TypeA)

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if w.msg == nil {
		t.Fatal("응답이 없습니다")
	}
	if w.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("예상 Rcode: %d, 실제: %d", dns.RcodeSuccess, w.msg.Rcode)
	}

	// CNAME + A 레코드 모두 반환되어야 함
	if len(w.msg.Answer) < 2 {
		t.Fatalf("예상 Answer 수: 최소 2 (CNAME, A), 실제: %d", len(w.msg.Answer))
	}

	// 첫 번째는 CNAME이어야 함
	cname, ok := w.msg.Answer[0].(*dns.CNAME)
	if !ok {
		t.Fatalf("첫 번째 응답은 CNAME이어야 합니다: %T", w.msg.Answer[0])
	}
	if cname.Target != "www.google.com." {
		t.Fatalf("예상 CNAME 타겟: www.google.com., 실제: %s", cname.Target)
	}

	// 마지막은 A 레코드여야 함
	_, ok = w.msg.Answer[len(w.msg.Answer)-1].(*dns.A)
	if !ok {
		t.Fatalf("마지막 응답은 A여야 합니다: %T", w.msg.Answer[len(w.msg.Answer)-1])
	}
}

// TestServeDNS_UpstreamForwarding은 업스트림 포워딩을 테스트합니다
func TestServeDNS_UpstreamForwarding(t *testing.T) {
	t.Skip("실제 DNS 서버가 필요하므로 스킵")

	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	// 업스트림 서버 추가
	upstream := &model.UpstreamServer{
		Name:     "Google DNS",
		Address:  "8.8.8.8:53",
		Protocol: "udp",
		Priority: 1,
		Enabled:  true,
	}

	_, err := db.Writer.Exec(
		`INSERT INTO upstream_servers (name, address, protocol, priority, enabled) VALUES (?, ?, ?, ?, ?)`,
		upstream.Name, upstream.Address, upstream.Protocol, upstream.Priority, upstream.Enabled,
	)
	if err != nil {
		t.Fatalf("업스트림 서버 추가 실패: %v", err)
	}

	// DNS 쿼리 생성 (존재하지 않는 도메인)
	req := new(dns.Msg)
	req.SetQuestion("www.google.com.", dns.TypeA)

	// 쿼리 실행
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	// 검증
	if !w.written {
		t.Fatal("응답이 작성되지 않음")
	}

	if w.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("예상 Rcode: %d, 실제: %d", dns.RcodeSuccess, w.msg.Rcode)
	}

	if len(w.msg.Answer) == 0 {
		t.Error("업스트림 응답이 없음")
	}
}

// TestServeDNS_NXDOMAIN은 NXDOMAIN 응답을 테스트합니다
func TestServeDNS_NXDOMAIN(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone은 없고 업스트림도 설정하지 않음

	// DNS 쿼리 생성
	req := new(dns.Msg)
	req.SetQuestion("nonexistent.example.com.", dns.TypeA)

	// 쿼리 실행
	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	// 검증
	if !w.written {
		t.Fatal("응답이 작성되지 않음")
	}

	if w.msg.Rcode != dns.RcodeNameError {
		t.Errorf("예상 Rcode: %d (NXDOMAIN), 실제: %d", dns.RcodeNameError, w.msg.Rcode)
	}

	// Negative 캐시 확인
	if entry, ok := handler.cache.Get("nonexistent.example.com.", "A"); !ok {
		t.Error("Negative 캐시가 저장되지 않음")
	} else if !entry.IsNegative {
		t.Error("Negative 캐시 플래그가 설정되지 않음")
	}
}

// TestBuildResponse는 응답 생성을 테스트합니다
func TestBuildResponse(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	question := dns.Question{
		Name:   "test.example.com.",
		Qtype:  dns.TypeA,
		Qclass: dns.ClassINET,
	}

	records := []*model.Record{
		{
			Name:    "test.example.com.",
			Type:    "A",
			Content: "192.0.2.1",
			TTL:     300,
			Enabled: true,
		},
		{
			Name:    "test.example.com.",
			Type:    "A",
			Content: "192.0.2.2",
			TTL:     300,
			Enabled: true,
		},
		{
			Name:    "test.example.com.",
			Type:    "A",
			Content: "192.0.2.3",
			TTL:     300,
			Enabled: false, // 비활성화된 레코드
		},
	}

	resp := handler.buildResponse(question, records)

	// 검증: 활성화된 레코드만 포함
	if len(resp.Answer) != 2 {
		t.Errorf("예상 Answer 수: 2, 실제: %d", len(resp.Answer))
	}

	// IP 확인
	ips := make([]string, 0)
	for _, rr := range resp.Answer {
		if a, ok := rr.(*dns.A); ok {
			ips = append(ips, a.A.String())
		}
	}

	expectedIPs := []string{"192.0.2.1", "192.0.2.2"}
	for i, expected := range expectedIPs {
		if i >= len(ips) || ips[i] != expected {
			t.Errorf("예상 IP[%d]: %s, 실제: %s", i, expected, ips[i])
		}
	}
}

// TestRecordToRR은 레코드 타입 변환을 테스트합니다
func TestRecordToRR(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	tests := []struct {
		name    string
		record  *model.Record
		checkFn func(t *testing.T, rr dns.RR)
	}{
		{
			name: "A 레코드",
			record: &model.Record{
				Name:    "test.example.com.",
				Type:    "A",
				Content: "192.0.2.1",
				TTL:     300,
			},
			checkFn: func(t *testing.T, rr dns.RR) {
				a, ok := rr.(*dns.A)
				if !ok {
					t.Fatal("A 레코드가 아님")
				}
				if a.A.String() != "192.0.2.1" {
					t.Errorf("예상 IP: 192.0.2.1, 실제: %s", a.A.String())
				}
			},
		},
		{
			name: "AAAA 레코드",
			record: &model.Record{
				Name:    "test.example.com.",
				Type:    "AAAA",
				Content: "2001:db8::1",
				TTL:     300,
			},
			checkFn: func(t *testing.T, rr dns.RR) {
				aaaa, ok := rr.(*dns.AAAA)
				if !ok {
					t.Fatal("AAAA 레코드가 아님")
				}
				if aaaa.AAAA.String() != "2001:db8::1" {
					t.Errorf("예상 IP: 2001:db8::1, 실제: %s", aaaa.AAAA.String())
				}
			},
		},
		{
			name: "CNAME 레코드",
			record: &model.Record{
				Name:    "www.example.com.",
				Type:    "CNAME",
				Content: "target.example.com.",
				TTL:     300,
			},
			checkFn: func(t *testing.T, rr dns.RR) {
				cname, ok := rr.(*dns.CNAME)
				if !ok {
					t.Fatal("CNAME 레코드가 아님")
				}
				if cname.Target != "target.example.com." {
					t.Errorf("예상 Target: target.example.com., 실제: %s", cname.Target)
				}
			},
		},
		{
			name: "MX 레코드",
			record: &model.Record{
				Name:     "example.com.",
				Type:     "MX",
				Content:  "mail.example.com.",
				TTL:      300,
				Priority: 10,
			},
			checkFn: func(t *testing.T, rr dns.RR) {
				mx, ok := rr.(*dns.MX)
				if !ok {
					t.Fatal("MX 레코드가 아님")
				}
				if mx.Mx != "mail.example.com." {
					t.Errorf("예상 MX: mail.example.com., 실제: %s", mx.Mx)
				}
				if mx.Preference != 10 {
					t.Errorf("예상 Preference: 10, 실제: %d", mx.Preference)
				}
			},
		},
		{
			name: "TXT 레코드",
			record: &model.Record{
				Name:    "example.com.",
				Type:    "TXT",
				Content: "v=spf1 include:_spf.example.com ~all",
				TTL:     300,
			},
			checkFn: func(t *testing.T, rr dns.RR) {
				txt, ok := rr.(*dns.TXT)
				if !ok {
					t.Fatal("TXT 레코드가 아님")
				}
				if len(txt.Txt) != 1 || txt.Txt[0] != "v=spf1 include:_spf.example.com ~all" {
					t.Errorf("예상 TXT: [v=spf1 include:_spf.example.com ~all], 실제: %v", txt.Txt)
				}
			},
		},
		{
			name: "NS 레코드",
			record: &model.Record{
				Name:    "example.com.",
				Type:    "NS",
				Content: "ns1.example.com.",
				TTL:     3600,
			},
			checkFn: func(t *testing.T, rr dns.RR) {
				ns, ok := rr.(*dns.NS)
				if !ok {
					t.Fatal("NS 레코드가 아님")
				}
				if ns.Ns != "ns1.example.com." {
					t.Errorf("예상 NS: ns1.example.com., 실제: %s", ns.Ns)
				}
			},
		},
		{
			name: "SOA 레코드",
			record: &model.Record{
				Name:    "example.com.",
				Type:    "SOA",
				Content: "ns1.example.com. admin.example.com. 1 3600 900 86400 300",
				TTL:     3600,
			},
			checkFn: func(t *testing.T, rr dns.RR) {
				soa, ok := rr.(*dns.SOA)
				if !ok {
					t.Fatal("SOA 레코드가 아님")
				}
				if soa.Ns != "ns1.example.com." {
					t.Errorf("예상 NS: ns1.example.com., 실제: %s", soa.Ns)
				}
				if soa.Mbox != "admin.example.com." {
					t.Errorf("예상 Mbox: admin.example.com., 실제: %s", soa.Mbox)
				}
				if soa.Serial != 1 {
					t.Errorf("예상 Serial: 1, 실제: %d", soa.Serial)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := handler.recordToRR(tt.record)
			if rr == nil {
				t.Fatal("RR 변환 실패")
			}
			tt.checkFn(t, rr)
		})
	}
}

// TestExtractDomain은 도메인 추출을 테스트합니다
func TestExtractDomain(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone 생성
	zones := []*model.Zone{
		{Name: "example.com.", SOAMname: "ns1.example.com.", SOARname: "admin.example.com.", Enabled: true},
		{Name: "sub.example.com.", SOAMname: "ns1.sub.example.com.", SOARname: "admin.sub.example.com.", Enabled: true},
	}

	for _, zone := range zones {
		_, err := handler.zoneStorage.CreateZone(zone)
		if err != nil {
			t.Fatalf("Zone 생성 실패: %v", err)
		}
	}

	tests := []struct {
		fqdn     string
		expected string
	}{
		{"www.example.com.", "example.com."},
		{"api.example.com.", "example.com."},
		{"test.sub.example.com.", "sub.example.com."},
		{"sub.example.com.", "sub.example.com."},
		{"example.com.", "example.com."},
		{"nonexistent.test.com.", "test.com."}, // Zone이 없으면 루트 도메인
		{"single.", "single."},
	}

	for _, tt := range tests {
		t.Run(tt.fqdn, func(t *testing.T) {
			result := handler.extractDomain(tt.fqdn)
			if result != tt.expected {
				t.Errorf("예상: %s, 실제: %s", tt.expected, result)
			}
		})
	}
}

// TestHandlePrefetch는 Prefetch 기능을 테스트합니다
func TestHandlePrefetch(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone 생성
	zone := &model.Zone{
		Name:       "example.com.",
		SOAMname:   "ns1.example.com.",
		SOARname:   "admin.example.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}

	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone 생성 실패: %v", err)
	}

	// Record 생성
	record := &model.Record{
		ZoneID:  zoneID,
		Name:    "test.example.com.",
		Type:    "A",
		Content: "192.0.2.50",
		TTL:     300,
		Enabled: true,
	}

	_, err = handler.recordStorage.CreateRecord(record)
	if err != nil {
		t.Fatalf("Record 생성 실패: %v", err)
	}

	// Prefetch 실행
	handler.handlePrefetch("test.example.com.", "A")

	// 잠시 대기 (비동기 처리)
	time.Sleep(100 * time.Millisecond)

	// 캐시에 저장되었는지 확인
	entry, ok := handler.cache.Get("test.example.com.", "A")
	if !ok {
		t.Fatal("Prefetch 후 캐시에 저장되지 않음")
	}

	if entry.IsNegative {
		t.Error("Negative 캐시로 잘못 저장됨")
	}

	if len(entry.RRs) != 1 {
		t.Errorf("예상 RR 수: 1, 실제: %d", len(entry.RRs))
	}
}

// TestIntegration은 전체 흐름을 테스트합니다
func TestIntegration(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// 1. Zone 생성
	zone := &model.Zone{
		Name:       "example.com.",
		SOAMname:   "ns1.example.com.",
		SOARname:   "admin.example.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}

	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone 생성 실패: %v", err)
	}

	// 2. 다양한 Record 생성
	records := []*model.Record{
		{ZoneID: zoneID, Name: "www.example.com.", Type: "A", Content: "192.0.2.10", TTL: 300, Enabled: true},
		{ZoneID: zoneID, Name: "www.example.com.", Type: "A", Content: "192.0.2.11", TTL: 300, Enabled: true},
		{ZoneID: zoneID, Name: "mail.example.com.", Type: "A", Content: "192.0.2.20", TTL: 300, Enabled: true},
		{ZoneID: zoneID, Name: "example.com.", Type: "MX", Content: "mail.example.com.", TTL: 300, Priority: 10, Enabled: true},
		{ZoneID: zoneID, Name: "example.com.", Type: "TXT", Content: "v=spf1 mx ~all", TTL: 300, Enabled: true},
	}

	for _, record := range records {
		_, err := handler.recordStorage.CreateRecord(record)
		if err != nil {
			t.Fatalf("Record 생성 실패: %v", err)
		}
	}

	// 3. A 레코드 쿼리 (첫 번째 - L2 캐시)
	req1 := new(dns.Msg)
	req1.SetQuestion("www.example.com.", dns.TypeA)

	w1 := newMockWriter("192.0.2.100")
	handler.ServeDNS(w1, req1)

	if w1.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("A 레코드 쿼리 실패: Rcode=%d", w1.msg.Rcode)
	}

	if len(w1.msg.Answer) != 2 {
		t.Errorf("A 레코드 수 예상: 2, 실제: %d", len(w1.msg.Answer))
	}

	// 4. 동일 쿼리 재실행 (L1 캐시 히트)
	req2 := new(dns.Msg)
	req2.SetQuestion("www.example.com.", dns.TypeA)

	w2 := newMockWriter("192.0.2.100")
	handler.ServeDNS(w2, req2)

	stats := handler.cache.GetStats()
	if stats.Hits < 1 {
		t.Errorf("캐시 히트 예상: >= 1, 실제: %d", stats.Hits)
	}

	// 5. MX 레코드 쿼리
	req3 := new(dns.Msg)
	req3.SetQuestion("example.com.", dns.TypeMX)

	w3 := newMockWriter("192.0.2.100")
	handler.ServeDNS(w3, req3)

	if w3.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("MX 레코드 쿼리 실패: Rcode=%d", w3.msg.Rcode)
	}

	if len(w3.msg.Answer) != 1 {
		t.Errorf("MX 레코드 수 예상: 1, 실제: %d", len(w3.msg.Answer))
	}

	mx, ok := w3.msg.Answer[0].(*dns.MX)
	if !ok {
		t.Fatal("MX 레코드가 아님")
	}

	if mx.Preference != 10 {
		t.Errorf("MX Preference 예상: 10, 실제: %d", mx.Preference)
	}

	// 6. TXT 레코드 쿼리
	req4 := new(dns.Msg)
	req4.SetQuestion("example.com.", dns.TypeTXT)

	w4 := newMockWriter("192.0.2.100")
	handler.ServeDNS(w4, req4)

	if w4.msg.Rcode != dns.RcodeSuccess {
		t.Errorf("TXT 레코드 쿼리 실패: Rcode=%d", w4.msg.Rcode)
	}

	if len(w4.msg.Answer) != 1 {
		t.Errorf("TXT 레코드 수 예상: 1, 실제: %d", len(w4.msg.Answer))
	}

	// 7. 존재하지 않는 레코드 쿼리 (NXDOMAIN은 아니고 NOERROR with empty answer)
	req5 := new(dns.Msg)
	req5.SetQuestion("nonexistent.example.com.", dns.TypeA)

	w5 := newMockWriter("192.0.2.100")
	handler.ServeDNS(w5, req5)

	// Zone은 존재하지만 레코드가 없으므로 업스트림 포워딩됨
	// 업스트림 서버가 없으면 NXDOMAIN
	if w5.msg.Rcode != dns.RcodeNameError {
		t.Logf("존재하지 않는 레코드 Rcode: %d (예상: %d NXDOMAIN)", w5.msg.Rcode, dns.RcodeNameError)
	}

	// 8. 캐시 통계 확인
	finalStats := handler.cache.GetStats()
	t.Logf("최종 캐시 통계: Hits=%d, Misses=%d, Size=%d, Evictions=%d",
		finalStats.Hits, finalStats.Misses, finalStats.Size, finalStats.Evictions)

	if finalStats.Size < 3 {
		t.Errorf("캐시 크기 예상: >= 3, 실제: %d", finalStats.Size)
	}
}

// TestServeDNS_NoQuestion은 쿼리가 없는 경우를 테스트합니다
func TestServeDNS_NoQuestion(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// 빈 쿼리
	req := new(dns.Msg)

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	if w.msg.Rcode != dns.RcodeFormatError {
		t.Errorf("예상 Rcode: %d (FormatError), 실제: %d", dns.RcodeFormatError, w.msg.Rcode)
	}
}

// TestServeDNS_ANYQueryBlocked는 ANY 쿼리 차단을 테스트합니다 (RFC 8482)
func TestServeDNS_ANYQueryBlocked(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// ANY 쿼리
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeANY)

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	// NOTIMP (Not Implemented) 응답 확인
	if w.msg.Rcode != dns.RcodeNotImplemented {
		t.Errorf("예상 Rcode: %d (NotImplemented), 실제: %d", dns.RcodeNotImplemented, w.msg.Rcode)
	}

	// 응답에 Answer가 없어야 함
	if len(w.msg.Answer) != 0 {
		t.Errorf("ANY 쿼리 차단 시 Answer가 비어야 함, 실제: %d개", len(w.msg.Answer))
	}
}

// TestServeDNS_AuthoritativeFlag는 AA 플래그 설정을 테스트합니다 (RFC 1035)
func TestServeDNS_AuthoritativeFlag(t *testing.T) {
	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone과 Record 추가
	zoneID, _ := db.Writer.Exec(
		"INSERT INTO zones (name, soa_mname, soa_rname, soa_serial) VALUES (?, ?, ?, ?)",
		"example.com.", "ns1.example.com.", "admin.example.com.", 2026013101,
	)
	zid, _ := zoneID.LastInsertId()

	_, err := db.Writer.Exec(
		"INSERT INTO records (zone_id, name, type, content, ttl) VALUES (?, ?, ?, ?, ?)",
		zid, "www.example.com.", "A", "192.0.2.1", 300,
	)
	if err != nil {
		t.Fatalf("record insert failed: %v", err)
	}

	// Upstream 서버 추가 (포워딩 테스트용)
	_, err = db.Writer.Exec(
		"INSERT INTO upstream_servers (name, address, protocol, priority, enabled) VALUES (?, ?, ?, ?, ?)",
		"Test DNS", "8.8.8.8:53", "udp", 1, 1,
	)
	if err != nil {
		t.Fatalf("upstream insert failed: %v", err)
	}

	tests := []struct {
		name        string
		domain      string
		expectAA    bool
		description string
	}{
		{
			name:        "Zone 응답 - AA=true",
			domain:      "www.example.com.",
			expectAA:    true,
			description: "권한 서버로 응답하는 경우 AA 플래그 설정",
		},
		{
			name:        "캐시 히트 - AA=false",
			domain:      "www.example.com.",
			expectAA:    false,
			description: "캐시된 응답은 non-authoritative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := new(dns.Msg)
			req.SetQuestion(tt.domain, dns.TypeA)

			w := newMockWriter("192.0.2.100")
			handler.ServeDNS(w, req)

			if w.msg.Authoritative != tt.expectAA {
				t.Errorf("AA 플래그 예상: %v, 실제: %v (%s)", tt.expectAA, w.msg.Authoritative, tt.description)
			}
		})
	}
}

// TestServeDNS_RecursionDesired는 RD 플래그 처리를 테스트합니다 (RFC 1035)
func TestServeDNS_RecursionDesired(t *testing.T) {
	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	// Upstream 서버 추가
	_, err := db.Writer.Exec(
		"INSERT INTO upstream_servers (name, address, protocol, priority, enabled) VALUES (?, ?, ?, ?, ?)",
		"Test DNS", "8.8.8.8:53", "udp", 1, 1,
	)
	if err != nil {
		t.Fatalf("upstream insert failed: %v", err)
	}

	tests := []struct {
		name        string
		domain      string
		rd          bool
		expectRcode int
		description string
	}{
		{
			name:        "RD=0 (+norecurse) - REFUSED",
			domain:      "google.com.",
			rd:          false,
			expectRcode: dns.RcodeRefused,
			description: "재귀 요청 안 하면 업스트림 포워딩 안 함",
		},
		{
			name:        "RD=1 (기본) - 정상 처리",
			domain:      "google.com.",
			rd:          true,
			expectRcode: dns.RcodeSuccess,
			description: "재귀 요청하면 업스트림 포워딩",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := new(dns.Msg)
			req.SetQuestion(tt.domain, dns.TypeA)
			req.RecursionDesired = tt.rd

			w := newMockWriter("192.0.2.100")
			handler.ServeDNS(w, req)

			// RA (Recursion Available) 플래그는 항상 true여야 함
			if !w.msg.RecursionAvailable {
				t.Errorf("RA 플래그가 false (항상 true여야 함)")
			}

			// RD=0인 경우에만 Rcode 체크 (RD=1은 실제 upstream 필요)
			if !tt.rd && w.msg.Rcode != tt.expectRcode {
				t.Errorf("Rcode 예상: %d, 실제: %d (%s)", tt.expectRcode, w.msg.Rcode, tt.description)
			}
		})
	}
}

// TestServeDNS_NSID는 EDNS0 NSID 지원을 테스트합니다 (RFC 5001)
func TestServeDNS_NSID(t *testing.T) {
	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone과 Record 추가
	zoneID, _ := db.Writer.Exec(
		"INSERT INTO zones (name, soa_mname, soa_rname, soa_serial) VALUES (?, ?, ?, ?)",
		"example.com.", "ns1.example.com.", "admin.example.com.", 2026013101,
	)
	zid, _ := zoneID.LastInsertId()

	_, err := db.Writer.Exec(
		"INSERT INTO records (zone_id, name, type, content, ttl) VALUES (?, ?, ?, ?, ?)",
		zid, "www.example.com.", "A", "192.0.2.1", 300,
	)
	if err != nil {
		t.Fatalf("record insert failed: %v", err)
	}

	tests := []struct {
		name        string
		requestNSID bool
		expectNSID  bool
	}{
		{
			name:        "NSID 요청 (+nsid)",
			requestNSID: true,
			expectNSID:  true,
		},
		{
			name:        "NSID 미요청",
			requestNSID: false,
			expectNSID:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := new(dns.Msg)
			req.SetQuestion("www.example.com.", dns.TypeA)

			// EDNS0 설정
			req.SetEdns0(4096, false)

			// NSID 옵션 추가
			if tt.requestNSID {
				opt := req.IsEdns0()
				opt.Option = append(opt.Option, &dns.EDNS0_NSID{Code: dns.EDNS0NSID})
			}

			w := newMockWriter("192.0.2.100")
			handler.ServeDNS(w, req)

			// NSID 응답 확인
			opt := w.msg.IsEdns0()
			if opt == nil {
				t.Fatal("EDNS0 OPT 레코드가 없음")
			}

			nsidFound := false
			// RFC 5001: NSID는 hex 인코딩된 바이트 시퀀스로 전달됨
			expectedNSID := fmt.Sprintf("%x", "test-server")
			for _, option := range opt.Option {
				if nsid, ok := option.(*dns.EDNS0_NSID); ok {
					nsidFound = true
					if nsid.Nsid != expectedNSID {
						t.Errorf("NSID 값 예상: %s, 실제: %s", expectedNSID, nsid.Nsid)
					}
				}
			}

			if tt.expectNSID && !nsidFound {
				t.Error("NSID 요청했지만 응답에 없음")
			}
			if !tt.expectNSID && nsidFound {
				t.Error("NSID 요청 안 했는데 응답에 포함됨")
			}
		})
	}
}

// TestServeDNS_CHAOS는 CHAOS 클래스 쿼리를 테스트합니다
func TestServeDNS_CHAOS(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	tests := []struct {
		name         string
		domain       string
		expectAnswer bool
		expectText   string
		description  string
	}{
		{
			name:         "version.bind TXT",
			domain:       "version.bind.",
			expectAnswer: true,
			expectText:   "DNS-Go Test v1.0",
			description:  "CHAOS version.bind 쿼리",
		},
		{
			name:         "version.server TXT",
			domain:       "version.server.",
			expectAnswer: true,
			expectText:   "DNS-Go Test v1.0",
			description:  "CHAOS version.server 쿼리",
		},
		{
			name:         "hostname.bind TXT",
			domain:       "hostname.bind.",
			expectAnswer: true,
			expectText:   "test-server",
			description:  "CHAOS hostname.bind 쿼리",
		},
		{
			name:         "id.server TXT",
			domain:       "id.server.",
			expectAnswer: true,
			expectText:   "test-server",
			description:  "CHAOS id.server 쿼리",
		},
		{
			name:         "unsupported CHAOS",
			domain:       "unknown.bind.",
			expectAnswer: false,
			description:  "지원하지 않는 CHAOS 쿼리 - REFUSED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := new(dns.Msg)
			req.SetQuestion(tt.domain, dns.TypeTXT)
			req.Question[0].Qclass = dns.ClassCHAOS

			w := newMockWriter("192.0.2.100")
			handler.ServeDNS(w, req)

			if tt.expectAnswer {
				if len(w.msg.Answer) == 0 {
					t.Errorf("응답 예상했지만 Answer가 비어있음 (%s)", tt.description)
					return
				}

				txt, ok := w.msg.Answer[0].(*dns.TXT)
				if !ok {
					t.Errorf("TXT 레코드가 아님")
					return
				}

				if len(txt.Txt) == 0 || txt.Txt[0] != tt.expectText {
					t.Errorf("TXT 예상: %s, 실제: %v", tt.expectText, txt.Txt)
				}

				if txt.Hdr.Class != dns.ClassCHAOS {
					t.Errorf("Class 예상: CHAOS, 실제: %s", dns.ClassToString[txt.Hdr.Class])
				}
			} else {
				if w.msg.Rcode != dns.RcodeRefused {
					t.Errorf("Rcode 예상: REFUSED, 실제: %s", dns.RcodeToString[w.msg.Rcode])
				}
			}
		})
	}
}

// TestServeDNS_EDNS0Support는 EDNS0 지원을 테스트합니다
func TestServeDNS_EDNS0Support(t *testing.T) {
	handler, db, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone과 Record 추가
	zoneID, _ := db.Writer.Exec(
		"INSERT INTO zones (name, soa_mname, soa_rname, soa_serial) VALUES (?, ?, ?, ?)",
		"example.com.", "ns1.example.com.", "admin.example.com.", 2026013101,
	)
	zid, _ := zoneID.LastInsertId()

	_, err := db.Writer.Exec(
		"INSERT INTO records (zone_id, name, type, content, ttl) VALUES (?, ?, ?, ?, ?)",
		zid, "www.example.com.", "A", "192.0.2.1", 300,
	)
	if err != nil {
		t.Fatalf("record insert failed: %v", err)
	}

	tests := []struct {
		name           string
		requestEDNS    bool
		requestBufSize uint16
		expectEDNS     bool
		expectBufSize  uint16
	}{
		{
			name:           "EDNS0 요청 (4096 버퍼)",
			requestEDNS:    true,
			requestBufSize: 4096,
			expectEDNS:     true,
			expectBufSize:  1232, // Cloudflare 방식: 항상 1232
		},
		{
			name:           "EDNS0 요청 (512 버퍼)",
			requestEDNS:    true,
			requestBufSize: 512,
			expectEDNS:     true,
			expectBufSize:  1232, // Cloudflare 방식: 항상 1232
		},
		{
			name:        "EDNS0 없는 요청",
			requestEDNS: false,
			expectEDNS:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := new(dns.Msg)
			req.SetQuestion("www.example.com.", dns.TypeA)

			// EDNS0 설정
			if tt.requestEDNS {
				req.SetEdns0(tt.requestBufSize, false)
			}

			w := newMockWriter("192.0.2.100")
			handler.ServeDNS(w, req)

			// EDNS0 응답 확인
			opt := w.msg.IsEdns0()
			if tt.expectEDNS {
				if opt == nil {
					t.Errorf("EDNS0 OPT 레코드가 응답에 없음")
				} else if opt.UDPSize() != tt.expectBufSize {
					t.Errorf("UDP 버퍼 크기 예상: %d, 실제: %d", tt.expectBufSize, opt.UDPSize())
				}
			} else {
				if opt != nil {
					t.Errorf("EDNS0 요청하지 않았는데 OPT 레코드가 응답에 포함됨")
				}
			}
		})
	}
}

// TestServeDNS_CaseInsensitive는 RFC 4343 대소문자 무시 조회를 테스트합니다
func TestServeDNS_CaseInsensitive(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone 생성
	zone := &model.Zone{
		Name:       "example.com.",
		SOAMname:   "ns1.example.com.",
		SOARname:   "admin.example.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   900,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
	}

	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone 생성 실패: %v", err)
	}

	// Record 생성 (소문자)
	record := &model.Record{
		ZoneID:  zoneID,
		Name:    "svc-db01.example.com.",
		Type:    "A",
		Content: "10.96.50.150",
		TTL:     300,
		Enabled: true,
	}

	_, err = handler.recordStorage.CreateRecord(record)
	if err != nil {
		t.Fatalf("Record 생성 실패: %v", err)
	}

	tests := []struct {
		name   string
		domain string
	}{
		{"소문자", "svc-db01.example.com."},
		{"대문자", "SVC-DB01.EXAMPLE.COM."},
		{"Mixed case", "SVC-db01.Example.COM."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// L1 캐시 비우기
			handler.cache.Clear()

			req := new(dns.Msg)
			req.SetQuestion(tt.domain, dns.TypeA)

			w := newMockWriter("192.0.2.100")
			handler.ServeDNS(w, req)

			if !w.written {
				t.Fatal("응답이 작성되지 않음")
			}

			if w.msg.Rcode != dns.RcodeSuccess {
				t.Errorf("도메인 %q: 예상 NOERROR, 실제 %s", tt.domain, dns.RcodeToString[w.msg.Rcode])
			}

			if len(w.msg.Answer) != 1 {
				t.Fatalf("도메인 %q: 예상 Answer 수 1, 실제 %d", tt.domain, len(w.msg.Answer))
			}

			if a, ok := w.msg.Answer[0].(*dns.A); ok {
				if a.A.String() != "10.96.50.150" {
					t.Errorf("도메인 %q: 예상 IP 10.96.50.150, 실제 %s", tt.domain, a.A.String())
				}
			} else {
				t.Errorf("도메인 %q: A 레코드가 아님", tt.domain)
			}
		})
	}
}

// TestServeDNS_NegativeCache은 Negative 캐시를 테스트합니다
func TestServeDNS_NegativeCache(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Negative 캐시 미리 저장
	handler.cache.Set("nonexistent.example.com.", "A", nil, 300, true)

	// 쿼리 실행
	req := new(dns.Msg)
	req.SetQuestion("nonexistent.example.com.", dns.TypeA)

	w := newMockWriter("192.0.2.100")
	handler.ServeDNS(w, req)

	// 검증
	if w.msg.Rcode != dns.RcodeNameError {
		t.Errorf("예상 Rcode: %d (NXDOMAIN), 실제: %d", dns.RcodeNameError, w.msg.Rcode)
	}

	// 캐시 히트 확인
	stats := handler.cache.GetStats()
	if stats.Hits != 1 {
		t.Errorf("예상 캐시 히트: 1, 실제: %d", stats.Hits)
	}
}
