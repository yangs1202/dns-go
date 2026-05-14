package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dns-go/model"
	"dns-go/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupQueryLogsAPI(t *testing.T) (*API, *storage.QueryLogStorage) {
	db := storage.SetupTestDB(t)
	queryLogs := storage.NewQueryLogStorage(db)
	return &API{queryLogStorage: queryLogs}, queryLogs
}

func TestGetQueryLogsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api := &API{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/query-logs", nil)

	api.getQueryLogs(c)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetQueryLogsWithFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, store := setupQueryLogsAPI(t)
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, store.BatchInsert([]*model.QueryLog{
		{Timestamp: now.Add(-time.Minute), ClientIP: "192.0.2.1", Domain: "example.com.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1},
		{Timestamp: now, ClientIP: "192.0.2.2", Domain: "other.com.", QueryType: "AAAA", ResponseCode: "NXDOMAIN", ResponseSource: "upstream", LatencyMs: 2},
	}))

	path := "/api/query-logs?domain=example&page=1&page_size=1&query_type=A&response_code=NOERROR&response_source=zone&client_ip=192.0.2.1&start_time=" +
		now.Add(-2*time.Minute).Format(time.RFC3339) + "&end_time=" + now.Format(time.RFC3339)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)

	api.getQueryLogs(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp apiResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)

	data, ok := resp.Data.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(1), data["total"])
	assert.Equal(t, float64(1), data["page"])
	assert.Equal(t, float64(1), data["page_size"])
	assert.Equal(t, float64(1), data["total_pages"])
	logs, ok := data["logs"].([]interface{})
	require.True(t, ok)
	assert.Len(t, logs, 1)
}

func TestGetQueryLogsInvalidTimeAndParseIntDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, store := setupQueryLogsAPI(t)
	require.NoError(t, store.BatchInsert([]*model.QueryLog{{
		Timestamp: time.Now().UTC(), ClientIP: "192.0.2.1", Domain: "example.com.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1,
	}}))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/query-logs?page=-1&page_size=bad&start_time=bad&end_time=bad", nil)

	assert.Equal(t, 7, parseIntParam(c, "missing", 7))
	assert.Equal(t, 7, parseIntParam(c, "page", 7))
	assert.Equal(t, 7, parseIntParam(c, "page_size", 7))

	api.getQueryLogs(c)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetQueryLogsCapsPageSizeMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, store := setupQueryLogsAPI(t)
	require.NoError(t, store.BatchInsert([]*model.QueryLog{{
		Timestamp: time.Now().UTC(), ClientIP: "192.0.2.1", Domain: "example.com.", QueryType: "A", ResponseCode: "NOERROR", ResponseSource: "zone", LatencyMs: 1,
	}}))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/query-logs?page_size=500", nil)

	api.getQueryLogs(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp apiResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, float64(200), data["page_size"])
}
