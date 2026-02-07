package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"dns-go/gslb"
	"dns-go/model"
	"dns-go/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupHealthTestAPI(t *testing.T) (*API, *storage.Database) {
	db := storage.SetupTestDB(t)
	policyStorage := gslb.NewPolicyStorage(db)
	healthCheckStorage := gslb.NewHealthCheckStorage(db)
	healthStatus := &sync.Map{}

	api := &API{
		db:                 db,
		policyStorage:      policyStorage,
		healthCheckStorage: healthCheckStorage,
		healthStatus:       healthStatus,
	}
	return api, db
}

func createTestPolicy(api *API, name, domain string) int64 {
	id, _ := api.policyStorage.CreatePolicy(&model.GSLBPolicy{
		Name:       name,
		Domain:     domain,
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	})
	return id
}

func TestGetHealthStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Empty status", func(t *testing.T) {
		api, _ := setupHealthTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/health/status", nil)

		api.getHealthStatus(c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("With status data", func(t *testing.T) {
		api, _ := setupHealthTestAPI(t)
		api.healthStatus.Store(int64(1), "healthy")
		api.healthStatus.Store(int64(2), "unhealthy")

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/health/status", nil)

		api.getHealthStatus(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
	})

	t.Run("Nil healthStatus", func(t *testing.T) {
		api := &API{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/health/status", nil)

		api.getHealthStatus(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestListHealthChecks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Empty list", func(t *testing.T) {
		api, _ := setupHealthTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/health/checks", nil)

		api.listHealthChecks(c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Nil storage", func(t *testing.T) {
		api := &API{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/health/checks", nil)

		api.listHealthChecks(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("With health checks", func(t *testing.T) {
		api, _ := setupHealthTestAPI(t)
		policyID := createTestPolicy(api, "test", "example.com.")
		_, err := api.healthCheckStorage.CreateHealthCheck(&model.HealthCheck{
			PolicyID:           policyID,
			CheckType:          "http",
			Target:             "http://example.com/health",
			IntervalSec:        10,
			TimeoutSec:         5,
			HealthyThreshold:   3,
			UnhealthyThreshold: 2,
			Enabled:            true,
		})
		require.NoError(t, err)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/health/checks", nil)

		api.listHealthChecks(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		checks, ok := resp.Data.([]interface{})
		require.True(t, ok)
		assert.Len(t, checks, 1)
	})
}

func TestCreateHealthCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		policyID   string
		body       interface{}
		readOnly   bool
		nilStorage bool
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name: "Valid HTTP check",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid HTTPS check",
			body: healthCheckRequest{
				CheckType:          "https",
				Target:             "https://example.com/health",
				IntervalSec:        30,
				TimeoutSec:         10,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid TCP check",
			body: healthCheckRequest{
				CheckType:          "tcp",
				Target:             "example.com:443",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusCreated,
		},
		{
			name: "With enabled=false",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
				Enabled:            boolPtr(false),
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusCreated,
		},
		{
			name: "Read-only mode",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			readOnly:   true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Nil storage",
			body:       healthCheckRequest{CheckType: "http", Target: "t", IntervalSec: 10, TimeoutSec: 5, HealthyThreshold: 3, UnhealthyThreshold: 2},
			nilStorage: true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "Invalid policy ID",
			policyID:   "invalid",
			body:       healthCheckRequest{CheckType: "http", Target: "t", IntervalSec: 10, TimeoutSec: 5, HealthyThreshold: 3, UnhealthyThreshold: 2},
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Invalid JSON",
			body:       "invalid",
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing check_type",
			body: healthCheckRequest{
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid check_type",
			body: healthCheckRequest{
				CheckType:          "ping",
				Target:             "example.com",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing target",
			body: healthCheckRequest{
				CheckType:          "http",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid interval (zero)",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        0,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid timeout (zero)",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         0,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Timeout >= interval",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         10,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid healthy_threshold (zero)",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   0,
				UnhealthyThreshold: 2,
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid unhealthy_threshold (zero)",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 0,
			},
			setup:      func(api *API) int64 { return createTestPolicy(api, "test", "example.com.") },
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupHealthTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.healthCheckStorage = nil
			}
			policyID := tt.setup(api)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/gslb/policies/1/healthchecks", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.policyID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.policyID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", policyID)}}
			}

			api.createHealthCheck(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestUpdateHealthCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validBody := healthCheckRequest{
		CheckType:          "http",
		Target:             "http://example.com/health",
		IntervalSec:        10,
		TimeoutSec:         5,
		HealthyThreshold:   3,
		UnhealthyThreshold: 2,
	}

	createHC := func(api *API) int64 {
		policyID := createTestPolicy(api, "test", "example.com.")
		id, _ := api.healthCheckStorage.CreateHealthCheck(&model.HealthCheck{
			PolicyID:           policyID,
			CheckType:          "http",
			Target:             "http://example.com/health",
			IntervalSec:        10,
			TimeoutSec:         5,
			HealthyThreshold:   3,
			UnhealthyThreshold: 2,
			Enabled:            true,
		})
		return id
	}

	tests := []struct {
		name       string
		checkID    string
		body       interface{}
		readOnly   bool
		nilStorage bool
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name:       "Valid update",
			body:       validBody,
			setup:      createHC,
			wantStatus: http.StatusOK,
		},
		{
			name: "Update with enabled field",
			body: healthCheckRequest{
				CheckType:          "https",
				Target:             "https://example.com/health",
				IntervalSec:        20,
				TimeoutSec:         10,
				HealthyThreshold:   2,
				UnhealthyThreshold: 3,
				Enabled:            boolPtr(false),
			},
			setup:      createHC,
			wantStatus: http.StatusOK,
		},
		{
			name:       "Read-only mode",
			body:       validBody,
			readOnly:   true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Nil storage",
			body:       validBody,
			nilStorage: true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "Invalid ID",
			checkID:    "invalid",
			body:       validBody,
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Invalid JSON",
			body:       "invalid",
			setup:      createHC,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing check_type",
			body: healthCheckRequest{
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      createHC,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid check_type",
			body: healthCheckRequest{
				CheckType:          "udp",
				Target:             "example.com",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      createHC,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing target",
			body: healthCheckRequest{
				CheckType:          "http",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      createHC,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid interval",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        0,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      createHC,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid timeout",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         0,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      createHC,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Timeout >= interval",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        5,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 2,
			},
			setup:      createHC,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid healthy_threshold",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   0,
				UnhealthyThreshold: 2,
			},
			setup:      createHC,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid unhealthy_threshold",
			body: healthCheckRequest{
				CheckType:          "http",
				Target:             "http://example.com/health",
				IntervalSec:        10,
				TimeoutSec:         5,
				HealthyThreshold:   3,
				UnhealthyThreshold: 0,
			},
			setup:      createHC,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Non-existent check",
			body:       validBody,
			setup:      func(api *API) int64 { return 9999 },
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupHealthTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.healthCheckStorage = nil
			}
			checkID := tt.setup(api)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("PUT", "/api/health/checks/1", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.checkID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.checkID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", checkID)}}
			}

			api.updateHealthCheck(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestListHealthChecks_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupHealthTestAPI(t)
	_ = db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/health/checks", nil)

	api.listHealthChecks(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateHealthCheck_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupHealthTestAPI(t)
	policyID := createTestPolicy(api, "test", "example.com.")
	_ = db.Writer.Close()

	body, _ := json.Marshal(healthCheckRequest{
		CheckType: "http", Target: "http://example.com/health",
		IntervalSec: 10, TimeoutSec: 5, HealthyThreshold: 3, UnhealthyThreshold: 2,
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/gslb/policies/1/healthchecks", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", policyID)}}

	api.createHealthCheck(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDeleteHealthCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)

	createHC := func(api *API) int64 {
		policyID := createTestPolicy(api, "test", "example.com.")
		id, _ := api.healthCheckStorage.CreateHealthCheck(&model.HealthCheck{
			PolicyID:           policyID,
			CheckType:          "http",
			Target:             "http://example.com/health",
			IntervalSec:        10,
			TimeoutSec:         5,
			HealthyThreshold:   3,
			UnhealthyThreshold: 2,
			Enabled:            true,
		})
		return id
	}

	tests := []struct {
		name       string
		checkID    string
		readOnly   bool
		nilStorage bool
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name:       "Valid delete",
			setup:      createHC,
			wantStatus: http.StatusOK,
		},
		{
			name:       "Read-only mode",
			readOnly:   true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Nil storage",
			nilStorage: true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "Invalid ID",
			checkID:    "invalid",
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Non-existent check",
			setup:      func(api *API) int64 { return 9999 },
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupHealthTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.healthCheckStorage = nil
			}
			checkID := tt.setup(api)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("DELETE", "/api/health/checks/1", nil)

			if tt.checkID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.checkID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", checkID)}}
			}

			api.deleteHealthCheck(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}
