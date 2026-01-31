package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dns-go/model"
	"dns-go/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSyncAPI(t *testing.T) (*SyncAPI, *storage.Database) {
	db := storage.SetupTestDB(t)
	syncVersion := storage.NewSyncVersion(db)
	api := NewSyncAPI(syncVersion)
	return api, db
}

func TestNewSyncAPI(t *testing.T) {
	db := storage.SetupTestDB(t)
	syncVersion := storage.NewSyncVersion(db)
	api := NewSyncAPI(syncVersion)

	assert.NotNil(t, api)
	assert.NotNil(t, api.syncVersion)
	assert.Equal(t, syncVersion, api.syncVersion)
}

func TestGetMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		setup      func(db *storage.Database)
		wantStatus int
		wantFields []string
	}{
		{
			name:       "Initial metadata",
			setup:      func(db *storage.Database) {},
			wantStatus: http.StatusOK,
			wantFields: []string{"version", "checksum"},
		},
		{
			name: "After data insertion",
			setup: func(db *storage.Database) {
				storage.InsertTestZone(t, db, "example.com.")
			},
			wantStatus: http.StatusOK,
			wantFields: []string{"version", "checksum"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupSyncAPI(t)
			tt.setup(db)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/api/sync/metadata", nil)

			api.GetMetadata(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			for _, field := range tt.wantFields {
				assert.Contains(t, response, field)
			}
		})
	}
}

func TestGetFull(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		setup          func(db *storage.Database)
		wantStatus     int
		wantZoneCount  int
		wantRecordCount int
	}{
		{
			name:            "Empty database",
			setup:           func(db *storage.Database) {},
			wantStatus:      http.StatusOK,
			wantZoneCount:   0,
			wantRecordCount: 0,
		},
		{
			name: "With zones and records",
			setup: func(db *storage.Database) {
				zoneID := storage.InsertTestZone(t, db, "example.com.")
				storage.InsertTestRecord(t, db, zoneID, "www.example.com.", "A", "192.0.2.1")
				storage.InsertTestRecord(t, db, zoneID, "mail.example.com.", "A", "192.0.2.2")

				zoneID2 := storage.InsertTestZone(t, db, "test.com.")
				storage.InsertTestRecord(t, db, zoneID2, "www.test.com.", "A", "192.0.2.3")
			},
			wantStatus:      http.StatusOK,
			wantZoneCount:   2,
			wantRecordCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupSyncAPI(t)
			tt.setup(db)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/api/sync/full", nil)

			api.GetFull(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			// Check top-level fields
			assert.Contains(t, response, "version")
			assert.Contains(t, response, "checksum")
			assert.Contains(t, response, "data")

			// Check data structure
			data, ok := response["data"].(map[string]interface{})
			require.True(t, ok)

			assert.Contains(t, data, "zones")
			assert.Contains(t, data, "records")
			assert.Contains(t, data, "upstream_servers")

			// Verify counts
			if tt.wantZoneCount > 0 {
				zones, ok := data["zones"].([]interface{})
				require.True(t, ok)
				assert.Len(t, zones, tt.wantZoneCount)
			}

			if tt.wantRecordCount > 0 {
				records, ok := data["records"].([]interface{})
				require.True(t, ok)
				assert.Len(t, records, tt.wantRecordCount)
			}
		})
	}
}

func TestGetChanges(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		sinceVersion  string
		setup         func(db *storage.Database)
		wantStatus    int
		wantHasChanges bool
	}{
		{
			name:           "No changes (same version)",
			sinceVersion:   "0",
			setup:          func(db *storage.Database) {},
			wantStatus:     http.StatusOK,
			wantHasChanges: false,
		},
		{
			name:         "Has changes (old version)",
			sinceVersion: "0",
			setup: func(db *storage.Database) {
				zoneStorage := storage.NewZoneStorage(db)
				zone := &model.Zone{Name: "example.com.", Enabled: true}
				zoneStorage.CreateZone(zone)
			},
			wantStatus:     http.StatusOK,
			wantHasChanges: true,
		},
		{
			name:         "No changes (current version)",
			sinceVersion: "1",
			setup: func(db *storage.Database) {
				zoneStorage := storage.NewZoneStorage(db)
				zone := &model.Zone{Name: "example.com.", Enabled: true}
				zoneStorage.CreateZone(zone)
			},
			wantStatus:     http.StatusOK,
			wantHasChanges: false,
		},
		{
			name:         "Has changes (multiple operations)",
			sinceVersion: "0",
			setup: func(db *storage.Database) {
				zoneStorage := storage.NewZoneStorage(db)
				recordStorage := storage.NewRecordStorage(db)
				zone := &model.Zone{Name: "example.com.", Enabled: true}
				zoneID, _ := zoneStorage.CreateZone(zone)
				record := &model.Record{ZoneID: zoneID, Name: "www.example.com.", Type: "A", Content: "192.0.2.1", Enabled: true}
				recordStorage.CreateRecord(record)
			},
			wantStatus:     http.StatusOK,
			wantHasChanges: true,
		},
		{
			name:           "Future version",
			sinceVersion:   "999",
			setup:          func(db *storage.Database) {},
			wantStatus:     http.StatusOK,
			wantHasChanges: false,
		},
		{
			name:           "Invalid version (treated as 0)",
			sinceVersion:   "invalid",
			setup:          func(db *storage.Database) {},
			wantStatus:     http.StatusOK,
			wantHasChanges: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, db := setupSyncAPI(t)
			tt.setup(db)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/api/sync/changes?since_version="+tt.sinceVersion, nil)

			api.GetChanges(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.Contains(t, response, "current_version")
			assert.Contains(t, response, "has_changes")
			assert.Equal(t, tt.wantHasChanges, response["has_changes"])
		})
	}
}

func TestGetChanges_QueryParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupSyncAPI(t)

	// Insert data to increase version
	storage.InsertTestZone(t, db, "example.com.")

	tests := []struct {
		name       string
		queryParam string
		wantStatus int
	}{
		{
			name:       "With query parameter",
			queryParam: "?since_version=0",
			wantStatus: http.StatusOK,
		},
		{
			name:       "Without query parameter",
			queryParam: "",
			wantStatus: http.StatusOK,
		},
		{
			name:       "With invalid query parameter",
			queryParam: "?since_version=abc",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/api/sync/changes"+tt.queryParam, nil)

			api.GetChanges(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response map[string]interface{}
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.Contains(t, response, "current_version")
			assert.Contains(t, response, "has_changes")
		})
	}
}

func TestSyncAPI_Integration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupSyncAPI(t)

	// Step 1: Get initial metadata
	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	c1.Request = httptest.NewRequest("GET", "/api/sync/metadata", nil)
	api.GetMetadata(c1)
	assert.Equal(t, http.StatusOK, w1.Code)

	var metadata1 map[string]interface{}
	json.Unmarshal(w1.Body.Bytes(), &metadata1)
	initialVersion := metadata1["version"]

	// Step 2: Insert data
	zoneStorage := storage.NewZoneStorage(db)
	zone := &model.Zone{Name: "example.com.", Enabled: true}
	zoneStorage.CreateZone(zone)

	// Step 3: Get updated metadata
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest("GET", "/api/sync/metadata", nil)
	api.GetMetadata(c2)
	assert.Equal(t, http.StatusOK, w2.Code)

	var metadata2 map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &metadata2)
	newVersion := metadata2["version"]

	// Version should have increased
	assert.NotEqual(t, initialVersion, newVersion)

	// Step 4: Get full sync data
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request = httptest.NewRequest("GET", "/api/sync/full", nil)
	api.GetFull(c3)
	assert.Equal(t, http.StatusOK, w3.Code)

	var fullData map[string]interface{}
	json.Unmarshal(w3.Body.Bytes(), &fullData)

	data := fullData["data"].(map[string]interface{})
	zones := data["zones"].([]interface{})
	assert.Len(t, zones, 1)
}
