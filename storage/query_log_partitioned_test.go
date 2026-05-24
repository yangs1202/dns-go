package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"dns-go/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPartitionedQueryLogStorageBatchInsertQueryAndDeleteShard(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPartitionedQueryLogStorage(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 5, 24, 3, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -2)
	require.NoError(t, store.BatchInsert([]*model.QueryLog{
		{Timestamp: old, ClientIP: "192.0.2.1", Domain: "old.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1},
		{Timestamp: now.Add(-time.Minute), ClientIP: "192.0.2.2", Domain: "api.example.", QueryType: "AAAA", ResponseCode: "NOERROR", ResponseSource: "cache", LatencyMs: 0.2},
		{Timestamp: now, ClientIP: "192.0.2.3", Domain: "www.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "upstream", LatencyMs: 3},
	}))

	got, total, err := store.Query(QueryLogFilter{Domain: "example", Page: 1, PageSize: 2})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, got, 2)
	assert.Equal(t, "www.example.", got[0].Domain)
	assert.Equal(t, "api.example.", got[1].Domain)

	start := now.Add(-2 * time.Minute)
	end := now.Add(time.Minute)
	got, total, err = store.Query(QueryLogFilter{StartTime: &start, EndTime: &end, QueryType: "A"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, "www.example.", got[0].Domain)

	deleted, err := store.DeleteBefore(now.AddDate(0, 0, -1))
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	assert.NoFileExists(t, filepath.Join(dir, "query_logs_2026-05-22.db"))
	assert.FileExists(t, filepath.Join(dir, "query_logs_2026-05-24.db"))
}

func TestPartitionedQueryLogStorageDeleteClosesActiveOldWriter(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPartitionedQueryLogStorage(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	old := time.Date(2026, 5, 20, 23, 0, 0, 0, time.UTC)
	require.NoError(t, store.BatchInsert([]*model.QueryLog{{
		Timestamp: old, ClientIP: "192.0.2.1", Domain: "old.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1,
	}}))

	path := filepath.Join(dir, "query_logs_2026-05-20.db")
	require.FileExists(t, path)

	deleted, err := store.DeleteBefore(time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	assert.NoFileExists(t, path)

	require.NoError(t, store.BatchInsert([]*model.QueryLog{{
		Timestamp: old.AddDate(0, 0, 2), ClientIP: "192.0.2.2", Domain: "new.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1,
	}}))
	assert.FileExists(t, filepath.Join(dir, "query_logs_2026-05-22.db"))
}

func TestPartitionedQueryLogStorageIgnoresNonShardFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "query_logs_bad.db"), []byte("ignore"), 0644))

	store, err := NewPartitionedQueryLogStorage(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	logs, total, err := store.Query(QueryLogFilter{})
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, logs)
}

func TestPartitionedQueryLogStoragePaginationAcrossShards(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPartitionedQueryLogStorage(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	base := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.BatchInsert([]*model.QueryLog{
		{Timestamp: base.Add(-72 * time.Hour), ClientIP: "192.0.2.1", Domain: "one.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1},
		{Timestamp: base.Add(-48 * time.Hour), ClientIP: "192.0.2.1", Domain: "two.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1},
		{Timestamp: base.Add(-24 * time.Hour), ClientIP: "192.0.2.1", Domain: "three.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1},
		{Timestamp: base, ClientIP: "192.0.2.1", Domain: "four.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1},
	}))

	got, total, err := store.Query(QueryLogFilter{Page: 2, PageSize: 2})
	require.NoError(t, err)
	assert.Equal(t, int64(4), total)
	require.Len(t, got, 2)
	assert.Equal(t, "two.example.", got[0].Domain)
	assert.Equal(t, "one.example.", got[1].Domain)
}

func TestPartitionedQueryLogStorageDeleteBeforeKeepsCutoffDay(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPartitionedQueryLogStorage(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	cutoff := time.Date(2026, 5, 24, 9, 30, 0, 0, time.UTC)
	require.NoError(t, store.BatchInsert([]*model.QueryLog{
		{Timestamp: cutoff.AddDate(0, 0, -1), ClientIP: "192.0.2.1", Domain: "old.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1},
		{Timestamp: cutoff.Add(-time.Hour), ClientIP: "192.0.2.2", Domain: "same-day.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1},
	}))

	deleted, err := store.DeleteBefore(cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	assert.NoFileExists(t, filepath.Join(dir, "query_logs_2026-05-23.db"))
	assert.FileExists(t, filepath.Join(dir, "query_logs_2026-05-24.db"))
}

func TestPartitionedQueryLogStorageDeleteRemovesWalAndShmFiles(t *testing.T) {
	dir := t.TempDir()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "query_logs_2026-05-20.db"+suffix), []byte("x"), 0644))
	}

	store, err := NewPartitionedQueryLogStorage(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	deleted, err := store.DeleteBefore(time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	assert.NoFileExists(t, filepath.Join(dir, "query_logs_2026-05-20.db"))
	assert.NoFileExists(t, filepath.Join(dir, "query_logs_2026-05-20.db-wal"))
	assert.NoFileExists(t, filepath.Join(dir, "query_logs_2026-05-20.db-shm"))
}

func TestPartitionedQueryLogStorageQueryActiveWriterShard(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPartitionedQueryLogStorage(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 5, 24, 13, 0, 0, 0, time.UTC)
	require.NoError(t, store.BatchInsert([]*model.QueryLog{{
		Timestamp: now, ClientIP: "192.0.2.1", Domain: "active.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "cache", LatencyMs: 0.1,
	}}))

	got, total, err := store.Query(QueryLogFilter{ResponseSource: "cache"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, "active.example.", got[0].Domain)
}
