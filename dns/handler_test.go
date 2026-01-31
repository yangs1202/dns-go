package dns

import (
	"dns-go/model"
	"dns-go/storage"
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
	os.Remove(dbPath)

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
	handler, err := NewHandler(zoneStorage, recordStorage, resolver, db)
	if err != nil {
		t.Fatalf("핸들러 생성 실패: %v", err)
	}

	// Cleanup 함수
	cleanup := func() {
		db.Close()
		os.Remove(dbPath)
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
		name     string
		record   *model.Record
		checkFn  func(t *testing.T, rr dns.RR)
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
