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

	return &Server{
		config:  cfg,
		handler: handler,
		udp: &dns.Server{
			Addr:    addr,
			Net:     "udp",
			Handler: handler,
			UDPSize: 65535,
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
	errChan := make(chan error, 2)

	if s.config.UDP {
		go func() {
			log.Printf("UDP DNS 서버 시작: %s", s.udp.Addr)
			if err := s.udp.ListenAndServe(); err != nil {
				errChan <- fmt.Errorf("UDP 서버 실패: %w", err)
			}
		}()
	}

	if s.config.TCP {
		go func() {
			log.Printf("TCP DNS 서버 시작: %s", s.tcp.Addr)
			if err := s.tcp.ListenAndServe(); err != nil {
				errChan <- fmt.Errorf("TCP 서버 실패: %w", err)
			}
		}()
	}

	// 서버 시작 대기 (짧은 시간)
	time.Sleep(100 * time.Millisecond)

	// 에러 확인
	select {
	case err := <-errChan:
		return err
	default:
		return nil
	}
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
