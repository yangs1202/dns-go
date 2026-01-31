package storage

import "fmt"

// Migrate는 데이터베이스 스키마를 생성합니다
func (db *Database) Migrate() error {
	schemas := []string{
		// Zone 관리
		`CREATE TABLE IF NOT EXISTS zones (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			soa_mname TEXT DEFAULT '',
			soa_rname TEXT DEFAULT '',
			soa_serial INTEGER DEFAULT 1,
			soa_refresh INTEGER DEFAULT 3600,
			soa_retry INTEGER DEFAULT 900,
			soa_expire INTEGER DEFAULT 86400,
			soa_minimum INTEGER DEFAULT 300,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// DNS 레코드
		`CREATE TABLE IF NOT EXISTS records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			zone_id INTEGER NOT NULL REFERENCES zones(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			content TEXT NOT NULL,
			ttl INTEGER DEFAULT 300,
			priority INTEGER DEFAULT 0,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_records_lookup ON records(name, type, enabled)`,

		// GSLB 정책
		`CREATE TABLE IF NOT EXISTS gslb_policies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			domain TEXT NOT NULL,
			record_type TEXT NOT NULL DEFAULT 'A',
			ttl INTEGER DEFAULT 30,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gslb_policies_domain ON gslb_policies(domain, record_type, enabled)`,

		// GSLB 풀
		`CREATE TABLE IF NOT EXISTS gslb_pools (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			policy_id INTEGER NOT NULL REFERENCES gslb_policies(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			match_type TEXT NOT NULL,
			match_value TEXT NOT NULL,
			priority INTEGER DEFAULT 0,
			fallback_pool INTEGER DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_gslb_pools_policy ON gslb_pools(policy_id, priority)`,

		// GSLB 풀 멤버
		`CREATE TABLE IF NOT EXISTS gslb_members (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pool_id INTEGER NOT NULL REFERENCES gslb_pools(id) ON DELETE CASCADE,
			address TEXT NOT NULL,
			weight INTEGER DEFAULT 100,
			enabled INTEGER DEFAULT 1
		)`,

		// 헬스체크 설정
		`CREATE TABLE IF NOT EXISTS health_checks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			member_id INTEGER NOT NULL REFERENCES gslb_members(id) ON DELETE CASCADE,
			check_type TEXT NOT NULL DEFAULT 'tcp',
			target TEXT NOT NULL,
			interval_sec INTEGER DEFAULT 10,
			timeout_sec INTEGER DEFAULT 5,
			healthy_threshold INTEGER DEFAULT 3,
			unhealthy_threshold INTEGER DEFAULT 2,
			enabled INTEGER DEFAULT 1
		)`,

		// 캐시 설정 관리
		`CREATE TABLE IF NOT EXISTS cache_settings (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			enabled INTEGER DEFAULT 1,
			max_size INTEGER DEFAULT 10000,
			default_ttl INTEGER DEFAULT 300,
			min_ttl INTEGER DEFAULT 60,
			max_ttl INTEGER DEFAULT 86400,
			negative_ttl INTEGER DEFAULT 300,
			prefetch_trigger REAL DEFAULT 0.9,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// 업스트림 리졸버 관리
		`CREATE TABLE IF NOT EXISTS upstream_servers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			address TEXT NOT NULL,
			protocol TEXT NOT NULL DEFAULT 'udp',
			priority INTEGER DEFAULT 0,
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// 광고차단 필터 소스 관리
		`CREATE TABLE IF NOT EXISTS adblock_sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL UNIQUE,
			enabled INTEGER DEFAULT 1,
			last_sync DATETIME,
			last_modified TEXT,
			rule_count INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// 광고차단 도메인
		`CREATE TABLE IF NOT EXISTS adblock_domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT NOT NULL UNIQUE,
			source_id INTEGER NOT NULL REFERENCES adblock_sources(id) ON DELETE CASCADE,
			added_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_adblock_domains_lookup ON adblock_domains(domain)`,

		// 광고차단 통계
		`CREATE TABLE IF NOT EXISTS adblock_stats (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			blocked_domain TEXT NOT NULL,
			client_ip TEXT NOT NULL,
			query_time DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_adblock_stats_time ON adblock_stats(query_time)`,

		// 기본 캐시 설정 삽입
		`INSERT OR IGNORE INTO cache_settings (id, enabled, max_size, default_ttl, min_ttl, max_ttl, negative_ttl, prefetch_trigger)
		 VALUES (1, 1, 10000, 300, 60, 86400, 300, 0.9)`,
	}

	for _, schema := range schemas {
		if _, err := db.Writer.Exec(schema); err != nil {
			return fmt.Errorf("스키마 실행 실패: %w\n%s", err, schema)
		}
	}

	return nil
}
