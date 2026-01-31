# Phase 5: 헬스체크 + 모니터링

## 목표

GSLB 멤버 서버의 헬스 상태를 주기적으로 확인하고 통계를 제공

## 예상 소요 시간

2시간

## 헬스체크 원리

```
1. GSLB 멤버마다 헬스체크 설정 (HTTP/HTTPS/TCP)
2. 백그라운드 워커가 주기적으로 프로브 전송
3. 연속 실패/성공 횟수로 상태 판정
4. healthStatus sync.Map 업데이트
5. GSLB 엔진이 정상 서버만 선택
```

---

## 구현 순서

### 1단계: 헬스체크 설정 CRUD (30분)

**파일**: `gslb/healthcheck.go` (모델 및 Storage)

#### HealthCheckStorage
```go
type HealthCheckStorage struct {
    db *storage.Database
}

func (s *HealthCheckStorage) GetHealthCheck(id int64) (*model.HealthCheck, error)
func (s *HealthCheckStorage) GetHealthCheckByMember(memberID int64) (*model.HealthCheck, error)
func (s *HealthCheckStorage) ListHealthChecks() ([]*model.HealthCheck, error)
func (s *HealthCheckStorage) CreateHealthCheck(check *model.HealthCheck) (int64, error)
func (s *HealthCheckStorage) UpdateHealthCheck(check *model.HealthCheck) error
func (s *HealthCheckStorage) DeleteHealthCheck(id int64) error
```

**기본값**:
- check_type: "tcp"
- interval_sec: 10
- timeout_sec: 5
- healthy_threshold: 3
- unhealthy_threshold: 2

**테스트**:
- CRUD 기본 동작
- 멤버별 헬스체크 조회

---

### 2단계: 헬스체크 워커 (60분)

**파일**: `gslb/healthcheck_worker.go`, `gslb/healthcheck_worker_test.go`

#### HealthCheckWorker
```go
type HealthCheckWorker struct {
    storage      *HealthCheckStorage
    memberStorage *PoolStorage
    healthStatus *sync.Map  // key: member_id (int64), value: HealthStatus
    stopCh       chan struct{}
    wg           sync.WaitGroup
}

type HealthStatus struct {
    Healthy          bool
    ConsecutiveFails int
    ConsecutiveOKs   int
    LastCheck        time.Time
    LastError        string
}

func NewHealthCheckWorker(storage, memberStorage, healthStatus) *HealthCheckWorker
func (w *HealthCheckWorker) Start()
func (w *HealthCheckWorker) Stop()
func (w *HealthCheckWorker) runCheck(check *model.HealthCheck, member *model.GSLBMember)
func (w *HealthCheckWorker) probe(check *model.HealthCheck, member *model.GSLBMember) error
```

**Start 로직**:
```go
func (w *HealthCheckWorker) Start() {
    w.stopCh = make(chan struct{})

    // 모든 헬스체크 조회
    checks, err := w.storage.ListHealthChecks()
    if err != nil {
        log.Printf("헬스체크 목록 조회 실패: %v", err)
        return
    }

    log.Printf("헬스체크 시작: %d개 체크", len(checks))

    // 각 헬스체크마다 고루틴 시작
    for _, check := range checks {
        if !check.Enabled {
            continue
        }

        w.wg.Add(1)
        go func(c *model.HealthCheck) {
            defer w.wg.Done()
            w.runCheckLoop(c)
        }(check)
    }
}

func (w *HealthCheckWorker) runCheckLoop(check *model.HealthCheck) {
    ticker := time.NewTicker(time.Duration(check.IntervalSec) * time.Second)
    defer ticker.Stop()

    // 즉시 첫 체크 실행
    member, _ := w.memberStorage.GetMember(check.MemberID)
    if member != nil {
        w.runCheck(check, member)
    }

    for {
        select {
        case <-ticker.C:
            member, _ := w.memberStorage.GetMember(check.MemberID)
            if member != nil {
                w.runCheck(check, member)
            }
        case <-w.stopCh:
            return
        }
    }
}
```

**runCheck 로직**:
```go
func (w *HealthCheckWorker) runCheck(check *model.HealthCheck, member *model.GSLBMember) {
    // 현재 상태 조회
    statusVal, _ := w.healthStatus.Load(member.ID)
    var status HealthStatus
    if statusVal != nil {
        status = statusVal.(HealthStatus)
    } else {
        // 초기 상태: healthy로 시작
        status = HealthStatus{
            Healthy: true,
        }
    }

    // 프로브 실행
    err := w.probe(check, member)
    status.LastCheck = time.Now()

    if err != nil {
        // 실패
        status.LastError = err.Error()
        status.ConsecutiveFails++
        status.ConsecutiveOKs = 0

        // unhealthy_threshold 초과 시 unhealthy로 전환
        if status.ConsecutiveFails >= int(check.UnhealthyThreshold) {
            if status.Healthy {
                log.Printf("멤버 %d (%s) unhealthy로 전환: %v", member.ID, member.Address, err)
            }
            status.Healthy = false
        }
    } else {
        // 성공
        status.LastError = ""
        status.ConsecutiveOKs++
        status.ConsecutiveFails = 0

        // healthy_threshold 초과 시 healthy로 전환
        if status.ConsecutiveOKs >= int(check.HealthyThreshold) {
            if !status.Healthy {
                log.Printf("멤버 %d (%s) healthy로 복구", member.ID, member.Address)
            }
            status.Healthy = true
        }
    }

    // 상태 업데이트
    w.healthStatus.Store(member.ID, status)
}
```

**probe 로직**:
```go
func (w *HealthCheckWorker) probe(check *model.HealthCheck, member *model.GSLBMember) error {
    timeout := time.Duration(check.TimeoutSec) * time.Second

    switch check.CheckType {
    case "http":
        return w.probeHTTP(check.Target, timeout, false)
    case "https":
        return w.probeHTTP(check.Target, timeout, true)
    case "tcp":
        return w.probeTCP(check.Target, timeout)
    default:
        return fmt.Errorf("지원하지 않는 헬스체크 타입: %s", check.CheckType)
    }
}

func (w *HealthCheckWorker) probeHTTP(url string, timeout time.Duration, https bool) error {
    client := &http.Client{
        Timeout: timeout,
        Transport: &http.Transport{
            TLSClientConfig: &tls.Config{
                InsecureSkipVerify: true,  // 자체 서명 인증서 허용
            },
        },
    }

    resp, err := client.Get(url)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    // 2xx 응답이면 healthy
    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        return nil
    }

    return fmt.Errorf("HTTP %d", resp.StatusCode)
}

func (w *HealthCheckWorker) probeTCP(address string, timeout time.Duration) error {
    conn, err := net.DialTimeout("tcp", address, timeout)
    if err != nil {
        return err
    }
    conn.Close()
    return nil
}
```

**Stop 로직**:
```go
func (w *HealthCheckWorker) Stop() {
    close(w.stopCh)
    w.wg.Wait()
    log.Println("헬스체크 워커 중지 완료")
}
```

**테스트**:
- HTTP 프로브 (모킹)
- HTTPS 프로브
- TCP 프로브
- 연속 실패/성공 카운터
- Healthy/Unhealthy 전환
- 타임아웃

---

### 3단계: GSLB 엔진 헬스 필터 통합 (이미 구현됨)

**파일**: `gslb/engine.go` (Phase 3에서 이미 구현)

**filterHealthyMembers**:
```go
func (e *Engine) filterHealthyMembers(members []*model.GSLBMember) []*model.GSLBMember {
    var healthy []*model.GSLBMember

    for _, member := range members {
        // healthStatus에서 상태 조회
        statusVal, ok := e.healthStatus.Load(member.ID)
        if !ok {
            // 상태 정보 없으면 healthy로 간주 (초기 상태)
            healthy = append(healthy, member)
            continue
        }

        status := statusVal.(HealthStatus)
        if status.Healthy {
            healthy = append(healthy, member)
        }
    }

    // 모든 멤버가 unhealthy면 모든 멤버 사용 (fail-open)
    if len(healthy) == 0 {
        log.Println("모든 멤버가 unhealthy, 전체 사용 (fail-open)")
        return members
    }

    return healthy
}
```

---

### 4단계: 헬스 상태 API (30min)

**파일**: `web/api_health.go`, `web/api_health_test.go`

**엔드포인트**:

#### GET /api/gslb/health
- **설명**: 전체 헬스 상태 조회
- **응답**:
  ```json
  {
    "success": true,
    "data": [
      {
        "member_id": 1,
        "address": "1.2.3.4",
        "pool_name": "korea-pool",
        "healthy": true,
        "consecutive_fails": 0,
        "consecutive_oks": 5,
        "last_check": "2026-01-31T10:00:00Z",
        "last_error": ""
      },
      {
        "member_id": 2,
        "address": "5.6.7.8",
        "pool_name": "us-pool",
        "healthy": false,
        "consecutive_fails": 3,
        "consecutive_oks": 0,
        "last_check": "2026-01-31T10:00:05Z",
        "last_error": "connection refused"
      }
    ]
  }
  ```

#### GET /api/gslb/health/:member_id
- **설명**: 특정 멤버 헬스 상태

#### POST /api/gslb/health/:member_id/check
- **설명**: 수동 헬스체크 트리거
- **응답**:
  ```json
  {
    "success": true,
    "data": {
      "member_id": 1,
      "result": "healthy",
      "response_time_ms": 15.3,
      "checked_at": "2026-01-31T10:00:00Z"
    }
  }
  ```

**헬스체크 설정 관리**:
```
GET    /api/gslb/members/:member_id/healthcheck      # 헬스체크 설정 조회
POST   /api/gslb/members/:member_id/healthcheck      # 헬스체크 설정 생성
PUT    /api/gslb/healthchecks/:id                    # 헬스체크 설정 수정
DELETE /api/gslb/healthchecks/:id                    # 헬스체크 설정 삭제
```

**POST /api/gslb/members/:member_id/healthcheck 요청**:
```json
{
  "check_type": "http",
  "target": "http://1.2.3.4:80/health",
  "interval_sec": 10,
  "timeout_sec": 5,
  "healthy_threshold": 3,
  "unhealthy_threshold": 2,
  "enabled": true
}
```

**테스트**:
- 헬스 상태 조회
- 수동 체크 트리거
- 헬스체크 설정 CRUD

---

### 5단계: 쿼리 로깅 및 통계 강화 (30min)

**파일**: `dns/stats.go`, `dns/handler.go` 수정

#### QueryStats
```go
type QueryStats struct {
    mu sync.RWMutex

    TotalQueries   uint64  // atomic
    QueriesPerType sync.Map  // key: qtype (string), value: uint64
    QueriesPerProto sync.Map  // key: "udp"/"tcp", value: uint64

    CacheHits     uint64  // atomic
    CacheMisses   uint64  // atomic
    GSLBQueries   uint64  // atomic
    BlockedQueries uint64  // atomic
    UpstreamForwards uint64  // atomic

    StartTime time.Time
}

func NewQueryStats() *QueryStats
func (s *QueryStats) RecordQuery(qtype, proto string)
func (s *QueryStats) RecordCacheHit()
func (s *QueryStats) RecordCacheMiss()
func (s *QueryStats) RecordGSLB()
func (s *QueryStats) RecordBlocked()
func (s *QueryStats) RecordUpstream()
func (s *QueryStats) GetSnapshot() *StatsSnapshot
```

**StatsSnapshot**:
```json
{
  "server": {
    "uptime_seconds": 86400,
    "version": "0.1.0",
    "start_time": "2026-01-30T10:00:00Z"
  },
  "dns": {
    "total_queries": 1234567,
    "queries_per_second": 1523.5,
    "by_type": {
      "A": 800000,
      "AAAA": 200000,
      "CNAME": 100000
    },
    "by_protocol": {
      "udp": 1000000,
      "tcp": 234567
    }
  },
  "cache": {
    "hits": 1050000,
    "misses": 184567,
    "hit_rate": 0.851,
    "l1_size": 8523,
    "l1_evictions": 100
  },
  "routing": {
    "gslb_queries": 50000,
    "blocked_queries": 30000,
    "upstream_forwards": 104567
  }
}
```

**Handler에 통합**:
```go
type Handler struct {
    // ...
    stats *QueryStats
}

func (h *Handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
    // 프로토콜 판별
    proto := "udp"
    if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
        proto = "tcp"
    }

    // 쿼리 기록
    if len(r.Question) > 0 {
        qtype := dns.TypeToString[r.Question[0].Qtype]
        h.stats.RecordQuery(qtype, proto)
    }

    // ... 처리 로직 ...

    // L1 캐시 히트
    if cacheHit {
        h.stats.RecordCacheHit()
    } else {
        h.stats.RecordCacheMiss()
    }

    // GSLB 사용
    if gslbUsed {
        h.stats.RecordGSLB()
    }

    // 광고차단
    if blocked {
        h.stats.RecordBlocked()
    }

    // 업스트림 포워딩
    if forwarded {
        h.stats.RecordUpstream()
    }
}
```

**테스트**:
- 쿼리 카운팅
- 타입별/프로토콜별 통계
- QPS 계산
- 스냅샷 생성

---

### 6단계: 강화된 통계 API (20min)

**파일**: `web/api_stats.go` 수정

**GET /api/stats 응답**:
```json
{
  "success": true,
  "data": {
    "server": {
      "uptime_seconds": 86400,
      "version": "0.2.0",
      "go_version": "go1.21.0",
      "start_time": "2026-01-30T10:00:00Z"
    },
    "dns": {
      "total_queries": 1234567,
      "queries_per_second": 1523.5,
      "by_type": {
        "A": 800000,
        "AAAA": 200000,
        "CNAME": 100000,
        "MX": 50000,
        "TXT": 84567
      },
      "by_protocol": {
        "udp": 1000000,
        "tcp": 234567
      }
    },
    "cache": {
      "l1": {
        "hits": 1050000,
        "misses": 184567,
        "hit_rate": 0.851,
        "size": 8523,
        "evictions": 100,
        "memory_mb": 25.6
      }
    },
    "routing": {
      "gslb_queries": 50000,
      "gslb_percentage": 0.041,
      "blocked_queries": 30000,
      "blocked_percentage": 0.024,
      "upstream_forwards": 104567,
      "upstream_percentage": 0.085
    },
    "database": {
      "zones": 10,
      "records": 523,
      "upstream_servers": 3,
      "gslb_policies": 5,
      "gslb_pools": 12,
      "gslb_members": 30,
      "blocked_domains": 1523456
    },
    "health": {
      "total_members": 30,
      "healthy_members": 28,
      "unhealthy_members": 2
    }
  }
}
```

**GET /api/stats/realtime** (WebSocket 또는 SSE, 선택사항):
- 실시간 QPS 스트리밍
- 1초마다 업데이트

**테스트**:
- 통계 조회
- 백분율 계산
- DB 카운트

---

### 7단계: main.go 통합 (10min)

**파일**: `main.go` 수정

**헬스체크 워커 시작**:
```go
// 헬스체크 Storage
healthCheckStorage := gslb.NewHealthCheckStorage(db)

// healthStatus sync.Map
healthStatus := &sync.Map{}

// 헬스체크 워커
healthWorker := gslb.NewHealthCheckWorker(healthCheckStorage, poolStorage, healthStatus)
healthWorker.Start()
defer healthWorker.Stop()

log.Println("헬스체크 워커 시작")

// GSLB 엔진에 healthStatus 전달
gslbEngine := gslb.NewEngine(policyStorage, poolStorage, geoipResolver, healthStatus)

// 쿼리 통계
queryStats := dns.NewQueryStats()

// DNS 핸들러
handler, err := dns.NewHandler(..., gslbEngine, adblockFilter, adblockStorage, queryStats)
```

---

## 헬스체크 시나리오

### 시나리오 1: HTTP 헬스체크
```bash
# 헬스체크 설정 추가
curl -X POST http://localhost:8080/api/gslb/members/1/healthcheck \
  -H "Content-Type: application/json" \
  -d '{
    "check_type": "http",
    "target": "http://1.2.3.4:80/health",
    "interval_sec": 10,
    "timeout_sec": 5,
    "healthy_threshold": 3,
    "unhealthy_threshold": 2
  }'

# 상태 확인
curl http://localhost:8080/api/gslb/health
```

### 시나리오 2: 멤버 장애 시 자동 제외
```
1. 멤버 1, 2, 3이 모두 healthy
2. 멤버 2가 다운
3. 헬스체크 2회 연속 실패
4. 멤버 2가 unhealthy로 전환
5. GSLB가 멤버 1, 3만 사용
6. 멤버 2 복구
7. 헬스체크 3회 연속 성공
8. 멤버 2가 healthy로 복구
9. GSLB가 멤버 1, 2, 3 모두 사용
```

---

## 예상 파일 및 라인 수

- `gslb/healthcheck.go`: ~100줄 (Storage)
- `gslb/healthcheck_worker.go`: ~350줄
- `dns/stats.go`: ~200줄
- `web/api_health.go`: ~200줄
- `web/api_stats.go`: ~150줄 (수정)
- 테스트 파일들: ~700줄

**총 예상**: ~1,700줄

---

## Phase 5 완료 후 상태

- ✅ GSLB 멤버 헬스체크 (HTTP/HTTPS/TCP)
- ✅ 연속 실패/성공 임계값 기반 상태 전환
- ✅ Unhealthy 멤버 자동 제외
- ✅ Fail-open (모든 멤버 unhealthy 시 전체 사용)
- ✅ 상세한 쿼리 통계
- ✅ 실시간 모니터링 API

**프로젝트 완료!** 🎉

---

## 최종 프로젝트 상태

### 전체 통계 (Phase 1~5 완료 시)
- **총 Go 파일**: ~50개
- **총 코드 라인**: ~14,000줄
- **테스트 커버리지**: 80%+
- **기능**: DNS 서버, 3-Tier 캐싱, REST API, GSLB, 광고차단, 헬스체크

### 핵심 기능
1. **고성능 DNS 서버** (80K~150K QPS)
2. **3-Tier 캐싱** (L1 + L2 + SQLite)
3. **REST API** (완전한 관리 기능)
4. **GSLB** (지역/CIDR 기반 라우팅)
5. **광고차단** (Bloom Filter 기반)
6. **헬스체크** (HTTP/HTTPS/TCP)
7. **실시간 통계** (QPS, 히트율, 헬스 상태)

### 프로덕션 레디
- ✅ Graceful shutdown
- ✅ 에러 처리
- ✅ 로깅
- ✅ 동시성 안전
- ✅ 테스트 커버리지 80%+
- ✅ 문서화 (README, 각 Phase 플랜)
- ✅ 성능 최적화 (캐싱, Bloom Filter 등)

**다음 단계**: 배포, 벤치마크, 프로덕션 모니터링
