# 멀티 스테이지 빌드
FROM golang:1.21-alpine AS builder

# 빌드에 필요한 패키지 설치
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# 의존성 먼저 다운로드 (캐시 활용)
COPY go.mod go.sum ./
RUN go mod download

# 소스 코드 복사
COPY . .

# 바이너리 빌드
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -ldflags="-w -s" -o dns-go .

# 최종 이미지
FROM alpine:latest

# 런타임 패키지 설치
RUN apk --no-cache add ca-certificates tzdata sqlite

WORKDIR /app

# 빌더에서 바이너리 복사
COPY --from=builder /build/dns-go .
COPY --from=builder /build/config.yaml .

# 데이터 디렉토리 생성
RUN mkdir -p /data

# 헬스체크
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/stats || exit 1

# DNS 포트(53) + Web 포트(8080) 노출
EXPOSE 53/udp 53/tcp 8080

# 볼륨
VOLUME ["/data"]

# 실행
CMD ["./dns-go"]
