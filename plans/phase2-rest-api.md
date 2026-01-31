# Phase 2: REST API + 캐시 관리

## 목표

DNS 서버를 웹 기반으로 관리할 수 있는 REST API 구현

## 예상 소요 시간

2~3시간

## 기술 스택

- **웹 프레임워크**: `github.com/gin-gonic/gin` v1.10.0
- **미들웨어**: CORS, 로깅, 에러 핸들링
- **응답 형식**: JSON

## 구현 순서

### 1단계: Gin 라우터 + 미들웨어 설정 (30분)

**파일**: `web/router.go`, `web/middleware.go`

**기능**:
- Gin 라우터 초기화
- CORS 미들웨어 (개발 시 필요)
- 로깅 미들웨어 (요청/응답 로깅)
- 에러 핸들링 미들웨어
- Recovery 미들웨어 (패닉 복구)

**라우트 그룹**:
```go
/api
├── /zones          # Zone 관리
├── /records        # Record 관리
├── /upstream       # Upstream 서버 관리
├── /cache          # 캐시 관리
└── /stats          # 통계
```

**테스트**:
- 라우터 초기화 테스트
- 미들웨어 체인 테스트
- CORS 헤더 테스트

---

### 2단계: Zone 관리 API (30분)

**파일**: `web/api_zones.go`, `web/api_zones_test.go`

**엔드포인트**:

#### GET /api/zones
- **설명**: Zone 목록 조회
- **응답**:
  ```json
  {
    "success": true,
    "data": [
      {
        "id": 1,
        "name": "example.com.",
        "soa_mname": "ns1.example.com.",
        "soa_rname": "admin.example.com.",
        "soa_serial": 2026013101,
        "soa_refresh": 3600,
        "soa_retry": 900,
        "soa_expire": 86400,
        "soa_minimum": 300,
        "enabled": true,
        "created_at": "2026-01-31T10:00:00Z",
        "updated_at": "2026-01-31T10:00:00Z"
      }
    ]
  }
  ```

#### GET /api/zones/:id
- **설명**: Zone 상세 조회
- **응답**: 단일 Zone 객체

#### POST /api/zones
- **설명**: Zone 생성
- **요청**:
  ```json
  {
    "name": "example.com.",
    "soa_mname": "ns1.example.com.",
    "soa_rname": "admin.example.com.",
    "enabled": true
  }
  ```
- **응답**: 생성된 Zone (ID 포함)
- **검증**:
  - name은 필수, FQDN 형식 (끝에 `.` 필요)
  - name은 unique

#### PUT /api/zones/:id
- **설명**: Zone 수정
- **요청**: Zone 전체 필드 (PATCH는 미지원)
- **응답**: 수정된 Zone
- **부수 효과**: L2 캐시 무효화

#### DELETE /api/zones/:id
- **설명**: Zone 삭제
- **응답**: `{"success": true, "message": "Zone 삭제 완료"}`
- **부수 효과**: CASCADE로 레코드도 삭제, L2 캐시 무효화

**에러 응답**:
```json
{
  "success": false,
  "error": "Zone을 찾을 수 없습니다"
}
```

**테스트**:
- 각 엔드포인트별 정상 케이스
- 검증 실패 케이스
- 404 에러 케이스
- L2 캐시 무효화 확인

---

### 3단계: Record 관리 API (30분)

**파일**: `web/api_records.go`, `web/api_records_test.go`

**엔드포인트**:

#### GET /api/zones/:zone_id/records
- **설명**: Zone의 모든 레코드 조회
- **쿼리 파라미터**:
  - `type` (선택): 레코드 타입 필터 (A, AAAA, CNAME 등)
- **응답**:
  ```json
  {
    "success": true,
    "data": [
      {
        "id": 1,
        "zone_id": 1,
        "name": "www.example.com.",
        "type": "A",
        "content": "192.0.2.1",
        "ttl": 300,
        "priority": 0,
        "enabled": true,
        "created_at": "2026-01-31T10:00:00Z",
        "updated_at": "2026-01-31T10:00:00Z"
      }
    ]
  }
  ```

#### GET /api/records/:id
- **설명**: Record 상세 조회

#### POST /api/zones/:zone_id/records
- **설명**: Record 생성
- **요청**:
  ```json
  {
    "name": "www.example.com.",
    "type": "A",
    "content": "192.0.2.1",
    "ttl": 300,
    "priority": 0
  }
  ```
- **검증**:
  - name, type, content 필수
  - type은 지원 타입만 (A, AAAA, CNAME, MX, TXT, NS, SOA)
  - content는 타입별 형식 검증 (IP 주소, 도메인 등)
  - MX/SRV는 priority 필수

#### PUT /api/records/:id
- **설명**: Record 수정
- **부수 효과**: L1 + L2 캐시 무효화

#### DELETE /api/records/:id
- **설명**: Record 삭제
- **부수 효과**: L1 + L2 캐시 무효화

**레코드 타입별 검증**:
- **A**: IPv4 주소 형식
- **AAAA**: IPv6 주소 형식
- **CNAME**: FQDN 형식
- **MX**: FQDN 형식 + priority 필수
- **TXT**: 임의 문자열
- **NS**: FQDN 형식
- **SOA**: 복잡한 형식 (별도 파싱)

**테스트**:
- CRUD 기본 동작
- 타입별 검증
- 필터링 (type 파라미터)
- 캐시 무효화 확인

---

### 4단계: Upstream 서버 관리 API (30분)

**파일**: `web/api_upstream.go`, `web/api_upstream_test.go`

**엔드포인트**:

#### GET /api/upstream
- **설명**: Upstream 서버 목록 조회 (priority 오름차순)
- **쿼리 파라미터**:
  - `enabled` (선택): true/false로 필터

#### GET /api/upstream/:id
- **설명**: Upstream 서버 상세 조회

#### POST /api/upstream
- **설명**: Upstream 서버 추가
- **요청**:
  ```json
  {
    "name": "Google DNS",
    "address": "8.8.8.8:53",
    "protocol": "udp",
    "priority": 1,
    "enabled": true
  }
  ```
- **검증**:
  - name, address, protocol 필수
  - protocol은 "udp", "tcp", "tcp-tls" 중 하나
  - address는 "host:port" 형식
  - priority는 음수 불가

#### PUT /api/upstream/:id
- **설명**: Upstream 서버 수정
- **부수 효과**: L2 캐시 무효화

#### DELETE /api/upstream/:id
- **설명**: Upstream 서버 삭제
- **부수 효과**: L2 캐시 무효화

#### POST /api/upstream/:id/test
- **설명**: Upstream 서버 연결 테스트
- **응답**:
  ```json
  {
    "success": true,
    "data": {
      "server": "8.8.8.8:53",
      "protocol": "udp",
      "response_time_ms": 15.3,
      "status": "healthy",
      "tested_at": "2026-01-31T10:00:00Z"
    }
  }
  ```
- **동작**:
  - 실제 DNS 쿼리 전송 (google.com A 레코드)
  - 응답 시간 측정
  - 타임아웃 5초

**테스트**:
- CRUD 기본 동작
- 연결 테스트 (모킹)
- 프로토콜 검증
- 캐시 무효화 확인

---

### 5단계: Cache Settings API (20분)

**파일**: `web/api_cache.go`, `web/api_cache_test.go`

**엔드포인트**:

#### GET /api/cache/settings
- **설명**: 캐시 설정 조회
- **응답**:
  ```json
  {
    "success": true,
    "data": {
      "id": 1,
      "enabled": true,
      "max_size": 10000,
      "default_ttl": 300,
      "min_ttl": 60,
      "max_ttl": 86400,
      "negative_ttl": 300,
      "prefetch_trigger": 0.9,
      "updated_at": "2026-01-31T10:00:00Z"
    }
  }
  ```

#### PUT /api/cache/settings
- **설명**: 캐시 설정 수정
- **요청**: 전체 설정 필드
- **부수 효과**: L1 캐시 재구성 (새 설정 적용)
- **검증**: storage/cache_settings.go의 검증 로직 재사용

#### POST /api/cache/clear
- **설명**: 전체 캐시 초기화
- **응답**: `{"success": true, "message": "캐시가 초기화되었습니다"}`
- **부수 효과**: L1 캐시 Clear() 호출

#### POST /api/cache/clear/:domain
- **설명**: 특정 도메인 캐시 무효화
- **응답**: `{"success": true, "message": "example.com. 캐시가 무효화되었습니다"}`
- **부수 효과**: L1 캐시 Delete(domain) 호출

#### GET /api/cache/stats
- **설명**: 캐시 통계 조회
- **응답**:
  ```json
  {
    "success": true,
    "data": {
      "l1_cache": {
        "hits": 123456,
        "misses": 12345,
        "evictions": 100,
        "size": 8523,
        "hit_rate": 0.909,
        "memory_mb": 25.6
      },
      "l2_cache": {
        "zone_hits": 5000,
        "zone_misses": 100,
        "record_hits": 8000,
        "record_misses": 200
      }
    }
  }
  ```
- **구현**:
  - L1: DNSCache.GetStats() 활용
  - L2: storage 레이어에 통계 카운터 추가 필요 (선택사항)
  - 메모리 사용량: runtime.MemStats 활용

**테스트**:
- 설정 조회/수정
- 캐시 초기화
- 도메인별 무효화
- 통계 조회

---

### 6단계: 통계 API (20분)

**파일**: `web/api_stats.go`, `web/api_stats_test.go`

**엔드포인트**:

#### GET /api/stats
- **설명**: 전체 서버 통계
- **응답**:
  ```json
  {
    "success": true,
    "data": {
      "server": {
        "uptime_seconds": 86400,
        "version": "0.1.0",
        "go_version": "go1.21.0"
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
        "l1_hit_rate": 0.859,
        "l2_hit_rate": 0.956,
        "upstream_forward_rate": 0.042
      },
      "database": {
        "zones": 10,
        "records": 523,
        "upstream_servers": 3
      }
    }
  }
  ```

**구현**:
- 서버 시작 시간 저장 (main.go)
- DNS 쿼리 카운터 추가 (handler.go에 atomic 카운터)
- 타입별/프로토콜별 카운터 (sync.Map)
- DB 카운트 쿼리

**테스트**:
- 통계 조회
- 카운터 증가 확인

---

### 7단계: 웹 서버 통합 (20분)

**파일**: `main.go` 수정, `web/server.go`

**기능**:
- Gin 서버 초기화
- DNS 서버와 웹 서버 동시 실행 (별도 고루틴)
- Graceful shutdown (둘 다)

**main.go 수정**:
```go
// 웹 서버 초기화
ginRouter := web.NewRouter(db, zoneStorage, recordStorage, upstreamStorage, dnsHandler)
webServer := web.NewServer(&cfg.Web, ginRouter)

// 웹 서버 시작
go func() {
    if err := webServer.Start(); err != nil {
        log.Fatalf("웹 서버 시작 실패: %v", err)
    }
}()

// DNS 서버 시작
if err := dnsServer.Start(); err != nil {
    log.Fatalf("DNS 서버 시작 실패: %v", err)
}

// Graceful shutdown
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
<-sigChan

webServer.Stop()
dnsServer.Stop()
```

**테스트**:
- 웹 서버 시작/종료
- 동시 실행 테스트
- Graceful shutdown

---

## API 응답 형식 표준

### 성공 응답
```json
{
  "success": true,
  "data": { /* 실제 데이터 */ }
}
```

### 에러 응답
```json
{
  "success": false,
  "error": "에러 메시지",
  "code": "VALIDATION_ERROR"  // 선택사항
}
```

### 페이지네이션 (Phase 2에서는 미구현, 향후 확장 가능)
```json
{
  "success": true,
  "data": [ /* 항목들 */ ],
  "pagination": {
    "page": 1,
    "per_page": 50,
    "total": 523
  }
}
```

---

## 테스트 전략

### 단위 테스트
- 각 API 핸들러별 테스트
- 요청 검증 테스트
- 에러 처리 테스트

### 통합 테스트
- `httptest.Server`로 실제 HTTP 요청/응답 테스트
- DB와 통합된 CRUD 흐름 테스트
- 캐시 무효화 확인

### 테스트 데이터
- `setupTestAPI(t)` 헬퍼 함수로 테스트 환경 구성
- 인메모리 DB 사용
- 각 테스트 후 롤백

---

## 의존성 추가

```bash
go get github.com/gin-gonic/gin@v1.10.0
```

---

## 디렉토리 구조

```
web/
├── router.go           # Gin 라우터 설정
├── router_test.go
├── middleware.go       # 미들웨어
├── middleware_test.go
├── server.go           # 웹 서버 (Start/Stop)
├── api_zones.go        # Zone API
├── api_zones_test.go
├── api_records.go      # Record API
├── api_records_test.go
├── api_upstream.go     # Upstream API
├── api_upstream_test.go
├── api_cache.go        # Cache API
├── api_cache_test.go
├── api_stats.go        # 통계 API
└── api_stats_test.go
```

---

## 완료 기준

- [x] 모든 API 엔드포인트 구현
- [x] 검증 로직 구현
- [x] 에러 처리
- [x] 테스트 작성 (각 API별 5개 이상)
- [x] 웹 서버와 DNS 서버 동시 실행
- [x] Graceful shutdown
- [x] Postman 콜렉션 작성 (선택사항)

---

## 예상 파일 및 라인 수

- `web/router.go`: ~100줄
- `web/middleware.go`: ~80줄
- `web/server.go`: ~60줄
- `web/api_zones.go`: ~200줄
- `web/api_records.go`: ~250줄
- `web/api_upstream.go`: ~250줄
- `web/api_cache.go`: ~150줄
- `web/api_stats.go`: ~100줄
- 테스트 파일들: ~1,500줄

**총 예상**: ~2,690줄

---

## Phase 2 완료 후 상태

- ✅ REST API로 Zone/Record/Upstream 관리 가능
- ✅ 웹 UI 없이도 curl/Postman으로 전체 관리 가능
- ✅ 캐시 설정 동적 변경
- ✅ 실시간 통계 조회
- ✅ 업스트림 서버 헬스 체크

**다음 Phase 3**: GSLB 구현
