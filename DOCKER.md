# DNS-GO Docker 사용 가이드

## 빠른 시작

### 1. 도커 이미지 빌드

```bash
docker build -t dns-go:latest .
```

### 2. 단독 실행

```bash
# 데이터 디렉토리 생성
mkdir -p data

# 컨테이너 실행
docker run -d \
  --name dns-go \
  -p 53:53/udp \
  -p 53:53/tcp \
  -p 8080:8080 \
  -v $(pwd)/data:/data \
  -v $(pwd)/config.yaml:/app/config.yaml:ro \
  dns-go:latest
```

### 3. Docker Compose 실행 (권장)

```bash
# 시작
docker-compose up -d

# 로그 확인
docker-compose logs -f

# 중지
docker-compose down

# 중지 + 볼륨 삭제
docker-compose down -v
```

---

## 설정

### 데이터 볼륨

컨테이너는 `/data` 디렉토리에 SQLite DB를 저장합니다.

```bash
# 로컬 data/ 디렉토리가 컨테이너의 /data에 마운트됨
data/
└── dns-go.db
```

### 설정 파일

도커 환경에서는 `config.docker.yaml` 사용:

```yaml
database:
  path: "/data/dns-go.db"  # 볼륨 경로

geoip:
  city_db: "/app/GeoLite2-City.mmdb"  # 옵션
```

### GeoIP 데이터베이스 (선택사항)

GSLB 기능을 사용하려면 GeoLite2 DB 필요:

```bash
# MaxMind에서 다운로드 (무료 회원가입 필요)
# https://dev.maxmind.com/geoip/geolite2-free-geolocation-data

# 프로젝트 루트에 배치
./GeoLite2-City.mmdb
```

없으면 GeoIP 기능만 비활성화되고 나머지는 정상 동작합니다.

---

## 헬스체크

컨테이너는 자동 헬스체크를 수행합니다:

```bash
# 헬스 상태 확인
docker inspect --format='{{.State.Health.Status}}' dns-go

# 헬스체크 로그
docker inspect --format='{{json .State.Health}}' dns-go | jq
```

헬스체크 엔드포인트: `http://localhost:8080/api/stats`

---

## 포트

| 포트 | 프로토콜 | 용도 |
|------|---------|------|
| 53 | UDP | DNS 쿼리 (주로 사용) |
| 53 | TCP | DNS over TCP |
| 8080 | HTTP | REST API + 관리 |

### 포트 변경

```bash
# 다른 포트로 실행 (예: 5353)
docker run -d \
  -p 5353:53/udp \
  -p 5353:53/tcp \
  -p 8080:8080 \
  dns-go:latest
```

---

## API 테스트

컨테이너 실행 후:

```bash
# 통계 확인
curl http://localhost:8080/api/stats

# Zone 목록
curl http://localhost:8080/api/zones

# 캐시 통계
curl http://localhost:8080/api/cache/stats
```

---

## DNS 테스트

### dig 사용

```bash
# A 레코드 쿼리
dig @localhost example.com A

# 포트 변경 시
dig @localhost -p 5353 example.com A
```

### nslookup 사용

```bash
nslookup example.com localhost
```

---

## 로그

### 실시간 로그

```bash
# Docker Compose
docker-compose logs -f

# Docker
docker logs -f dns-go
```

### 로그 설정

`docker-compose.yml`에서 로그 로테이션 설정됨:

```yaml
logging:
  driver: "json-file"
  options:
    max-size: "10m"  # 파일당 최대 10MB
    max-file: "3"    # 최대 3개 보관
```

---

## 프로덕션 배포

### 환경 변수로 설정 오버라이드 (향후 지원)

```bash
docker run -d \
  -e DNS_PORT=53 \
  -e WEB_PORT=8080 \
  -e LOG_LEVEL=warn \
  dns-go:latest
```

### 리소스 제한

```yaml
services:
  dns-go:
    deploy:
      resources:
        limits:
          cpus: '2'
          memory: 1G
        reservations:
          cpus: '0.5'
          memory: 256M
```

### 보안 강화

```yaml
services:
  dns-go:
    read_only: true  # 읽기 전용 루트 파일시스템
    tmpfs:
      - /tmp
    cap_drop:
      - ALL
    cap_add:
      - NET_BIND_SERVICE  # 53번 포트 바인딩
```

---

## 트러블슈팅

### 1. 포트 53 바인딩 실패

**증상**: `bind: permission denied`

**해결**:
```bash
# Linux: systemd-resolved 비활성화
sudo systemctl stop systemd-resolved
sudo systemctl disable systemd-resolved

# 또는 다른 포트 사용
-p 5353:53/udp
```

### 2. 데이터베이스 권한 오류

**증상**: `unable to open database file`

**해결**:
```bash
# data/ 디렉토리 권한 확인
chmod 755 data/
```

### 3. GeoIP 경고

**증상**: `GeoIP DB 로드 실패 (GeoIP 비활성)`

**해결**:
```bash
# GeoLite2-City.mmdb 다운로드
# 또는 docker-compose.yml에서 해당 볼륨 주석 처리
```

### 4. 헬스체크 실패

**증상**: Container health: unhealthy

**해결**:
```bash
# 웹 서버 동작 확인
curl http://localhost:8080/api/stats

# 컨테이너 내부에서 확인
docker exec -it dns-go wget -qO- http://localhost:8080/api/stats
```

---

## 성능 최적화

### 캐시 크기 조정

REST API로 캐시 설정 변경:

```bash
curl -X PUT http://localhost:8080/api/cache/settings \
  -H "Content-Type: application/json" \
  -d '{
    "max_size": 50000,
    "default_ttl": 600
  }'
```

### 업스트림 서버 추가

```bash
curl -X POST http://localhost:8080/api/upstream \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Cloudflare DNS",
    "address": "1.1.1.1:53",
    "protocol": "udp",
    "priority": 1,
    "enabled": true
  }'
```

---

## 백업 및 복원

### 데이터베이스 백업

```bash
# 백업
docker exec dns-go sqlite3 /data/dns-go.db ".backup /data/backup.db"
cp data/backup.db ~/backups/dns-go-$(date +%Y%m%d).db

# 복원
cp ~/backups/dns-go-20260131.db data/dns-go.db
docker-compose restart
```

---

## 모니터링

### Prometheus 메트릭 (향후 지원)

```bash
curl http://localhost:8080/metrics
```

### 통계 API

```bash
# 전체 통계
curl http://localhost:8080/api/stats | jq

# 캐시 통계
curl http://localhost:8080/api/cache/stats | jq

# 헬스체크 상태
curl http://localhost:8080/api/gslb/health | jq
```

---

## 예제: 완전한 스택

```yaml
version: '3.8'

services:
  dns-go:
    build: .
    ports:
      - "53:53/udp"
      - "53:53/tcp"
      - "8080:8080"
    volumes:
      - ./data:/data
      - ./config.yaml:/app/config.yaml:ro

  prometheus:
    image: prom/prometheus
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
    ports:
      - "9090:9090"

  grafana:
    image: grafana/grafana
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
```

---

## 다음 단계

1. **Zone 및 Record 추가**: `/api/zones`, `/api/records`
2. **GSLB 설정**: `/api/gslb/policies`
3. **광고차단 필터 추가**: `/api/adblock/sources`
4. **모니터링 설정**: Prometheus + Grafana

자세한 API 문서는 `README.md` 참조.
