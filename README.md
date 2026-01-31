# DNS-Go

고성능 DNS 서버 with L1+L2 다층 캐싱

## 특징

### ✅ Phase 1 완료 (기본 DNS 서버 + 다층 캐시)

- **3-Tier 캐싱 아키텍처**
  - L1 캐시: DNS 응답 캐시 (70~90% 히트율 목표)
  - L2 캐시: Zone/Record/Upstream 캐시 (95%+ 히트율 목표)
  - SQLite: WAL 모드 + Reader/Writer 분리

- **DNS 서버**
  - UDP/TCP 동시 지원
  - miekg/dns 라이브러리 활용
  - EDNS Client Subnet 지원

- **업스트림 리졸버**
  - DB 기반 서버 관리
  - 우선순위 기반 선택
  - UDP/TCP/TCP-TLS 프로토콜 지원

- **캐시 기능**
  - TTL 기반 만료
  - Negative 캐싱 (NXDOMAIN)
  - Prefetch (TTL 90% 시점에 백그라운드 갱신)
  - LRU 제거

- **지원 레코드 타입**
  - A, AAAA, CNAME, MX, TXT, NS, SOA

## 빌드 및 실행

### 빌드
```bash
go build -o dns-go main.go
```

### 초기화 (업스트림 서버 추가)
```bash
go run init_db.go cmd_init.go
```

### 실행
```bash
sudo ./dns-go
```

**참고**: DNS 서버는 기본적으로 53번 포트를 사용하므로 root 권한이 필요합니다.

### 테스트용 실행 (비 root)
`config.yaml`에서 포트를 53이 아닌 다른 포트로 변경:
```yaml
dns:
  port: 5353
```

그 다음 실행:
```bash
./dns-go
```

## 설정

`config.yaml` 파일을 수정하여 서버 설정을 변경할 수 있습니다.

```yaml
dns:
  listen: "0.0.0.0"
  port: 53
  tcp: true
  udp: true

upstream:
  timeout: 5s

database:
  path: "./dns-go.db"

# 캐시 설정은 DB에서 관리 (cache_settings 테이블)
# REST API로 런타임에 변경 가능 (Phase 2에서 구현 예정)
```

## 테스트

### 전체 테스트 실행
```bash
go test ./...
```

### 커버리지 확인
```bash
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

**현재 커버리지**: 82.3%

### 패키지별 커버리지
- `config`: 100.0%
- `storage`: 83.7%
- `dns`: 79.1%

## 설정

### UDP 버퍼 크기 (EDNS0)

`config.yaml`에서 UDP 버퍼 크기를 조정할 수 있습니다:

```yaml
dns:
  udp_size: 1232  # EDNS0 UDP 버퍼 크기
```

**권장 값:**

| 크기 | 용도 | 설명 |
|------|------|------|
| 512  | RFC 1035 기본값 | ❌ DNSSEC 응답에 부족 (비권장) |
| 1232 | RFC 6891 권장 | ✅ IPv4 MTU 고려, DNSSEC 지원 (기본값) |
| 1410 | Cloudflare 권장 | ✅ IPv6 MTU 고려, 대부분의 네트워크에서 안전 |
| 4096 | 최대 실용값 | ⚠️ 일부 네트워크에서 문제 발생 가능 |

**이유:**
- DNSSEC 서명 포함 시 응답 크기가 1000~1500 bytes로 증가
- 512 bytes 초과 시 TC(Truncated) 플래그 발생 → TCP 재질의 (2배 느림)
- 1232+ bytes면 대부분의 DNSSEC 응답을 UDP로 처리 가능

## DNS 쿼리 테스트

### dig 명령어
```bash
# A 레코드 조회
dig @127.0.0.1 example.com A

# 업스트림 포워딩 테스트 (로컬 레코드 없는 경우)
dig @127.0.0.1 google.com A

# DNSSEC 응답 테스트 (큰 응답)
dig @127.0.0.1 +dnssec cloudflare.com A

# TCP 프로토콜
dig @127.0.0.1 example.com A +tcp
```

### nslookup 명령어
```bash
nslookup example.com 127.0.0.1
```

## 데이터베이스 구조

SQLite 데이터베이스 (`dns-go.db`)에 다음 테이블이 생성됩니다:

- `zones` - DNS Zone 관리
- `records` - DNS 레코드
- `gslb_policies` - GSLB 정책 (Phase 3)
- `gslb_pools` - GSLB 풀 (Phase 3)
- `gslb_members` - GSLB 멤버 (Phase 3)
- `health_checks` - 헬스체크 설정 (Phase 5)
- `cache_settings` - 캐시 설정 (Singleton)
- `upstream_servers` - 업스트림 리졸버
- `adblock_sources` - 광고차단 필터 소스 (Phase 4)
- `adblock_domains` - 광고차단 도메인 (Phase 4)
- `adblock_stats` - 광고차단 통계 (Phase 4)

## 성능

### 예상 QPS (벤치마크 예정)

| 시나리오 | QPS | 레이턴시 (P99) |
|----------|-----|----------------|
| L1 캐시 히트 | 80K ~ 150K | < 0.3ms |
| L2 캐시 히트 | 30K ~ 50K | < 1ms |
| SQLite 조회 | 5K ~ 10K | 2 ~ 5ms |
| 업스트림 포워딩 | 1K ~ 3K | 10 ~ 50ms |

### 메모리 사용량 (예상)
- Go 런타임: ~50MB
- L1 캐시 (10K 항목): ~30MB
- L2 캐시: ~20MB
- SQLite 캐시: ~50MB
- **총합**: ~150MB (Adblock/GSLB 미포함)

## 개발 로드맵

### ✅ Phase 1: 기본 DNS 서버 + 다층 캐시 (완료)
- [x] 설정 파일 파싱
- [x] SQLite 스키마 마이그레이션
- [x] Zone/Record/Upstream CRUD + L2 캐시
- [x] Cache Settings CRUD
- [x] L1 DNS 응답 캐시 (LRU, TTL, Prefetch)
- [x] DNS 서버 (UDP/TCP)
- [x] DNS 쿼리 핸들러
- [x] 업스트림 리졸버

### 🚧 Phase 2: REST API + 캐시 관리 (다음 단계)
- [ ] Gin 라우터 + 미들웨어
- [ ] Zone 관리 API
- [ ] Record 관리 API
- [ ] Upstream 서버 관리 API
- [ ] Cache Settings API
- [ ] 통계 API

### 🚧 Phase 3: GSLB 구현
- [ ] GSLB 정책/풀/멤버 CRUD
- [ ] GSLB 매칭 엔진 (CIDR, GeoIP)
- [ ] GeoIP 래퍼
- [ ] EDNS Client Subnet 처리
- [ ] GSLB API

### 🚧 Phase 4: 광고차단 기능
- [ ] Adblock Source CRUD
- [ ] AdGuard 필터 파서
- [ ] Bloom Filter 기반 차단 엔진
- [ ] HTTP 다운로더
- [ ] 주기적 동기화 워커
- [ ] Adblock API

### 🚧 Phase 5: 헬스체크 + 모니터링
- [ ] 헬스체크 워커 (HTTP/TCP)
- [ ] 헬스 상태 기반 풀 멤버 필터링
- [ ] 헬스 상태 API
- [ ] 쿼리 로깅 및 통계

## 테스트 결과

전체 68개 테스트가 통과했습니다:

```
✅ config: 3 tests (100.0% coverage)
✅ storage: 50 tests (83.7% coverage)
✅ dns: 25 tests (79.1% coverage)
```

## 기술 스택

| 컴포넌트 | 라이브러리 | 버전 |
|----------|-----------|------|
| DNS 서버 | github.com/miekg/dns | v1.1.62 |
| 저장소 | modernc.org/sqlite | v1.44.3 |
| 설정 | gopkg.in/yaml.v3 | v3.0.1 |
| 테스트 | github.com/stretchr/testify | v1.11.1 |

## 라이선스

MIT License

## 저자

Claude Code (Anthropic)
