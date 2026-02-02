package dns

import (
	"dns-go/model"
	"testing"

	"github.com/miekg/dns"
)

// TestRFC1035_NoRecordType은 RFC 1035 표준 준수를 테스트합니다.
// RFC 1035: 도메인은 존재하지만 특정 타입의 레코드가 없을 때는 NOERROR를 반환해야 합니다.
// NXDOMAIN은 도메인 자체가 존재하지 않을 때만 사용해야 합니다.
func TestRFC1035_NoRecordType(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone 생성
	zone := &model.Zone{
		Name:          "example.com.",
		Enabled:       true,
		AllowFallback: false, // Fallback 비활성화하여 로컬에서만 처리
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone 생성 실패: %v", err)
	}

	// A 레코드만 생성 (AAAA, MX, TXT 등은 없음)
	record := &model.Record{
		ZoneID:  zoneID,
		Name:    "test.example.com.",
		Type:    "A",
		Content: "192.0.2.1",
		TTL:     300,
		Enabled: true,
	}
	_, err = handler.recordStorage.CreateRecord(record)
	if err != nil {
		t.Fatalf("Record 생성 실패: %v", err)
	}

	tests := []struct {
		name         string
		qname        string
		qtype        uint16
		expectedCode int
		description  string
	}{
		{
			name:         "A 레코드 조회 (존재함)",
			qname:        "test.example.com.",
			qtype:        dns.TypeA,
			expectedCode: dns.RcodeSuccess,
			description:  "A 레코드가 존재하므로 NOERROR 반환",
		},
		{
			name:         "AAAA 레코드 조회 (존재하지 않음)",
			qname:        "test.example.com.",
			qtype:        dns.TypeAAAA,
			expectedCode: dns.RcodeSuccess, // NOERROR여야 함 (NXDOMAIN 아님!)
			description:  "RFC 1035: 도메인은 존재하지만 AAAA 레코드가 없으므로 NOERROR 반환",
		},
		{
			name:         "MX 레코드 조회 (존재하지 않음)",
			qname:        "test.example.com.",
			qtype:        dns.TypeMX,
			expectedCode: dns.RcodeSuccess, // NOERROR여야 함
			description:  "RFC 1035: 도메인은 존재하지만 MX 레코드가 없으므로 NOERROR 반환",
		},
		{
			name:         "TXT 레코드 조회 (존재하지 않음)",
			qname:        "test.example.com.",
			qtype:        dns.TypeTXT,
			expectedCode: dns.RcodeSuccess, // NOERROR여야 함
			description:  "RFC 1035: 도메인은 존재하지만 TXT 레코드가 없으므로 NOERROR 반환",
		},
		{
			name:         "CNAME 레코드 조회 (존재하지 않음)",
			qname:        "test.example.com.",
			qtype:        dns.TypeCNAME,
			expectedCode: dns.RcodeSuccess, // NOERROR여야 함
			description:  "RFC 1035: 도메인은 존재하지만 CNAME 레코드가 없으므로 NOERROR 반환",
		},
		{
			name:         "존재하지 않는 도메인 조회",
			qname:        "nonexistent.example.com.",
			qtype:        dns.TypeA,
			expectedCode: dns.RcodeNameError, // NXDOMAIN이어야 함
			description:  "RFC 1035: 도메인 자체가 존재하지 않으므로 NXDOMAIN 반환",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// DNS 요청 생성
			req := new(dns.Msg)
			req.SetQuestion(tt.qname, tt.qtype)
			req.RecursionDesired = true

			// 응답 캡처를 위한 mock writer
			writer := &mockResponseWriter{}

			// 핸들러 실행
			handler.ServeDNS(writer, req)

			// 응답 검증
			if writer.msg == nil {
				t.Fatal("응답이 없습니다")
			}

			if writer.msg.Rcode != tt.expectedCode {
				t.Errorf("%s\n예상 Rcode: %s (%d)\n실제 Rcode: %s (%d)\n설명: %s",
					tt.name,
					dns.RcodeToString[tt.expectedCode],
					tt.expectedCode,
					dns.RcodeToString[writer.msg.Rcode],
					writer.msg.Rcode,
					tt.description,
				)
			}

			// NOERROR인 경우 SOA 레코드가 Authority 섹션에 있어야 함
			if tt.expectedCode == dns.RcodeSuccess && len(writer.msg.Answer) == 0 {
				if len(writer.msg.Ns) == 0 {
					t.Errorf("NOERROR (빈 응답)인 경우 Authority 섹션에 SOA 레코드가 있어야 합니다")
				}
			}

			// NXDOMAIN인 경우에도 SOA 레코드가 있어야 함
			if tt.expectedCode == dns.RcodeNameError {
				if len(writer.msg.Ns) == 0 {
					t.Errorf("NXDOMAIN 응답에는 Authority 섹션에 SOA 레코드가 있어야 합니다 (RFC 2308)")
				}
			}
		})
	}
}

// TestRFC1035_DomainExistenceVsRecordType은 도메인 존재 여부와 레코드 타입 존재 여부를 구분하는 테스트입니다.
func TestRFC1035_DomainExistenceVsRecordType(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Zone 생성
	zone := &model.Zone{
		Name:          "test.org.",
		Enabled:       true,
		AllowFallback: false,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone 생성 실패: %v", err)
	}

	// www.test.org에 A 레코드 생성
	record := &model.Record{
		ZoneID:  zoneID,
		Name:    "www.test.org.",
		Type:    "A",
		Content: "203.0.113.1",
		TTL:     600,
		Enabled: true,
	}
	_, err = handler.recordStorage.CreateRecord(record)
	if err != nil {
		t.Fatalf("Record 생성 실패: %v", err)
	}

	scenarios := []struct {
		scenario     string
		qname        string
		qtype        uint16
		expectedCode int
		reason       string
	}{
		{
			scenario:     "시나리오 1: 도메인 존재, 레코드 타입 존재",
			qname:        "www.test.org.",
			qtype:        dns.TypeA,
			expectedCode: dns.RcodeSuccess,
			reason:       "A 레코드가 존재하므로 NOERROR + 응답 반환",
		},
		{
			scenario:     "시나리오 2: 도메인 존재, 레코드 타입 없음",
			qname:        "www.test.org.",
			qtype:        dns.TypeAAAA,
			expectedCode: dns.RcodeSuccess, // 이것이 핵심!
			reason:       "도메인은 존재하지만 AAAA가 없으므로 NOERROR (빈 응답)",
		},
		{
			scenario:     "시나리오 3: 도메인 자체가 존재하지 않음",
			qname:        "notfound.test.org.",
			qtype:        dns.TypeA,
			expectedCode: dns.RcodeNameError,
			reason:       "도메인이 존재하지 않으므로 NXDOMAIN",
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.scenario, func(t *testing.T) {
			req := new(dns.Msg)
			req.SetQuestion(sc.qname, sc.qtype)
			req.RecursionDesired = true

			writer := &mockResponseWriter{}
			handler.ServeDNS(writer, req)

			if writer.msg.Rcode != sc.expectedCode {
				t.Errorf("\n시나리오: %s\n쿼리: %s %s\n예상: %s\n실제: %s\n이유: %s",
					sc.scenario,
					sc.qname,
					dns.TypeToString[sc.qtype],
					dns.RcodeToString[sc.expectedCode],
					dns.RcodeToString[writer.msg.Rcode],
					sc.reason,
				)
			}
		})
	}
}

// TestRFC1035_NginxUpstreamScenario는 실제 Nginx upstream 조회 시나리오를 재현합니다.
func TestRFC1035_NginxUpstreamScenario(t *testing.T) {
	handler, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// 실제 상황: yangs.sh zone에 lb.gs.kube.yangs.sh A 레코드만 있음
	zone := &model.Zone{
		Name:          "yangs.sh.",
		Enabled:       true,
		AllowFallback: false,
	}
	zoneID, err := handler.zoneStorage.CreateZone(zone)
	if err != nil {
		t.Fatalf("Zone 생성 실패: %v", err)
	}

	// A 레코드 추가
	record := &model.Record{
		ZoneID:  zoneID,
		Name:    "lb.gs.kube.yangs.sh.",
		Type:    "A",
		Content: "10.96.50.201",
		TTL:     300,
		Enabled: true,
	}
	_, err = handler.recordStorage.CreateRecord(record)
	if err != nil {
		t.Fatalf("Record 생성 실패: %v", err)
	}

	t.Run("Nginx IPv4 조회 (성공)", func(t *testing.T) {
		req := new(dns.Msg)
		req.SetQuestion("lb.gs.kube.yangs.sh.", dns.TypeA)
		req.RecursionDesired = true

		writer := &mockResponseWriter{}
		handler.ServeDNS(writer, req)

		if writer.msg.Rcode != dns.RcodeSuccess {
			t.Errorf("A 레코드 조회 실패: %s", dns.RcodeToString[writer.msg.Rcode])
		}

		if len(writer.msg.Answer) == 0 {
			t.Error("A 레코드 응답이 없습니다")
		}
	})

	t.Run("Nginx IPv6 조회 (NOERROR 반환해야 함)", func(t *testing.T) {
		req := new(dns.Msg)
		req.SetQuestion("lb.gs.kube.yangs.sh.", dns.TypeAAAA)
		req.RecursionDesired = true

		writer := &mockResponseWriter{}
		handler.ServeDNS(writer, req)

		// 핵심 테스트: NXDOMAIN이 아니라 NOERROR여야 함!
		if writer.msg.Rcode != dns.RcodeSuccess {
			t.Errorf("AAAA 레코드 없을 때 NOERROR 반환해야 하지만 %s 반환됨\n"+
				"이 버그로 인해 Nginx가 'host not found' 에러 발생!",
				dns.RcodeToString[writer.msg.Rcode])
		}

		// 빈 응답이어야 함 (ANSWER 섹션 비어있음)
		if len(writer.msg.Answer) != 0 {
			t.Error("AAAA 레코드가 없으므로 ANSWER 섹션이 비어있어야 합니다")
		}

		// Authority 섹션에 SOA가 있어야 함
		if len(writer.msg.Ns) == 0 {
			t.Error("빈 응답인 경우 Authority 섹션에 SOA 레코드가 있어야 합니다")
		}
	})
}
