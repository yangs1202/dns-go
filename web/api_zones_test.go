package web

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func setupTestAPI(t *testing.T) (*API, *storage.Database) {
	db := storage.SetupTestDB(t)
	zoneStorage := storage.NewZoneStorage(db)
	recordStorage := storage.NewRecordStorage(db)

	api := &API{
		zoneStorage:   zoneStorage,
		recordStorage: recordStorage,
		readOnly:      false,
	}

	return api, db
}

func TestNormalizeFQDN(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Add dot to simple domain",
			input:    "example.com",
			expected: "example.com.",
		},
		{
			name:     "Already has dot",
			input:    "example.com.",
			expected: "example.com.",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "With spaces",
			input:    "  example.com  ",
			expected: "example.com.",
		},
		{
			name:     "Subdomain",
			input:    "www.example.com",
			expected: "www.example.com.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeFQDN(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRemoveFQDNDot(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Remove trailing dot",
			input:    "example.com.",
			expected: "example.com",
		},
		{
			name:     "No trailing dot",
			input:    "example.com",
			expected: "example.com",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Just a dot",
			input:    ".",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeFQDNDot(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToZoneResponse(t *testing.T) {
	now := time.Now()
	zone := &model.Zone{
		ID:            1,
		Name:          "example.com.",
		SOAMname:      "ns1.example.com.",
		SOARname:      "admin.example.com.",
		SOASerial:     1,
		SOARefresh:    3600,
		SOARetry:      900,
		SOAExpire:     86400,
		SOAMinimum:    300,
		Enabled:       true,
		AllowFallback: false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	resp := toZoneResponse(zone)

	assert.Equal(t, zone.ID, resp.ID)
	assert.Equal(t, "example.com", resp.Name)           // Dot removed
	assert.Equal(t, "ns1.example.com", resp.SOAMname)   // Dot removed
	assert.Equal(t, "admin.example.com", resp.SOARname) // Dot removed
	assert.Equal(t, zone.SOASerial, resp.SOASerial)
	assert.Equal(t, zone.Enabled, resp.Enabled)
	assert.Equal(t, zone.AllowFallback, resp.AllowFallback)
	assert.Equal(t, zone.CreatedAt, resp.CreatedAt)
	assert.Equal(t, zone.UpdatedAt, resp.UpdatedAt)
}

func TestListZones(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)

	tests := []struct {
		name       string
		setup      func()
		wantStatus int
		wantCount  int
	}{
		{
			name:       "Empty list",
			setup:      func() {},
			wantStatus: http.StatusOK,
			wantCount:  0,
		},
		{
			name: "Multiple zones",
			setup: func() {
				storage.InsertTestZone(t, db, "example.com.")
				storage.InsertTestZone(t, db, "test.com.")
			},
			wantStatus: http.StatusOK,
			wantCount:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Fresh DB for each test
			api, db = setupTestAPI(t)
			tt.setup()

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/api/zones", nil)

			api.listZones(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response apiResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.True(t, response.Success)
			if tt.wantCount > 0 {
				zones, ok := response.Data.([]interface{})
				require.True(t, ok)
				assert.Len(t, zones, tt.wantCount)
			}
		})
	}
}

func TestGetZone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		zoneID     string
		setup      func(api *API, db *storage.Database) int64
		wantStatus int
		wantError  bool
	}{
		{
			name:   "Valid zone",
			zoneID: "1",
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusOK,
			wantError:  false,
		},
		{
			name:       "Invalid zone ID",
			zoneID:     "invalid",
			setup:      func(api *API, db *storage.Database) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:       "Zone not found",
			zoneID:     "9999",
			setup:      func(api *API, db *storage.Database) int64 { return 0 },
			wantStatus: http.StatusNotFound,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupTestAPI(t)
			zoneID := tt.setup(api, db)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			if tt.name == "Valid zone" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", zoneID)}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.zoneID}}
			}

			api.getZone(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response apiResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.wantError {
				assert.False(t, response.Success)
				assert.NotEmpty(t, response.Error)
			} else {
				assert.True(t, response.Success)
				assert.NotNil(t, response.Data)
			}
		})
	}
}

func TestCreateZone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       interface{}
		readOnly   bool
		wantStatus int
		wantError  bool
	}{
		{
			name: "Valid zone",
			body: zoneRequest{
				Name:       "example.com",
				SOAMname:   "ns1.example.com",
				SOARname:   "admin.example.com",
				SOASerial:  1,
				SOARefresh: 3600,
				SOARetry:   900,
				SOAExpire:  86400,
				SOAMinimum: 300,
			},
			wantStatus: http.StatusCreated,
			wantError:  false,
		},
		{
			name: "Missing name",
			body: zoneRequest{
				SOAMname: "ns1.example.com",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:       "Invalid JSON",
			body:       "invalid json",
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name: "Read-only mode",
			body: zoneRequest{
				Name: "example.com",
			},
			readOnly:   true,
			wantStatus: http.StatusForbidden,
			wantError:  true,
		},
		{
			name: "With enabled field",
			body: zoneRequest{
				Name:    "example.com",
				Enabled: boolPtr(false),
			},
			wantStatus: http.StatusCreated,
			wantError:  false,
		},
		{
			name: "With allow_fallback field",
			body: zoneRequest{
				Name:          "example.com",
				AllowFallback: boolPtr(true),
			},
			wantStatus: http.StatusCreated,
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupTestAPI(t)
			api.readOnly = tt.readOnly

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/zones", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			api.createZone(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.wantError {
				if success, ok := response["success"]; ok {
					assert.False(t, success.(bool))
				} else {
					// Read-only mode response has "error" field but no "success" field
					assert.Contains(t, response, "error")
				}
			} else {
				assert.True(t, response["success"].(bool))
			}
		})
	}
}

func TestUpdateZone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		zoneID     string
		body       interface{}
		readOnly   bool
		setup      func(api *API, db *storage.Database) int64
		wantStatus int
		wantError  bool
	}{
		{
			name:   "Valid update",
			zoneID: "1",
			body: zoneRequest{
				Name:       "updated.com",
				SOAMname:   "ns2.updated.com",
				SOASerial:  2,
				SOARefresh: 7200,
			},
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusOK,
			wantError:  false,
		},
		{
			name:       "Invalid zone ID",
			zoneID:     "invalid",
			body:       zoneRequest{Name: "test.com"},
			setup:      func(api *API, db *storage.Database) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:   "Missing name",
			zoneID: "1",
			body: zoneRequest{
				SOAMname: "ns1.example.com",
			},
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:   "Read-only mode",
			zoneID: "1",
			body: zoneRequest{
				Name: "example.com",
			},
			readOnly: true,
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusForbidden,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupTestAPI(t)
			api.readOnly = tt.readOnly
			zoneID := tt.setup(api, db)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("PUT", "/api/zones/1", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.name == "Valid update" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", zoneID)}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.zoneID}}
			}

			api.updateZone(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.wantError {
				if success, ok := response["success"]; ok {
					assert.False(t, success.(bool))
				} else {
					// Read-only mode response has "error" field but no "success" field
					assert.Contains(t, response, "error")
				}
			} else {
				assert.True(t, response["success"].(bool))
			}
		})
	}
}

func TestDeleteZone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		zoneID     string
		readOnly   bool
		setup      func(api *API, db *storage.Database) int64
		wantStatus int
		wantError  bool
	}{
		{
			name:   "Valid delete",
			zoneID: "1",
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusOK,
			wantError:  false,
		},
		{
			name:       "Invalid zone ID",
			zoneID:     "invalid",
			setup:      func(api *API, db *storage.Database) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:     "Read-only mode",
			zoneID:   "1",
			readOnly: true,
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusForbidden,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupTestAPI(t)
			api.readOnly = tt.readOnly
			zoneID := tt.setup(api, db)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("DELETE", "/api/zones/1", nil)

			if tt.name == "Valid delete" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", zoneID)}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.zoneID}}
			}

			api.deleteZone(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.wantError {
				if success, ok := response["success"]; ok {
					assert.False(t, success.(bool))
				} else {
					// Read-only mode response has "error" field but no "success" field
					assert.Contains(t, response, "error")
				}
			} else {
				assert.True(t, response["success"].(bool))
			}
		})
	}
}

func TestListZones_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)
	_ = db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/zones", nil)

	api.listZones(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateZone_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)
	_ = db.Writer.Close()

	body, _ := json.Marshal(zoneRequest{Name: "example.com"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/zones", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	api.createZone(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDeleteZone_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)
	zoneID := storage.InsertTestZone(t, db, "example.com.")
	_ = db.Writer.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/zones/1", nil)
	c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", zoneID)}}

	api.deleteZone(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateZone_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)
	zoneID := storage.InsertTestZone(t, db, "example.com.")
	_ = db.Writer.Close()

	body, _ := json.Marshal(zoneRequest{Name: "updated.com", SOAMname: "ns1.updated.com"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/zones/1", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", zoneID)}}

	api.updateZone(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// Helper function
func boolPtr(b bool) *bool {
	return &b
}
