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
	case "http", "https":
		// Target URL 파싱
		targetURL := check.Target
		var parsedURL *url.URL
		var err error

		if strings.HasPrefix(targetURL, "http://") || strings.HasPrefix(targetURL, "https://") {
			// 전체 URL인 경우
			parsedURL, err = url.Parse(targetURL)
			if err != nil {
				return fmt.Errorf("invalid target URL: %w", err)
			}
		} else {
			// 경로만 있는 경우, 멤버 IP와 조합
			scheme := "http"
			if check.CheckType == "https" {
				scheme = "https"
			}
			// 경로가 /로 시작하지 않으면 추가
			path := targetURL
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			fullURL := fmt.Sprintf("%s://%s%s", scheme, member.Address, path)
			parsedURL, err = url.Parse(fullURL)
			if err != nil {
				return fmt.Errorf("invalid target URL: %w", err)
			}
		}

		// 실제 연결할 주소: 멤버 IP + 포트
		port := parsedURL.Port()
		if port == "" {
			if parsedURL.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}

		// 원본 Host 헤더 보존
		originalHost := parsedURL.Host

		// 멤버 주소 (포트 포함 여부 확인)
		memberHost := member.Address
		if strings.Contains(memberHost, ":") {
			// 이미 포트가 포함된 경우
			memberHost = member.Address
		} else {
			// 포트가 없으면 추가
			memberHost = net.JoinHostPort(member.Address, port)
		}

		// 요청 URL을 멤버 IP로 변경 (Host 헤더는 나중에 수동 설정)
		requestURL := &url.URL{
			Scheme:   parsedURL.Scheme,
			Host:     memberHost,
			Path:     parsedURL.Path,
			RawQuery: parsedURL.RawQuery,
		}

		// HTTP 요청 생성
		req, err := http.NewRequest("GET", requestURL.String(), nil)
		if err != nil {
			return err
		}

		// Host 헤더를 원본 도메인으로 설정
		req.Host = originalHost

		// Transport 설정
		transport := &http.Transport{}
		if parsedURL.Scheme == "https" {
			transport.TLSClientConfig = &tls.Config{
				InsecureSkipVerify: true,
				ServerName:         parsedURL.Hostname(), // SNI에 원본 도메인 사용
			}
		}

		client := &http.Client{
			Timeout:   time.Duration(check.TimeoutSec) * time.Second,
			Transport: transport,
		}

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()

		expectedCodes := check.ExpectedCodes
		if len(expectedCodes) == 0 {
			expectedCodes = []int{200}
		}
		for _, code := range expectedCodes {
			if resp.StatusCode == code {
				return nil
			}
		}
		return fmt.Errorf("http status %d (expected: %v)", resp.StatusCode, expectedCodes)
	case "tcp":
		// Target이 "host:port" 형태면 포트만 추출, 아니면 Target을 포트로 사용
		var port string
		if strings.Contains(check.Target, ":") {
			_, port, _ = net.SplitHostPort(check.Target)
		} else {
			port = check.Target
		}
		target := net.JoinHostPort(member.Address, port)

		conn, err := net.DialTimeout("tcp", target, time.Duration(check.TimeoutSec)*time.Second)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	default:
		// 기본값: TCP 체크
		var port string
		if strings.Contains(check.Target, ":") {
			_, port, _ = net.SplitHostPort(check.Target)
		} else {
			port = check.Target
		}
		target := net.JoinHostPort(member.Address, port)

		conn, err := net.DialTimeout("tcp", target, time.Duration(check.TimeoutSec)*time.Second)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	}
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
