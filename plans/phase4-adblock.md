# Phase 4: 광고차단 기능 구현

## 목표

AdGuard 스타일 필터 기반 DNS 레벨 광고차단

## 예상 소요 시간

2~3시간

## 기술 스택

- **Bloom Filter**: `github.com/bits-and-blooms/bloom/v3`
- **HTTP Client**: 표준 라이브러리 `net/http`
- **파서**: 정규표현식 + 문자열 처리

## 광고차단 원리

```
1. 필터 파일 다운로드 (AdGuard, EasyList 등)
2. 도메인 추출 (||example.com^ 형식)
3. Bloom Filter + SQLite에 저장
4. DNS 쿼리 시 Bloom Filter로 빠른 체크
   - 있으면 → SQLite 확인 → 차단 (0.0.0.0 응답)
   - 없으면 → 정상 처리
```

---

## 구현 순서

### 1단계: Adblock Source CRUD (40분)

**파일**: `storage/adblock.go`, `storage/adblock_test.go`

#### AdblockStorage
```go
type AdblockStorage struct {
    db    *storage.Database
    cache *AdblockCache  // L2 캐시 (10분 TTL)
}

// 소스 관리
func (s *AdblockStorage) GetAdblockSource(id int64) (*model.AdblockSource, error)
func (s *AdblockStorage) ListAdblockSources() ([]*model.AdblockSource, error)
func (s *AdblockStorage) GetEnabledAdblockSources() ([]*model.AdblockSource, error)  // L2 캐시
func (s *AdblockStorage) CreateAdblockSource(source *model.AdblockSource) (int64, error)
func (s *AdblockStorage) UpdateAdblockSource(source *model.AdblockSource) error
func (s *AdblockStorage) DeleteAdblockSource(id int64) error

// 도메인 관리
func (s *AdblockStorage) AddBlockedDomain(sourceID int64, domain string) error
func (s *AdblockStorage) RemoveBlockedDomains(sourceID int64) error  // 소스의 모든 도메인 삭제
func (s *AdblockStorage) IsBlocked(domain string) (bool, error)
func (s *AdblockStorage) GetBlockedDomainCount() (int64, error)

// 통계
func (s *AdblockStorage) RecordBlockedQuery(domain, clientIP string) error
func (s *AdblockStorage) GetBlockedStats(limit int) ([]*BlockedStat, error)
```

**AdblockCache**:
```go
type AdblockCache struct {
    mu      sync.RWMutex
    sources []*model.AdblockSource
    expiry  time.Time
    ttl     time.Duration
}
```

**BlockedStat**:
```go
type BlockedStat struct {
    Domain string
    Count  int64
}
```

**테스트**:
- CRUD 기본 동작
- 활성화된 소스만 조회
- 도메인 추가/삭제
- 차단 확인
- 통계

---

### 2단계: 필터 파서 (40분)

**파일**: `adblock/loader.go`, `adblock/loader_test.go`

#### FilterLoader
```go
type FilterLoader struct {
    client *http.Client
}

func NewFilterLoader() *FilterLoader
func (l *FilterLoader) Download(url, lastModified string) ([]string, string, error)
func (l *FilterLoader) ParseRules(content string) []string
```

**Download 로직**:
```go
func (l *FilterLoader) Download(url, lastModified string) ([]string, string, error) {
    req, _ := http.NewRequest("GET", url, nil)

    // ETag/Last-Modified 활용 (변경되지 않았으면 다운로드 스킵)
    if lastModified != "" {
        req.Header.Set("If-Modified-Since", lastModified)
    }

    resp, err := l.client.Do(req)
    if err != nil {
        return nil, "", err
    }
    defer resp.Body.Close()

    // 304 Not Modified
    if resp.StatusCode == http.StatusNotModified {
        return nil, lastModified, nil
    }

    // 200 OK
    body, _ := io.ReadAll(resp.Body)
    rules := l.ParseRules(string(body))

    newLastModified := resp.Header.Get("Last-Modified")
    if newLastModified == "" {
        newLastModified = resp.Header.Get("ETag")
    }

    return rules, newLastModified, nil
}
```

**ParseRules 로직**:
```go
func (l *FilterLoader) ParseRules(content string) []string {
    var domains []string
    lines := strings.Split(content, "\n")

    for _, line := range lines {
        line = strings.TrimSpace(line)

        // 주석 무시
        if strings.HasPrefix(line, "!") || strings.HasPrefix(line, "#") {
            continue
        }

        // ||domain.com^ 형식 추출
        if strings.HasPrefix(line, "||") && strings.Contains(line, "^") {
            domain := extractDomain(line)
            if domain != "" {
                domains = append(domains, domain)
            }
        }
    }

    return domains
}

func extractDomain(rule string) string {
    // "||example.com^$third-party" → "example.com"
    rule = strings.TrimPrefix(rule, "||")

    // ^ 이전까지 추출
    if idx := strings.Index(rule, "^"); idx != -1 {
        rule = rule[:idx]
    }

    // 수정자 제거 ($로 시작)
    if idx := strings.Index(rule, "$"); idx != -1 {
        rule = rule[:idx]
    }

    // 도메인 정규화
    domain := strings.ToLower(rule)
    domain = strings.TrimSpace(domain)
    domain = strings.TrimSuffix(domain, ".")  // 끝의 . 제거

    // 유효성 검증
    if !isValidDomain(domain) {
        return ""
    }

    return domain
}

func isValidDomain(domain string) bool {
    // 간단한 검증: 알파벳, 숫자, 하이픈, 점만 허용
    matched, _ := regexp.MatchString(`^[a-z0-9.-]+$`, domain)
    return matched && len(domain) > 0
}
```

**지원 필터 형식**:
- `||example.com^` - 도메인 차단
- `||example.com^$third-party` - 수정자는 무시 (DNS 레벨에서는 불필요)
- `! Comment` - 주석 무시
- `# Comment` - 주석 무시

**미지원 필터**:
- `/.*ads.*/` - 정규표현식 (복잡도 높음)
- `@@||exception.com^` - 예외 규칙 (Phase 4에서는 미구현)

**테스트**:
- HTTP 다운로드 (모킹)
- ETag/Last-Modified 처리
- 필터 파싱
- 도메인 추출
- 정규화

---

### 3단계: Bloom Filter 기반 차단 엔진 (50분)

**파일**: `adblock/filter.go`, `adblock/filter_test.go`

#### AdblockFilter
```go
type AdblockFilter struct {
    storage     *storage.AdblockStorage
    bloomFilter *bloom.BloomFilter
    mu          sync.RWMutex
    enabled     bool
}

func NewAdblockFilter(storage *storage.AdblockStorage, enabled bool) *AdblockFilter
func (f *AdblockFilter) IsBlocked(domain string) bool
func (f *AdblockFilter) RebuildBloomFilter() error
func (f *AdblockFilter) GetStats() FilterStats
func (f *AdblockFilter) SetEnabled(enabled bool)
```

**Bloom Filter 생성**:
```go
func (f *AdblockFilter) RebuildBloomFilter() error {
    // 모든 차단 도메인 조회
    var domains []string
    rows, err := f.storage.db.Reader.Query("SELECT domain FROM adblock_domains")
    if err != nil {
        return err
    }
    defer rows.Close()

    for rows.Next() {
        var domain string
        rows.Scan(&domain)
        domains = append(domains, domain)
    }

    // Bloom Filter 생성
    // 파라미터: 예상 도메인 수, False Positive Rate
    n := uint(len(domains))
    if n == 0 {
        n = 1000  // 최소값
    }

    bf := bloom.NewWithEstimates(n, 0.01)  // 1% FP rate

    // 도메인 추가
    for _, domain := range domains {
        bf.Add([]byte(domain))
    }

    // 원자적 교체
    f.mu.Lock()
    f.bloomFilter = bf
    f.mu.Unlock()

    log.Printf("Bloom Filter 재생성: %d개 도메인, 메모리: %.2f MB",
        len(domains), float64(bf.Cap())/8/1024/1024)

    return nil
}
```

**IsBlocked 로직**:
```go
func (f *AdblockFilter) IsBlocked(domain string) bool {
    if !f.enabled {
        return false
    }

    // 도메인 정규화
    domain = normalizeDomain(domain)

    // Bloom Filter 체크 (Fast Path)
    f.mu.RLock()
    bf := f.bloomFilter
    f.mu.RUnlock()

    if bf == nil {
        return false
    }

    if !bf.Test([]byte(domain)) {
        // Bloom Filter에 없으면 확실히 차단 아님
        return false
    }

    // Bloom Filter에 있으면 DB 확인 (False Positive 검증)
    blocked, err := f.storage.IsBlocked(domain)
    if err != nil {
        log.Printf("차단 확인 실패: %v", err)
        return false
    }

    return blocked
}

func normalizeDomain(domain string) string {
    domain = strings.ToLower(domain)
    domain = strings.TrimSuffix(domain, ".")  // FQDN 처리
    return domain
}
```

**FilterStats**:
```go
type FilterStats struct {
    Enabled       bool
    DomainCount   int64
    BloomSizeMB   float64
    FalsePositive float64  // 0.01 (1%)
}
```

**메모리 효율**:
- 1M 도메인, FP 1% → 약 2.4MB
- 10M 도메인 → 약 24MB

**테스트**:
- Bloom Filter 생성
- 차단 확인 (히트/미스)
- False Positive 확인
- 재생성
- 동시성

---

### 4단계: 주기적 동기화 워커 (30min)

**파일**: `adblock/sync.go`, `adblock/sync_test.go`

#### Syncer
```go
type Syncer struct {
    storage *storage.AdblockStorage
    loader  *FilterLoader
    filter  *AdblockFilter
    ticker  *time.Ticker
    stopCh  chan struct{}
}

func NewSyncer(storage, loader, filter, interval) *Syncer
func (s *Syncer) Start()
func (s *Syncer) Stop()
func (s *Syncer) SyncAll() error
func (s *Syncer) SyncSource(source *model.AdblockSource) error
```

**Start 로직**:
```go
func (s *Syncer) Start() {
    s.ticker = time.NewTicker(s.interval)  // 기본 1시간
    s.stopCh = make(chan struct{})

    go func() {
        // 시작 시 즉시 동기화
        if err := s.SyncAll(); err != nil {
            log.Printf("초기 동기화 실패: %v", err)
        }

        // 주기적 동기화
        for {
            select {
            case <-s.ticker.C:
                if err := s.SyncAll(); err != nil {
                    log.Printf("동기화 실패: %v", err)
                }
            case <-s.stopCh:
                s.ticker.Stop()
                return
            }
        }
    }()
}
```

**SyncAll 로직**:
```go
func (s *Syncer) SyncAll() error {
    // 활성화된 소스 조회
    sources, err := s.storage.GetEnabledAdblockSources()
    if err != nil {
        return err
    }

    log.Printf("광고차단 동기화 시작: %d개 소스", len(sources))

    for _, source := range sources {
        if err := s.SyncSource(source); err != nil {
            log.Printf("소스 동기화 실패 (%s): %v", source.Name, err)
            continue  // 다른 소스는 계속 진행
        }
    }

    // Bloom Filter 재생성
    if err := s.filter.RebuildBloomFilter(); err != nil {
        return fmt.Errorf("Bloom Filter 재생성 실패: %w", err)
    }

    log.Println("광고차단 동기화 완료")
    return nil
}
```

**SyncSource 로직**:
```go
func (s *Syncer) SyncSource(source *model.AdblockSource) error {
    // 필터 다운로드
    domains, newLastModified, err := s.loader.Download(source.URL, source.LastModified)
    if err != nil {
        return err
    }

    // 변경 없음 (304 Not Modified)
    if domains == nil {
        log.Printf("소스 변경 없음: %s", source.Name)
        return nil
    }

    log.Printf("소스 다운로드 완료: %s (%d개 도메인)", source.Name, len(domains))

    // 트랜잭션 시작
    tx, err := s.storage.db.Writer.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()

    // 기존 도메인 삭제
    _, err = tx.Exec("DELETE FROM adblock_domains WHERE source_id = ?", source.ID)
    if err != nil {
        return err
    }

    // 새 도메인 삽입 (배치)
    stmt, err := tx.Prepare("INSERT INTO adblock_domains (domain, source_id) VALUES (?, ?)")
    if err != nil {
        return err
    }
    defer stmt.Close()

    for _, domain := range domains {
        if _, err := stmt.Exec(domain, source.ID); err != nil {
            log.Printf("도메인 삽입 실패: %s: %v", domain, err)
        }
    }

    // 소스 메타데이터 업데이트
    _, err = tx.Exec(
        "UPDATE adblock_sources SET last_sync = CURRENT_TIMESTAMP, last_modified = ?, rule_count = ? WHERE id = ?",
        newLastModified, len(domains), source.ID,
    )
    if err != nil {
        return err
    }

    // 커밋
    return tx.Commit()
}
```

**테스트**:
- 동기화 시작/중지
- 전체 동기화
- 소스별 동기화
- ETag 처리
- 트랜잭션

---

### 5단계: DNS 핸들러 Adblock 통합 (20분)

**파일**: `dns/handler.go` 수정

**ServeDNS 수정**:
```go
func (h *Handler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
    // ... L1 캐시 확인 ...

    // 광고차단 필터 체크
    if h.adblockFilter != nil && h.adblockFilter.IsBlocked(question.Name) {
        log.Printf("광고차단: %s", question.Name)

        // 통계 기록
        clientIP := extractClientIPFromRequest(w, r)
        h.adblockStorage.RecordBlockedQuery(question.Name, clientIP.String())

        // 0.0.0.0 응답
        m := buildBlockedResponse(r)

        // L1 캐시 저장 (Negative)
        h.cache.Set(question.Name, qtype, m.Answer, 300, true)

        w.WriteMsg(m)
        return
    }

    // ... GSLB, Zone/Record 조회 ...
}
```

**buildBlockedResponse**:
```go
func buildBlockedResponse(r *dns.Msg) *dns.Msg {
    m := new(dns.Msg)
    m.SetReply(r)
    m.Authoritative = true

    if len(r.Question) > 0 {
        q := r.Question[0]

        if q.Qtype == dns.TypeA {
            // 0.0.0.0 응답
            rr := &dns.A{
                Hdr: dns.RR_Header{
                    Name:   q.Name,
                    Rrtype: dns.TypeA,
                    Class:  dns.ClassINET,
                    Ttl:    300,
                },
                A: net.IPv4zero,
            }
            m.Answer = append(m.Answer, rr)
        } else if q.Qtype == dns.TypeAAAA {
            // :: 응답
            rr := &dns.AAAA{
                Hdr: dns.RR_Header{
                    Name:   q.Name,
                    Rrtype: dns.TypeAAAA,
                    Class:  dns.ClassINET,
                    Ttl:    300,
                },
                AAAA: net.IPv6zero,
            }
            m.Answer = append(m.Answer, rr)
        } else {
            // 다른 타입은 NXDOMAIN
            m.SetRcode(r, dns.RcodeNameError)
        }
    }

    return m
}
```

**Handler 구조체 수정**:
```go
type Handler struct {
    // ...
    adblockFilter  *adblock.AdblockFilter
    adblockStorage *storage.AdblockStorage
}
```

**테스트**:
- 차단 도메인 쿼리
- 0.0.0.0 응답
- L1 캐시 저장
- 통계 기록

---

### 6단계: Adblock API (30min)

**파일**: `web/api_adblock.go`, `web/api_adblock_test.go`

**엔드포인트**:

#### 소스 관리
```
GET    /api/adblock/sources           # 소스 목록
POST   /api/adblock/sources           # 소스 추가
GET    /api/adblock/sources/:id       # 소스 상세
PUT    /api/adblock/sources/:id       # 소스 수정
DELETE /api/adblock/sources/:id       # 소스 삭제
POST   /api/adblock/sources/:id/sync  # 수동 동기화 트리거
```

**POST /api/adblock/sources 요청**:
```json
{
  "name": "AdGuard DNS Filter",
  "url": "https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
  "enabled": true
}
```

**POST /api/adblock/sources/:id/sync 응답**:
```json
{
  "success": true,
  "data": {
    "source": "AdGuard DNS Filter",
    "domains_added": 15234,
    "synced_at": "2026-01-31T10:00:00Z"
  }
}
```

#### 통계
```
GET /api/adblock/stats    # 차단 통계
GET /api/adblock/status   # 필터 상태
```

**GET /api/adblock/stats 응답**:
```json
{
  "success": true,
  "data": {
    "total_blocked": 12345,
    "top_blocked": [
      {"domain": "ads.example.com", "count": 523},
      {"domain": "tracker.site.net", "count": 412}
    ],
    "sources": [
      {
        "name": "AdGuard DNS Filter",
        "rule_count": 15234,
        "last_sync": "2026-01-31T10:00:00Z"
      }
    ]
  }
}
```

**GET /api/adblock/status 응답**:
```json
{
  "success": true,
  "data": {
    "enabled": true,
    "domain_count": 1523456,
    "bloom_size_mb": 2.4,
    "sources_count": 3,
    "last_sync": "2026-01-31T10:00:00Z"
  }
}
```

**테스트**:
- CRUD 기본 동작
- 동기화 트리거
- 통계 조회
- 상태 조회

---

### 7단계: main.go 통합 (20분)

**파일**: `main.go` 수정

**Adblock 초기화**:
```go
// Adblock Storage 초기화
adblockStorage := storage.NewAdblockStorage(db)

// Bloom Filter 생성
adblockFilter := adblock.NewAdblockFilter(adblockStorage, cfg.Adblock.Enabled)

// 필터 로더
filterLoader := adblock.NewFilterLoader()

// 동기화 워커
syncer := adblock.NewSyncer(adblockStorage, filterLoader, adblockFilter, cfg.Adblock.SyncInterval)
syncer.Start()
defer syncer.Stop()

log.Println("광고차단 초기화 완료")

// DNS 핸들러에 전달
handler, err := dns.NewHandler(..., gslbEngine, adblockFilter, adblockStorage)
```

**테스트**:
- Adblock 초기화
- 동기화 워커 시작
- 통합 실행

---

## 기본 필터 소스

### 추천 필터 목록
1. **AdGuard DNS Filter** (가장 많이 사용)
   - URL: `https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt`
   - 약 50,000개 도메인

2. **EasyList** (광고 차단)
   - URL: `https://easylist.to/easylist/easylist.txt`
   - 약 70,000개 규칙

3. **EasyPrivacy** (추적 차단)
   - URL: `https://easylist.to/easylist/easyprivacy.txt`
   - 약 20,000개 규칙

### init_db.go 수정
```go
// 기본 광고차단 소스 추가
defaultSources := []struct {
    name string
    url  string
}{
    {
        "AdGuard DNS Filter",
        "https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
    },
}

for _, s := range defaultSources {
    _, err := db.Writer.Exec(
        "INSERT OR IGNORE INTO adblock_sources (name, url, enabled) VALUES (?, ?, 1)",
        s.name, s.url,
    )
    if err != nil {
        log.Printf("광고차단 소스 추가 실패: %v", err)
    } else {
        log.Printf("광고차단 소스 추가: %s", s.name)
    }
}
```

---

## 테스트 시나리오

### 시나리오 1: 광고 도메인 차단
```bash
# 차단된 도메인 쿼리
dig @127.0.0.1 ads.example.com A
# 응답: 0.0.0.0

# 정상 도메인 쿼리
dig @127.0.0.1 www.example.com A
# 응답: 실제 IP
```

### 시나리오 2: 필터 동기화
```bash
# 수동 동기화
curl -X POST http://localhost:8080/api/adblock/sources/1/sync

# 통계 확인
curl http://localhost:8080/api/adblock/stats
```

---

## 예상 파일 및 라인 수

- `storage/adblock.go`: ~300줄
- `adblock/filter.go`: ~200줄
- `adblock/loader.go`: ~250줄
- `adblock/sync.go`: ~180줄
- `web/api_adblock.go`: ~250줄
- 테스트 파일들: ~1,000줄

**총 예상**: ~2,180줄

---

## Phase 4 완료 후 상태

- ✅ AdGuard 스타일 필터 지원
- ✅ Bloom Filter로 고성능 차단 (< 1μs)
- ✅ 주기적 자동 동기화 (1시간)
- ✅ 차단 통계
- ✅ 0.0.0.0 응답 (브라우저 즉시 실패)
- ✅ L1 캐시에 차단 응답 저장 (반복 쿼리 빠름)

**다음 Phase 5**: 헬스체크 + 모니터링
