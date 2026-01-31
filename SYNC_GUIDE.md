# Primary/Secondary 동기화 시스템 가이드

## 📌 개요

DNS-Go는 **Primary (상위) / Secondary (하위) 서버 동기화**를 지원합니다.

- **Primary 서버**: 메인 IDC에 배치, Read/Write 가능
- **Secondary 서버**: 서브 IDC에 배치, Read-Only, Primary 데이터 자동 동기화
- **동기화 방식**: Pull 방식 (Secondary가 Primary를 주기적으로 폴링)
- **동기화 주기**: 1초 (기본값, 설정 가능)

---

## 🏗️ 아키텍처

```
Primary (Main IDC)               Secondary (Sub IDC 1, 2, 3...)
┌─────────────────┐              ┌─────────────────┐
│  dns-go (R/W)   │              │  dns-go (R-Only)│
│  ┌───────────┐  │              │  ┌───────────┐  │
│  │ REST API  │  │◄────Poll─────┤  │Sync Worker│  │
│  │ (Zones)   │  │   (1초마다)  │  │ (1초 주기)│  │
│  │ (Records) │  │              │  └───────────┘  │
│  └───────────┘  │              │  ┌───────────┐  │
│  ┌───────────┐  │              │  │  SQLite   │  │
│  │  SQLite   │  │              │  │ (Replica) │  │
│  └───────────┘  │              │  └───────────┘  │
└─────────────────┘              └─────────────────┘
```

---

## ⚙️ 설정

### Primary 서버 설정

**config.primary.yaml**
```yaml
dns:
  listen: "0.0.0.0"
  port: 53
  nsid: "primary-dns"

web:
  listen: "0.0.0.0"
  port: 8080

database:
  path: "./dns-primary.db"

sync:
  mode: "primary"      # Primary 모드
  readonly: false      # Write 허용
```

### Secondary 서버 설정

**config.secondary.yaml**
```yaml
dns:
  listen: "0.0.0.0"
  port: 53
  nsid: "secondary-dns-01"

web:
  listen: "0.0.0.0"
  port: 8080

database:
  path: "./dns-secondary.db"

sync:
  mode: "secondary"                        # Secondary 모드
  primary_url: "http://primary-dns:8080"  # Primary 서버 주소
  interval: 1s                             # 동기화 주기 (1초)
  readonly: true                           # Write 차단
```

---

## 🚀 실행 방법

### 1. Primary 서버 시작

```bash
# 빌드
go build -o dns-server main.go

# Primary 실행
./dns-server --config=config.primary.yaml
```

**로그:**
```
DNS-Go v0.3.0 (빌드: 2026-01-31)
Primary 모드: Sync API 활성화
DNS 서버 시작 성공: 0.0.0.0:53
Web 서버 시작 성공: 0.0.0.0:8080
```

### 2. Secondary 서버 시작 (각 IDC에서)

```bash
# Secondary 실행
./dns-server --config=config.secondary.yaml
```

**로그:**
```
DNS-Go v0.3.0 (빌드: 2026-01-31)
Secondary 모드: Primary=http://primary-dns:8080, Interval=1s
Read-Only 모드 활성화 (Write API 차단)
DNS 서버 시작 성공: 0.0.0.0:53
Web 서버 시작 성공: 0.0.0.0:8080
Full Sync 시작...
Full Sync 완료: Version=5, Zones=10, Records=150
```

---

## 📡 동기화 동작

### 초기 동기화 (Full Sync)

Secondary 서버 시작 시 자동으로 Primary의 전체 데이터를 가져옵니다.

```
1. Secondary → Primary: GET /api/sync/full
2. Primary → Secondary: 전체 데이터 (Zones, Records, Upstreams)
3. Secondary: 로컬 DB에 데이터 저장
```

### 증분 동기화 (Incremental Sync)

1초마다 변경사항을 체크하고 동기화합니다.

```
1. Secondary → Primary: GET /api/sync/metadata
2. Primary → Secondary: {version: 100, checksum: "abc123"}
3. Secondary: 로컬 버전(90)과 비교
4. 버전 불일치 → Full Sync 실행
```

---

## 🧪 테스트

### 자동 테스트 스크립트

```bash
# 테스트 실행
./test-sync.sh
```

**테스트 시나리오:**
1. Primary 서버 시작
2. Primary에 Zone/Record 생성
3. Secondary 서버 시작 → 자동 Full Sync
4. Secondary에서 데이터 조회 (동기화 확인)
5. Secondary에 Write 시도 → 403 Forbidden
6. Primary에 새 Record 추가
7. 2초 대기
8. Secondary에서 새 Record 확인 (증분 동기화 확인)

### 수동 테스트

#### 1. Primary에 데이터 생성

```bash
# Zone 생성
curl -X POST http://primary:8080/api/zones \
  -H "Content-Type: application/json" \
  -d '{"name":"example.com","allow_fallback":true}'

# Record 생성
curl -X POST http://primary:8080/api/zones/1/records \
  -H "Content-Type: application/json" \
  -d '{"name":"www.example.com","type":"A","content":"10.0.0.100","ttl":300}'
```

#### 2. Secondary에서 동기화 확인

```bash
# 1초 대기 후 조회
sleep 2

# Zones 조회
curl http://secondary:8080/api/zones

# Records 조회
curl http://secondary:8080/api/records
```

#### 3. Secondary Write 차단 확인

```bash
# 403 Forbidden 반환
curl -X POST http://secondary:8080/api/zones \
  -H "Content-Type: application/json" \
  -d '{"name":"forbidden.com"}'

# 응답:
# {"error":"Read-Only mode (Secondary server)"}
```

---

## 🔍 Sync API (Primary만)

### GET /api/sync/metadata

Primary의 현재 상태 조회

**요청:**
```bash
curl http://primary:8080/api/sync/metadata
```

**응답:**
```json
{
  "version": 1234,
  "checksum": "abc123def456"
}
```

### GET /api/sync/full

전체 데이터 Export

**요청:**
```bash
curl http://primary:8080/api/sync/full
```

**응답:**
```json
{
  "version": 1234,
  "checksum": "abc123def456",
  "data": {
    "zones": [...],
    "records": [...],
    "upstream_servers": [...]
  }
}
```

### GET /api/sync/changes?since_version=X

변경사항 조회 (현재는 간단 구현)

**요청:**
```bash
curl http://primary:8080/api/sync/changes?since_version=1000
```

**응답:**
```json
{
  "current_version": 1234,
  "has_changes": true
}
```

---

## 📊 성능

### 동기화 지연

- **Full Sync**: 5~10초 (데이터 크기에 따라)
- **Incremental Sync**: 1~2초 (폴링 주기 + 네트워크)
- **변경 감지**: 즉시 (버전 비교)

### 처리량

- **Primary**: 제한 없음 (일반 Write 처리)
- **Secondary**: 초당 1회 폴링
- **동시 Secondary**: 제한 없음 (10~100대 가능)

### 네트워크 트래픽

- **변경 없음**: 1KB/초 (Metadata만)
- **변경 있음**: 전체 데이터 크기 (Full Sync)

---

## 🛠️ 운영 시나리오

### 1. 새 Secondary 추가

```bash
# 1. 새 IDC에 dns-server 배포
scp dns-server secondary-new:/usr/local/bin/

# 2. config.secondary.yaml 복사 및 수정
scp config.secondary.yaml secondary-new:/etc/dns-go/config.yaml
# primary_url을 Primary 주소로 수정

# 3. Secondary 시작
ssh secondary-new "systemctl start dns-server"

# 4. 로그 확인
ssh secondary-new "journalctl -u dns-server -f"
# → Full Sync 완료 메시지 확인
```

### 2. Primary 장애 시

- **Secondary는 계속 DNS 서비스 제공** (Read-Only)
- 동기화만 중단 (마지막 상태 유지)
- Primary 복구 후 자동으로 Incremental Sync 재개

### 3. Secondary 재시작

```bash
# Secondary 재시작
systemctl restart dns-server

# → 자동으로 Full Sync 실행
# → 최신 데이터로 업데이트
```

### 4. 동기화 주기 변경

**config.secondary.yaml**
```yaml
sync:
  interval: 5s  # 1초 → 5초로 변경
```

```bash
systemctl restart dns-server
```

---

## 🔐 보안 고려사항

### 1. Primary URL HTTPS 사용

```yaml
sync:
  primary_url: "https://primary-dns.example.com:8443"
```

### 2. 네트워크 격리

- Primary와 Secondary는 내부 네트워크에서만 통신
- 방화벽으로 8080 포트 외부 접근 차단

### 3. Read-Only 강제

```yaml
sync:
  readonly: true  # 필수!
```

---

## 🐛 트러블슈팅

### Secondary가 동기화 안 됨

**증상:**
```
Incremental Sync 실패: Primary 연결 실패: connection refused
```

**해결:**
1. Primary URL 확인: `curl http://primary:8080/api/sync/metadata`
2. 네트워크 확인: `ping primary`
3. 방화벽 확인: `telnet primary 8080`

### Full Sync 실패

**증상:**
```
Full Sync 실패: Zones 삽입 실패: UNIQUE constraint failed
```

**해결:**
```bash
# Secondary DB 초기화
rm -f dns-secondary.db
systemctl restart dns-server
```

### 동기화 지연

**증상:**
Primary에서 변경한 데이터가 Secondary에 10초 뒤 반영됨

**해결:**
```yaml
# 동기화 주기 단축
sync:
  interval: 500ms  # 0.5초
```

---

## 📈 모니터링

### 동기화 상태 확인

```bash
# Primary 버전
curl -s http://primary:8080/api/sync/metadata | jq .version

# Secondary 버전
sqlite3 dns-secondary.db "SELECT last_sync_version FROM sync_state WHERE id=1"
```

### 로그 모니터링

```bash
# Full Sync 로그
journalctl -u dns-server | grep "Full Sync"

# Sync 실패 로그
journalctl -u dns-server | grep "Sync 실패"
```

---

## ✅ 체크리스트

### Primary 서버
- [ ] `sync.mode = "primary"` 설정
- [ ] `sync.readonly = false` 설정
- [ ] Web API 8080 포트 접근 가능
- [ ] `/api/sync/metadata` 엔드포인트 정상 동작

### Secondary 서버
- [ ] `sync.mode = "secondary"` 설정
- [ ] `sync.primary_url` Primary 주소로 설정
- [ ] `sync.readonly = true` 설정 (필수!)
- [ ] `sync.interval` 적절한 값 (기본 1s)
- [ ] Full Sync 성공 로그 확인
- [ ] Write API 403 반환 확인

---

## 📚 참고

- **아키텍처 설계**: `.claude/docs/pull-sync-implementation.md`
- **구현 코드**:
  - `storage/sync_version.go` - 버전 관리
  - `sync/worker.go` - Secondary 동기화 워커
  - `web/api_sync.go` - Primary Sync API
- **설정 예시**:
  - `config.primary.yaml`
  - `config.secondary.yaml`
