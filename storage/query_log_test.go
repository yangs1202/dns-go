package storage

import (
	"dns-go/model"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueryLogStorageBatchInsertAndQuery(t *testing.T) {
	db := SetupTestDB(t)
	store := NewQueryLogStorage(db)
	require.NotNil(t, store)

	now := time.Now().UTC().Truncate(time.Second)
	logs := []*model.QueryLog{
		{
			Timestamp:      now.Add(-2 * time.Minute),
			ClientIP:       "192.0.2.10",
			Domain:         "example.com.",
			QueryType:      "A",
			ResponseCode:   "NOERROR",
			ResponseSource: "cache",
			LatencyMs:      0.25,
			ResponseData:   "192.0.2.1",
			Protocol:       "udp",
			ResponseSize:   64,
			EDNSPresent:    true,
			EDNSVersion:    0,
			EDNSBufferSize: 1232,
		},
		{
			Timestamp:      now.Add(-1 * time.Minute),
			ClientIP:       "192.0.2.11",
			Domain:         "api.example.com.",
			QueryType:      "AAAA",
			ResponseCode:   "NXDOMAIN",
			ResponseSource: "upstream",
			LatencyMs:      2.5,
			Protocol:       "tcp",
			ResponseSize:   96,
		},
	}

	require.NoError(t, store.BatchInsert(nil))
	require.NoError(t, store.BatchInsert(logs))

	got, total, err := store.Query(QueryLogFilter{
		Domain:         "example.com",
		QueryType:      "A",
		ResponseCode:   "NOERROR",
		ResponseSource: "cache",
		ClientIP:       "192.0.2.10",
		Page:           1,
		PageSize:       10,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, "example.com.", got[0].Domain)
	assert.Equal(t, "udp", got[0].Protocol)
	assert.True(t, got[0].EDNSPresent)
	assert.Equal(t, 1232, got[0].EDNSBufferSize)
	assert.Equal(t, "192.0.2.1", got[0].ResponseData)

	start := now.Add(-90 * time.Second)
	end := now.Add(30 * time.Second)
	got, total, err = store.Query(QueryLogFilter{StartTime: &start, EndTime: &end})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, "api.example.com.", got[0].Domain)
}

func TestQueryLogStorageQueryPaginationBounds(t *testing.T) {
	db := SetupTestDB(t)
	store := NewQueryLogStorage(db)
	now := time.Now().UTC()

	for i := 0; i < 3; i++ {
		require.NoError(t, store.BatchInsert([]*model.QueryLog{{
			Timestamp:      now.Add(time.Duration(i) * time.Second),
			ClientIP:       "192.0.2.10",
			Domain:         "example.com.",
			QueryType:      "A",
			ResponseCode:   "NOERROR",
			ResponseSource: "zone",
			LatencyMs:      1,
		}}))
	}

	got, total, err := store.Query(QueryLogFilter{Page: -1, PageSize: 500})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, got, 3)
}

func TestQueryLogStorageDeleteBefore(t *testing.T) {
	db := SetupTestDB(t)
	store := NewQueryLogStorage(db)
	now := time.Now().UTC()

	require.NoError(t, store.BatchInsert([]*model.QueryLog{
		{Timestamp: now.Add(-48 * time.Hour), ClientIP: "192.0.2.1", Domain: "old.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1},
		{Timestamp: now, ClientIP: "192.0.2.2", Domain: "new.example.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1},
	}))

	deleted, err := store.DeleteBefore(now.Add(-24 * time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	got, total, err := store.Query(QueryLogFilter{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, got, 1)
	assert.Equal(t, "new.example.", got[0].Domain)
}
