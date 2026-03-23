package dns

import (
	"crypto/tls"
	"dns-go/metrics"
	"dns-go/model"
	"dns-go/storage"
	"fmt"
	"strconv"
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

// Forward는 모든 업스트림 서버에 동시 요청하여 가장 빠른 응답을 반환합니다
func (r *Resolver) Forward(req *dns.Msg) (*dns.Msg, error) {
	servers, err := r.storage.ListEnabledUpstreamServers()
	if err != nil {
		return nil, fmt.Errorf("활성화된 업스트림 서버 목록 조회 실패: %w", err)
	}

	if len(servers) == 0 {
		return nil, fmt.Errorf("활성화된 업스트림 서버가 없습니다")
	}

	// 서버가 1개면 바로 요청
	if len(servers) == 1 {
		return r.forwardToServer(servers[0], req)
	}

	type result struct {
		resp *dns.Msg
		err  error
	}

	ch := make(chan result, len(servers))

	// 모든 서버에 동시 요청
	for _, server := range servers {
		go func(s *model.UpstreamServer) {
			// 각 고루틴에서 별도의 요청 메시지 사용 (race 방지)
			reqCopy := req.Copy()
			resp, err := r.forwardToServer(s, reqCopy)
			ch <- result{resp: resp, err: err}
		}(server)
	}

	// 가장 빠른 성공 응답 반환, 모두 실패 시 마지막 에러 반환
	var lastErr error
	for range servers {
		res := <-ch
		if res.err == nil {
			return res.resp, nil
		}
		lastErr = res.err
	}

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

	serverID := strconv.FormatInt(server.ID, 10)
	start := time.Now()
	resp, _, err := client.Exchange(req, server.Address)
	duration := time.Since(start).Seconds()

	if err != nil {
		metrics.UpstreamRequestsTotal.WithLabelValues(serverID, server.Name, "error").Inc()
		metrics.UpstreamDurationSeconds.WithLabelValues(serverID, server.Name).Observe(duration)
		return nil, fmt.Errorf("서버 %s (%s)로 쿼리 전달 실패: %w", server.Name, server.Address, err)
	}

	metrics.UpstreamRequestsTotal.WithLabelValues(serverID, server.Name, "success").Inc()
	metrics.UpstreamDurationSeconds.WithLabelValues(serverID, server.Name).Observe(duration)
	return resp, nil
}
