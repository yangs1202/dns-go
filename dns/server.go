package dns

import (
	"context"
	"dns-go/config"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/miekg/dns"
)

// Server는 DNS 서버입니다
type Server struct {
	config  *config.DNSConfig
	handler dns.Handler
	udp     *dns.Server
	tcp     *dns.Server
}

// NewServer는 새로운 DNS 서버를 생성합니다
func NewServer(cfg *config.DNSConfig, handler dns.Handler) *Server {
	addr := fmt.Sprintf("%s:%d", cfg.Listen, cfg.Port)

	// UDP 버퍼 크기 설정
	// - 512: RFC 1035 기본값 (너무 작음, DNSSEC 불가)
	// - 1232: RFC 6891 권장 (IPv4 MTU 고려, DNSSEC 지원)
	// - 1410: Cloudflare 권장 (IPv6 MTU 고려)
	// - 4096: 최대 실용값 (과도할 수 있음)
	udpSize := cfg.UDPSize
	if udpSize == 0 {
		udpSize = 1232 // 기본값
	}

	return &Server{
		config:  cfg,
		handler: handler,
		udp: &dns.Server{
			Addr:    addr,
			Net:     "udp",
			Handler: handler,
			UDPSize: udpSize,
		},
		tcp: &dns.Server{
			Addr:    addr,
			Net:     "tcp",
			Handler: handler,
		},
	}
}

// Start는 DNS 서버를 시작합니다
func (s *Server) Start() error {
	if s.config.UDP {
		packetConn, err := net.ListenPacket("udp", s.udp.Addr)
		if err != nil {
			return fmt.Errorf("UDP 서버 listen 실패: %w", err)
		}
		s.udp.PacketConn = packetConn
		go func() {
			log.Printf("UDP DNS 서버 시작: %s", s.udp.Addr)
			if err := s.udp.ActivateAndServe(); err != nil {
				log.Printf("UDP 서버 실패: %v", err)
			}
		}()
	}

	if s.config.TCP {
		listener, err := net.Listen("tcp", s.tcp.Addr)
		if err != nil {
			if s.config.UDP && s.udp != nil {
				_ = s.udp.Shutdown()
			}
			return fmt.Errorf("TCP 서버 listen 실패: %w", err)
		}
		s.tcp.Listener = listener
		go func() {
			log.Printf("TCP DNS 서버 시작: %s", s.tcp.Addr)
			if err := s.tcp.ActivateAndServe(); err != nil {
				log.Printf("TCP 서버 실패: %v", err)
			}
		}()
	}

	return nil
}

// Stop는 DNS 서버를 중지합니다
func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var err error

	if s.config.UDP && s.udp != nil {
		if e := s.udp.ShutdownContext(ctx); e != nil {
			// "server not started" 에러는 무시
			if e.Error() != "dns: server not started" {
				err = e
				log.Printf("UDP 서버 종료 실패: %v", e)
			}
		}
	}

	if s.config.TCP && s.tcp != nil {
		if e := s.tcp.ShutdownContext(ctx); e != nil {
			// "server not started" 에러는 무시
			if e.Error() != "dns: server not started" {
				err = e
				log.Printf("TCP 서버 종료 실패: %v", e)
			}
		}
	}

	return err
}

// GetAddr는 서버 주소를 반환합니다
func (s *Server) GetAddr() string {
	return s.udp.Addr
}

// Query는 DNS 쿼리를 수행합니다 (테스트용)
func Query(serverAddr, domain, qtype string) (*dns.Msg, error) {
	c := new(dns.Client)
	c.Timeout = 5 * time.Second

	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(domain), dns.StringToType[qtype])
	m.RecursionDesired = true

	r, _, err := c.Exchange(m, serverAddr)
	if err != nil {
		return nil, fmt.Errorf("쿼리 실패: %w", err)
	}

	return r, nil
}

// ExtractClientIP는 EDNS Client Subnet에서 클라이언트 IP를 추출합니다
func ExtractClientIP(r *dns.Msg) net.IP {
	// EDNS OPT 레코드 확인
	opt := r.IsEdns0()
	if opt == nil {
		return nil
	}

	// EDNS Client Subnet 옵션 찾기
	for _, option := range opt.Option {
		if subnet, ok := option.(*dns.EDNS0_SUBNET); ok {
			return subnet.Address
		}
	}

	return nil
}
