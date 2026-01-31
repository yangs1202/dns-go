# GSLB API 스펙

## 목차
- [개요](#개요)
- [Policy API](#policy-api)
- [Pool API](#pool-api)
- [Member API](#member-api)
- [HealthCheck API](#healthcheck-api)
- [전체 설정 예제](#전체-설정-예제)

---

## 개요

GSLB (Global Server Load Balancing)는 클라이언트의 위치, IP 대역 등에 따라 최적의 서버 IP를 반환하는 DNS 기반 로드밸런싱 시스템입니다.

### 구조
```
Policy (도메인)
└── Pool (매칭 조건)
    └── Member (실제 IP)
        └── HealthCheck (헬스체크)
```

### 동작 흐름
1. 클라이언트가 DNS 쿼리 (`api.example.com`)
2. Policy 매칭 확인
3. Pool 조건 매칭 (IP 대역, 지역 등)
4. 활성 Member 선택 (가중치 기반)
5. IP 주소 반환

---

## Policy API

Policy는 GSLB가 적용될 도메인과 기본 설정을 정의합니다.

### 📍 Policy 생성
```http
POST /api/gslb/policies
```

**Request Body:**
```json
{
  "name": "string",        // 필수 - Policy 이름
  "domain": "string",      // 필수 - 도메인 (예: "api.example.com")
  "record_type": "string", // 선택 - "A" 또는 "AAAA" (기본값: "A")
  "ttl": 300,              // 선택 - TTL (초 단위, 기본값: 300)
  "enabled": true          // 선택 - 활성화 여부 (기본값: true)
}
```

**Example:**
```bash
curl -X POST http://localhost:8080/api/gslb/policies \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Global API LB",
    "domain": "api.example.com",
    "record_type": "A",
    "ttl": 60
  }'
```

**Response:**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "name": "Global API LB",
    "domain": "api.example.com.",
    "record_type": "A",
    "ttl": 60,
    "enabled": true,
    "created_at": "2026-01-31T14:00:00Z"
  }
}
```

### 📍 Policy 목록 조회
```http
GET /api/gslb/policies
```

**Response:**
```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "name": "Global API LB",
      "domain": "api.example.com.",
      "record_type": "A",
      "ttl": 60,
      "enabled": true,
      "created_at": "2026-01-31T14:00:00Z"
    }
  ]
}
```

### 📍 Policy 수정
```http
PUT /api/gslb/policies/:id
```

### 📍 Policy 삭제
```http
DELETE /api/gslb/policies/:id
```

---

## Pool API

Pool은 특정 조건(IP 대역, 지역 등)에 매칭되는 서버 그룹입니다.

### 📍 Pool 생성
```http
POST /api/gslb/policies/:policy_id/pools
```

**Request Body:**
```json
{
  "name": "string",           // 필수 - Pool 이름
  "match_type": "string",     // 필수 - "subnet", "geo", "default"
  "match_value": "string",    // 선택 - 매칭 값
  "priority": 10,             // 선택 - 우선순위 (낮을수록 높음, 기본값: 10)
  "fallback_pool": false      // 선택 - Fallback 여부 (기본값: false)
}
```

### Match Type 상세 설명

#### 1. `subnet` - IP 대역 매칭
클라이언트 IP가 특정 대역에 속하는지 확인합니다.

**예시:**
```json
{
  "name": "Korea Internal Network",
  "match_type": "subnet",
  "match_value": "10.97.0.0/16",
  "priority": 10
}
```
- `10.97.0.0/16` 대역에서 오는 쿼리에 매칭
- 내부망 사용자를 내부 서버로 라우팅

#### 2. `geo` - 지리적 위치 매칭
클라이언트의 국가/대륙 코드로 매칭합니다.

**예시:**
```json
{
  "name": "Korea Users",
  "match_type": "geo",
  "match_value": "KR",
  "priority": 10
}
```
- 한국 사용자를 한국 서버로 라우팅
- GeoIP 데이터베이스 필요

#### 3. `default` - 기본 Pool
다른 Pool에 매칭되지 않을 때 사용됩니다.

**예시:**
```json
{
  "name": "Global Default",
  "match_type": "default",
  "match_value": "",
  "priority": 100,
  "fallback_pool": true
}
```
- 항상 매칭됨
- 가장 낮은 우선순위 권장

### 📍 Pool 목록 조회
```http
GET /api/gslb/policies/:policy_id/pools
```

**Example:**
```bash
curl http://localhost:8080/api/gslb/policies/1/pools
```

**Response:**
```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "policy_id": 1,
      "name": "Korea Subnet",
      "match_type": "subnet",
      "match_value": "10.97.0.0/16",
      "priority": 10,
      "fallback_pool": false
    },
    {
      "id": 2,
      "policy_id": 1,
      "name": "Default Pool",
      "match_type": "default",
      "match_value": "",
      "priority": 100,
      "fallback_pool": true
    }
  ]
}
```

### 📍 Pool 수정
```http
PUT /api/gslb/pools/:id
```

### 📍 Pool 삭제
```http
DELETE /api/gslb/pools/:id
```

---

## Member API

Member는 Pool에 속한 실제 서버의 IP 주소입니다.

### 📍 Member 생성
```http
POST /api/gslb/pools/:pool_id/members
```

**Request Body:**
```json
{
  "address": "string",   // 필수 - IP 주소 (포트 제외!)
  "weight": 100,         // 선택 - 가중치 (0-100, 기본값: 0)
  "enabled": true        // 선택 - 활성화 여부 (기본값: true)
}
```

### ⚠️ 중요: IP 주소 형식

Member의 `address`는 **순수 IP 주소만** 허용합니다.

| ✅ 올바른 예 | ❌ 잘못된 예 |
|------------|------------|
| `10.97.11.18` | `10.97.11.18:80` |
| `192.168.1.100` | `192.168.1.100:443` |
| `2001:db8::1` | `[2001:db8::1]:80` |

**이유:**
- DNS 응답은 IP 주소만 포함 (포트 정보 없음)
- 포트는 HealthCheck의 `target`에 지정

**Example:**
```bash
curl -X POST http://localhost:8080/api/gslb/pools/1/members \
  -H "Content-Type: application/json" \
  -d '{
    "address": "10.97.11.18",
    "weight": 100,
    "enabled": true
  }'
```

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

**Error Response (포트 포함 시):**
```json
{
  "success": false,
  "error": "address는 유효한 IP 주소여야 합니다 (포트 제외)",
  "code": "VALIDATION_ERROR"
}
```

### Weight (가중치) 설명
- 높은 가중치 = 더 많은 트래픽
- 예: Weight 100인 서버는 Weight 50인 서버의 2배 트래픽 수신
- 가중치 0 = 비활성화와 동일

### 📍 Member 목록 조회
```http
GET /api/gslb/pools/:pool_id/members
```

### 📍 Member 수정
```http
PUT /api/gslb/members/:id
```

### 📍 Member 삭제
```http
DELETE /api/gslb/members/:id
```

---

## HealthCheck API

HealthCheck는 Member의 상태를 주기적으로 확인합니다.

### 📍 HealthCheck 생성
```http
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

### Check Type 상세 설명

#### 1. `http` - HTTP/HTTPS 헬스체크

Target URL의 scheme을 보고 자동으로 HTTP 또는 HTTPS 요청을 수행합니다.

**HTTP 예시:**
```json
{
  "check_type": "http",
  "target": "http://10.97.11.18/health",
  "interval_sec": 10,
  "timeout_sec": 5,
  "healthy_threshold": 2,
  "unhealthy_threshold": 3,
  "enabled": true
}
```

**HTTPS 예시:**
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

**특징:**
- Target URL이 `https://`로 시작하면 HTTPS 요청
- Target URL이 `http://`로 시작하면 HTTP 요청
- 성공 조건: HTTP 상태 코드 200-299
- TLS 인증서 검증 비활성화 (`InsecureSkipVerify: true`)
- GET 메소드만 지원

#### 2. `tcp` - TCP 포트 체크

단순 TCP 연결 가능 여부를 확인합니다.

**예시:**
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

**특징:**
- 포트 연결 성공 여부만 확인
- 데이터 교환 없음
- 빠른 체크 가능

### Threshold (임계값) 설명

**healthy_threshold:**
- 연속으로 성공해야 하는 횟수
- 예: `2`인 경우, 2번 연속 성공 시 정상으로 판정

**unhealthy_threshold:**
- 연속으로 실패해야 하는 횟수
- 예: `3`인 경우, 3번 연속 실패 시 비정상으로 판정

**동작 예시:**
```
Initial: Healthy
Check 1: Fail (consecutive_fails: 1)
Check 2: Fail (consecutive_fails: 2)
Check 3: Fail (consecutive_fails: 3) -> Unhealthy
Check 4: Success (consecutive_oks: 1)
Check 5: Success (consecutive_oks: 2) -> Healthy
```

### 📍 HealthCheck 목록 조회
```http
GET /api/gslb/healthchecks
```

**Response:**
```json
{
  "success": true,
  "data": [
    {
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
  ]
}
```

### 📍 헬스 상태 조회
```http
GET /api/gslb/health
```

**Response:**
```json
{
  "success": true,
  "data": {
    "1": {
      "healthy": true,
      "last_check": "2026-01-31T14:30:00Z",
      "consecutive_oks": 5,
      "consecutive_fails": 0,
      "last_error": ""
    },
    "2": {
      "healthy": false,
      "last_check": "2026-01-31T14:30:05Z",
      "consecutive_oks": 0,
      "consecutive_fails": 4,
      "last_error": "connection timeout"
    }
  }
}
```

### 📍 HealthCheck 수정
```http
PUT /api/gslb/healthchecks/:id
```

### 📍 HealthCheck 삭제
```http
DELETE /api/gslb/healthchecks/:id
```

---

## 전체 설정 예제

### 시나리오: 글로벌 API 서버 GSLB 구성

**요구사항:**
- 한국 내부망 사용자 → 한국 서버
- 그 외 사용자 → 글로벌 서버
- 헬스체크로 장애 서버 자동 제외

### Step 1: Policy 생성
```bash
curl -X POST http://localhost:8080/api/gslb/policies \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Global API",
    "domain": "api.example.com",
    "record_type": "A",
    "ttl": 60
  }'
```
**Response:** `{"success":true,"data":{"id":1,...}}`

---

### Step 2: Pool 생성 (Korea Subnet)
```bash
curl -X POST http://localhost:8080/api/gslb/policies/1/pools \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Korea Internal",
    "match_type": "subnet",
    "match_value": "10.97.0.0/16",
    "priority": 10
  }'
```
**Response:** `{"success":true,"data":{"id":1,...}}`

---

### Step 3: Pool 생성 (Default)
```bash
curl -X POST http://localhost:8080/api/gslb/policies/1/pools \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Global Default",
    "match_type": "default",
    "priority": 100,
    "fallback_pool": true
  }'
```
**Response:** `{"success":true,"data":{"id":2,...}}`

---

### Step 4: Member 추가 (Korea Pool)
```bash
curl -X POST http://localhost:8080/api/gslb/pools/1/members \
  -H "Content-Type: application/json" \
  -d '{
    "address": "10.97.11.18",
    "weight": 100,
    "enabled": true
  }'
```
**Response:** `{"success":true,"data":{"id":1,...}}`

---

### Step 5: Member 추가 (Global Pool)
```bash
curl -X POST http://localhost:8080/api/gslb/pools/2/members \
  -H "Content-Type: application/json" \
  -d '{
    "address": "104.21.9.238",
    "weight": 100,
    "enabled": true
  }'
```
**Response:** `{"success":true,"data":{"id":2,...}}`

---

### Step 6: HealthCheck 추가 (Korea)
```bash
curl -X POST http://localhost:8080/api/gslb/members/1/healthcheck \
  -H "Content-Type: application/json" \
  -d '{
    "check_type": "http",
    "target": "https://10.97.11.18/health",
    "interval_sec": 10,
    "timeout_sec": 5,
    "healthy_threshold": 2,
    "unhealthy_threshold": 3,
    "enabled": true
  }'
```

---

### Step 7: HealthCheck 추가 (Global)
```bash
curl -X POST http://localhost:8080/api/gslb/members/2/healthcheck \
  -H "Content-Type: application/json" \
  -d '{
    "check_type": "tcp",
    "target": "104.21.9.238:443",
    "interval_sec": 10,
    "timeout_sec": 5,
    "healthy_threshold": 2,
    "unhealthy_threshold": 3,
    "enabled": true
  }'
```

---

### Step 8: DNS 쿼리 테스트

**한국 내부망에서 쿼리 (10.97.x.x):**
```bash
dig @localhost api.example.com

; ANSWER SECTION:
api.example.com.  60  IN  A  10.97.11.18
```

**외부망에서 쿼리:**
```bash
dig @localhost api.example.com

; ANSWER SECTION:
api.example.com.  60  IN  A  104.21.9.238
```

---

## 구성 다이어그램

```
Policy: api.example.com (TTL: 60s)
│
├─ Pool 1: Korea Internal (Priority: 10)
│  ├─ Match: subnet = 10.97.0.0/16
│  └─ Member 1: 10.97.11.18 (Weight: 100)
│     └─ HealthCheck: HTTPS https://10.97.11.18/health
│
└─ Pool 2: Global Default (Priority: 100, Fallback)
   ├─ Match: default
   └─ Member 2: 104.21.9.238 (Weight: 100)
      └─ HealthCheck: TCP 104.21.9.238:443
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

### HTTP 상태 코드
- `200 OK`: 조회/수정/삭제 성공
- `201 Created`: 생성 성공
- `400 Bad Request`: 잘못된 요청 (validation 실패)
- `404 Not Found`: 리소스 없음
- `500 Internal Server Error`: 서버 에러

---

## 주요 변경사항

### 2026-01-31
1. **Member.Address 변경**
   - Before: `"10.97.11.18:80"` (포트 포함)
   - After: `"10.97.11.18"` (IP만)
   - 이유: DNS 응답은 IP만 포함, 포트는 HealthCheck에서 사용

2. **HealthCheck.check_type 단순화**
   - Before: `"http"`, `"https"`, `"tcp"`
   - After: `"http"`, `"tcp"`
   - 변경: URL scheme으로 자동 감지 (http:// vs https://)

3. **IP 주소 Validation 추가**
   - Member 생성/수정 시 IP 형식 검증
   - 포트 포함 시 에러 반환
