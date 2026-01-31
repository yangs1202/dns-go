package dns

import (
	"crypto/tls"
	"dns-go/model"
	"dns-go/storage"
	"fmt"
	"time"

	"github.com/miekg/dns"
)

// Resolver는 DNS 쿼리를 업스트림 서버로 전달하는 리졸버입니다
type Resolver struct {
	storage *storage.UpstreamStorage
	timeout time.Duration
}

// NewResolver는 새로운 Resolver를 생성합니다
func NewResolver(storage *storage.UpstreamStorage, timeout time.Duration) *Resolver {
	return &Resolver{
		storage: storage,
		timeout: timeout,
	}
}

// Forward는 DNS 쿼리를 우선순위 기반으로 업스트림 서버에 전달합니다
func (r *Resolver) Forward(req *dns.Msg) (*dns.Msg, error) {
	// 활성화된 업스트림 서버 목록 조회 (L2 캐시 활용)
	servers, err := r.storage.ListEnabledUpstreamServers()
	if err != nil {
		return nil, fmt.Errorf("활성화된 업스트림 서버 목록 조회 실패: %w", err)
	}

	if len(servers) == 0 {
		return nil, fmt.Errorf("활성화된 업스트림 서버가 없습니다")
	}

	// priority 오름차순으로 정렬된 서버 순회
	var lastErr error
	for _, server := range servers {
		resp, err := r.forwardToServer(server, req)
		if err == nil {
			// 첫 번째 성공한 응답 반환
			return resp, nil
		}
		lastErr = err
	}

	// 모든 서버 실패 시 에러 반환
	return nil, fmt.Errorf("모든 업스트림 서버 실패: %w", lastErr)
}

// forwardToServer는 단일 업스트림 서버로 DNS 쿼리를 전달합니다
func (r *Resolver) forwardToServer(server *model.UpstreamServer, req *dns.Msg) (*dns.Msg, error) {
	client := &dns.Client{
		Timeout: r.timeout,
	}

	switch server.Protocol {
	case "udp":
		client.Net = "udp"
	case "tcp":
		client.Net = "tcp"
	case "tcp-tls":
		client.Net = "tcp-tls"
		client.TLSConfig = &tls.Config{
			ServerName: server.Address,
		}
	default:
		return nil, fmt.Errorf("지원하지 않는 프로토콜: %s", server.Protocol)
	}

	resp, _, err := client.Exchange(req, server.Address)
	if err != nil {
		return nil, fmt.Errorf("서버 %s (%s)로 쿼리 전달 실패: %w", server.Name, server.Address, err)
	}

	return resp, nil
}
