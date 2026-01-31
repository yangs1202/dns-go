# DNS-Go API 스펙

## 목차
- [GSLB API](#gslb-api)
  - [Policy](#policy)
  - [Pool](#pool)
  - [Member](#member)
  - [HealthCheck](#healthcheck)
- [Zone API](#zone-api)
- [Record API](#record-api)

---

## GSLB API

### Policy

#### 📍 Policy 생성
```
POST /api/gslb/policies
```

**Request Body:**
```json
{
  "name": "string",        // 필수 - Policy 이름
  "domain": "string",      // 필수 - 도메인 (예: "lb.gslb.yangs.sh")
  "record_type": "string", // 선택 - "A" 또는 "AAAA" (기본값: "A")
  "ttl": 300,              // 선택 - TTL (초 단위, 기본값: 300)
  "enabled": true          // 선택 - 활성화 여부 (기본값: true)
}
```

**Response:**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "name": "My GSLB Policy",
    "domain": "lb.gslb.yangs.sh.",
    "record_type": "A",
    "ttl": 300,
    "enabled": true,
    "created_at": "2026-01-31T..."
  }
}
```

#### 📍 Policy 목록 조회
```
GET /api/gslb/policies
```

#### 📍 Policy 수정
```
PUT /api/gslb/policies/:id
```

#### 📍 Policy 삭제
```
DELETE /api/gslb/policies/:id
```

---

### Pool

#### 📍 Pool 생성
```
POST /api/gslb/policies/:policy_id/pools
```

**Request Body:**
```json
{
  "name": "string",           // 필수 - Pool 이름
  "match_type": "string",     // 필수 - "subnet", "geo", "default"
  "match_value": "string",    // 선택 - 매칭 값
  "priority": 10,             // 선택 - 우선순위 (낮을수록 높음)
  "fallback_pool": false      // 선택 - Fallback 여부 (기본값: false)
}
```

**match_type 설명:**
- `"subnet"`: IP 대역 매칭
  - `match_value`: CIDR (예: `"10.97.0.0/16"`)
- `"geo"`: 지리적 위치 매칭
  - `match_value`: 국가 코드 (예: `"KR"`, `"US"`)
- `"default"`: 기본 Pool (항상 매칭)
  - `match_value`: 빈 문자열 또는 생략

**Response:**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "policy_id": 1,
    "name": "Korea Pool",
    "match_type": "subnet",
    "match_value": "10.97.0.0/16",
    "priority": 10,
    "fallback_pool": false
  }
}
```

#### 📍 Pool 목록 조회
```
GET /api/gslb/policies/:policy_id/pools
```

#### 📍 Pool 수정
```
PUT /api/gslb/pools/:id
```

#### 📍 Pool 삭제
```
DELETE /api/gslb/pools/:id
```

---

### Member

#### 📍 Member 생성
```
POST /api/gslb/pools/:pool_id/members
```

**Request Body:**
```json
{
  "address": "string",   // 필수 - IP 주소 (포트 제외!)
  "weight": 100,         // 선택 - 가중치 (기본값: 0)
  "enabled": true        // 선택 - 활성화 여부 (기본값: true)
}
```

**⚠️ 중요:**
- `address`는 **순수 IP 주소만** 입력 (포트 포함 불가)
- ✅ 올바른 예: `"10.97.11.18"`, `"2001:db8::1"`
- ❌ 잘못된 예: `"10.97.11.18:80"`, `"10.97.11.18:443"`

**Response:**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "pool_id": 1,
    "address": "10.97.11.18",
    "weight": 100,
    "enabled": true
  }
}
```

#### 📍 Member 목록 조회
```
GET /api/gslb/pools/:pool_id/members
```

#### 📍 Member 수정
```
PUT /api/gslb/members/:id
```

#### 📍 Member 삭제
```
DELETE /api/gslb/members/:id
```

---

### HealthCheck

#### 📍 HealthCheck 생성
```
POST /api/gslb/members/:member_id/healthcheck
```

**Request Body:**
```json
{
  "check_type": "string",           // 필수 - "http" 또는 "tcp"
  "target": "string",               // 필수 - 체크 대상 URL/주소
  "interval_sec": 10,               // 선택 - 체크 간격 (초, 기본값: 10)
  "timeout_sec": 5,                 // 선택 - 타임아웃 (초, 기본값: 5)
  "healthy_threshold": 2,           // 선택 - 정상 판정 임계값 (기본값: 2)
  "unhealthy_threshold": 3,         // 선택 - 비정상 판정 임계값 (기본값: 3)
  "enabled": true                   // 선택 - 활성화 여부 (기본값: true)
}
```

**check_type 설명:**

| check_type | Target 형식 | 설명 |
|------------|------------|------|
| `http` | `http://IP/path` 또는 `https://IP/path` | HTTP/HTTPS GET 요청 (scheme 자동 감지) |
| `tcp` | `IP:PORT` | TCP 포트 연결 테스트 |

**http 타입 예시:**
```json
{
  "check_type": "http",
  "target": "https://10.97.11.18/health",
  "interval_sec": 10,
  "timeout_sec": 5,
  "healthy_threshold": 2,
  "unhealthy_threshold": 3,
  "enabled": true
}
```
- Target URL의 `https://`를 보고 자동으로 HTTPS 요청
- 성공 조건: HTTP 상태 코드 200-299
- TLS 인증서 검증 비활성화 (`InsecureSkipVerify: true`)

**tcp 타입 예시:**
```json
{
  "check_type": "tcp",
  "target": "10.97.11.18:443",
  "interval_sec": 5,
  "timeout_sec": 3,
  "healthy_threshold": 2,
  "unhealthy_threshold": 3,
  "enabled": true
}
```
- TCP 연결 성공 여부만 확인
- 데이터 교환 없음

**Response:**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "member_id": 1,
    "check_type": "http",
    "target": "https://10.97.11.18/health",
    "interval_sec": 10,
    "timeout_sec": 5,
    "healthy_threshold": 2,
    "unhealthy_threshold": 3,
    "enabled": true
  }
}
```

#### 📍 HealthCheck 목록 조회
```
GET /api/gslb/healthchecks
```

#### 📍 HealthCheck 수정
```
PUT /api/gslb/healthchecks/:id
```

#### 📍 HealthCheck 삭제
```
DELETE /api/gslb/healthchecks/:id
```

#### 📍 헬스 상태 조회
```
GET /api/gslb/health
```

**Response:**
```json
{
  "success": true,
  "data": {
    "1": {
      "healthy": true,
      "last_check": "2026-01-31T...",
      "consecutive_oks": 5,
      "consecutive_fails": 0,
      "last_error": ""
    }
  }
}
```

---

## Zone API

#### 📍 Zone 생성
```
POST /api/zones
```

**Request Body:**
```json
{
  "name": "string",              // 필수 - Zone 이름 (예: "example.com")
  "soa_mname": "string",         // 선택 - Primary NS
  "soa_rname": "string",         // 선택 - Admin email
  "soa_serial": 1,               // 선택 - Serial number
  "soa_refresh": 3600,           // 선택 - Refresh interval
  "soa_retry": 900,              // 선택 - Retry interval
  "soa_expire": 86400,           // 선택 - Expire time
  "soa_minimum": 300,            // 선택 - Minimum TTL
  "enabled": true,               // 선택 - 활성화 여부
  "allow_fallback": true         // 선택 - Fallback 허용 여부
}
```

#### 📍 Zone 목록 조회
```
GET /api/zones
```

#### 📍 Zone 조회
```
GET /api/zones/:id
```

#### 📍 Zone 수정
```
PUT /api/zones/:id
```

#### 📍 Zone 삭제
```
DELETE /api/zones/:id
```

---

## Record API

#### 📍 Record 생성
```
POST /api/zones/:zone_id/records
```

**Request Body:**
```json
{
  "name": "string",        // 필수 - 레코드 이름 (예: "www.example.com")
  "type": "string",        // 필수 - "A", "AAAA", "CNAME", "MX", "TXT", etc.
  "content": "string",     // 필수 - 레코드 값
  "ttl": 300,              // 선택 - TTL (기본값: 300)
  "priority": 0,           // 선택 - MX 우선순위 (기본값: 0)
  "enabled": true          // 선택 - 활성화 여부 (기본값: true)
}
```

**Response:**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "zone_id": 1,
    "zone": {
      "id": 1,
      "name": "example.com",
      ...
    },
    "name": "www.example.com",
    "type": "A",
    "content": "192.168.1.1",
    "ttl": 300,
    "priority": 0,
    "enabled": true,
    "created_at": "2026-01-31T...",
    "updated_at": "2026-01-31T..."
  }
}
```

**📌 참고:**
- 레코드 응답에는 해당 Zone의 전체 정보가 포함됩니다
- CNAME 레코드의 경우 자동으로 FQDN 형식으로 변환됩니다

#### 📍 전체 Record 목록 조회
```
GET /api/records
```

#### 📍 Zone별 Record 목록 조회
```
GET /api/zones/:zone_id/records
```

#### 📍 Record 수정
```
PUT /api/records/:id
```

**⚠️ 주의:**
- `zone_id`는 변경할 수 없습니다 (기존 zone_id 유지)
- Zone을 변경하려면 삭제 후 재생성 필요

#### 📍 Record 삭제
```
DELETE /api/records/:id
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
  "error": "에러 메시지",
  "code": "ERROR_CODE"
}
```

**HTTP 상태 코드:**
- `200 OK`: 조회/수정/삭제 성공
- `201 Created`: 생성 성공
- `400 Bad Request`: 잘못된 요청
- `403 Forbidden`: Read-Only 모드 (Secondary 서버)
- `404 Not Found`: 리소스 없음
- `500 Internal Server Error`: 서버 에러

---

## 예제 시나리오

### GSLB 설정 전체 흐름

```bash
# 1. Policy 생성
curl -X POST http://localhost:8080/api/gslb/policies \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Global LB",
    "domain": "api.example.com",
    "record_type": "A",
    "ttl": 60
  }'
# Response: {"success":true,"data":{"id":1,...}}

# 2. Pool 생성 (Korea)
curl -X POST http://localhost:8080/api/gslb/policies/1/pools \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Korea Pool",
    "match_type": "subnet",
    "match_value": "10.0.0.0/8",
    "priority": 10
  }'
# Response: {"success":true,"data":{"id":1,...}}

# 3. Pool 생성 (Default)
curl -X POST http://localhost:8080/api/gslb/policies/1/pools \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Default Pool",
    "match_type": "default",
    "priority": 100,
    "fallback_pool": true
  }'
# Response: {"success":true,"data":{"id":2,...}}

# 4. Member 추가 (Korea Pool)
curl -X POST http://localhost:8080/api/gslb/pools/1/members \
  -H "Content-Type: application/json" \
  -d '{
    "address": "10.0.1.10",
    "weight": 100,
    "enabled": true
  }'
# Response: {"success":true,"data":{"id":1,...}}

# 5. HealthCheck 추가
curl -X POST http://localhost:8080/api/gslb/members/1/healthcheck \
  -H "Content-Type: application/json" \
  -d '{
    "check_type": "http",
    "target": "https://10.0.1.10/health",
    "interval_sec": 10,
    "timeout_sec": 5,
    "healthy_threshold": 2,
    "unhealthy_threshold": 3,
    "enabled": true
  }'
# Response: {"success":true,"data":{"id":1,...}}

# 6. DNS 쿼리 테스트
dig @localhost api.example.com
# 10.0.0.0/8 대역에서 쿼리하면 10.0.1.10 반환
# 다른 대역에서 쿼리하면 Default Pool의 IP 반환
```

---

## 변경 이력

### 2026-01-31
- Member.Address는 순수 IP만 허용 (포트 제외)
- HealthCheck.Target에 포트/경로 포함
- check_type "https" 제거, "http"로 통일 (URL scheme 자동 감지)
- Record 응답에 Zone 정보 포함
- CNAME 레코드 자동 해석 및 A/AAAA 레코드 추가 조회
- AdBlock API last_sync, last_modified를 ISO 8601 형식으로 통일
