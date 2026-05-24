package storage

import (
	"database/sql"
	"dns-go/model"
	"fmt"
	"strings"
	"time"
)

// QueryLogRepository는 쿼리 로그 저장소 동작을 정의합니다.
type QueryLogRepository interface {
	BatchInsert(logs []*model.QueryLog) error
	Query(filter QueryLogFilter) ([]*model.QueryLog, int64, error)
	DeleteBefore(cutoff time.Time) (int64, error)
}

// QueryLogStorage는 DNS 쿼리 로그 저장소입니다
type QueryLogStorage struct {
	db *Database
}

// NewQueryLogStorage는 새로운 QueryLogStorage를 생성합니다
func NewQueryLogStorage(db *Database) *QueryLogStorage {
	return &QueryLogStorage{db: db}
}

type queryLogSchemaExecutor interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}

const queryLogsTableSchema = `CREATE TABLE IF NOT EXISTS query_logs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	timestamp DATETIME NOT NULL,
	client_ip TEXT NOT NULL,
	domain TEXT NOT NULL,
	query_type TEXT NOT NULL,
	response_code TEXT NOT NULL,
	response_source TEXT NOT NULL,
	latency_ms REAL NOT NULL,
	response_data TEXT DEFAULT '',
	protocol TEXT DEFAULT 'udp',
	response_size INTEGER DEFAULT 0,
	edns_present INTEGER DEFAULT 0,
	edns_version INTEGER DEFAULT 0,
	edns_buffer_size INTEGER DEFAULT 0
)`

var queryLogsIndexSchemas = []string{
	`CREATE INDEX IF NOT EXISTS idx_query_logs_timestamp ON query_logs(timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_query_logs_domain ON query_logs(domain, timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_query_logs_client_ip ON query_logs(client_ip, timestamp)`,
	`CREATE INDEX IF NOT EXISTS idx_query_logs_source ON query_logs(response_source, timestamp)`,
}

func ensureQueryLogSchema(exec queryLogSchemaExecutor) error {
	if _, err := exec.Exec(queryLogsTableSchema); err != nil {
		return fmt.Errorf("query_logs 테이블 생성 실패: %w", err)
	}
	for _, schema := range queryLogsIndexSchemas {
		if _, err := exec.Exec(schema); err != nil {
			return fmt.Errorf("query_logs 인덱스 생성 실패: %w", err)
		}
	}
	return nil
}

// BatchInsert는 여러 쿼리 로그를 한 번의 트랜잭션으로 삽입합니다
func (s *QueryLogStorage) BatchInsert(logs []*model.QueryLog) error {
	if len(logs) == 0 {
		return nil
	}

	tx, err := s.db.Writer.Begin()
	if err != nil {
		return fmt.Errorf("트랜잭션 시작 실패: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`INSERT INTO query_logs (timestamp, client_ip, domain, query_type, response_code, response_source, latency_ms, response_data, protocol, response_size, edns_present, edns_version, edns_buffer_size)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepared statement 실패: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, l := range logs {
		ednsPresent := 0
		if l.EDNSPresent {
			ednsPresent = 1
		}
		_, err := stmt.Exec(
			l.Timestamp.UTC(),
			l.ClientIP,
			l.Domain,
			l.QueryType,
			l.ResponseCode,
			l.ResponseSource,
			l.LatencyMs,
			l.ResponseData,
			l.Protocol,
			l.ResponseSize,
			ednsPresent,
			l.EDNSVersion,
			l.EDNSBufferSize,
		)
		if err != nil {
			return fmt.Errorf("로그 삽입 실패: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("query log batch commit 실패: %w", err)
	}
	return nil
}

// QueryLogFilter는 쿼리 로그 검색 필터입니다
type QueryLogFilter struct {
	Domain         string
	ClientIP       string
	QueryType      string
	ResponseCode   string
	ResponseSource string
	StartTime      *time.Time
	EndTime        *time.Time
	Page           int
	PageSize       int
}

// Query는 필터 조건에 맞는 쿼리 로그를 반환합니다
func (s *QueryLogStorage) Query(filter QueryLogFilter) ([]*model.QueryLog, int64, error) {
	filter = normalizeQueryLogFilter(filter)
	where, args := buildQueryLogWhere(filter)

	// 총 개수 조회
	var total int64
	countQuery := "SELECT COUNT(*) FROM query_logs" + where
	if err := s.db.Reader.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("카운트 조회 실패: %w", err)
	}

	// 데이터 조회
	offset := (filter.Page - 1) * filter.PageSize
	dataQuery := "SELECT id, timestamp, client_ip, domain, query_type, response_code, response_source, latency_ms, response_data, protocol, response_size, edns_present, edns_version, edns_buffer_size FROM query_logs" + where + " ORDER BY timestamp DESC LIMIT ? OFFSET ?"
	dataArgs := append(args, filter.PageSize, offset)

	rows, err := s.db.Reader.Query(dataQuery, dataArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("데이터 조회 실패: %w", err)
	}
	defer func() { _ = rows.Close() }()

	logs, err := scanQueryLogRows(rows)
	if err != nil {
		return nil, 0, err
	}
	return logs, total, nil
}

func normalizeQueryLogFilter(filter QueryLogFilter) QueryLogFilter {
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 50
	}
	if filter.PageSize > 200 {
		filter.PageSize = 200
	}
	return filter
}

func buildQueryLogWhere(filter QueryLogFilter) (string, []interface{}) {
	var conditions []string
	var args []interface{}

	if filter.Domain != "" {
		conditions = append(conditions, "domain LIKE ?")
		args = append(args, "%"+filter.Domain+"%")
	}
	if filter.ClientIP != "" {
		conditions = append(conditions, "client_ip = ?")
		args = append(args, filter.ClientIP)
	}
	if filter.QueryType != "" {
		conditions = append(conditions, "query_type = ?")
		args = append(args, filter.QueryType)
	}
	if filter.ResponseCode != "" {
		conditions = append(conditions, "response_code = ?")
		args = append(args, filter.ResponseCode)
	}
	if filter.ResponseSource != "" {
		conditions = append(conditions, "response_source = ?")
		args = append(args, filter.ResponseSource)
	}
	if filter.StartTime != nil {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, filter.StartTime.UTC())
	}
	if filter.EndTime != nil {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, filter.EndTime.UTC())
	}

	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func scanQueryLogRows(rows *sql.Rows) ([]*model.QueryLog, error) {
	var logs []*model.QueryLog
	for rows.Next() {
		var l model.QueryLog
		var ednsPresent int
		if err := rows.Scan(
			&l.ID, &l.Timestamp, &l.ClientIP, &l.Domain, &l.QueryType,
			&l.ResponseCode, &l.ResponseSource, &l.LatencyMs, &l.ResponseData,
			&l.Protocol, &l.ResponseSize, &ednsPresent, &l.EDNSVersion, &l.EDNSBufferSize,
		); err != nil {
			return nil, fmt.Errorf("행 스캔 실패: %w", err)
		}
		l.EDNSPresent = ednsPresent == 1
		logs = append(logs, &l)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query log 행 반복 실패: %w", err)
	}
	return logs, nil
}

// DeleteBefore는 지정 시각 이전의 로그를 배치 삭제합니다
func (s *QueryLogStorage) DeleteBefore(cutoff time.Time) (int64, error) {
	var totalDeleted int64
	for {
		result, err := s.db.Writer.Exec("DELETE FROM query_logs WHERE id IN (SELECT id FROM query_logs WHERE timestamp < ? LIMIT 5000)", cutoff.UTC())
		if err != nil {
			return totalDeleted, fmt.Errorf("로그 삭제 실패: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return totalDeleted, fmt.Errorf("삭제 결과 확인 실패: %w", err)
		}
		totalDeleted += affected
		if affected < 5000 {
			break
		}
	}
	return totalDeleted, nil
}
