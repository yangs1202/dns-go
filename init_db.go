package main

import (
	"dns-go/config"
	"dns-go/storage"
	"log"
)

// InitDB는 초기 데이터를 데이터베이스에 삽입합니다
func InitDB() {
	// 설정 로드
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("설정 로드 실패: %v", err)
	}

	// 데이터베이스 연결
	db, err := storage.NewDatabase(cfg.Database.Path)
	if err != nil {
		log.Fatalf("데이터베이스 연결 실패: %v", err)
	}
	defer db.Close()

	log.Println("데이터베이스 초기화 시작...")

	// Storage 초기화
	upstreamStorage := storage.NewUpstreamStorage(db)

	// 기본 업스트림 서버 추가
	servers := []struct {
		name     string
		address  string
		protocol string
		priority int64
	}{
		{"Google DNS", "8.8.8.8:53", "udp", 1},
		{"Cloudflare DNS", "1.1.1.1:53", "udp", 2},
		{"Google DNS (Secondary)", "8.8.4.4:53", "udp", 3},
	}

	for _, s := range servers {
		// 이미 존재하는지 확인
		existing, err := upstreamStorage.ListUpstreamServers()
		if err != nil {
			log.Printf("업스트림 서버 목록 조회 실패: %v", err)
			continue
		}

		found := false
		for _, e := range existing {
			if e.Address == s.address {
				found = true
				break
			}
		}

		if !found {
			id, err := db.Writer.Exec(
				"INSERT INTO upstream_servers (name, address, protocol, priority, enabled) VALUES (?, ?, ?, ?, 1)",
				s.name, s.address, s.protocol, s.priority,
			)
			if err != nil {
				log.Printf("업스트림 서버 추가 실패 (%s): %v", s.name, err)
			} else {
				lastID, _ := id.LastInsertId()
				log.Printf("업스트림 서버 추가 성공: %s (ID: %d)", s.name, lastID)
			}
		} else {
			log.Printf("업스트림 서버 이미 존재: %s", s.name)
		}
	}

	log.Println("데이터베이스 초기화 완료!")

	// 기본 광고차단 소스 추가
	defaultSources := []struct {
		name string
		url  string
	}{
		{
			name: "AdGuard DNS Filter",
			url:  "https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt",
		},
	}

	for _, s := range defaultSources {
		_, err := db.Writer.Exec(
			"INSERT OR IGNORE INTO adblock_sources (name, url, enabled) VALUES (?, ?, 1)",
			s.name, s.url,
		)
		if err != nil {
			log.Printf("광고차단 소스 추가 실패: %v", err)
		} else {
			log.Printf("광고차단 소스 추가: %s", s.name)
		}
	}
}
