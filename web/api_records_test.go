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
	lastQueryAt := now.Add(-10 * time.Minute)
	record := &model.Record{
		ID:          1,
		ZoneID:      1,
		Name:        "www.example.com.",
		Type:        "A",
		Content:     "192.0.2.1",
		TTL:         3600,
		Priority:    0,
		Enabled:     true,
		LastQueryAt: &lastQueryAt,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	zone := &model.Zone{
		ID:         1,
		Name:       "example.com.",
		SOAMname:   "ns1.example.com.",
		SOARname:   "admin.example.com.",
		SOASerial:  1,
		SOARefresh: 3600,
		SOARetry:   600,
		SOAExpire:  86400,
		SOAMinimum: 300,
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Zone 없이 변환
	respWithoutZone := toRecordResponse(record, nil)
	assert.Equal(t, record.ID, respWithoutZone.ID)
	assert.Equal(t, record.ZoneID, respWithoutZone.ZoneID)
	assert.Nil(t, respWithoutZone.Zone)
	assert.Equal(t, "www.example.com", respWithoutZone.Name) // Dot removed
	assert.Equal(t, record.Type, respWithoutZone.Type)
	assert.Equal(t, record.Content, respWithoutZone.Content)
	assert.Equal(t, record.TTL, respWithoutZone.TTL)
	assert.Equal(t, record.Priority, respWithoutZone.Priority)
	assert.Equal(t, record.Enabled, respWithoutZone.Enabled)
	require.NotNil(t, respWithoutZone.LastQueryAt)
	assert.Equal(t, record.LastQueryAt.Unix(), respWithoutZone.LastQueryAt.Unix())
	assert.Equal(t, record.CreatedAt, respWithoutZone.CreatedAt)
	assert.Equal(t, record.UpdatedAt, respWithoutZone.UpdatedAt)

	// Zone과 함께 변환
	respWithZone := toRecordResponse(record, zone)
	assert.Equal(t, record.ID, respWithZone.ID)
	assert.Equal(t, record.ZoneID, respWithZone.ZoneID)
	assert.NotNil(t, respWithZone.Zone)
	assert.Equal(t, zone.ID, respWithZone.Zone.ID)
	assert.Equal(t, "example.com", respWithZone.Zone.Name) // Dot removed
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
				first, ok := records[0].(map[string]interface{})
				require.True(t, ok)
				_, exists := first["last_query_at"]
				assert.True(t, exists)
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
					first, ok := records[0].(map[string]interface{})
					require.True(t, ok)
					_, exists := first["last_query_at"]
					assert.True(t, exists)
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

func TestValidateRecordContent(t *testing.T) {
	tests := []struct {
		name       string
		recordType string
		content    string
		wantError  bool
	}{
		// A records
		{name: "Valid A record", recordType: "A", content: "192.0.2.1", wantError: false},
		{name: "Invalid A record - IPv6", recordType: "A", content: "2001:db8::1", wantError: true},
		{name: "Invalid A record - text", recordType: "A", content: "not-an-ip", wantError: true},

		// AAAA records
		{name: "Valid AAAA record", recordType: "AAAA", content: "2001:db8::1", wantError: false},
		{name: "Invalid AAAA record - IPv4", recordType: "AAAA", content: "192.0.2.1", wantError: true},
		{name: "Invalid AAAA record - text", recordType: "AAAA", content: "not-an-ip", wantError: true},

		// CNAME records
		{name: "Valid CNAME", recordType: "CNAME", content: "www.example.com", wantError: false},
		{name: "Valid CNAME with dot", recordType: "CNAME", content: "www.example.com.", wantError: false},
		{name: "Invalid CNAME empty", recordType: "CNAME", content: "", wantError: true},
		{name: "Invalid CNAME with space", recordType: "CNAME", content: "bad name.com", wantError: true},

		// NS records
		{name: "Valid NS", recordType: "NS", content: "ns1.example.com", wantError: false},
		{name: "Invalid NS empty", recordType: "NS", content: ".", wantError: true},

		// PTR records
		{name: "Valid PTR", recordType: "PTR", content: "host.example.com", wantError: false},

		// MX records
		{name: "Valid MX", recordType: "MX", content: "mail.example.com", wantError: false},
		{name: "Invalid MX empty", recordType: "MX", content: "", wantError: true},
		{name: "Invalid MX with space", recordType: "MX", content: "bad mail.com", wantError: true},

		// TXT records (no specific validation)
		{name: "Valid TXT", recordType: "TXT", content: "v=spf1 include:example.com ~all", wantError: false},

		// SRV records (no specific validation)
		{name: "Valid SRV with explicit priority", recordType: "SRV", content: "10 5 443 server.example.com", wantError: false},
		{name: "Valid SRV using record priority", recordType: "SRV", content: "5 443 server.example.com", wantError: false},
		{name: "Invalid SRV missing fields", recordType: "SRV", content: "443 server.example.com", wantError: true},
		{name: "Invalid SRV non-numeric port", recordType: "SRV", content: "10 5 port server.example.com", wantError: true},

		// SOA records
		{name: "Valid SOA", recordType: "SOA", content: "ns1.example.com. admin.example.com. 1 3600 900 86400 300", wantError: false},
		{name: "Invalid SOA missing fields", recordType: "SOA", content: "ns1.example.com. admin.example.com.", wantError: true},
		{name: "Invalid SOA non-numeric serial", recordType: "SOA", content: "ns1.example.com. admin.example.com. serial 3600 900 86400 300", wantError: true},

		// CAA records
		{name: "Valid CAA", recordType: "CAA", content: "0 issue letsencrypt.org", wantError: false},
		{name: "Invalid CAA missing fields", recordType: "CAA", content: "0 issue", wantError: true},
		{name: "Invalid CAA flag", recordType: "CAA", content: "999 issue letsencrypt.org", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := validateRecordContent(tt.recordType, tt.content)
			if tt.wantError {
				assert.NotEmpty(t, result)
			} else {
				assert.Empty(t, result)
			}
		})
	}
}

func TestCreateRecord_ContentValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       recordRequest
		wantStatus int
	}{
		{
			name:       "Invalid A content (IPv6)",
			body:       recordRequest{Name: "test.example.com", Type: "A", Content: "2001:db8::1"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Invalid AAAA content (IPv4)",
			body:       recordRequest{Name: "test.example.com", Type: "AAAA", Content: "192.0.2.1"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Invalid record type",
			body:       recordRequest{Name: "test.example.com", Type: "INVALID", Content: "test"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Invalid CNAME content",
			body:       recordRequest{Name: "test.example.com", Type: "CNAME", Content: "bad name"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Invalid MX content",
			body:       recordRequest{Name: "test.example.com", Type: "MX", Content: "bad name"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Negative TTL",
			body:       recordRequest{Name: "test.example.com", Type: "A", Content: "192.0.2.1", TTL: -1},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Negative priority",
			body:       recordRequest{Name: "test.example.com", Type: "A", Content: "192.0.2.1", Priority: -1},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Overflow MX priority",
			body:       recordRequest{Name: "test.example.com", Type: "MX", Content: "mail.example.com.", Priority: 65536},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Overflow SRV priority",
			body:       recordRequest{Name: "_sip._tcp.example.com", Type: "SRV", Content: "5 5060 sip.example.com.", Priority: 65536},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Zone not found",
			body:       recordRequest{Name: "test.example.com", Type: "A", Content: "192.0.2.1"},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupTestAPI(t)

			// Only create zone for content validation tests, not for "Zone not found"
			var zoneID int64 = 9999
			if tt.name != "Zone not found" {
				zoneID = storage.InsertTestZone(t, db, "example.com.")
			}

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/zones/1/records", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", zoneID)}}

			api.createRecord(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestRecordTypeValidationMessageIncludesSOA(t *testing.T) {
	gin.SetMode(gin.TestMode)

	api, db := setupTestAPI(t)
	zoneID := storage.InsertTestZone(t, db, "example.com.")
	recordID := storage.InsertTestRecord(t, db, zoneID, "test.example.com.", "A", "192.0.2.1")

	body, _ := json.Marshal(recordRequest{Name: "test.example.com", Type: "INVALID", Content: "test"})

	createRecorder := httptest.NewRecorder()
	createContext, _ := gin.CreateTestContext(createRecorder)
	createContext.Request = httptest.NewRequest("POST", "/api/zones/1/records", bytes.NewReader(body))
	createContext.Request.Header.Set("Content-Type", "application/json")
	createContext.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", zoneID)}}
	api.createRecord(createContext)

	assert.Equal(t, http.StatusBadRequest, createRecorder.Code)
	assert.Contains(t, createRecorder.Body.String(), "SOA")

	updateRecorder := httptest.NewRecorder()
	updateContext, _ := gin.CreateTestContext(updateRecorder)
	updateContext.Request = httptest.NewRequest("PUT", "/api/records/1", bytes.NewReader(body))
	updateContext.Request.Header.Set("Content-Type", "application/json")
	updateContext.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", recordID)}}
	api.updateRecord(updateContext)

	assert.Equal(t, http.StatusBadRequest, updateRecorder.Code)
	assert.Contains(t, updateRecorder.Body.String(), "SOA")
}

func TestUpdateRecord_ContentValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       recordRequest
		wantStatus int
	}{
		{
			name:       "Invalid A content",
			body:       recordRequest{Name: "test.example.com", Type: "A", Content: "not-ip"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Invalid type",
			body:       recordRequest{Name: "test.example.com", Type: "INVALID", Content: "test"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Missing type",
			body:       recordRequest{Name: "test.example.com", Content: "test"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Missing content",
			body:       recordRequest{Name: "test.example.com", Type: "A"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Negative TTL",
			body:       recordRequest{Name: "test.example.com", Type: "A", Content: "1.2.3.4", TTL: -1},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Negative priority",
			body:       recordRequest{Name: "test.example.com", Type: "A", Content: "1.2.3.4", Priority: -1},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Overflow MX priority",
			body:       recordRequest{Name: "test.example.com", Type: "MX", Content: "mail.example.com.", Priority: 65536},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Overflow SRV priority",
			body:       recordRequest{Name: "_sip._tcp.example.com", Type: "SRV", Content: "5 5060 sip.example.com.", Priority: 65536},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Valid update with enabled",
			body:       recordRequest{Name: "test.example.com", Type: "A", Content: "1.2.3.4", Enabled: boolPtr(false)},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupTestAPI(t)
			zoneID := storage.InsertTestZone(t, db, "example.com.")
			recordID := storage.InsertTestRecord(t, db, zoneID, "test.example.com.", "A", "192.0.2.1")

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("PUT", "/api/records/1", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", recordID)}}

			api.updateRecord(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestListAllRecords_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)
	_ = db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/records", nil)

	api.listAllRecords(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListRecords_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)
	_ = db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{gin.Param{Key: "id", Value: "1"}}

	api.listRecords(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateRecord_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)
	zoneID := storage.InsertTestZone(t, db, "example.com.")
	_ = db.Writer.Close()

	body, _ := json.Marshal(recordRequest{Name: "www.example.com", Type: "A", Content: "1.2.3.4"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/zones/1/records", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", zoneID)}}

	api.createRecord(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDeleteRecord_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)
	zoneID := storage.InsertTestZone(t, db, "example.com.")
	recordID := storage.InsertTestRecord(t, db, zoneID, "www.example.com.", "A", "1.2.3.4")
	_ = db.Writer.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/api/records/1", nil)
	c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", recordID)}}

	api.deleteRecord(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateRecord_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)
	zoneID := storage.InsertTestZone(t, db, "example.com.")
	recordID := storage.InsertTestRecord(t, db, zoneID, "www.example.com.", "A", "1.2.3.4")
	_ = db.Writer.Close()

	body, _ := json.Marshal(recordRequest{Name: "www.example.com", Type: "A", Content: "5.6.7.8"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/records/1", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", recordID)}}

	api.updateRecord(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
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
