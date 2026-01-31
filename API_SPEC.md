# DNS-GO API 명세서

## 목차
- [Zone API](#zone-api)
- [Record API](#record-api)
- [Upstream API](#upstream-api)
- [Cache API](#cache-api)
- [Statistics API](#statistics-api)
- [GSLB API](#gslb-api)
- [Adblock API](#adblock-api)
- [Health Check API](#health-check-api)

---

## Zone API

DNS Zone 관리를 위한 API

### 1. Zone 목록 조회
```
GET /api/zones
```
**용도**: 등록된 모든 Zone 목록을 조회

**응답 예시**:
```json
[
  {
    "id": 1,
    "name": "example.com",
    "soa_mname": "ns1.example.com",
    "soa_rname": "admin.example.com",
    "soa_serial": 2024010101,
    "soa_refresh": 3600,
    "soa_retry": 600,
    "soa_expire": 86400,
    "soa_minimum": 300,
    "enabled": true,
    "allow_fallback": false,
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z"
  }
]
```

### 2. Zone 상세 조회
```
GET /api/zones/:id
```
**용도**: 특정 Zone의 상세 정보 조회

**파라미터**:
- `id` (path): Zone ID

### 3. Zone 생성
```
POST /api/zones
```
**용도**: 새로운 DNS Zone 생성

**요청 바디**:
```json
{
  "name": "example.com",
  "soa_mname": "ns1.example.com",
  "soa_rname": "admin.example.com",
  "soa_serial": 2024010101,
  "soa_refresh": 3600,
  "soa_retry": 600,
  "soa_expire": 86400,
  "soa_minimum": 300,
  "enabled": true,
  "allow_fallback": false
}
```

**필수 필드**:
- `name`: Zone 도메인명

### 4. Zone 수정
```
PUT /api/zones/:id
```
**용도**: 기존 Zone 정보 수정

**파라미터**:
- `id` (path): Zone ID

### 5. Zone 삭제
```
DELETE /api/zones/:id
```
**용도**: Zone 삭제 (관련 레코드도 함께 삭제됨)

**파라미터**:
- `id` (path): Zone ID

---

## Record API

DNS Record 관리를 위한 API

### 1. 전체 Record 목록 조회
```
GET /api/records
```
**용도**: 모든 Zone의 Record를 조회 (zone_id 없이 전체 조회)

**응답 예시**:
```json
[
  {
    "id": 1,
    "zone_id": 1,
    "name": "www.example.com",
    "type": "A",
    "content": "192.168.1.1",
    "ttl": 300,
    "priority": 0,
    "enabled": true,
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z"
  }
]
```

### 2. Zone별 Record 목록 조회
```
GET /api/zones/:id/records
```
**용도**: 특정 Zone에 속한 Record 목록 조회

**파라미터**:
- `id` (path): Zone ID

### 3. Record 생성
```
POST /api/zones/:id/records
```
**용도**: Zone에 새로운 DNS Record 추가

**파라미터**:
- `id` (path): Zone ID

**요청 바디**:
```json
{
  "name": "www.example.com",
  "type": "A",
  "content": "192.168.1.1",
  "ttl": 300,
  "priority": 0,
  "enabled": true
}
```

**필수 필드**:
- `name`: 레코드명
- `type`: 레코드 타입 (A, AAAA, CNAME, MX, TXT 등)
- `content`: 레코드 값

**지원 타입**: A, AAAA, CNAME, MX, TXT, NS, PTR, SRV 등

### 4. Record 수정
```
PUT /api/records/:id
```
**용도**: 기존 Record 정보 수정

**파라미터**:
- `id` (path): Record ID

### 5. Record 삭제
```
DELETE /api/records/:id
```
**용도**: Record 삭제

**파라미터**:
- `id` (path): Record ID

---

## Upstream API

Upstream DNS 서버 관리를 위한 API

### 1. Upstream 목록 조회
```
GET /api/upstreams
```
**용도**: 등록된 모든 Upstream DNS 서버 목록 조회

**응답 예시**:
```json
[
  {
    "id": 1,
    "name": "Google DNS",
    "address": "8.8.8.8:53",
    "protocol": "udp",
    "priority": 1,
    "enabled": true,
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z"
  }
]
```

### 2. Upstream 생성
```
POST /api/upstreams
```
**용도**: 새로운 Upstream DNS 서버 등록

**요청 바디**:
```json
{
  "name": "Google DNS",
  "address": "8.8.8.8:53",
  "protocol": "udp",
  "priority": 1,
  "enabled": true
}
```

**필수 필드**:
- `name`: 서버명
- `address`: 서버 주소 (IP:포트)

**지원 프로토콜**:
- `udp`: UDP (기본값)
- `tcp`: TCP
- `tcp-tls`: DNS over TLS

### 3. Upstream 수정
```
PUT /api/upstreams/:id
```
**용도**: Upstream 서버 정보 수정

**파라미터**:
- `id` (path): Upstream ID

### 4. Upstream 삭제
```
DELETE /api/upstreams/:id
```
**용도**: Upstream 서버 삭제

**파라미터**:
- `id` (path): Upstream ID

### 5. Upstream 연결 테스트
```
POST /api/upstreams/:id/test
```
**용도**: Upstream 서버 연결 상태 및 응답 시간 테스트

**파라미터**:
- `id` (path): Upstream ID

**응답 예시**:
```json
{
  "status": "ok",
  "latency": 15,
  "rcode": "NOERROR",
  "answers": 1,
  "protocol": "udp"
}
```

---

## Cache API

DNS 쿼리 캐시 관리를 위한 API

### 1. 캐시 설정 조회
```
GET /api/cache/settings
```
**용도**: 현재 캐시 설정 조회

**응답 예시**:
```json
{
  "enabled": true,
  "max_size": 10000,
  "default_ttl": 300,
  "min_ttl": 60,
  "max_ttl": 3600,
  "negative_ttl": 60,
  "prefetch_trigger": 0.9
}
```

### 2. 캐시 설정 수정
```
PUT /api/cache/settings
```
**용도**: 캐시 설정 변경 (실시간 반영)

**요청 바디**:
```json
{
  "enabled": true,
  "max_size": 10000,
  "default_ttl": 300,
  "min_ttl": 60,
  "max_ttl": 3600,
  "negative_ttl": 60,
  "prefetch_trigger": 0.9
}
```

**설정 항목**:
- `enabled`: 캐시 활성화 여부
- `max_size`: 최대 캐시 엔트리 수
- `default_ttl`: 기본 TTL (초)
- `min_ttl`: 최소 TTL (초)
- `max_ttl`: 최대 TTL (초)
- `negative_ttl`: 실패 응답 캐시 TTL (초)
- `prefetch_trigger`: 사전 갱신 임계값 (0.0 ~ 1.0)

### 3. 전체 캐시 삭제
```
POST /api/cache/clear
```
**용도**: 모든 캐시 엔트리 삭제

### 4. 도메인별 캐시 삭제
```
POST /api/cache/clear/:domain
```
**용도**: 특정 도메인의 캐시만 삭제

**파라미터**:
- `domain` (path): 도메인명

### 5. 캐시 통계 조회
```
GET /api/cache/stats
```
**용도**: 캐시 히트/미스 통계 조회

**응답 예시**:
```json
{
  "size": 1234,
  "capacity": 10000,
  "hits": 5678,
  "misses": 1234,
  "evictions": 100
}
```

---

## Statistics API

DNS 쿼리 통계를 위한 API

### 1. 통계 조회
```
GET /api/stats
```
**용도**: DNS 쿼리 통계 및 캐시 통계 조회

**응답 예시**:
```json
{
  "queries": {
    "total": 10000,
    "l1_hits": 5000,
    "l1_misses": 3000,
    "upstream_hits": 2000
  },
  "cache": {
    "size": 1234,
    "capacity": 10000,
    "hits": 5678,
    "misses": 1234,
    "evictions": 100
  }
}
```

**통계 항목**:
- `total`: 전체 쿼리 수
- `l1_hits`: L1 캐시 히트 (내부 Zone)
- `l1_misses`: L1 캐시 미스
- `upstream_hits`: Upstream 쿼리 성공

---

## GSLB API

Global Server Load Balancing을 위한 API

### Policy API

DNS 기반 로드밸런싱 정책 관리

#### 1. Policy 목록 조회
```
GET /api/gslb/policies
```
**용도**: 등록된 모든 GSLB 정책 목록 조회

#### 2. Policy 생성
```
POST /api/gslb/policies
```
**용도**: 새로운 GSLB 정책 생성

**요청 바디**:
```json
{
  "name": "web-service-lb",
  "domain": "www.example.com",
  "record_type": "A",
  "ttl": 60,
  "enabled": true
}
```

**필수 필드**:
- `name`: 정책명
- `domain`: 대상 도메인

#### 3. Policy 수정
```
PUT /api/gslb/policies/:id
```
**용도**: 기존 정책 수정

#### 4. Policy 삭제
```
DELETE /api/gslb/policies/:id
```
**용도**: 정책 삭제 (관련 Pool, Member도 함께 삭제됨)

### Pool API

정책 내 서버 그룹(Pool) 관리

#### 1. Pool 목록 조회
```
GET /api/gslb/policies/:id/pools
```
**용도**: 특정 정책의 Pool 목록 조회

**파라미터**:
- `id` (path): Policy ID

#### 2. Pool 생성
```
POST /api/gslb/policies/:id/pools
```
**용도**: 정책에 새로운 Pool 추가

**요청 바디**:
```json
{
  "name": "asia-pool",
  "match_type": "geo",
  "match_value": "KR,JP,CN",
  "priority": 1,
  "fallback_pool": false
}
```

**매칭 타입**:
- `geo`: 지역 기반 매칭
- `subnet`: 서브넷 기반 매칭
- `asn`: AS Number 기반 매칭
- `default`: 기본 Pool (fallback)

#### 3. Pool 수정
```
PUT /api/gslb/pools/:id
```
**용도**: Pool 설정 수정

#### 4. Pool 삭제
```
DELETE /api/gslb/pools/:id
```
**용도**: Pool 삭제 (관련 Member도 함께 삭제됨)

### Member API

Pool 내 실제 서버(Member) 관리

#### 1. Member 목록 조회
```
GET /api/gslb/pools/:id/members
```
**용도**: Pool에 속한 Member 목록 조회

**파라미터**:
- `id` (path): Pool ID

#### 2. Member 생성
```
POST /api/gslb/pools/:id/members
```
**용도**: Pool에 새로운 서버 추가

**요청 바디**:
```json
{
  "address": "192.168.1.10",
  "weight": 100,
  "enabled": true
}
```

**필드**:
- `address`: 서버 IP 주소
- `weight`: 가중치 (로드밸런싱 비율)
- `enabled`: 활성화 여부

#### 3. Member 수정
```
PUT /api/gslb/members/:id
```
**용도**: Member 정보 수정 (가중치, 활성화 상태 등)

#### 4. Member 삭제
```
DELETE /api/gslb/members/:id
```
**용도**: Member 삭제

---

## Health Check API

GSLB Member의 헬스체크 설정 관리

### 1. 헬스 상태 조회
```
GET /api/gslb/health
```
**용도**: 모든 Member의 현재 헬스 상태 조회

**응답 예시**:
```json
{
  "1": {
    "healthy": true,
    "last_check": "2024-01-01T00:00:00Z",
    "consecutive_successes": 5,
    "consecutive_failures": 0
  }
}
```

### 2. 헬스체크 목록 조회
```
GET /api/gslb/healthchecks
```
**용도**: 등록된 모든 헬스체크 설정 조회

### 3. 헬스체크 생성
```
POST /api/gslb/members/:id/healthcheck
```
**용도**: Member에 헬스체크 설정 추가

**파라미터**:
- `id` (path): Member ID

**요청 바디**:
```json
{
  "check_type": "tcp",
  "target": "192.168.1.10:80",
  "interval_sec": 10,
  "timeout_sec": 5,
  "healthy_threshold": 3,
  "unhealthy_threshold": 2,
  "enabled": true
}
```

**체크 타입**:
- `tcp`: TCP 연결 체크
- `http`: HTTP 응답 체크
- `https`: HTTPS 응답 체크
- `icmp`: ICMP Ping 체크

**설정 항목**:
- `interval_sec`: 체크 주기 (초)
- `timeout_sec`: 타임아웃 (초)
- `healthy_threshold`: 정상 판정 임계값
- `unhealthy_threshold`: 장애 판정 임계값

### 4. 헬스체크 수정
```
PUT /api/gslb/healthchecks/:id
```
**용도**: 헬스체크 설정 수정

### 5. 헬스체크 삭제
```
DELETE /api/gslb/healthchecks/:id
```
**용도**: 헬스체크 설정 삭제

---

## Adblock API

광고/악성 도메인 차단 기능 관리

### 1. Adblock Source 목록 조회
```
GET /api/adblock/sources
```
**용도**: 등록된 차단 목록 소스 조회

**응답 예시**:
```json
[
  {
    "id": 1,
    "name": "AdGuard",
    "url": "https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
    "enabled": true,
    "last_sync": "2024-01-01T00:00:00Z",
    "domain_count": 50000,
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z"
  }
]
```

### 2. Adblock Source 생성
```
POST /api/adblock/sources
```
**용도**: 새로운 차단 목록 소스 추가

**요청 바디**:
```json
{
  "name": "AdGuard",
  "url": "https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
  "enabled": true
}
```

**필수 필드**:
- `name`: 소스명
- `url`: 차단 목록 URL

### 3. Adblock Source 수정
```
PUT /api/adblock/sources/:id
```
**용도**: 소스 정보 수정

### 4. Adblock Source 삭제
```
DELETE /api/adblock/sources/:id
```
**용도**: 소스 삭제 (관련 도메인도 함께 삭제됨)

### 5. Adblock Source 동기화
```
POST /api/adblock/sources/:id/sync
```
**용도**: 특정 소스의 차단 목록을 다운로드하여 업데이트

**파라미터**:
- `id` (path): Source ID

### 6. Adblock 통계 조회
```
GET /api/adblock/stats?limit=10
```
**용도**: 차단된 도메인 통계 조회 (상위 N개)

**쿼리 파라미터**:
- `limit`: 조회할 개수 (기본값: 10)

**응답 예시**:
```json
[
  {
    "domain": "ads.example.com",
    "block_count": 1234,
    "last_blocked": "2024-01-01T00:00:00Z"
  }
]
```

### 7. Adblock 상태 조회
```
GET /api/adblock/status
```
**용도**: Adblock 전체 상태 및 요약 정보 조회

**응답 예시**:
```json
{
  "sources": 3,
  "domain_count": 150000,
  "last_sync": "2024-01-01T00:00:00Z"
}
```

---

## 공통 응답 형식

### 성공 응답
```json
{
  "success": true,
  "data": { ... }
}
```

### 에러 응답
```json
{
  "success": false,
  "error": {
    "message": "에러 메시지",
    "code": "ERROR_CODE"
  }
}
```

### HTTP 상태 코드
- `200 OK`: 조회/수정 성공
- `201 Created`: 생성 성공
- `400 Bad Request`: 잘못된 요청
- `404 Not Found`: 리소스를 찾을 수 없음
- `500 Internal Server Error`: 서버 내부 오류
- `502 Bad Gateway`: Upstream 서버 오류

---

## 참고사항

### FQDN 형식
- API 요청 시 도메인명은 마침표 없이 입력 (`example.com`)
- 내부적으로 FQDN 형식으로 저장됨 (`example.com.`)
- API 응답 시 마침표가 제거되어 반환됨

### 기본값
- `enabled`: 대부분의 리소스는 생성 시 `true`가 기본값
- `protocol` (Upstream): `udp`가 기본값
- `ttl`: 설정하지 않으면 Zone의 SOA minimum 값 사용

### 제한사항
- 도메인명 길이: 253자 이하
- 레이블당 길이: 63자 이하
- 캐시 최대 크기: 설정 가능 (기본값: 10000)
