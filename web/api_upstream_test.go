package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"dns-go/model"
	"dns-go/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupUpstreamTestAPI(t *testing.T) (*API, *storage.Database) {
	db := storage.SetupTestDB(t)
	upstreamStorage := storage.NewUpstreamStorage(db)

	api := &API{
		upstreamStorage: upstreamStorage,
		db:              db,
	}
	return api, db
}

func createTestUpstream(api *API, name, address, protocol string) int64 {
	id, _ := api.upstreamStorage.CreateUpstreamServer(&model.UpstreamServer{
		Name:     name,
		Address:  address,
		Protocol: protocol,
		Priority: 0,
		Enabled:  true,
	})
	return id
}

func TestListUpstreams(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Empty list", func(t *testing.T) {
		api, _ := setupUpstreamTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/upstreams", nil)

		api.listUpstreams(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
	})

	t.Run("With upstreams", func(t *testing.T) {
		api, _ := setupUpstreamTestAPI(t)
		createTestUpstream(api, "Google DNS", "8.8.8.8:53", "udp")

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/upstreams", nil)

		api.listUpstreams(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		servers, ok := resp.Data.([]interface{})
		require.True(t, ok)
		assert.Len(t, servers, 1)
	})
}

func TestCreateUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       interface{}
		wantStatus int
	}{
		{
			name: "Valid UDP upstream",
			body: upstreamRequest{
				Name:    "Google DNS",
				Address: "8.8.8.8:53",
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid TCP upstream",
			body: upstreamRequest{
				Name:     "Google DNS TCP",
				Address:  "8.8.8.8:53",
				Protocol: "tcp",
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid TCP-TLS upstream",
			body: upstreamRequest{
				Name:     "Cloudflare DoT",
				Address:  "1.1.1.1:853",
				Protocol: "tcp-tls",
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "With enabled=false",
			body: upstreamRequest{
				Name:    "Disabled",
				Address: "1.1.1.1:53",
				Enabled: boolPtr(false),
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "Invalid JSON",
			body:       "invalid",
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing name",
			body: upstreamRequest{
				Address: "8.8.8.8:53",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing address",
			body: upstreamRequest{
				Name: "Google DNS",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Address without port",
			body: upstreamRequest{
				Name:    "Google DNS",
				Address: "8.8.8.8",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Address with invalid host",
			body: upstreamRequest{
				Name:    "Bad",
				Address: "not-an-ip:53",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Address with invalid port",
			body: upstreamRequest{
				Name:    "Bad port",
				Address: "8.8.8.8:99999",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Address with port 0",
			body: upstreamRequest{
				Name:    "Bad port",
				Address: "8.8.8.8:0",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid protocol",
			body: upstreamRequest{
				Name:     "Bad protocol",
				Address:  "8.8.8.8:53",
				Protocol: "http",
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Negative priority",
			body: upstreamRequest{
				Name:     "Bad priority",
				Address:  "8.8.8.8:53",
				Priority: -1,
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupUpstreamTestAPI(t)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/upstreams", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			api.createUpstream(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestUpdateUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		upstreamID string
		body       interface{}
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name: "Valid update",
			body: upstreamRequest{
				Name:     "Updated DNS",
				Address:  "1.1.1.1:53",
				Protocol: "tcp",
			},
			setup: func(api *API) int64 {
				return createTestUpstream(api, "Google DNS", "8.8.8.8:53", "udp")
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "With enabled field",
			body: upstreamRequest{
				Name:    "Test",
				Address: "1.1.1.1:53",
				Enabled: boolPtr(false),
			},
			setup: func(api *API) int64 {
				return createTestUpstream(api, "Google DNS", "8.8.8.8:53", "udp")
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "Invalid ID",
			upstreamID: "invalid",
			body:       upstreamRequest{Name: "test", Address: "1.1.1.1:53"},
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid JSON",
			body: "invalid",
			setup: func(api *API) int64 {
				return createTestUpstream(api, "Google DNS", "8.8.8.8:53", "udp")
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing name",
			body: upstreamRequest{Address: "1.1.1.1:53"},
			setup: func(api *API) int64 {
				return createTestUpstream(api, "Google DNS", "8.8.8.8:53", "udp")
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid address format",
			body: upstreamRequest{Name: "test", Address: "no-port"},
			setup: func(api *API) int64 {
				return createTestUpstream(api, "Google DNS", "8.8.8.8:53", "udp")
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid host",
			body: upstreamRequest{Name: "test", Address: "bad-host:53"},
			setup: func(api *API) int64 {
				return createTestUpstream(api, "Google DNS", "8.8.8.8:53", "udp")
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid port",
			body: upstreamRequest{Name: "test", Address: "1.1.1.1:99999"},
			setup: func(api *API) int64 {
				return createTestUpstream(api, "Google DNS", "8.8.8.8:53", "udp")
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid protocol",
			body: upstreamRequest{Name: "test", Address: "1.1.1.1:53", Protocol: "quic"},
			setup: func(api *API) int64 {
				return createTestUpstream(api, "Google DNS", "8.8.8.8:53", "udp")
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Negative priority",
			body: upstreamRequest{Name: "test", Address: "1.1.1.1:53", Priority: -1},
			setup: func(api *API) int64 {
				return createTestUpstream(api, "Google DNS", "8.8.8.8:53", "udp")
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupUpstreamTestAPI(t)
			upID := tt.setup(api)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("PUT", "/api/upstreams/1", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.upstreamID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.upstreamID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", upID)}}
			}

			api.updateUpstream(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestDeleteUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		upstreamID string
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name: "Valid delete",
			setup: func(api *API) int64 {
				return createTestUpstream(api, "Google DNS", "8.8.8.8:53", "udp")
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "Invalid ID",
			upstreamID: "invalid",
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Non-existent upstream",
			setup:      func(api *API) int64 { return 9999 },
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupUpstreamTestAPI(t)
			upID := tt.setup(api)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("DELETE", "/api/upstreams/1", nil)

			if tt.upstreamID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.upstreamID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", upID)}}
			}

			api.deleteUpstream(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestTestUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Invalid ID", func(t *testing.T) {
		api, _ := setupUpstreamTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/upstreams/invalid/test", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: "invalid"}}

		api.testUpstream(c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Non-existent upstream", func(t *testing.T) {
		api, _ := setupUpstreamTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/upstreams/9999/test", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: "9999"}}

		api.testUpstream(c)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("Upstream with unreachable server", func(t *testing.T) {
		api, _ := setupUpstreamTestAPI(t)
		// Use a non-routable address so it fails fast
		id := createTestUpstream(api, "Bad", "192.0.2.1:53", "udp")

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/upstreams/1/test", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", id)}}

		api.testUpstream(c)

		// Should fail with 502 since the server is unreachable
		assert.Equal(t, http.StatusBadGateway, w.Code)
	})
}

func TestListUpstreams_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupUpstreamTestAPI(t)
	db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/upstreams", nil)

	api.listUpstreams(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateUpstream_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupUpstreamTestAPI(t)
	db.Writer.Close()

	body, _ := json.Marshal(upstreamRequest{Name: "Test", Address: "8.8.8.8:53"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/upstreams", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	api.createUpstream(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDeleteUpstream_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupUpstreamTestAPI(t)
	id := createTestUpstream(api, "Test", "8.8.8.8:53", "udp")
	db.Writer.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/upstreams/1", nil)
	c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", id)}}

	api.deleteUpstream(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestQueryUpstream(t *testing.T) {
	// Test with unreachable address - should return error
	_, err := queryUpstream("192.0.2.1:53", "udp")
	assert.Error(t, err)
}
