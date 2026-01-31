package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dns-go/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestNewRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)
	db := storage.SetupTestDB(t)
	syncVersion := storage.NewSyncVersion(db)
	syncAPI := NewSyncAPI(syncVersion)

	router := NewRouter(api, syncAPI)

	assert.NotNil(t, router)
}

func TestNewRouter_WithoutSyncAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)

	// syncAPI can be nil
	router := NewRouter(api, nil)

	assert.NotNil(t, router)
}

func TestRouter_ZoneEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)
	router := NewRouter(api, nil)

	tests := []struct {
		name   string
		method string
		path   string
		setup  func()
	}{
		{
			name:   "List zones",
			method: "GET",
			path:   "/api/zones",
			setup:  func() {},
		},
		{
			name:   "Get zone",
			method: "GET",
			path:   "/api/zones/1",
			setup: func() {
				storage.InsertTestZone(t, db, "example.com.")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			// Should not return 404
			assert.NotEqual(t, http.StatusNotFound, w.Code)
		})
	}
}

func TestRouter_RecordEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)
	router := NewRouter(api, nil)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{
			name:   "List all records",
			method: "GET",
			path:   "/api/records",
		},
		{
			name:   "List zone records",
			method: "GET",
			path:   "/api/zones/1/records",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			// Should not return 404
			assert.NotEqual(t, http.StatusNotFound, w.Code)
		})
	}
}

func TestRouter_SyncEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)
	db := storage.SetupTestDB(t)
	syncVersion := storage.NewSyncVersion(db)
	syncAPI := NewSyncAPI(syncVersion)

	tests := []struct {
		name        string
		syncAPI     *SyncAPI
		path        string
		shouldExist bool
	}{
		{
			name:        "Sync endpoints exist when syncAPI provided",
			syncAPI:     syncAPI,
			path:        "/api/sync/metadata",
			shouldExist: true,
		},
		{
			name:        "Sync endpoints don't exist when syncAPI is nil",
			syncAPI:     nil,
			path:        "/api/sync/metadata",
			shouldExist: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := NewRouter(api, tt.syncAPI)

			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if tt.shouldExist {
				assert.NotEqual(t, http.StatusNotFound, w.Code, "Sync endpoint should exist")
			} else {
				assert.Equal(t, http.StatusNotFound, w.Code, "Sync endpoint should not exist")
			}
		})
	}
}

func TestRouter_MiddlewareChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)
	router := NewRouter(api, nil)

	req := httptest.NewRequest("GET", "/api/zones", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	// Check CORS headers are present
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestRouter_NotFoundRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)
	router := NewRouter(api, nil)

	req := httptest.NewRequest("GET", "/api/nonexistent", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
