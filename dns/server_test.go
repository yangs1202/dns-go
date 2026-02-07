package dns

import (
	"dns-go/config"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockHandler는 테스트용 DNS 핸들러입니다
type MockHandler struct {
	response *dns.Msg
}

func (h *MockHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	var m *dns.Msg

	if h.response != nil {
		// 사용자 정의 응답을 쿼리에 맞게 조정
		m = new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		m.Answer = h.response.Answer
	} else {
		// 기본 응답: A 레코드
		m = new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true

		if len(r.Question) > 0 {
			q := r.Question[0]
			if q.Qtype == dns.TypeA {
				rr, _ := dns.NewRR(q.Name + " 300 IN A 192.0.2.1")
				m.Answer = []dns.RR{rr}
			}
		}
	}

	_ = w.WriteMsg(m)
}

func TestNewServer(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15353,
		UDP:    true,
		TCP:    true,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	assert.NotNil(t, server)
	assert.NotNil(t, server.udp)
	assert.NotNil(t, server.tcp)
	assert.Equal(t, "127.0.0.1:15353", server.udp.Addr)
	assert.Equal(t, "127.0.0.1:15353", server.tcp.Addr)
}

func TestServerStartStop(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15354,
		UDP:    true,
		TCP:    false,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	// 서버 시작
	err := server.Start()
	require.NoError(t, err)

	// 짧은 대기
	time.Sleep(200 * time.Millisecond)

	// 서버 중지
	err = server.Stop()
	assert.NoError(t, err)
}

func TestServerUDP(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15355,
		UDP:    true,
		TCP:    false,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	time.Sleep(200 * time.Millisecond)

	// UDP 쿼리 테스트
	resp, err := Query("127.0.0.1:15355", "example.com", "A")
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Len(t, resp.Answer, 1)
	assert.Equal(t, dns.TypeA, resp.Answer[0].Header().Rrtype)
}

func TestServerTCP(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15356,
		UDP:    false,
		TCP:    true,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	time.Sleep(200 * time.Millisecond)

	// TCP 쿼리 테스트
	c := new(dns.Client)
	c.Net = "tcp"
	c.Timeout = 5 * time.Second

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn("example.com"), dns.TypeA)
	m.RecursionDesired = true

	resp, _, err := c.Exchange(m, "127.0.0.1:15356")
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Len(t, resp.Answer, 1)
	assert.Equal(t, dns.TypeA, resp.Answer[0].Header().Rrtype)
}

func TestServerBothProtocols(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15357,
		UDP:    true,
		TCP:    true,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	time.Sleep(200 * time.Millisecond)

	// UDP 쿼리
	respUDP, err := Query("127.0.0.1:15357", "example.com", "A")
	require.NoError(t, err)
	assert.Len(t, respUDP.Answer, 1)

	// TCP 쿼리
	c := new(dns.Client)
	c.Net = "tcp"
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn("example.com"), dns.TypeA)

	respTCP, _, err := c.Exchange(m, "127.0.0.1:15357")
	require.NoError(t, err)
	assert.Len(t, respTCP.Answer, 1)
}

func TestQueryFunction(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15358,
		UDP:    true,
		TCP:    false,
	}

	// 사용자 정의 응답
	customResp := new(dns.Msg)
	customResp.SetQuestion(dns.Fqdn("test.com"), dns.TypeAAAA)
	rr, _ := dns.NewRR("test.com. 300 IN AAAA 2001:db8::1")
	customResp.Answer = []dns.RR{rr}

	handler := &MockHandler{response: customResp}
	server := NewServer(cfg, handler)

	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	time.Sleep(200 * time.Millisecond)

	// Query 함수 테스트
	resp, err := Query("127.0.0.1:15358", "test.com", "AAAA")
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Len(t, resp.Answer, 1)
	assert.Equal(t, dns.TypeAAAA, resp.Answer[0].Header().Rrtype)
}

func TestExtractClientIP(t *testing.T) {
	tests := []struct {
		name     string
		setupMsg func() *dns.Msg
		want     string
	}{
		{
			name: "EDNS Client Subnet 있음",
			setupMsg: func() *dns.Msg {
				m := new(dns.Msg)
				m.SetQuestion(dns.Fqdn("example.com"), dns.TypeA)

				opt := new(dns.OPT)
				opt.Hdr.Name = "."
				opt.Hdr.Rrtype = dns.TypeOPT

				subnet := new(dns.EDNS0_SUBNET)
				subnet.Code = dns.EDNS0SUBNET
				subnet.Family = 1 // IPv4
				subnet.SourceNetmask = 24
				subnet.SourceScope = 0
				subnet.Address = []byte{203, 0, 113, 1}

				opt.Option = append(opt.Option, subnet)
				m.Extra = append(m.Extra, opt)

				return m
			},
			want: "203.0.113.1",
		},
		{
			name: "EDNS Client Subnet IPv6",
			setupMsg: func() *dns.Msg {
				m := new(dns.Msg)
				m.SetQuestion(dns.Fqdn("example.com"), dns.TypeA)

				opt := new(dns.OPT)
				opt.Hdr.Name = "."
				opt.Hdr.Rrtype = dns.TypeOPT

				subnet := new(dns.EDNS0_SUBNET)
				subnet.Code = dns.EDNS0SUBNET
				subnet.Family = 2 // IPv6
				subnet.SourceNetmask = 64
				subnet.SourceScope = 0
				subnet.Address = []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}

				opt.Option = append(opt.Option, subnet)
				m.Extra = append(m.Extra, opt)

				return m
			},
			want: "2001:db8::1",
		},
		{
			name: "EDNS 없음",
			setupMsg: func() *dns.Msg {
				m := new(dns.Msg)
				m.SetQuestion(dns.Fqdn("example.com"), dns.TypeA)
				return m
			},
			want: "",
		},
		{
			name: "EDNS 있지만 Client Subnet 없음",
			setupMsg: func() *dns.Msg {
				m := new(dns.Msg)
				m.SetQuestion(dns.Fqdn("example.com"), dns.TypeA)

				opt := new(dns.OPT)
				opt.Hdr.Name = "."
				opt.Hdr.Rrtype = dns.TypeOPT
				m.Extra = append(m.Extra, opt)

				return m
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := tt.setupMsg()
			ip := ExtractClientIP(m)

			if tt.want == "" {
				assert.Nil(t, ip)
			} else {
				require.NotNil(t, ip)
				assert.Equal(t, tt.want, ip.String())
			}
		})
	}
}

func TestGetAddr(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "0.0.0.0",
		Port:   53,
		UDP:    true,
		TCP:    false,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	assert.Equal(t, "0.0.0.0:53", server.GetAddr())
}

func TestServerStopWithoutStart(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15359,
		UDP:    true,
		TCP:    false,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	// 시작하지 않고 중지 시도
	err := server.Stop()
	// 에러가 발생할 수 있지만 패닉은 없어야 함
	_ = err
}

func TestMultipleQueries(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15360,
		UDP:    true,
		TCP:    false,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	time.Sleep(200 * time.Millisecond)

	// 여러 쿼리 동시 전송
	for i := 0; i < 10; i++ {
		resp, err := Query("127.0.0.1:15360", "example.com", "A")
		require.NoError(t, err)
		assert.Len(t, resp.Answer, 1)
	}
}

func TestDifferentQueryTypes(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15361,
		UDP:    true,
		TCP:    false,
	}

	handler := &MockHandler{}
	server := NewServer(cfg, handler)

	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	time.Sleep(200 * time.Millisecond)

	tests := []string{"A", "AAAA", "MX", "TXT", "NS"}

	for _, qtype := range tests {
		resp, err := Query("127.0.0.1:15361", "example.com", qtype)
		require.NoError(t, err, "쿼리 타입: %s", qtype)
		assert.NotNil(t, resp)
	}
}

func TestServerRecursionDesired(t *testing.T) {
	cfg := &config.DNSConfig{
		Listen: "127.0.0.1",
		Port:   15362,
		UDP:    true,
		TCP:    false,
	}

	rdCh := make(chan bool, 1)
	handler := &MockHandler{}
	handler.response = nil

	// RD 플래그 확인용 핸들러
	customHandler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		select {
		case rdCh <- r.RecursionDesired:
		default:
		}
		m := new(dns.Msg)
		m.SetReply(r)
		_ = w.WriteMsg(m)
	})

	server := NewServer(cfg, customHandler)

	err := server.Start()
	require.NoError(t, err)
	defer func() { _ = server.Stop() }()

	time.Sleep(200 * time.Millisecond)

	_, err = Query("127.0.0.1:15362", "example.com", "A")
	require.NoError(t, err)

	select {
	case rdFlag := <-rdCh:
		assert.True(t, rdFlag, "RecursionDesired 플래그가 설정되어야 합니다")
	case <-time.After(1 * time.Second):
		t.Fatal("RD 플래그를 수신하지 못했습니다")
	}
}
