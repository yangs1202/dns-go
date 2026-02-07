package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dns-go/dns"
	"dns-go/model"
	"dns-go/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupCacheTestAPI(t *testing.T) (*API, *storage.Database) {
	db := storage.SetupTestDB(t)
	api := &API{
		db: db,
	}
	return api, db
}

func setupCacheTestAPIWithHandler(t *testing.T) (*API, *storage.Database) {
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
	)
	require.NoError(t, err)

	api := &API{
		db:         db,
		dnsHandler: handler,
	}
	return api, db
}

func TestGetCacheSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Default settings", func(t *testing.T) {
		api, _ := setupCacheTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/cache/settings", nil)

		api.getCacheSettings(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		assert.NotNil(t, resp.Data)
	})
}

func TestUpdateCacheSettings(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       interface{}
		wantStatus int
	}{
		{
			name: "Valid update with enabled",
			body: cacheSettingsRequest{
				Enabled: boolPtr(true),
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "Valid update with max_size",
			body: cacheSettingsRequest{
				MaxSize: int64Ptr(5000),
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "Valid update with prefetch_trigger",
			body: cacheSettingsRequest{
				PrefetchTrigger: float64Ptr(0.8),
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "Invalid JSON",
			body:       "invalid",
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid max_size (zero)",
			body: cacheSettingsRequest{
				MaxSize: int64Ptr(0),
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Negative default_ttl",
			body: cacheSettingsRequest{
				DefaultTTL: int64Ptr(-1),
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Negative min_ttl",
			body: cacheSettingsRequest{
				MinTTL: int64Ptr(-1),
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Negative max_ttl",
			body: cacheSettingsRequest{
				MaxTTL: int64Ptr(-1),
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Negative negative_ttl",
			body: cacheSettingsRequest{
				NegativeTTL: int64Ptr(-1),
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Prefetch trigger too high",
			body: cacheSettingsRequest{
				PrefetchTrigger: float64Ptr(1.5),
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Prefetch trigger negative",
			body: cacheSettingsRequest{
				PrefetchTrigger: float64Ptr(-0.1),
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupCacheTestAPI(t)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("PUT", "/api/cache/settings", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			api.updateCacheSettings(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestUpdateCacheSettings_WithHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	api, _ := setupCacheTestAPIWithHandler(t)

	body, _ := json.Marshal(cacheSettingsRequest{
		Enabled: boolPtr(true),
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/cache/settings", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	api.updateCacheSettings(c)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetCacheSettings_WithHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	api, _ := setupCacheTestAPIWithHandler(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/cache/settings", nil)

	api.getCacheSettings(c)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp apiResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
}

func TestClearCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Nil dnsHandler", func(t *testing.T) {
		api, _ := setupCacheTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/cache/clear", nil)

		api.clearCache(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("With dnsHandler", func(t *testing.T) {
		api, _ := setupCacheTestAPIWithHandler(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/cache/clear", nil)

		api.clearCache(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
	})
}

func TestClearCacheDomain(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Nil dnsHandler", func(t *testing.T) {
		api, _ := setupCacheTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("DELETE", "/api/cache/domain/example.com", nil)
		c.Params = gin.Params{gin.Param{Key: "domain", Value: "example.com"}}

		api.clearCacheDomain(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("With dnsHandler and valid domain", func(t *testing.T) {
		api, _ := setupCacheTestAPIWithHandler(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("DELETE", "/api/cache/domain/example.com", nil)
		c.Params = gin.Params{gin.Param{Key: "domain", Value: "example.com"}}

		api.clearCacheDomain(c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("With dnsHandler and empty domain", func(t *testing.T) {
		api, _ := setupCacheTestAPIWithHandler(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("DELETE", "/api/cache/domain/", nil)
		c.Params = gin.Params{gin.Param{Key: "domain", Value: ""}}

		api.clearCacheDomain(c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestGetCacheStats(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Nil dnsHandler", func(t *testing.T) {
		api, _ := setupCacheTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/cache/stats", nil)

		api.getCacheStats(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("With dnsHandler", func(t *testing.T) {
		api, _ := setupCacheTestAPIWithHandler(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/cache/stats", nil)

		api.getCacheStats(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
	})
}

func TestToCacheSettings(t *testing.T) {
	current := &model.CacheSettings{
		ID:              1,
		Enabled:         true,
		MaxSize:         10000,
		DefaultTTL:      300,
		MinTTL:          60,
		MaxTTL:          86400,
		NegativeTTL:     300,
		PrefetchTrigger: 0.9,
	}

	t.Run("No changes", func(t *testing.T) {
		req := cacheSettingsRequest{}
		result := toCacheSettings(req, current)
		assert.Equal(t, current.Enabled, result.Enabled)
		assert.Equal(t, current.MaxSize, result.MaxSize)
		assert.Equal(t, current.DefaultTTL, result.DefaultTTL)
	})

	t.Run("Update all fields", func(t *testing.T) {
		req := cacheSettingsRequest{
			Enabled:         boolPtr(false),
			MaxSize:         int64Ptr(5000),
			DefaultTTL:      int64Ptr(600),
			MinTTL:          int64Ptr(30),
			MaxTTL:          int64Ptr(43200),
			NegativeTTL:     int64Ptr(150),
			PrefetchTrigger: float64Ptr(0.75),
		}
		result := toCacheSettings(req, current)
		assert.False(t, result.Enabled)
		assert.Equal(t, int64(5000), result.MaxSize)
		assert.Equal(t, int64(600), result.DefaultTTL)
		assert.Equal(t, int64(30), result.MinTTL)
		assert.Equal(t, int64(43200), result.MaxTTL)
		assert.Equal(t, int64(150), result.NegativeTTL)
		assert.InDelta(t, 0.75, result.PrefetchTrigger, 0.01)
	})
}

func TestGetCacheSettings_ClosedDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupCacheTestAPI(t)
	_ = db.Writer.Close()
	_ = db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/cache/settings", nil)

	api.getCacheSettings(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateCacheSettings_ClosedDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupCacheTestAPI(t)
	_ = db.Writer.Close()
	_ = db.Reader.Close()

	body, _ := json.Marshal(cacheSettingsRequest{Enabled: boolPtr(true)})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/cache/settings", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	api.updateCacheSettings(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// Helper functions
func int64Ptr(v int64) *int64 {
	return &v
}

func float64Ptr(v float64) *float64 {
	return &v
}
