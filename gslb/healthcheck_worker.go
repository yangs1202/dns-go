package gslb

import (
	"crypto/tls"
	"dns-go/metrics"
	"dns-go/model"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	healthCheckTypeHTTP  = "http"
	healthCheckTypeHTTPS = "https"
	healthCheckTypeTCP   = "tcp"

	defaultHTTPHealthStatus = http.StatusOK
)

type HealthCheckWorker struct {
	storage      *HealthCheckStorage
	poolStorage  *PoolStorage
	healthStatus *sync.Map
	stopCh       chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
	runners      sync.Map // map[int64]chan struct{} - 각 헬스체크의 종료 채널
	running      sync.Map // map[int64]struct{} - 현재 실행 중인 체크 (중복 방지)
}

func NewHealthCheckWorker(storage *HealthCheckStorage, poolStorage *PoolStorage, healthStatus *sync.Map) *HealthCheckWorker {
	return &HealthCheckWorker{
		storage:      storage,
		poolStorage:  poolStorage,
		healthStatus: healthStatus,
		stopCh:       make(chan struct{}),
	}
}

func (w *HealthCheckWorker) Start() {
	checks, err := w.storage.ListHealthChecks()
	if err != nil {
		log.Printf("헬스체크 목록 조회 실패: %v", err)
		return
	}
	log.Printf("헬스체크 시작: %d개 체크", len(checks))

	for _, check := range checks {
		if !check.Enabled {
			continue
		}
		w.startCheck(check)
	}
}

func (w *HealthCheckWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	w.wg.Wait()
}

func (w *HealthCheckWorker) startCheck(check *model.HealthCheck) {
	stopCh := make(chan struct{})
	if _, loaded := w.runners.LoadOrStore(check.ID, stopCh); loaded {
		log.Printf("헬스체크 이미 실행 중: check_id=%d", check.ID)
		return
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.runCheckLoop(check, stopCh)
	}()
}

func (w *HealthCheckWorker) runCheckLoop(check *model.HealthCheck, stopCh chan struct{}) {
	defer func() {
		w.runners.CompareAndDelete(check.ID, stopCh)
	}()

	ticker := time.NewTicker(time.Duration(check.IntervalSec) * time.Second)
	defer ticker.Stop()

	// 초기 체크 실행
	w.runPolicyCheck(check)

	for {
		select {
		case <-ticker.C:
			w.runPolicyCheck(check)
		case <-stopCh:
			log.Printf("헬스체크 종료: check_id=%d", check.ID)
			return
		case <-w.stopCh:
			return
		}
	}
}

// runPolicyCheck는 GSLB 정책에 속한 모든 멤버를 체크합니다
func (w *HealthCheckWorker) runPolicyCheck(check *model.HealthCheck) {
	// 이미 실행 중인 체크가 있으면 skip (고루틴 누적 방지)
	if _, loaded := w.running.LoadOrStore(check.ID, struct{}{}); loaded {
		log.Printf("[HEALTH] check_id=%d 이전 체크 실행 중, skip", check.ID)
		return
	}
	defer w.running.Delete(check.ID)

	// Policy에 속한 모든 Pool 조회
	pools, err := w.poolStorage.GetPoolsByPolicy(check.PolicyID)
	if err != nil {
		log.Printf("풀 목록 조회 실패 (policy_id=%d): %v", check.PolicyID, err)
		return
	}

	// 멤버별 병렬 체크 (최대 10개 동시 실행)
	sem := make(chan struct{}, 10)
	var wg sync.WaitGroup

	for _, pool := range pools {
		members, err := w.poolStorage.GetMembersByPool(pool.ID)
		if err != nil {
			log.Printf("멤버 목록 조회 실패 (pool_id=%d): %v", pool.ID, err)
			continue
		}

		for _, member := range members {
			if !member.Enabled {
				continue
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(m *model.GSLBMember, p *model.GSLBPool) {
				defer func() { <-sem; wg.Done() }()
				w.runCheck(check, m, p)
			}(member, pool)
		}
	}

	wg.Wait()
}

func (w *HealthCheckWorker) runCheck(check *model.HealthCheck, member *model.GSLBMember, pool *model.GSLBPool) {
	err := w.probe(check, member)
	status := w.getStatus(member.ID)
	status.LastCheck = time.Now()

	prevHealthy := status.Healthy

	if err == nil {
		status.ConsecutiveOKs++
		status.ConsecutiveFails = 0
		status.LastError = ""
		if status.ConsecutiveOKs >= int(check.HealthyThreshold) {
			status.Healthy = true
		}
	} else {
		status.ConsecutiveFails++
		status.ConsecutiveOKs = 0
		status.LastError = err.Error()
		if status.ConsecutiveFails >= int(check.UnhealthyThreshold) {
			status.Healthy = false
		}
	}

	w.healthStatus.Store(member.ID, status)

	if prevHealthy != status.Healthy {
		if status.Healthy {
			log.Printf("[HEALTH] member_id=%d addr=%s 상태 변경: unhealthy → healthy (check_id=%d, pool=%s)", member.ID, member.Address, check.ID, pool.Name)
		} else {
			log.Printf("[HEALTH] member_id=%d addr=%s 상태 변경: healthy → unhealthy (check_id=%d, pool=%s, error=%s)", member.ID, member.Address, check.ID, pool.Name, status.LastError)
		}
	} else if err != nil {
		log.Printf("[HEALTH] member_id=%d addr=%s 체크 실패 (check_id=%d, pool=%s, fails=%d/%d, error=%s)", member.ID, member.Address, check.ID, pool.Name, status.ConsecutiveFails, check.UnhealthyThreshold, err.Error())
	} else {
		log.Printf("[HEALTH] member_id=%d addr=%s 체크 성공 (check_id=%d, pool=%s, healthy=%v)", member.ID, member.Address, check.ID, pool.Name, status.Healthy)
	}

	healthValue := 0.0
	if status.Healthy {
		healthValue = 1.0
	}
	metrics.GSLBHealthStatus.WithLabelValues(
		strconv.FormatInt(member.ID, 10),
		member.Address,
		strconv.FormatInt(pool.ID, 10),
		pool.Name,
	).Set(healthValue)
}

func (w *HealthCheckWorker) probe(check *model.HealthCheck, member *model.GSLBMember) error {
	switch check.CheckType {
	case healthCheckTypeHTTP, healthCheckTypeHTTPS:
		return w.probeHTTP(check, member)
	case healthCheckTypeTCP:
		return probeTCP(check.Target, member.Address, time.Duration(check.TimeoutSec)*time.Second)
	default:
		return probeTCP(check.Target, member.Address, time.Duration(check.TimeoutSec)*time.Second)
	}
}

func (w *HealthCheckWorker) probeHTTP(check *model.HealthCheck, member *model.GSLBMember) error {
	requestURL, originalHost, sniHost, err := buildHealthCheckRequestURL(check, member)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return fmt.Errorf("healthcheck request 생성 실패: %w", err)
	}
	req.Host = originalHost

	transport := &http.Transport{}
	if requestURL.Scheme == healthCheckTypeHTTPS {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         sniHost,
		}
	}

	client := &http.Client{
		Timeout:   time.Duration(check.TimeoutSec) * time.Second,
		Transport: transport,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http healthcheck 실패: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	expectedCodes := expectedHTTPStatusCodes(check.ExpectedCodes)
	for _, code := range expectedCodes {
		if resp.StatusCode == code {
			return nil
		}
	}
	return fmt.Errorf("http status %d (expected: %v)", resp.StatusCode, expectedCodes)
}

func buildHealthCheckRequestURL(check *model.HealthCheck, member *model.GSLBMember) (*url.URL, string, string, error) {
	parsedURL, fullURLTarget, err := parseHealthCheckTargetURL(check, member)
	if err != nil {
		return nil, "", "", err
	}

	port := healthCheckRequestPort(parsedURL, fullURLTarget, member.Address)

	requestURL := &url.URL{
		Scheme:   parsedURL.Scheme,
		Host:     memberAddressWithPort(member.Address, port),
		Path:     parsedURL.Path,
		RawQuery: parsedURL.RawQuery,
	}

	if fullURLTarget {
		return requestURL, parsedURL.Host, parsedURL.Hostname(), nil
	}
	return requestURL, member.Address, hostWithoutPort(member.Address), nil
}

func parseHealthCheckTargetURL(check *model.HealthCheck, member *model.GSLBMember) (*url.URL, bool, error) {
	if strings.HasPrefix(check.Target, "http://") || strings.HasPrefix(check.Target, "https://") {
		parsedURL, err := url.Parse(check.Target)
		if err != nil {
			return nil, false, fmt.Errorf("invalid target URL: %w", err)
		}
		return parsedURL, true, nil
	}

	path := check.Target
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	relativeURL, err := url.Parse(path)
	if err != nil {
		return nil, false, fmt.Errorf("invalid target URL: %w", err)
	}
	return &url.URL{
		Scheme:   check.CheckType,
		Host:     member.Address,
		Path:     relativeURL.Path,
		RawQuery: relativeURL.RawQuery,
	}, false, nil
}

func memberAddressWithPort(address, port string) string {
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(address, port)
}

func healthCheckRequestPort(parsedURL *url.URL, fullURLTarget bool, memberAddress string) string {
	if fullURLTarget {
		if port := parsedURL.Port(); port != "" {
			return port
		}
		return defaultHTTPPort(parsedURL.Scheme)
	}

	if _, port, err := net.SplitHostPort(memberAddress); err == nil {
		return port
	}
	return defaultHTTPPort(parsedURL.Scheme)
}

func hostWithoutPort(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}

func defaultHTTPPort(scheme string) string {
	if scheme == healthCheckTypeHTTPS {
		return "443"
	}
	return "80"
}

func expectedHTTPStatusCodes(codes []int) []int {
	if len(codes) == 0 {
		return []int{defaultHTTPHealthStatus}
	}
	return codes
}

func probeTCP(target, memberAddress string, timeout time.Duration) error {
	port := target
	if strings.Contains(target, ":") {
		_, parsedPort, err := net.SplitHostPort(target)
		if err != nil {
			return fmt.Errorf("tcp target 포트 파싱 실패: %w", err)
		}
		port = parsedPort
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(memberAddress, port), timeout)
	if err != nil {
		return fmt.Errorf("tcp healthcheck 실패: %w", err)
	}
	_ = conn.Close()
	return nil
}

func (w *HealthCheckWorker) getStatus(memberID int64) HealthStatus {
	if v, ok := w.healthStatus.Load(memberID); ok {
		if status, ok := v.(HealthStatus); ok {
			return status
		}
	}
	return HealthStatus{Healthy: true}
}

// AddCheck는 새로운 헬스체크를 동적으로 추가합니다
func (w *HealthCheckWorker) AddCheck(check *model.HealthCheck) {
	if !check.Enabled {
		return
	}

	// 이미 실행 중인지 확인
	log.Printf("헬스체크 추가: check_id=%d, policy_id=%d", check.ID, check.PolicyID)
	w.startCheck(check)
}

// RemoveCheck는 실행 중인 헬스체크를 제거합니다
func (w *HealthCheckWorker) RemoveCheck(checkID int64) {
	if v, ok := w.runners.LoadAndDelete(checkID); ok {
		if stopCh, ok := v.(chan struct{}); ok {
			log.Printf("헬스체크 제거: check_id=%d", checkID)
			close(stopCh)
		}
	}
}

// UpdateCheck는 헬스체크를 업데이트합니다 (기존 제거 후 재시작)
func (w *HealthCheckWorker) UpdateCheck(check *model.HealthCheck) {
	w.RemoveCheck(check.ID)
	w.AddCheck(check)
}

// Restart는 모든 헬스체크를 재시작합니다 (동기화 후 호출)
func (w *HealthCheckWorker) Restart() {
	log.Println("헬스체크 워커 재시작 중...")

	// 모든 실행 중인 체크 종료
	w.runners.Range(func(key, value interface{}) bool {
		if _, loaded := w.runners.LoadAndDelete(key); loaded {
			if stopCh, ok := value.(chan struct{}); ok {
				close(stopCh)
			}
		}
		return true
	})

	// 기존 고루틴 종료 대기
	w.wg.Wait()

	// 모든 헬스체크 재시작
	checks, err := w.storage.ListHealthChecks()
	if err != nil {
		log.Printf("헬스체크 목록 조회 실패: %v", err)
		return
	}

	log.Printf("헬스체크 재시작: %d개 체크", len(checks))
	for _, check := range checks {
		if !check.Enabled {
			continue
		}
		w.startCheck(check)
	}
}
