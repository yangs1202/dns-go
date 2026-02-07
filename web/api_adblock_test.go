package web

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dns-go/adblock"
	"dns-go/model"
	"dns-go/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAdblockTestAPI(t *testing.T) (*API, *storage.Database) {
	db := storage.SetupTestDB(t)
	adblockStorage := storage.NewAdblockStorage(db)

	api := &API{
		db:             db,
		adblockStorage: adblockStorage,
	}
	return api, db
}

func createTestAdblockSource(api *API, name, url string) int64 {
	id, _ := api.adblockStorage.CreateAdblockSource(&model.AdblockSource{
		Name:    name,
		URL:     url,
		Enabled: true,
	})
	return id
}

func TestToAdblockSourceResponse(t *testing.T) {
	now := time.Now()

	t.Run("Without optional fields", func(t *testing.T) {
		src := &model.AdblockSource{
			ID:        1,
			Name:      "Test Source",
			URL:       "https://example.com/list.txt",
			Enabled:   true,
			RuleCount: 1000,
			CreatedAt: now,
			UpdatedAt: now,
		}
		resp := toAdblockSourceResponse(src)
		assert.Equal(t, src.ID, resp.ID)
		assert.Equal(t, src.Name, resp.Name)
		assert.Equal(t, src.URL, resp.URL)
		assert.True(t, resp.Enabled)
		assert.Nil(t, resp.LastSync)
		assert.Nil(t, resp.LastModified)
		assert.Equal(t, int64(1000), resp.RuleCount)
	})

	t.Run("With LastSync", func(t *testing.T) {
		src := &model.AdblockSource{
			ID:        1,
			Name:      "Test",
			URL:       "https://example.com/list.txt",
			Enabled:   true,
			LastSync:  sql.NullTime{Time: now, Valid: true},
			CreatedAt: now,
			UpdatedAt: now,
		}
		resp := toAdblockSourceResponse(src)
		assert.NotNil(t, resp.LastSync)
	})

	t.Run("With valid LastModified", func(t *testing.T) {
		src := &model.AdblockSource{
			ID:           1,
			Name:         "Test",
			URL:          "https://example.com/list.txt",
			Enabled:      true,
			LastModified: sql.NullString{String: "Sat, 31 Jan 2026 12:12:23 GMT", Valid: true},
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		resp := toAdblockSourceResponse(src)
		assert.NotNil(t, resp.LastModified)
	})

	t.Run("With invalid LastModified format", func(t *testing.T) {
		src := &model.AdblockSource{
			ID:           1,
			Name:         "Test",
			URL:          "https://example.com/list.txt",
			Enabled:      true,
			LastModified: sql.NullString{String: "invalid-date", Valid: true},
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		resp := toAdblockSourceResponse(src)
		assert.Nil(t, resp.LastModified)
	})
}

func TestListAdblockSources(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Empty list", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adblock/sources", nil)

		api.listAdblockSources(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
	})

	t.Run("Nil storage", func(t *testing.T) {
		api := &API{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adblock/sources", nil)

		api.listAdblockSources(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("With sources", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		createTestAdblockSource(api, "Source1", "https://example.com/list1.txt")
		createTestAdblockSource(api, "Source2", "https://example.com/list2.txt")

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adblock/sources", nil)

		api.listAdblockSources(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		sources, ok := resp.Data.([]interface{})
		require.True(t, ok)
		assert.Len(t, sources, 2)
	})
}

func TestCreateAdblockSource(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       interface{}
		nilStorage bool
		wantStatus int
	}{
		{
			name: "Valid source",
			body: adblockSourceRequest{
				Name: "AdGuard",
				URL:  "https://adguardteam.github.io/list.txt",
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid with HTTP URL",
			body: adblockSourceRequest{
				Name: "Test",
				URL:  "http://example.com/list.txt",
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "With enabled=false",
			body: adblockSourceRequest{
				Name:    "Disabled",
				URL:     "https://example.com/list.txt",
				Enabled: boolPtr(false),
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "Nil storage",
			body:       adblockSourceRequest{Name: "Test", URL: "https://example.com/list.txt"},
			nilStorage: true,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "Invalid JSON",
			body:       "invalid",
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing name",
			body: adblockSourceRequest{
				URL: "https://example.com/list.txt",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing URL",
			body: adblockSourceRequest{
				Name: "Test",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid URL scheme",
			body: adblockSourceRequest{
				Name: "Test",
				URL:  "ftp://example.com/list.txt",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Not a URL",
			body: adblockSourceRequest{
				Name: "Test",
				URL:  "not-a-url",
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupAdblockTestAPI(t)
			if tt.nilStorage {
				api.adblockStorage = nil
			}

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/adblock/sources", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			api.createAdblockSource(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestUpdateAdblockSource(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		sourceID   string
		body       interface{}
		nilStorage bool
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name: "Valid update",
			body: adblockSourceRequest{
				Name: "Updated Source",
				URL:  "https://example.com/updated.txt",
			},
			setup: func(api *API) int64 {
				return createTestAdblockSource(api, "Test", "https://example.com/list.txt")
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "With enabled field",
			body: adblockSourceRequest{
				Name:    "Updated",
				URL:     "https://example.com/updated.txt",
				Enabled: boolPtr(false),
			},
			setup: func(api *API) int64 {
				return createTestAdblockSource(api, "Test", "https://example.com/list.txt")
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "Nil storage",
			body:       adblockSourceRequest{Name: "t", URL: "https://a.com/b.txt"},
			nilStorage: true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "Invalid ID",
			sourceID:   "invalid",
			body:       adblockSourceRequest{Name: "t", URL: "https://a.com/b.txt"},
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Invalid JSON",
			body:       "invalid",
			setup:      func(api *API) int64 { return createTestAdblockSource(api, "t", "https://a.com/b.txt") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing name",
			body: adblockSourceRequest{URL: "https://a.com/b.txt"},
			setup: func(api *API) int64 {
				return createTestAdblockSource(api, "t", "https://a.com/b.txt")
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing URL",
			body: adblockSourceRequest{Name: "test"},
			setup: func(api *API) int64 {
				return createTestAdblockSource(api, "t", "https://a.com/b.txt")
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid URL scheme",
			body: adblockSourceRequest{Name: "test", URL: "ftp://a.com/b.txt"},
			setup: func(api *API) int64 {
				return createTestAdblockSource(api, "t", "https://a.com/b.txt")
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Non-existent source",
			body: adblockSourceRequest{Name: "test", URL: "https://a.com/b.txt"},
			setup: func(api *API) int64 {
				return 9999
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupAdblockTestAPI(t)
			if tt.nilStorage {
				api.adblockStorage = nil
			}
			sourceID := tt.setup(api)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("PUT", "/api/adblock/sources/1", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.sourceID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.sourceID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", sourceID)}}
			}

			api.updateAdblockSource(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestDeleteAdblockSource(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		sourceID   string
		nilStorage bool
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name: "Valid delete",
			setup: func(api *API) int64 {
				return createTestAdblockSource(api, "Test", "https://example.com/list.txt")
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "Nil storage",
			nilStorage: true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "Invalid ID",
			sourceID:   "invalid",
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Non-existent source",
			setup:      func(api *API) int64 { return 9999 },
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupAdblockTestAPI(t)
			if tt.nilStorage {
				api.adblockStorage = nil
			}
			sourceID := tt.setup(api)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("DELETE", "/api/adblock/sources/1", nil)

			if tt.sourceID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.sourceID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", sourceID)}}
			}

			api.deleteAdblockSource(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

// mockLoader implements adblock.LoaderInterface
type mockLoader struct{}

func (m *mockLoader) Download(url, lastModified string) ([]string, string, error) {
	return []string{"example.com"}, "", nil
}

// mockFilter implements adblock.FilterInterface
type mockFilter struct{}

func (m *mockFilter) Rebuild() error {
	return nil
}

func TestSyncAdblockSource(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Nil syncer", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/adblock/sources/1/sync", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: "1"}}

		api.syncAdblockSource(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("Invalid ID", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/adblock/sources/invalid/sync", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: "invalid"}}

		api.syncAdblockSource(c)

		// Should return 500 because syncer is nil (checked before ID parse)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("With syncer and valid ID", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		syncer := adblock.NewSyncer(api.adblockStorage, &mockLoader{}, &mockFilter{}, time.Hour)
		api.adblockSyncer = syncer

		sourceID := createTestAdblockSource(api, "Test", "https://example.com/list.txt")

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/adblock/sources/1/sync", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", sourceID)}}

		api.syncAdblockSource(c)

		assert.Equal(t, http.StatusAccepted, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
	})

	t.Run("With syncer and invalid ID", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		syncer := adblock.NewSyncer(api.adblockStorage, &mockLoader{}, &mockFilter{}, time.Hour)
		api.adblockSyncer = syncer

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/adblock/sources/invalid/sync", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: "invalid"}}

		api.syncAdblockSource(c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestGetAdblockStats(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Empty stats", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adblock/stats", nil)

		api.getAdblockStats(c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Nil storage", func(t *testing.T) {
		api := &API{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adblock/stats", nil)

		api.getAdblockStats(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("With custom limit", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adblock/stats?limit=5", nil)

		api.getAdblockStats(c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("With invalid limit", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adblock/stats?limit=abc", nil)

		api.getAdblockStats(c)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestListAdblockSources_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupAdblockTestAPI(t)
	_ = db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/adblock/sources", nil)

	api.listAdblockSources(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateAdblockSource_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupAdblockTestAPI(t)
	_ = db.Writer.Close()

	body, _ := json.Marshal(adblockSourceRequest{Name: "Test", URL: "https://example.com/list.txt"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/adblock/sources", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	api.createAdblockSource(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDeleteAdblockSource_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupAdblockTestAPI(t)
	sourceID := createTestAdblockSource(api, "Test", "https://example.com/list.txt")
	_ = db.Writer.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/adblock/sources/1", nil)
	c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", sourceID)}}

	api.deleteAdblockSource(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetAdblockStats_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupAdblockTestAPI(t)
	_ = db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/adblock/stats", nil)

	api.getAdblockStats(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetAdblockStatus_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupAdblockTestAPI(t)
	_ = db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/adblock/status", nil)

	api.getAdblockStatus(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetAdblockStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Empty status", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adblock/status", nil)

		api.getAdblockStatus(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
	})

	t.Run("Nil storage", func(t *testing.T) {
		api := &API{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adblock/status", nil)

		api.getAdblockStatus(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("With sources", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		createTestAdblockSource(api, "Source1", "https://example.com/list1.txt")

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adblock/status", nil)

		api.getAdblockStatus(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
	})

	t.Run("With synced sources", func(t *testing.T) {
		api, _ := setupAdblockTestAPI(t)
		sourceID := createTestAdblockSource(api, "Source1", "https://example.com/list1.txt")
		// Update source with LastSync set
		src, _ := api.adblockStorage.GetAdblockSource(sourceID)
		src.LastSync = sql.NullTime{Time: time.Now(), Valid: true}
		_ = api.adblockStorage.UpdateAdblockSource(src)

		// Add blocked domains
		_ = api.adblockStorage.AddBlockedDomain(sourceID, "ads.example.com")

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/adblock/status", nil)

		api.getAdblockStatus(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		data := resp.Data.(map[string]interface{})
		assert.Equal(t, float64(1), data["sources"])
		assert.Equal(t, float64(1), data["domain_count"])
	})
}
