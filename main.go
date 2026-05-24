package main

import (
	"dns-go/adblock"
	"dns-go/config"
	"dns-go/dns"
	"dns-go/gslb"
	"dns-go/storage"
	"dns-go/sync"
	"dns-go/web"
	"fmt"
	"log"
	"os"
	"os/signal"
	synclib "sync"
	"syscall"
)

func main() {
	if err := run("config.yaml", waitForShutdownSignal); err != nil {
		log.Fatalf("%v", err)
	}
}

type shutdownWaiter func(<-chan os.Signal) os.Signal

func waitForShutdownSignal(sigChan <-chan os.Signal) os.Signal {
	return <-sigChan
}

func run(configPath string, waitForSignal shutdownWaiter) error {
	// 설정 로드
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("설정 로드 실패: %w", err)
	}

	// 설정 검증
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("설정 검증 실패: %w", err)
	}

	// 데이터베이스 연결
	db, err := storage.NewDatabase(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("데이터베이스 연결 실패: %w", err)
	}
	defer func() { _ = db.Close() }()

	log.Printf("데이터베이스 연결 성공: %s", cfg.Database.Path)

	// Storage 레이어 초기화
	zoneStorage := storage.NewZoneStorage(db)
	recordStorage := storage.NewRecordStorage(db)
	upstreamStorage := storage.NewUpstreamStorage(db)

	log.Println("Storage 레이어 초기화 완료")

	// 업스트림 리졸버 초기화
	resolver := dns.NewResolver(upstreamStorage, cfg.Upstream.Timeout)

	log.Println("업스트림 리졸버 초기화 완료")

	// GeoIP 리졸버 초기화 (선택)
	var geoipResolver *gslb.GeoIPResolver
	if cfg.GeoIP.CityDB != "" {
		var err error
		geoipResolver, err = gslb.NewGeoIPResolver(cfg.GeoIP.CityDB)
		if err != nil {
			log.Printf("GeoIP DB 로드 실패 (GeoIP 비활성): %v", err)
			geoipResolver = nil
		} else {
			defer func() { _ = geoipResolver.Close() }()
			log.Println("GeoIP 리졸버 초기화 완료")
		}
	}

	// GSLB Storage 초기화
	policyStorage := gslb.NewPolicyStorage(db)
	poolStorage := gslb.NewPoolStorage(db)
	healthStatus := &synclib.Map{}
	gslbEngine := gslb.NewEngine(policyStorage, poolStorage, geoipResolver, healthStatus)

	log.Println("GSLB 엔진 초기화 완료")

	// 헬스체크 워커 초기화
	healthCheckStorage := gslb.NewHealthCheckStorage(db)
	healthWorker := gslb.NewHealthCheckWorker(healthCheckStorage, poolStorage, healthStatus)
	healthWorker.Start()
	defer healthWorker.Stop()
	log.Println("헬스체크 워커 시작")

	// Sync Version 초기화 (Primary/Secondary 공통)
	syncVersion := storage.NewSyncVersion(db)

	// Adblock 초기화
	adblockStorage := storage.NewAdblockStorage(db)
	adblockFilter := adblock.NewFilter(adblockStorage, cfg.Adblock.Enabled)
	adblockLoader := adblock.NewLoader()
	adblockSyncer := adblock.NewSyncer(adblockStorage, adblockLoader, adblockFilter, cfg.Adblock.SyncInterval)

	// Secondary 모드에서는 Primary에서 adblock 데이터를 동기화받으므로 HTTP 동기화 불필요
	if cfg.Sync.Mode != config.SyncModeSecondary {
		adblockSyncer.SetVersionIncrementer(syncVersion)
		adblockSyncer.Start()
		defer adblockSyncer.Stop()
	}
	log.Println("Adblock 초기화 완료")

	// 쿼리 통계
	queryStats := dns.NewQueryStats()

	// 쿼리 로그 초기화
	var queryLogStorage storage.QueryLogRepository
	var queryLogWriter *dns.QueryLogWriter
	if cfg.Logging.QueryLog.Enabled {
		queryLogStorage, err = storage.NewPartitionedQueryLogStorage(cfg.Logging.QueryLog.Dir)
		if err != nil {
			return fmt.Errorf("쿼리 로그 저장소 초기화 실패: %w", err)
		}
		if closer, ok := queryLogStorage.(interface{ Close() error }); ok {
			defer func() { _ = closer.Close() }()
		}
		queryLogWriter = dns.NewQueryLogWriter(queryLogStorage, cfg.Logging.QueryLog.FlushInterval, cfg.Logging.QueryLog.BufferSize)
		queryLogCleaner := dns.NewQueryLogCleaner(queryLogStorage, cfg.Logging.QueryLog.RetentionDays)
		defer queryLogCleaner.Stop()
		log.Printf("쿼리 로그 활성화 (dir: %s, 보관: %d일, 플러시: %v)", cfg.Logging.QueryLog.Dir, cfg.Logging.QueryLog.RetentionDays, cfg.Logging.QueryLog.FlushInterval)
	}

	// DNS 핸들러 초기화
	handler, err := dns.NewHandler(
		zoneStorage,
		recordStorage,
		resolver,
		db,
		queryStats,
		gslbEngine,
		adblockFilter,
		adblockStorage,
		cfg.Adblock.BlockResponse,
		cfg.DNS.NSID,    // NSID
		cfg.DNS.Version, // CHAOS version
		queryLogWriter,
	)
	if err != nil {
		return fmt.Errorf("DNS 핸들러 초기화 실패: %w", err)
	}
	defer handler.Stop()

	log.Printf("DNS 핸들러 초기화 완료 (NSID: %s)", cfg.DNS.NSID)

	// DNS 서버 초기화 및 시작
	server := dns.NewServer(&cfg.DNS, handler)

	if err := server.Start(); err != nil {
		return fmt.Errorf("DNS 서버 시작 실패: %w", err)
	}
	dnsStopped := false
	defer func() {
		if !dnsStopped {
			_ = server.Stop()
		}
	}()

	log.Printf("DNS 서버 시작 성공: %s", server.GetAddr())

	// Sync Worker 시작 (Secondary 모드)
	var syncWorker *sync.Worker
	if cfg.Sync.Mode == config.SyncModeSecondary {
		syncWorker = sync.NewWorker(cfg.Sync.PrimaryURL, db, cfg.Sync.Interval)

		// 동기화 완료 시 콜백 설정 (헬스체크 재시작, 캐시 클리어, Adblock 필터 재빌드)
		syncWorker.SetSyncCompleteCallback(func() {
			log.Println("동기화 완료: 헬스체크 재시작, Adblock 재빌드 및 캐시 클리어")

			// 1. 헬스체크 워커 재시작
			healthWorker.Restart()

			// 2. Adblock 필터 재빌드 (Primary에서 동기화받은 도메인 반영)
			if err := adblockFilter.Rebuild(); err != nil {
				log.Printf("Adblock 필터 재빌드 실패: %v", err)
			}

			// 3. DNS 캐시 전체 클리어
			handler.ClearCache()
			log.Println("DNS 캐시 클리어 완료")
		})

		syncWorker.Start()
		defer syncWorker.Stop()
		log.Printf("Secondary 모드: Primary=%s, Interval=%v", cfg.Sync.PrimaryURL, cfg.Sync.Interval)
	}

	// Sync API (Primary 모드에서만 제공)
	var syncAPI *web.SyncAPI
	if cfg.Sync.Mode == config.SyncModePrimary {
		syncAPI = web.NewSyncAPI(syncVersion)
		log.Println("Primary 모드: Sync API 활성화")
	}

	// Web API 서버 초기화 및 시작
	api := web.NewAPI(zoneStorage, recordStorage, upstreamStorage, db, handler, queryStats, policyStorage, poolStorage, adblockStorage, adblockSyncer, adblockFilter, healthCheckStorage, healthStatus, healthWorker, queryLogStorage)

	// Read-Only 모드 설정 (Secondary)
	if cfg.Sync.ReadOnly {
		api.SetReadOnly(true)
		log.Println("Read-Only 모드 활성화 (Write API 차단)")
	}

	// 서버 정보 API 초기화
	serverInfoAPI := web.NewServerInfoAPI(cfg, db)

	webServer := web.NewServer(cfg.Web.Listen, cfg.Web.Port, api, syncAPI, serverInfoAPI)

	webErrCh, err := webServer.StartAsync()
	if err != nil {
		return fmt.Errorf("Web 서버 시작 실패: %w", err)
	}

	log.Printf("Web 서버 시작 성공: %s", webServer.Addr())

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	sig := waitForSignal(sigChan)
	log.Printf("종료 신호 수신: %v", sig)

	// 서버 종료
	if err := server.Stop(); err != nil {
		log.Printf("서버 종료 실패: %v", err)
	}
	dnsStopped = true
	if err := webServer.Stop(); err != nil {
		log.Printf("Web 서버 종료 실패: %v", err)
	}
	if err, ok := <-webErrCh; ok && err != nil {
		log.Printf("Web 서버 종료: %v", err)
	}

	log.Println("서버 종료 완료")
	return nil
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
