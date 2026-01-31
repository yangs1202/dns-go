#!/bin/bash

# Primary/Secondary 동기화 테스트 스크립트

set -e

echo "=== Primary/Secondary 동기화 시스템 테스트 ==="
echo ""

# 1. Primary 서버 시작 (백그라운드)
echo "[1] Primary 서버 시작..."
./dns-server --config=config.primary.yaml > primary.log 2>&1 &
PRIMARY_PID=$!
echo "Primary PID: $PRIMARY_PID"
sleep 3

# 2. Primary에 Zone 생성
echo ""
echo "[2] Primary에 Zone 생성..."
curl -X POST http://localhost:8080/api/zones \
  -H "Content-Type: application/json" \
  -d '{"name":"test.com","allow_fallback":true}' \
  -s | jq .

sleep 1

# 3. Primary에 Record 생성
echo ""
echo "[3] Primary에 Record 생성..."
curl -X POST http://localhost:8080/api/zones/1/records \
  -H "Content-Type: application/json" \
  -d '{"name":"www.test.com","type":"A","content":"192.168.1.100","ttl":300}' \
  -s | jq .

sleep 1

# 4. Primary Sync Metadata 확인
echo ""
echo "[4] Primary Sync Metadata..."
curl -s http://localhost:8080/api/sync/metadata | jq .

# 5. Primary Full Sync 데이터 확인
echo ""
echo "[5] Primary Full Sync 데이터..."
curl -s http://localhost:8080/api/sync/full | jq '.data.zones, .data.records'

# 6. Secondary 서버 시작 (새 포트 사용)
echo ""
echo "[6] Secondary 서버 시작 (포트 5300, 8081)..."
sed 's/port: 53/port: 5300/; s/port: 8080/port: 8081/' config.secondary.yaml > config.secondary-test.yaml
./dns-server --config=config.secondary-test.yaml > secondary.log 2>&1 &
SECONDARY_PID=$!
echo "Secondary PID: $SECONDARY_PID"
sleep 5  # Full Sync 대기

# 7. Secondary에서 Zones 조회
echo ""
echo "[7] Secondary에서 Zones 조회..."
curl -s http://localhost:8081/api/zones | jq .

# 8. Secondary에서 Records 조회
echo ""
echo "[8] Secondary에서 Records 조회..."
curl -s http://localhost:8081/api/records | jq .

# 9. Secondary에 Write 시도 (403 에러 예상)
echo ""
echo "[9] Secondary에 Write 시도 (Read-Only 모드)..."
curl -X POST http://localhost:8081/api/zones \
  -H "Content-Type: application/json" \
  -d '{"name":"forbidden.com"}' \
  -s -w "\nHTTP Status: %{http_code}\n" | jq . || echo ""

# 10. Primary에 새 Record 추가
echo ""
echo "[10] Primary에 새 Record 추가..."
curl -X POST http://localhost:8080/api/zones/1/records \
  -H "Content-Type: application/json" \
  -d '{"name":"api.test.com","type":"A","content":"192.168.1.101","ttl":300}' \
  -s | jq .

echo ""
echo "[11] 동기화 대기 (2초)..."
sleep 2

# 12. Secondary에서 새 Record 확인
echo ""
echo "[12] Secondary에서 동기화된 Records 확인..."
curl -s http://localhost:8081/api/records | jq .

# 정리
echo ""
echo "=== 테스트 완료 ==="
echo ""
echo "서버 종료 (Primary: $PRIMARY_PID, Secondary: $SECONDARY_PID)..."
kill $PRIMARY_PID $SECONDARY_PID 2>/dev/null || true
sleep 1

echo "로그 파일:"
echo "  - primary.log"
echo "  - secondary.log"
