package sync

import (
	"database/sql"
	"dns-go/storage"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// SyncCallback은 동기화 완료 시 호출되는 콜백입니다
type SyncCallback func()

// Worker는 Secondary 서버의 동기화 워커입니다
type Worker struct {
	primaryURL     string
	db             *storage.Database
	interval       time.Duration
	stopChan       chan struct{}
	httpClient     *http.Client
	onSyncComplete SyncCallback // 동기화 완료 시 호출
}

// NewWorker는 Worker 인스턴스를 생성합니다
func NewWorker(primaryURL string, db *storage.Database, interval time.Duration) *Worker {
	return &Worker{
		primaryURL: primaryURL,
		db:         db,
		interval:   interval,
		stopChan:   make(chan struct{}),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// SetSyncCompleteCallback은 동기화 완료 콜백을 설정합니다
func (w *Worker) SetSyncCompleteCallback(callback SyncCallback) {
	w.onSyncComplete = callback
}

// Start는 동기화 워커를 시작합니다
func (w *Worker) Start() {
	log.Printf("Sync Worker 시작 (Primary: %s, Interval: %v)", w.primaryURL, w.interval)

	// 최초 Full Sync
	go func() {
		time.Sleep(2 * time.Second) // 서버 초기화 대기
		if err := w.fullSync(); err != nil {
			log.Printf("초기 Full Sync 실패: %v (계속 재시도)", err)
		}
	}()

	// 주기적 Incremental Sync
	ticker := time.NewTicker(w.interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := w.incrementalSync(); err != nil {
					log.Printf("Incremental Sync 실패: %v", err)
				}
			case <-w.stopChan:
				ticker.Stop()
				return
			}
		}
	}()
}

// Stop은 동기화 워커를 중지합니다
func (w *Worker) Stop() {
	close(w.stopChan)
}

// fullSync는 전체 데이터를 동기화합니다
func (w *Worker) fullSync() error {
	log.Println("Full Sync 시작...")

	// Primary에서 전체 데이터 가져오기
	resp, err := w.httpClient.Get(w.primaryURL + "/api/sync/full")
	if err != nil {
		return fmt.Errorf("primary 연결 실패: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("primary 응답 오류: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("응답 읽기 실패: %w", err)
	}

	var data struct {
		Version  int64  `json:"version"`
		Checksum string `json:"checksum"`
		Data     struct {
			Zones          []map[string]interface{} `json:"zones"`
			Records        []map[string]interface{} `json:"records"`
			GSLBPolicies   []map[string]interface{} `json:"gslb_policies"`
			GSLBPools      []map[string]interface{} `json:"gslb_pools"`
			GSLBMembers    []map[string]interface{} `json:"gslb_members"`
			HealthChecks   []map[string]interface{} `json:"health_checks"`
			AdblockSources []map[string]interface{} `json:"adblock_sources"`
			AdblockDomains []map[string]interface{} `json:"adblock_domains"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("json 파싱 실패: %w", err)
	}

	// 트랜잭션으로 전체 교체
	tx, err := w.db.Writer.Begin()
	if err != nil {
		return fmt.Errorf("트랜잭션 시작 실패: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 기존 데이터 삭제 (역순으로 삭제 - Foreign Key 제약)
	if _, err := tx.Exec("DELETE FROM records"); err != nil {
		return fmt.Errorf("records 삭제 실패: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM zones"); err != nil {
		return fmt.Errorf("zones 삭제 실패: %w", err)
	}

	// GSLB 관련 테이블 삭제 (역순으로 - Foreign Key 제약)
	if _, err := tx.Exec("DELETE FROM health_checks"); err != nil {
		return fmt.Errorf("health checks 삭제 실패: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM gslb_members"); err != nil {
		return fmt.Errorf("gslb members 삭제 실패: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM gslb_pools"); err != nil {
		return fmt.Errorf("gslb pools 삭제 실패: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM gslb_policies"); err != nil {
		return fmt.Errorf("gslb policies 삭제 실패: %w", err)
	}

	// Adblock 테이블 삭제 (domains 먼저 - Foreign Key 제약)
	if _, err := tx.Exec("DELETE FROM adblock_domains"); err != nil {
		return fmt.Errorf("adblock domains 삭제 실패: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM adblock_sources"); err != nil {
		return fmt.Errorf("adblock sources 삭제 실패: %w", err)
	}

	// Zones 삽입
	for _, zone := range data.Data.Zones {
		if err := w.insertZone(tx, zone); err != nil {
			return fmt.Errorf("zone 삽입 실패: %w", err)
		}
	}

	// Records 삽입
	for _, record := range data.Data.Records {
		if err := w.insertRecord(tx, record); err != nil {
			return fmt.Errorf("record 삽입 실패: %w", err)
		}
	}
	// Upstream은 Secondary별로 다를 수 있으므로 동기화하지 않음.

	// GSLB Policies 삽입
	for _, policy := range data.Data.GSLBPolicies {
		if err := w.insertGSLBPolicy(tx, policy); err != nil {
			return fmt.Errorf("gslb policy 삽입 실패: %w", err)
		}
	}

	// GSLB Pools 삽입
	for _, pool := range data.Data.GSLBPools {
		if err := w.insertGSLBPool(tx, pool); err != nil {
			return fmt.Errorf("gslb pool 삽입 실패: %w", err)
		}
	}

	// GSLB Members 삽입
	for _, member := range data.Data.GSLBMembers {
		if err := w.insertGSLBMember(tx, member); err != nil {
			return fmt.Errorf("gslb member 삽입 실패: %w", err)
		}
	}

	// Health Checks 삽입
	for _, check := range data.Data.HealthChecks {
		if err := w.insertHealthCheck(tx, check); err != nil {
			return fmt.Errorf("health check 삽입 실패: %w", err)
		}
	}

	// Adblock Sources 삽입
	for _, source := range data.Data.AdblockSources {
		if err := w.insertAdblockSource(tx, source); err != nil {
			return fmt.Errorf("adblock source 삽입 실패: %w", err)
		}
	}

	// Adblock Domains 삽입 (batch)
	if len(data.Data.AdblockDomains) > 0 {
		if err := w.insertAdblockDomainsBatch(tx, data.Data.AdblockDomains); err != nil {
			return fmt.Errorf("adblock domains 삽입 실패: %w", err)
		}
	}

	// Sync State 업데이트
	_, err = tx.Exec(`
		UPDATE sync_state
		SET last_sync_version = ?,
		    data_checksum = ?,
		    last_sync_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, data.Version, data.Checksum)
	if err != nil {
		return fmt.Errorf("sync state 업데이트 실패: %w", err)
	}

	// 트랜잭션 커밋
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("트랜잭션 커밋 실패: %w", err)
	}

	log.Printf("Full Sync 완료: Version=%d, Zones=%d, Records=%d, GSLB Policies=%d, Pools=%d, Members=%d, HealthChecks=%d, AdblockSources=%d, AdblockDomains=%d",
		data.Version, len(data.Data.Zones), len(data.Data.Records),
		len(data.Data.GSLBPolicies), len(data.Data.GSLBPools), len(data.Data.GSLBMembers), len(data.Data.HealthChecks),
		len(data.Data.AdblockSources), len(data.Data.AdblockDomains))

	// 동기화 완료 콜백 호출 (헬스체크 재시작, 캐시 클리어)
	if w.onSyncComplete != nil {
		w.onSyncComplete()
	}

	return nil
}

// incrementalSync는 변경사항만 동기화합니다
func (w *Worker) incrementalSync() error {
	// 로컬 버전 조회
	var localVersion int64
	err := w.db.Reader.QueryRow(`
		SELECT last_sync_version FROM sync_state WHERE id = 1
	`).Scan(&localVersion)
	if err == sql.ErrNoRows || localVersion == 0 {
		// 초기 상태면 Full Sync
		return w.fullSync()
	}
	if err != nil {
		return fmt.Errorf("로컬 버전 조회 실패: %w", err)
	}

	// Primary Metadata 조회
	resp, err := w.httpClient.Get(w.primaryURL + "/api/sync/metadata")
	if err != nil {
		return fmt.Errorf("primary 연결 실패: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("primary 응답 오류: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("응답 읽기 실패: %w", err)
	}

	var metadata struct {
		Version  int64  `json:"version"`
		Checksum string `json:"checksum"`
	}

	if err := json.Unmarshal(body, &metadata); err != nil {
		return fmt.Errorf("json 파싱 실패: %w", err)
	}

	// 버전이 같으면 스킵
	if metadata.Version == localVersion {
		return nil
	}

	// 버전이 다르면 Full Sync
	log.Printf("버전 불일치 감지: Local=%d, Primary=%d → Full Sync 시작", localVersion, metadata.Version)
	return w.fullSync()
}

// insertZone은 Zone을 삽입합니다
func (w *Worker) insertZone(tx *sql.Tx, zone map[string]interface{}) error {
	_, err := tx.Exec(`
		INSERT INTO zones (id, name, soa_mname, soa_rname, soa_serial, soa_refresh, soa_retry, soa_expire, soa_minimum, enabled, allow_fallback, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		zone["id"],
		zone["name"],
		zone["soa_mname"],
		zone["soa_rname"],
		zone["soa_serial"],
		zone["soa_refresh"],
		zone["soa_retry"],
		zone["soa_expire"],
		zone["soa_minimum"],
		zone["enabled"],
		zone["allow_fallback"],
		zone["created_at"],
		zone["updated_at"],
	)
	return err
}

// insertRecord는 Record를 삽입합니다
func (w *Worker) insertRecord(tx *sql.Tx, record map[string]interface{}) error {
	_, err := tx.Exec(`
		INSERT INTO records (id, zone_id, name, type, content, ttl, priority, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		record["id"],
		record["zone_id"],
		record["name"],
		record["type"],
		record["content"],
		record["ttl"],
		record["priority"],
		record["enabled"],
		record["created_at"],
		record["updated_at"],
	)
	return err
}

// insertUpstream은 Upstream Server를 삽입합니다
func (w *Worker) insertUpstream(tx *sql.Tx, upstream map[string]interface{}) error {
	_, err := tx.Exec(`
		INSERT INTO upstream_servers (id, name, address, protocol, priority, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		upstream["id"],
		upstream["name"],
		upstream["address"],
		upstream["protocol"],
		upstream["priority"],
		upstream["enabled"],
		upstream["created_at"],
		upstream["updated_at"],
	)
	return err
}

// insertGSLBPolicy는 GSLB Policy를 삽입합니다
func (w *Worker) insertGSLBPolicy(tx *sql.Tx, policy map[string]interface{}) error {
	_, err := tx.Exec(`
		INSERT INTO gslb_policies (id, name, domain, record_type, ttl, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		policy["id"],
		policy["name"],
		policy["domain"],
		policy["record_type"],
		policy["ttl"],
		policy["enabled"],
		policy["created_at"],
	)
	return err
}

// insertGSLBPool은 GSLB Pool을 삽입합니다
func (w *Worker) insertGSLBPool(tx *sql.Tx, pool map[string]interface{}) error {
	_, err := tx.Exec(`
		INSERT INTO gslb_pools (id, policy_id, name, match_type, match_value, priority, fallback_pool)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		pool["id"],
		pool["policy_id"],
		pool["name"],
		pool["match_type"],
		pool["match_value"],
		pool["priority"],
		pool["fallback_pool"],
	)
	return err
}

// insertGSLBMember는 GSLB Member를 삽입합니다
func (w *Worker) insertGSLBMember(tx *sql.Tx, member map[string]interface{}) error {
	_, err := tx.Exec(`
		INSERT INTO gslb_members (id, pool_id, address, weight, enabled)
		VALUES (?, ?, ?, ?, ?)
	`,
		member["id"],
		member["pool_id"],
		member["address"],
		member["weight"],
		member["enabled"],
	)
	return err
}

// insertAdblockSource는 Adblock Source를 삽입합니다
func (w *Worker) insertAdblockSource(tx *sql.Tx, source map[string]interface{}) error {
	_, err := tx.Exec(`
		INSERT INTO adblock_sources (id, name, url, enabled, last_sync, last_modified, rule_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		source["id"],
		source["name"],
		source["url"],
		source["enabled"],
		source["last_sync"],
		source["last_modified"],
		source["rule_count"],
		source["created_at"],
		source["updated_at"],
	)
	return err
}

// insertAdblockDomainsBatch는 Adblock Domain을 일괄 삽입합니다
func (w *Worker) insertAdblockDomainsBatch(tx *sql.Tx, domains []map[string]interface{}) error {
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO adblock_domains (domain, source_id) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, domain := range domains {
		if _, err := stmt.Exec(domain["domain"], domain["source_id"]); err != nil {
			return err
		}
	}
	return nil
}

// insertHealthCheck는 Health Check를 삽입합니다
func (w *Worker) insertHealthCheck(tx *sql.Tx, check map[string]interface{}) error {
	_, err := tx.Exec(`
		INSERT INTO health_checks (id, policy_id, check_type, target, interval_sec, timeout_sec, healthy_threshold, unhealthy_threshold, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		check["id"],
		check["policy_id"],
		check["check_type"],
		check["target"],
		check["interval_sec"],
		check["timeout_sec"],
		check["healthy_threshold"],
		check["unhealthy_threshold"],
		check["enabled"],
	)
	return err
}
