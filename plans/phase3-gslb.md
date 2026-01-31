# Phase 3: GSLB (Global Server Load Balancing) 구현

## 목표

지역/네트워크 기반 DNS 응답 제어로 트래픽을 최적 서버로 라우팅

## 예상 소요 시간

3~4시간

## 기술 스택

- **GeoIP**: `github.com/oschwald/geoip2-golang` v1.11.0
- **GeoIP DB**: MaxMind GeoLite2-City.mmdb
- **CIDR 매칭**: 표준 라이브러리 `net` 패키지

## GSLB 개념

### 동작 원리
```
클라이언트 IP: 203.0.113.50 (한국)
쿼리: app.example.com A

1. GSLB 정책 확인: app.example.com에 대한 정책 존재?
2. 풀 매칭: 클라이언트 IP가 어느 풀에 속하는가?
   - korea-pool (match_type: geo_country, match_value: KR) ✓
3. 풀 멤버 선택:
   - 헬스 필터: 정상 서버만
   - 가중치 선택: weight 기반 랜덤
4. 응답: 1.2.3.4 (한국 서버)
```

### 매칭 타입

| 타입 | 설명 | 예시 |
|------|------|------|
| **cidr** | CIDR 블록 매칭 | `10.0.0.0/8`, `203.0.113.0/24` |
| **geo_country** | 국가 코드 | `KR`, `US`, `JP` |
| **geo_continent** | 대륙 코드 | `AS` (아시아), `EU` (유럽), `NA` (북미) |
| **default** | 기본 풀 (모든 IP 매칭) | `*` |

### 우선순위
- `priority` 필드로 풀 순서 결정 (낮을수록 먼저 매칭)
- 첫 번째 매칭된 풀 사용
- `fallback_pool=1`인 풀은 모든 풀 실패 시 사용

---

## 구현 순서

### 1단계: GSLB 정책/풀/멤버 CRUD + L2 캐시 (60분)

**파일**: `gslb/policy.go`, `gslb/pool.go`, `gslb/policy_test.go`

#### PolicyStorage
```go
type PolicyStorage struct {
    db    *storage.Database
    cache *PolicyCache  // L2 캐시 (5분 TTL)
}

// CRUD 메서드
- GetPolicy(id) (*model.GSLBPolicy, error)
- GetPolicyByDomain(domain, recordType) (*model.GSLBPolicy, error)  // L2 캐시 활용
- ListPolicies() ([]*model.GSLBPolicy, error)
- CreatePolicy(policy) (int64, error)  // 캐시 무효화
- UpdatePolicy(policy) error  // 캐시 무효화
- DeletePolicy(id) error  // 캐시 무효화
```

**PolicyCache**:
```go
type PolicyCache struct {
    mu       sync.RWMutex
    policies map[string]*model.GSLBPolicy  // key: "domain:type" (예: "app.example.com.:A")
    expiry   time.Time
    ttl      time.Duration
}
```

#### PoolStorage
```go
type PoolStorage struct {
    db    *storage.Database
    cache *PoolCache  // L2 캐시 (5분 TTL)
}

// CRUD 메서드
- GetPool(id) (*model.GSLBPool, error)
- GetPoolsByPolicy(policyID) ([]*model.GSLBPool, error)  // priority 오름차순, L2 캐시
- CreatePool(pool) (int64, error)
- UpdatePool(pool) error
- DeletePool(id) error

// 멤버 관리
- GetMember(id) (*model.GSLBMember, error)
- GetMembersByPool(poolID) ([]*model.GSLBMember, error)  // L2 캐시
- CreateMember(member) (int64, error)
- UpdateMember(member) error
- DeleteMember(id) error
```

**PoolCache**:
```go
type PoolCache struct {
    mu     sync.RWMutex
    pools  map[int64][]*model.GSLBPool  // key: policy_id
    members map[int64][]*model.GSLBMember  // key: pool_id
    expiry map[int64]time.Time
    ttl    time.Duration
}
```

**테스트**:
- CRUD 기본 동작
- L2 캐시 히트/미스
- 캐시 무효화
- 우선순위 정렬

---

### 2단계: GeoIP 래퍼 (30분)

**파일**: `gslb/geoip.go`, `gslb/geoip_test.go`

**기능**:
```go
type GeoIPResolver struct {
    cityDB *geoip2.Reader
}

func NewGeoIPResolver(dbPath string) (*GeoIPResolver, error)
func (g *GeoIPResolver) GetCountry(ip net.IP) (string, error)  // "KR", "US" 등
func (g *GeoIPResolver) GetContinent(ip net.IP) (string, error)  // "AS", "EU" 등
func (g *GeoIPResolver) Close() error
```

**동작**:
- MaxMind GeoLite2-City.mmdb 로드
- IP → 국가 코드 (ISO 3166-1 alpha-2)
- IP → 대륙 코드

**에러 처리**:
- DB 파일 없으면 경고 로그 + GeoIP 기능 비활성화
- 비활성화 시 geo_country, geo_continent 풀은 매칭 실패

**테스트**:
- 테스트용 GeoIP DB 생성 (모킹)
- 국가/대륙 조회
- DB 없을 때 처리

---

### 3단계: GSLB 매칭 엔진 (60분)

**파일**: `gslb/engine.go`, `gslb/engine_test.go`

**Engine 구조체**:
```go
type Engine struct {
    policyStorage *PolicyStorage
    poolStorage   *PoolStorage
    geoip         *GeoIPResolver
    healthStatus  *sync.Map  // key: member_id, value: bool (healthy)
}

func NewEngine(policyStorage, poolStorage, geoip, healthStatus) *Engine
func (e *Engine) Resolve(domain, qtype string, clientIP net.IP) ([]net.IP, uint32, error)
```

**Resolve 로직**:
```go
1. GetPolicyByDomain(domain, qtype)로 정책 조회 (L2 캐시 활용)
   - 없으면 nil 반환 (일반 DNS 처리)

2. GetPoolsByPolicy(policyID)로 풀 목록 조회 (L2 캐시 활용)
   - priority 오름차순으로 정렬됨

3. 각 풀에 대해 매칭 시도 (matchPool):
   - match_type에 따라 분기:
     - "cidr": CIDR 블록 매칭
     - "geo_country": GeoIP 국가 매칭
     - "geo_continent": GeoIP 대륙 매칭
     - "default": 항상 매칭
   - 첫 번째 매칭 성공 시 해당 풀 사용
   - fallback_pool은 마지막에만 고려

4. 선택된 풀의 멤버 조회 (L2 캐시 활용)

5. 헬스 필터 (filterHealthyMembers):
   - healthStatus에서 각 member_id 조회
   - healthy=true인 멤버만 선택
   - 모두 unhealthy면 모든 멤버 사용 (fail-open)

6. 가중치 선택 (selectByWeight):
   - 각 멤버의 weight 합산
   - 가중 랜덤 알고리즘으로 하나 선택
   - weight=0인 멤버는 제외

7. 선택된 멤버의 address를 IP로 파싱하여 반환
   - TTL은 정책의 ttl 사용 (기본 30초)
```

**헬퍼 함수**:
```go
func (e *Engine) matchPool(pool *model.GSLBPool, clientIP net.IP) bool
func (e *Engine) filterHealthyMembers(members []*model.GSLBMember) []*model.GSLBMember
func (e *Engine) selectByWeight(members []*model.GSLBMember) *model.GSLBMember
```

**CIDR 매칭**:
```go
func matchCIDR(clientIP net.IP, cidr string) bool {
    _, network, err := net.ParseCIDR(cidr)
    if err != nil {
        return false
    }
    return network.Contains(clientIP)
}
```

**가중치 선택 알고리즘**:
```go
totalWeight := 0
for _, member := range members {
    totalWeight += member.Weight
}

random := rand.Intn(totalWeight)
sum := 0
for _, member := range members {
    sum += member.Weight
    if random < sum {
        return member
    }
}
```

**테스트**:
- CIDR 매칭
- GeoIP 매칭 (모킹)
- 우선순위 기반 풀 선택
- 헬스 필터
- 가중치 선택
- 폴백 풀
- 전체 통합 테스트

---

### 4단계: DNS 핸들러 GSLB 통합 (30분)

**파일**: `dns/handler.go` 수정

**ServeDNS 수정**:
```go
func (h *Handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
    // ... L1 캐시 확인 ...

    // 클라이언트 IP 추출
    clientIP := extractClientIPFromRequest(w, r)

    // TODO 주석 제거: GSLB 정책 확인
    if h.gslbEngine != nil {
        ips, ttl, err := h.gslbEngine.Resolve(question.Name, qtype, clientIP)
        if err == nil && len(ips) > 0 {
            // GSLB 응답 생성
            m := buildGSLBResponse(question, ips, ttl)

            // L1 캐시 저장
            h.cache.Set(question.Name, qtype, m.Answer, int64(ttl), false)

            w.WriteMsg(m)
            return
        }
    }

    // ... 기존 Zone/Record 조회 로직 ...
}
```

**extractClientIPFromRequest**:
```go
func extractClientIPFromRequest(w dns.ResponseWriter, r *dns.Msg) net.IP {
    // 1. EDNS Client Subnet 확인
    if ip := ExtractClientIP(r); ip != nil {
        return ip
    }

    // 2. RemoteAddr에서 추출
    addr := w.RemoteAddr()
    if udpAddr, ok := addr.(*net.UDPAddr); ok {
        return udpAddr.IP
    }
    if tcpAddr, ok := addr.(*net.TCPAddr); ok {
        return tcpAddr.IP
    }

    return nil
}
```

**buildGSLBResponse**:
```go
func buildGSLBResponse(question dns.Question, ips []net.IP, ttl uint32) *dns.Msg {
    m := new(dns.Msg)
    m.SetReply(r)
    m.Authoritative = true

    for _, ip := range ips {
        if ip.To4() != nil {
            // IPv4
            rr := &dns.A{
                Hdr: dns.RR_Header{
                    Name:   question.Name,
                    Rrtype: dns.TypeA,
                    Class:  dns.ClassINET,
                    Ttl:    ttl,
                },
                A: ip,
            }
            m.Answer = append(m.Answer, rr)
        } else {
            // IPv6
            rr := &dns.AAAA{
                Hdr: dns.RR_Header{
                    Name:   question.Name,
                    Rrtype: dns.TypeAAAA,
                    Class:  dns.ClassINET,
                    Ttl:    ttl,
                },
                AAAA: ip,
            }
            m.Answer = append(m.Answer, rr)
        }
    }

    return m
}
```

**Handler 구조체 수정**:
```go
type Handler struct {
    cache         *DNSCache
    zoneStorage   *storage.ZoneStorage
    recordStorage *storage.RecordStorage
    resolver      *Resolver
    cacheSettings *storage.Database
    gslbEngine    *gslb.Engine  // 추가
}
```

**NewHandler 수정**:
```go
func NewHandler(..., gslbEngine *gslb.Engine) (*Handler, error) {
    // ...
    handler := &Handler{
        // ...
        gslbEngine: gslbEngine,
    }
    // ...
}
```

**테스트**:
- GSLB 응답 생성
- L1 캐시 저장 확인
- EDNS Client Subnet 처리
- 통합 테스트

---

### 5단계: GSLB API (40분)

**파일**: `web/api_gslb.go`, `web/api_gslb_test.go`

**엔드포인트**:

#### 정책 관리
```
GET    /api/gslb/policies           # 정책 목록
POST   /api/gslb/policies           # 정책 생성
GET    /api/gslb/policies/:id       # 정책 상세
PUT    /api/gslb/policies/:id       # 정책 수정
DELETE /api/gslb/policies/:id       # 정책 삭제
```

**POST /api/gslb/policies 요청**:
```json
{
  "name": "web-global",
  "domain": "app.example.com.",
  "record_type": "A",
  "ttl": 30,
  "enabled": true
}
```

#### 풀 관리
```
GET    /api/gslb/policies/:policy_id/pools  # 풀 목록
POST   /api/gslb/policies/:policy_id/pools  # 풀 생성
GET    /api/gslb/pools/:id                  # 풀 상세
PUT    /api/gslb/pools/:id                  # 풀 수정
DELETE /api/gslb/pools/:id                  # 풀 삭제
```

**POST /api/gslb/policies/:policy_id/pools 요청**:
```json
{
  "name": "korea-pool",
  "match_type": "geo_country",
  "match_value": "KR",
  "priority": 1,
  "fallback_pool": false
}
```

#### 멤버 관리
```
GET    /api/gslb/pools/:pool_id/members  # 멤버 목록
POST   /api/gslb/pools/:pool_id/members  # 멤버 추가
GET    /api/gslb/members/:id             # 멤버 상세
PUT    /api/gslb/members/:id             # 멤버 수정
DELETE /api/gslb/members/:id             # 멤버 삭제
```

**POST /api/gslb/pools/:pool_id/members 요청**:
```json
{
  "address": "1.2.3.4",
  "weight": 100,
  "enabled": true
}
```

**검증**:
- domain은 FQDN 형식
- record_type은 A 또는 AAAA
- match_type은 "cidr", "geo_country", "geo_continent", "default" 중 하나
- match_value는 match_type에 맞는 형식:
  - cidr: CIDR 형식 검증
  - geo_country: 2자리 국가 코드
  - geo_continent: 2자리 대륙 코드
  - default: "*"만 허용
- address는 IP 주소 형식
- weight는 0~100

**테스트**:
- CRUD 기본 동작
- 검증 로직
- 계층 구조 (Policy → Pool → Member)
- 캐시 무효화

---

### 6단계: main.go 통합 (20분)

**파일**: `main.go` 수정

**GSLB 엔진 초기화**:
```go
// GeoIP 리졸버 초기화 (선택사항)
var geoipResolver *gslb.GeoIPResolver
if cfg.GeoIP.CityDB != "" {
    var err error
    geoipResolver, err = gslb.NewGeoIPResolver(cfg.GeoIP.CityDB)
    if err != nil {
        log.Printf("GeoIP DB 로드 실패 (GeoIP 기능 비활성화): %v", err)
        geoipResolver = nil
    } else {
        defer geoipResolver.Close()
        log.Println("GeoIP 리졸버 초기화 완료")
    }
}

// GSLB Storage 초기화
policyStorage := gslb.NewPolicyStorage(db)
poolStorage := gslb.NewPoolStorage(db)

// 헬스 상태 맵 (Phase 5에서 구현, 임시로 빈 맵)
healthStatus := &sync.Map{}

// GSLB 엔진 초기화
gslbEngine := gslb.NewEngine(policyStorage, poolStorage, geoipResolver, healthStatus)
log.Println("GSLB 엔진 초기화 완료")

// DNS 핸들러 초기화 (gslbEngine 전달)
handler, err := dns.NewHandler(zoneStorage, recordStorage, resolver, db, gslbEngine)
```

**테스트**:
- GSLB 엔진 초기화
- GeoIP 없을 때 처리
- 통합 실행

---

## GeoIP 데이터베이스 다운로드

### GeoLite2 City 다운로드
MaxMind에서 무료로 제공:
1. https://dev.maxmind.com/geoip/geolite2-free-geolocation-data 접속
2. 회원가입 (무료)
3. GeoLite2-City.mmdb 다운로드
4. `./GeoLite2-City.mmdb`에 저장

### 라이선스
- GeoLite2는 Creative Commons Attribution-ShareAlike 4.0 라이선스
- 상업적 사용 가능

---

## 테스트 시나리오

### 시나리오 1: 지역 기반 라우팅
```bash
# 한국 IP에서 쿼리
dig @127.0.0.1 +subnet=203.0.113.1/24 app.example.com A
# 응답: 1.2.3.4 (한국 서버)

# 미국 IP에서 쿼리
dig @127.0.0.1 +subnet=198.51.100.1/24 app.example.com A
# 응답: 5.6.7.8 (미국 서버)
```

### 시나리오 2: CIDR 기반 라우팅
```bash
# 내부 네트워크 (10.0.0.0/8)
dig @127.0.0.1 +subnet=10.0.0.1/24 internal.example.com A
# 응답: 192.168.1.100 (내부 서버)

# 외부 네트워크
dig @127.0.0.1 +subnet=203.0.113.1/24 internal.example.com A
# 응답: NXDOMAIN (또는 public 서버)
```

### 시나리오 3: 가중치 기반 선택
```bash
# 동일한 풀에서 여러 번 쿼리
for i in {1..10}; do
  dig @127.0.0.1 app.example.com A +short
done

# 출력 (weight에 따라 분포):
# 1.2.3.4  (70% - weight 70)
# 5.6.7.8  (30% - weight 30)
```

---

## 예상 파일 및 라인 수

- `gslb/policy.go`: ~350줄
- `gslb/pool.go`: ~400줄
- `gslb/geoip.go`: ~80줄
- `gslb/engine.go`: ~300줄
- `web/api_gslb.go`: ~400줄
- 테스트 파일들: ~1,500줄

**총 예상**: ~3,030줄

---

## Phase 3 완료 후 상태

- ✅ 지역 기반 트래픽 라우팅
- ✅ CIDR 기반 내부/외부 분리
- ✅ 가중치 기반 로드 밸런싱
- ✅ 다층 풀 우선순위
- ✅ 폴백 풀 지원
- ✅ L2 캐시로 고성능 (GSLB 조회 < 1ms)

**다음 Phase 4**: 광고차단 기능
