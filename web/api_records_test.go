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

func TestToRecordResponse(t *testing.T) {
	now := time.Now()
	record := &model.Record{
		ID:        1,
		ZoneID:    1,
		Name:      "www.example.com.",
		Type:      "A",
		Content:   "192.0.2.1",
		TTL:       3600,
		Priority:  0,
		Enabled:   true,
		CreatedAt: now,
		UpdatedAt: now,
	}

	resp := toRecordResponse(record)

	assert.Equal(t, record.ID, resp.ID)
	assert.Equal(t, record.ZoneID, resp.ZoneID)
	assert.Equal(t, "www.example.com", resp.Name) // Dot removed
	assert.Equal(t, record.Type, resp.Type)
	assert.Equal(t, record.Content, resp.Content)
	assert.Equal(t, record.TTL, resp.TTL)
	assert.Equal(t, record.Priority, resp.Priority)
	assert.Equal(t, record.Enabled, resp.Enabled)
	assert.Equal(t, record.CreatedAt, resp.CreatedAt)
	assert.Equal(t, record.UpdatedAt, resp.UpdatedAt)
}

func TestListAllRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		setup      func(api *API, db *storage.Database)
		wantStatus int
		wantCount  int
	}{
		{
			name:       "Empty list",
			setup:      func(api *API, db *storage.Database) {},
			wantStatus: http.StatusOK,
			wantCount:  0,
		},
		{
			name: "Multiple records",
			setup: func(api *API, db *storage.Database) {
				zoneID := storage.InsertTestZone(t, db, "example.com.")
				storage.InsertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")
				storage.InsertTestRecord(t, db, zoneID, "mail.example.com.", "A", "192.0.2.2")
			},
			wantStatus: http.StatusOK,
			wantCount:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupTestAPI(t)
			tt.setup(api, db)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/api/records", nil)

			api.listAllRecords(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response apiResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.True(t, response.Success)
			if tt.wantCount > 0 {
				records, ok := response.Data.([]interface{})
				require.True(t, ok)
				assert.Len(t, records, tt.wantCount)
			}
		})
	}
}

func TestListRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		zoneID     string
		setup      func(api *API, db *storage.Database) int64
		wantStatus int
		wantCount  int
		wantError  bool
	}{
		{
			name:   "Valid zone with records",
			zoneID: "1",
			setup: func(api *API, db *storage.Database) int64 {
				zoneID := storage.InsertTestZone(t, db, "example.com.")
				storage.InsertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")
				storage.InsertTestRecord(t, db, zoneID, "mail.example.com.", "A", "192.0.2.2")
				return zoneID
			},
			wantStatus: http.StatusOK,
			wantCount:  2,
			wantError:  false,
		},
		{
			name:   "Valid zone with no records",
			zoneID: "1",
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusOK,
			wantCount:  0,
			wantError:  false,
		},
		{
			name:       "Invalid zone ID",
			zoneID:     "invalid",
			setup:      func(api *API, db *storage.Database) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupTestAPI(t)
			zoneID := tt.setup(api, db)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			if !tt.wantError {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", zoneID)}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.zoneID}}
			}

			api.listRecords(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response apiResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.wantError {
				assert.False(t, response.Success)
			} else {
				assert.True(t, response.Success)
				if tt.wantCount > 0 {
					records, ok := response.Data.([]interface{})
					require.True(t, ok)
					assert.Len(t, records, tt.wantCount)
				}
			}
		})
	}
}

func TestCreateRecord(t *testing.T) {
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
			name:   "Valid record",
			zoneID: "1",
			body: recordRequest{
				Name:     "www.example.com",
				Type:     "A",
				Content:  "192.0.2.1",
				TTL:      3600,
				Priority: 0,
			},
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusCreated,
			wantError:  false,
		},
		{
			name:   "Missing name",
			zoneID: "1",
			body: recordRequest{
				Type:    "A",
				Content: "192.0.2.1",
			},
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:   "Missing type",
			zoneID: "1",
			body: recordRequest{
				Name:    "www.example.com",
				Content: "192.0.2.1",
			},
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:   "Missing content",
			zoneID: "1",
			body: recordRequest{
				Name: "www.example.com",
				Type: "A",
			},
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:       "Invalid zone ID",
			zoneID:     "invalid",
			body:       recordRequest{Name: "www.example.com", Type: "A", Content: "192.0.2.1"},
			setup:      func(api *API, db *storage.Database) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:   "Read-only mode",
			zoneID: "1",
			body: recordRequest{
				Name:    "www.example.com",
				Type:    "A",
				Content: "192.0.2.1",
			},
			readOnly: true,
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusForbidden,
			wantError:  true,
		},
		{
			name:   "Type is uppercased",
			zoneID: "1",
			body: recordRequest{
				Name:    "www.example.com",
				Type:    "a",
				Content: "192.0.2.1",
			},
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusCreated,
			wantError:  false,
		},
		{
			name:   "With enabled field",
			zoneID: "1",
			body: recordRequest{
				Name:    "www.example.com",
				Type:    "A",
				Content: "192.0.2.1",
				Enabled: boolPtr(false),
			},
			setup: func(api *API, db *storage.Database) int64 {
				return storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusCreated,
			wantError:  false,
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
			c.Request = httptest.NewRequest("POST", "/api/zones/1/records", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.name != "Invalid zone ID" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", zoneID)}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.zoneID}}
			}

			api.createRecord(c)

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

func TestUpdateRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		recordID   string
		body       interface{}
		readOnly   bool
		setup      func(api *API, db *storage.Database) int64
		wantStatus int
		wantError  bool
	}{
		{
			name:     "Valid update",
			recordID: "1",
			body: recordRequest{
				Name:     "updated.example.com",
				Type:     "A",
				Content:  "192.0.2.100",
				TTL:      7200,
				Priority: 10,
			},
			setup: func(api *API, db *storage.Database) int64 {
				zoneID := storage.InsertTestZone(t, db, "example.com.")
				return storage.InsertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")
			},
			wantStatus: http.StatusOK,
			wantError:  false,
		},
		{
			name:       "Invalid record ID",
			recordID:   "invalid",
			body:       recordRequest{Name: "www.example.com", Type: "A", Content: "192.0.2.1"},
			setup:      func(api *API, db *storage.Database) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:       "Record not found",
			recordID:   "9999",
			body:       recordRequest{Name: "www.example.com", Type: "A", Content: "192.0.2.1"},
			setup:      func(api *API, db *storage.Database) int64 { return 0 },
			wantStatus: http.StatusNotFound,
			wantError:  true,
		},
		{
			name:     "Missing name",
			recordID: "1",
			body: recordRequest{
				Type:    "A",
				Content: "192.0.2.1",
			},
			setup: func(api *API, db *storage.Database) int64 {
				zoneID := storage.InsertTestZone(t, db, "example.com.")
				return storage.InsertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")
			},
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:     "Read-only mode",
			recordID: "1",
			body: recordRequest{
				Name:    "www.example.com",
				Type:    "A",
				Content: "192.0.2.1",
			},
			readOnly: true,
			setup: func(api *API, db *storage.Database) int64 {
				zoneID := storage.InsertTestZone(t, db, "example.com.")
				return storage.InsertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")
			},
			wantStatus: http.StatusForbidden,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupTestAPI(t)
			api.readOnly = tt.readOnly
			recordID := tt.setup(api, db)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("PUT", "/api/records/1", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.name == "Valid update" || tt.name == "Missing name" || tt.name == "Read-only mode" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", recordID)}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.recordID}}
			}

			api.updateRecord(c)

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

func TestDeleteRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		recordID   string
		readOnly   bool
		setup      func(api *API, db *storage.Database) int64
		wantStatus int
		wantError  bool
	}{
		{
			name:     "Valid delete",
			recordID: "1",
			setup: func(api *API, db *storage.Database) int64 {
				zoneID := storage.InsertTestZone(t, db, "example.com.")
				return storage.InsertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")
			},
			wantStatus: http.StatusOK,
			wantError:  false,
		},
		{
			name:       "Invalid record ID",
			recordID:   "invalid",
			setup:      func(api *API, db *storage.Database) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:     "Read-only mode",
			recordID: "1",
			readOnly: true,
			setup: func(api *API, db *storage.Database) int64 {
				zoneID := storage.InsertTestZone(t, db, "example.com.")
				return storage.InsertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")
			},
			wantStatus: http.StatusForbidden,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupTestAPI(t)
			api.readOnly = tt.readOnly
			recordID := tt.setup(api, db)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("DELETE", "/api/records/1", nil)

			if tt.name == "Valid delete" || tt.name == "Read-only mode" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", recordID)}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.recordID}}
			}

			api.deleteRecord(c)

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
