package storage

import (
	"database/sql"
	"dns-go/model"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const queryLogShardPrefix = "query_logs_"
const queryLogShardSuffix = ".db"
const queryLogShardDateLayout = "2006-01-02"

// PartitionedQueryLogStorage stores DNS query logs in one SQLite file per UTC day.
type PartitionedQueryLogStorage struct {
	dir string

	mu        sync.Mutex
	writerDay string
	writerDB  *sql.DB
}

// NewPartitionedQueryLogStorage creates a query-log storage backed by daily SQLite shards.
func NewPartitionedQueryLogStorage(dir string) (*PartitionedQueryLogStorage, error) {
	if dir == "" {
		return nil, fmt.Errorf("쿼리 로그 디렉터리가 설정되지 않았습니다")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("쿼리 로그 디렉터리 생성 실패: %w", err)
	}
	return &PartitionedQueryLogStorage{dir: dir}, nil
}

// Close closes the currently cached writer connection.
func (s *PartitionedQueryLogStorage) Close() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeWriterLocked()
}

// BatchInsert inserts logs into daily shard files grouped by each log timestamp.
func (s *PartitionedQueryLogStorage) BatchInsert(logs []*model.QueryLog) error {
	if len(logs) == 0 {
		return nil
	}

	grouped := make(map[string][]*model.QueryLog)
	for _, l := range logs {
		if l == nil {
			continue
		}
		day := queryLogShardDay(l.Timestamp)
		grouped[day] = append(grouped[day], l)
	}
	if len(grouped) == 0 {
		return nil
	}

	days := make([]string, 0, len(grouped))
	for day := range grouped {
		days = append(days, day)
	}
	sort.Strings(days)

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, day := range days {
		db, err := s.writerForDayLocked(day)
		if err != nil {
			return err
		}
		if err := insertQueryLogBatch(db, grouped[day]); err != nil {
			if closeErr := s.closeWriterLocked(); closeErr != nil {
				return fmt.Errorf("query log shard insert 실패 (%s): %w (writer 재오픈 준비 실패: %v)", day, err, closeErr)
			}
			return fmt.Errorf("query log shard insert 실패 (%s): %w", day, err)
		}
	}
	return nil
}

// Query returns query logs from all matching shard files, ordered by timestamp descending.
func (s *PartitionedQueryLogStorage) Query(filter QueryLogFilter) ([]*model.QueryLog, int64, error) {
	filter = normalizeQueryLogFilter(filter)
	shards, err := s.matchingShardPaths(filter)
	if err != nil {
		return nil, 0, err
	}
	if len(shards) == 0 {
		return nil, 0, nil
	}

	where, args := buildQueryLogWhere(filter)
	offset := (filter.Page - 1) * filter.PageSize
	limit := offset + filter.PageSize

	var total int64
	candidates := make([]*model.QueryLog, 0, limit)
	for _, shard := range shards {
		db, err := sql.Open("sqlite", queryLogShardReaderDSN(shard))
		if err != nil {
			return nil, 0, fmt.Errorf("query log shard 열기 실패 (%s): %w", shard, err)
		}

		shardTotal, shardLogs, queryErr := queryShard(db, where, args, limit)
		closeErr := db.Close()
		if queryErr != nil {
			return nil, 0, fmt.Errorf("query log shard 조회 실패 (%s): %w", shard, queryErr)
		}
		if closeErr != nil {
			return nil, 0, fmt.Errorf("query log shard 닫기 실패 (%s): %w", shard, closeErr)
		}

		total += shardTotal
		candidates = append(candidates, shardLogs...)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Timestamp.Equal(candidates[j].Timestamp) {
			return candidates[i].ID > candidates[j].ID
		}
		return candidates[i].Timestamp.After(candidates[j].Timestamp)
	})

	if offset >= len(candidates) {
		return nil, total, nil
	}
	end := offset + filter.PageSize
	if end > len(candidates) {
		end = len(candidates)
	}
	return candidates[offset:end], total, nil
}

// DeleteBefore deletes complete shard files older than the UTC day of cutoff.
func (s *PartitionedQueryLogStorage) DeleteBefore(cutoff time.Time) (int64, error) {
	cutoffDay := queryLogShardDay(cutoff)
	shards, err := s.listShardPaths()
	if err != nil {
		return 0, err
	}

	var deleted int64
	for day, path := range shards {
		if day >= cutoffDay {
			continue
		}
		if err := s.closeWriterForDay(day); err != nil {
			return deleted, err
		}
		removed, err := removeQueryLogShard(path)
		if err != nil {
			return deleted, err
		}
		if removed {
			deleted++
		}
	}
	return deleted, nil
}

func (s *PartitionedQueryLogStorage) writerForDayLocked(day string) (*sql.DB, error) {
	if s.writerDB != nil && s.writerDay == day {
		return s.writerDB, nil
	}
	if err := s.closeWriterLocked(); err != nil {
		return nil, err
	}

	path := s.shardPath(day)
	db, err := sql.Open("sqlite", queryLogShardWriterDSN(path))
	if err != nil {
		return nil, fmt.Errorf("query log shard writer 연결 실패 (%s): %w", path, err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("query log shard writer ping 실패 (%s): %w", path, err)
	}
	if err := ensureQueryLogSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("query log shard busy_timeout 설정 실패 (%s): %w", path, err)
	}

	s.writerDay = day
	s.writerDB = db
	return db, nil
}

func queryLogShardWriterDSN(path string) string {
	return sqliteDSN(path)
}

func queryLogShardReaderDSN(path string) string {
	return appendSQLiteParams(path, "mode=ro", "_pragma=busy_timeout(5000)")
}

func appendSQLiteParams(path string, params ...string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + strings.Join(params, "&")
}

func (s *PartitionedQueryLogStorage) closeWriterLocked() error {
	if s.writerDB == nil {
		s.writerDay = ""
		return nil
	}
	err := s.writerDB.Close()
	s.writerDB = nil
	s.writerDay = ""
	if err != nil {
		return fmt.Errorf("query log shard writer 닫기 실패: %w", err)
	}
	return nil
}

func (s *PartitionedQueryLogStorage) closeWriterForDay(day string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.writerDay != day {
		return nil
	}
	return s.closeWriterLocked()
}

func (s *PartitionedQueryLogStorage) matchingShardPaths(filter QueryLogFilter) ([]string, error) {
	shards, err := s.listShardPaths()
	if err != nil {
		return nil, err
	}

	startDay := ""
	endDay := ""
	if filter.StartTime != nil {
		startDay = queryLogShardDay(*filter.StartTime)
	}
	if filter.EndTime != nil {
		endDay = queryLogShardDay(*filter.EndTime)
	}

	var paths []string
	for day, path := range shards {
		if startDay != "" && day < startDay {
			continue
		}
		if endDay != "" && day > endDay {
			continue
		}
		paths = append(paths, path)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	return paths, nil
}

func (s *PartitionedQueryLogStorage) listShardPaths() (map[string]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("query log shard 목록 조회 실패: %w", err)
	}

	shards := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		day, ok := parseQueryLogShardName(entry.Name())
		if !ok {
			continue
		}
		shards[day] = filepath.Join(s.dir, entry.Name())
	}
	return shards, nil
}

func (s *PartitionedQueryLogStorage) shardPath(day string) string {
	return filepath.Join(s.dir, queryLogShardPrefix+day+queryLogShardSuffix)
}

func queryLogShardDay(t time.Time) string {
	return t.UTC().Format(queryLogShardDateLayout)
}

func parseQueryLogShardName(name string) (string, bool) {
	if !strings.HasPrefix(name, queryLogShardPrefix) || !strings.HasSuffix(name, queryLogShardSuffix) {
		return "", false
	}
	day := strings.TrimSuffix(strings.TrimPrefix(name, queryLogShardPrefix), queryLogShardSuffix)
	if _, err := time.Parse(queryLogShardDateLayout, day); err != nil {
		return "", false
	}
	return day, true
}

func insertQueryLogBatch(db *sql.DB, logs []*model.QueryLog) error {
	tx, err := db.Begin()
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
		if l == nil {
			continue
		}
		ednsPresent := 0
		if l.EDNSPresent {
			ednsPresent = 1
		}
		if _, err := stmt.Exec(
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
		); err != nil {
			return fmt.Errorf("로그 삽입 실패: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("query log batch commit 실패: %w", err)
	}
	return nil
}

func queryShard(db *sql.DB, where string, args []interface{}, limit int) (int64, []*model.QueryLog, error) {
	var total int64
	countQuery := "SELECT COUNT(*) FROM query_logs" + where
	if err := db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return 0, nil, fmt.Errorf("카운트 조회 실패: %w", err)
	}

	dataQuery := "SELECT id, timestamp, client_ip, domain, query_type, response_code, response_source, latency_ms, response_data, protocol, response_size, edns_present, edns_version, edns_buffer_size FROM query_logs" + where + " ORDER BY timestamp DESC LIMIT ?"
	dataArgs := append(append([]interface{}{}, args...), limit)
	rows, err := db.Query(dataQuery, dataArgs...)
	if err != nil {
		return 0, nil, fmt.Errorf("데이터 조회 실패: %w", err)
	}
	defer func() { _ = rows.Close() }()

	logs, err := scanQueryLogRows(rows)
	if err != nil {
		return 0, nil, err
	}
	return total, logs, nil
}

func removeQueryLogShard(path string) (bool, error) {
	removed := false
	for _, suffix := range []string{"", "-wal", "-shm"} {
		target := path + suffix
		if err := os.Remove(target); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, fmt.Errorf("query log shard 삭제 실패 (%s): %w", target, err)
		}
		if suffix == "" {
			removed = true
		}
	}
	return removed, nil
}
