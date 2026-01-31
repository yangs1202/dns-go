package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// SyncVersion은 동기화 버전 관리를 담당합니다
type SyncVersion struct {
	db *Database
}

// NewSyncVersion은 SyncVersion 인스턴스를 생성합니다
func NewSyncVersion(db *Database) *SyncVersion {
	return &SyncVersion{db: db}
}

// IncrementVersion은 데이터 변경 시 버전을 증가시킵니다
func (s *SyncVersion) IncrementVersion(tx *sql.Tx) error {
	var executor interface {
		Exec(query string, args ...interface{}) (sql.Result, error)
	}

	if tx != nil {
		executor = tx
	} else {
		executor = s.db.Writer
	}

	_, err := executor.Exec(`
		UPDATE sync_state
		SET last_sync_version = last_sync_version + 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`)
	return err
}

// GetVersion은 현재 동기화 버전을 조회합니다
func (s *SyncVersion) GetVersion() (int64, error) {
	var version int64
	err := s.db.Reader.QueryRow(`
		SELECT last_sync_version FROM sync_state WHERE id = 1
	`).Scan(&version)
	return version, err
}

// GetChecksum은 현재 저장된 체크섬을 조회합니다
func (s *SyncVersion) GetChecksum() (string, error) {
	var checksum sql.NullString
	err := s.db.Reader.QueryRow(`
		SELECT data_checksum FROM sync_state WHERE id = 1
	`).Scan(&checksum)
	if err != nil {
		return "", err
	}
	return checksum.String, nil
}

// CalculateChecksum은 전체 데이터의 체크섬을 계산합니다
func (s *SyncVersion) CalculateChecksum() (string, error) {
	data := make(map[string]interface{})

	// Zones
	zones, err := s.GetAllZones()
	if err != nil {
		return "", err
	}
	data["zones"] = zones

	// Records
	records, err := s.GetAllRecords()
	if err != nil {
		return "", err
	}
	data["records"] = records

	// Upstream Servers
	upstreams, err := s.GetAllUpstreams()
	if err != nil {
		return "", err
	}
	data["upstreams"] = upstreams

	// JSON 직렬화
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("JSON 직렬화 실패: %w", err)
	}

	// SHA256 해시
	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:]), nil
}

// UpdateChecksum은 체크섬을 업데이트합니다
func (s *SyncVersion) UpdateChecksum() error {
	checksum, err := s.CalculateChecksum()
	if err != nil {
		return err
	}

	_, err = s.db.Writer.Exec(`
		UPDATE sync_state
		SET data_checksum = ?,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, checksum)
	return err
}

// GetSyncState는 동기화 상태 전체를 조회합니다
func (s *SyncVersion) GetSyncState() (map[string]interface{}, error) {
	var version int64
	var checksum sql.NullString
	var lastSyncAt sql.NullTime

	err := s.db.Reader.QueryRow(`
		SELECT last_sync_version, data_checksum, last_sync_at
		FROM sync_state WHERE id = 1
	`).Scan(&version, &checksum, &lastSyncAt)
	if err != nil {
		return nil, err
	}

	state := map[string]interface{}{
		"version":  version,
		"checksum": checksum.String,
	}

	if lastSyncAt.Valid {
		state["last_sync_at"] = lastSyncAt.Time
	}

	return state, nil
}

// GetAllZones는 모든 Zone을 조회합니다
func (s *SyncVersion) GetAllZones() ([]map[string]interface{}, error) {
	rows, err := s.db.Reader.Query(`
		SELECT id, name, soa_mname, soa_rname, soa_serial, soa_refresh,
		       soa_retry, soa_expire, soa_minimum, enabled, allow_fallback,
		       created_at, updated_at
		FROM zones
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var zones []map[string]interface{}
	for rows.Next() {
		var id, soaSerial, soaRefresh, soaRetry, soaExpire, soaMinimum, enabled, allowFallback int
		var name, soaMname, soaRname string
		var createdAt, updatedAt time.Time

		err := rows.Scan(&id, &name, &soaMname, &soaRname, &soaSerial, &soaRefresh,
			&soaRetry, &soaExpire, &soaMinimum, &enabled, &allowFallback,
			&createdAt, &updatedAt)
		if err != nil {
			continue
		}

		zones = append(zones, map[string]interface{}{
			"id":             id,
			"name":           name,
			"soa_mname":      soaMname,
			"soa_rname":      soaRname,
			"soa_serial":     soaSerial,
			"soa_refresh":    soaRefresh,
			"soa_retry":      soaRetry,
			"soa_expire":     soaExpire,
			"soa_minimum":    soaMinimum,
			"enabled":        enabled,
			"allow_fallback": allowFallback,
			"created_at":     createdAt,
			"updated_at":     updatedAt,
		})
	}

	return zones, nil
}

// GetAllRecords는 모든 Record를 조회합니다
func (s *SyncVersion) GetAllRecords() ([]map[string]interface{}, error) {
	rows, err := s.db.Reader.Query(`
		SELECT id, zone_id, name, type, content, ttl, priority, enabled,
		       created_at, updated_at
		FROM records
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []map[string]interface{}
	for rows.Next() {
		var id, zoneID, ttl, priority, enabled int
		var name, recordType, content string
		var createdAt, updatedAt time.Time

		err := rows.Scan(&id, &zoneID, &name, &recordType, &content, &ttl,
			&priority, &enabled, &createdAt, &updatedAt)
		if err != nil {
			continue
		}

		records = append(records, map[string]interface{}{
			"id":         id,
			"zone_id":    zoneID,
			"name":       name,
			"type":       recordType,
			"content":    content,
			"ttl":        ttl,
			"priority":   priority,
			"enabled":    enabled,
			"created_at": createdAt,
			"updated_at": updatedAt,
		})
	}

	return records, nil
}

// GetAllUpstreams는 모든 Upstream Server를 조회합니다
func (s *SyncVersion) GetAllUpstreams() ([]map[string]interface{}, error) {
	rows, err := s.db.Reader.Query(`
		SELECT id, name, address, protocol, priority, enabled,
		       created_at, updated_at
		FROM upstream_servers
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var upstreams []map[string]interface{}
	for rows.Next() {
		var id, priority, enabled int
		var name, address, protocol string
		var createdAt, updatedAt time.Time

		err := rows.Scan(&id, &name, &address, &protocol, &priority, &enabled,
			&createdAt, &updatedAt)
		if err != nil {
			continue
		}

		upstreams = append(upstreams, map[string]interface{}{
			"id":         id,
			"name":       name,
			"address":    address,
			"protocol":   protocol,
			"priority":   priority,
			"enabled":    enabled,
			"created_at": createdAt,
			"updated_at": updatedAt,
		})
	}

	return upstreams, nil
}
