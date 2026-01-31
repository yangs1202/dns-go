package main

import (
	"dns-go/config"
	"dns-go/dns"
	"dns-go/storage"
	"dns-go/web"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// 설정 로드
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("설정 로드 실패: %v", err)
	}

	// 설정 검증
	if err := cfg.Validate(); err != nil {
		log.Fatalf("설정 검증 실패: %v", err)
	}

	// 데이터베이스 연결
	db, err := storage.NewDatabase(cfg.Database.Path)
	if err != nil {
		log.Fatalf("데이터베이스 연결 실패: %v", err)
	}
	defer db.Close()

	log.Printf("데이터베이스 연결 성공: %s", cfg.Database.Path)

	// Storage 레이어 초기화
	zoneStorage := storage.NewZoneStorage(db)
	recordStorage := storage.NewRecordStorage(db)
	upstreamStorage := storage.NewUpstreamStorage(db)

	log.Println("Storage 레이어 초기화 완료")

	// 업스트림 리졸버 초기화
	resolver := dns.NewResolver(upstreamStorage, cfg.Upstream.Timeout)

	log.Println("업스트림 리졸버 초기화 완료")

	// 쿼리 통계
	queryStats := dns.NewQueryStats()

	// DNS 핸들러 초기화
	handler, err := dns.NewHandler(zoneStorage, recordStorage, resolver, db, queryStats)
	if err != nil {
		log.Fatalf("DNS 핸들러 초기화 실패: %v", err)
	}

	log.Println("DNS 핸들러 초기화 완료")

	// DNS 서버 초기화 및 시작
	server := dns.NewServer(&cfg.DNS, handler)

	if err := server.Start(); err != nil {
		log.Fatalf("DNS 서버 시작 실패: %v", err)
	}

	log.Printf("DNS 서버 시작 성공: %s", server.GetAddr())

	// Web API 서버 초기화 및 시작
	api := web.NewAPI(zoneStorage, recordStorage, upstreamStorage, db, handler, queryStats)
	webServer := web.NewServer(cfg.Web.Listen, cfg.Web.Port, api)

	go func() {
		if err := webServer.Start(); err != nil {
			log.Printf("Web 서버 종료: %v", err)
		}
	}()

	log.Printf("Web 서버 시작 성공: %s", webServer.Addr())

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigChan
	log.Printf("종료 신호 수신: %v", sig)

	// 서버 종료
	if err := server.Stop(); err != nil {
		log.Printf("서버 종료 실패: %v", err)
	}
	if err := webServer.Stop(); err != nil {
		log.Printf("Web 서버 종료 실패: %v", err)
	}

	log.Println("서버 종료 완료")
}

// Version 정보
const (
	Version   = "0.1.0"
	BuildDate = "2026-01-31"
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	fmt.Printf("DNS-Go v%s (빌드: %s)\n", Version, BuildDate)
	fmt.Println("고성능 DNS 서버 with L1+L2 캐싱")
	fmt.Println("저자: Claude Code")
	fmt.Println()
}
