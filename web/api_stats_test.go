package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dns-go/dns"
	"dns-go/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupStatsTestAPI(t *testing.T) *API {
	db := storage.SetupTestDB(t)
	zoneStorage := storage.NewZoneStorage(db)
	recordStorage := storage.NewRecordStorage(db)
	queryStats := dns.NewQueryStats()

	handler, err := dns.NewHandler(
		zoneStorage,
		recordStorage,
		nil, // resolver
		db,
		queryStats,
		nil, // gslbEngine
		nil, // adblockFilter
		nil, // adblockStorage
		"",  // adblockResponse
		"",  // nsid
		"",  // version
		nil, // queryLogWriter
	)
	require.NoError(t, err)
	t.Cleanup(handler.Stop)

	api := &API{
		db:         db,
		queryStats: queryStats,
		dnsHandler: handler,
	}
	return api
}

func TestGetStats(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Nil queryStats", func(t *testing.T) {
		api := &API{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/stats", nil)

		api.getStats(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.False(t, resp.Success)
	})

	t.Run("Nil dnsHandler", func(t *testing.T) {
		api := &API{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/stats", nil)

		api.getStats(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("Valid stats", func(t *testing.T) {
		api := setupStatsTestAPI(t)

		// Increment some stats
		api.queryStats.IncTotal()
		api.queryStats.IncTotal()
		api.queryStats.IncL1Hit()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/stats", nil)

		api.getStats(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		assert.NotNil(t, resp.Data)
	})
}
