package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"dns-go/gslb"
	"dns-go/model"
	"dns-go/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGSLBTestAPI(t *testing.T) (*API, *storage.Database) {
	db := storage.SetupTestDB(t)
	zoneStorage := storage.NewZoneStorage(db)
	recordStorage := storage.NewRecordStorage(db)
	policyStorage := gslb.NewPolicyStorage(db)
	poolStorage := gslb.NewPoolStorage(db)

	api := &API{
		zoneStorage:   zoneStorage,
		recordStorage: recordStorage,
		policyStorage: policyStorage,
		poolStorage:   poolStorage,
		db:            db,
	}
	return api, db
}

// --- Policy Tests ---

func TestListPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Empty list", func(t *testing.T) {
		api, _ := setupGSLBTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/gslb/policies", nil)

		api.listPolicies(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
	})

	t.Run("Nil storage", func(t *testing.T) {
		api := &API{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/gslb/policies", nil)

		api.listPolicies(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("With policies", func(t *testing.T) {
		api, _ := setupGSLBTestAPI(t)
		// Create a policy first
		_, err := api.policyStorage.CreatePolicy(newGSLBPolicy("test-policy", "example.com."))
		require.NoError(t, err)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/gslb/policies", nil)

		api.listPolicies(c)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp apiResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.True(t, resp.Success)
		policies, ok := resp.Data.([]interface{})
		require.True(t, ok)
		assert.Len(t, policies, 1)
	})
}

func TestCreatePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		body       interface{}
		readOnly   bool
		nilStorage bool
		wantStatus int
		wantError  bool
	}{
		{
			name: "Valid policy",
			body: policyRequest{
				Name:   "test-policy",
				Domain: "gslb.example.com",
				TTL:    30,
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid policy with AAAA",
			body: policyRequest{
				Name:       "test-policy-v6",
				Domain:     "gslb.example.com",
				RecordType: "AAAA",
				TTL:        60,
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid policy with enabled=false",
			body: policyRequest{
				Name:    "test-disabled",
				Domain:  "gslb.example.com",
				Enabled: boolPtr(false),
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Read-only mode",
			body: policyRequest{
				Name:   "test",
				Domain: "gslb.example.com",
			},
			readOnly:   true,
			wantStatus: http.StatusForbidden,
			wantError:  true,
		},
		{
			name:       "Nil storage",
			body:       policyRequest{Name: "test", Domain: "gslb.example.com"},
			nilStorage: true,
			wantStatus: http.StatusInternalServerError,
			wantError:  true,
		},
		{
			name:       "Invalid JSON",
			body:       "invalid",
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name: "Missing name",
			body: policyRequest{
				Domain: "gslb.example.com",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name: "Missing domain",
			body: policyRequest{
				Name: "test",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name: "Invalid record_type",
			body: policyRequest{
				Name:       "test",
				Domain:     "gslb.example.com",
				RecordType: "CNAME",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name: "Negative TTL",
			body: policyRequest{
				Name:   "test",
				Domain: "gslb.example.com",
				TTL:    -1,
			},
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupGSLBTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.policyStorage = nil
			}

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/gslb/policies", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			api.createPolicy(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestUpdatePolicy(t *testing.T) {
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
			name: "Valid update",
			body: policyRequest{
				Name:   "updated-policy",
				Domain: "updated.example.com",
				TTL:    60,
			},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("test", "example.com."))
				return id
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "Read-only mode",
			body: policyRequest{
				Name:   "test",
				Domain: "example.com",
			},
			readOnly:   true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Nil storage",
			body:       policyRequest{Name: "test", Domain: "example.com"},
			nilStorage: true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "Invalid ID",
			policyID:   "invalid",
			body:       policyRequest{Name: "test", Domain: "example.com"},
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Invalid JSON",
			body:       "invalid",
			setup:      func(api *API) int64 { id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com.")); return id },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing name",
			body: policyRequest{Domain: "example.com"},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid record_type",
			body: policyRequest{Name: "test", Domain: "example.com", RecordType: "MX"},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Negative TTL",
			body: policyRequest{Name: "test", Domain: "example.com", TTL: -5},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "With enabled field",
			body: policyRequest{Name: "test", Domain: "example.com", Enabled: boolPtr(false)},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				return id
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupGSLBTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.policyStorage = nil
			}
			policyID := tt.setup(api)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("PUT", "/api/gslb/policies/1", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.policyID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.policyID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", policyID)}}
			}

			api.updatePolicy(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestDeletePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		policyID   string
		readOnly   bool
		nilStorage bool
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name: "Valid delete",
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("test", "example.com."))
				return id
			},
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
			policyID:   "invalid",
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Non-existent policy",
			setup:      func(api *API) int64 { return 9999 },
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupGSLBTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.policyStorage = nil
			}
			policyID := tt.setup(api)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("DELETE", "/api/gslb/policies/1", nil)

			if tt.policyID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.policyID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", policyID)}}
			}

			api.deletePolicy(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

// --- Pool Tests ---

func TestListPools(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Empty list", func(t *testing.T) {
		api, _ := setupGSLBTestAPI(t)
		policyID, err := api.policyStorage.CreatePolicy(newGSLBPolicy("test", "example.com."))
		require.NoError(t, err)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/gslb/policies/1/pools", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", policyID)}}

		api.listPools(c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Nil storage", func(t *testing.T) {
		api := &API{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/gslb/policies/1/pools", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: "1"}}

		api.listPools(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("Invalid policy ID", func(t *testing.T) {
		api, _ := setupGSLBTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/gslb/policies/invalid/pools", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: "invalid"}}

		api.listPools(c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestCreatePool(t *testing.T) {
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
			name: "Valid default pool",
			body: poolRequest{
				Name:      "default-pool",
				MatchType: "default",
			},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("test", "example.com."))
				return id
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid CIDR pool",
			body: poolRequest{
				Name:       "korea-pool",
				MatchType:  "cidr",
				MatchValue: "10.0.0.0/8",
				Priority:   1,
			},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("test", "example.com."))
				return id
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid geo_country pool",
			body: poolRequest{
				Name:       "kr-pool",
				MatchType:  "geo_country",
				MatchValue: "KR",
			},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("test", "example.com."))
				return id
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid geo_continent pool",
			body: poolRequest{
				Name:       "asia-pool",
				MatchType:  "geo_continent",
				MatchValue: "AS",
			},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("test", "example.com."))
				return id
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid fallback pool",
			body: poolRequest{
				Name:      "fallback-pool",
				MatchType: "fallback",
			},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("test", "example.com."))
				return id
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Read-only mode",
			body: poolRequest{Name: "test", MatchType: "default"},
			readOnly:   true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Nil storage",
			body:       poolRequest{Name: "test", MatchType: "default"},
			nilStorage: true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "Invalid policy ID",
			policyID:   "invalid",
			body:       poolRequest{Name: "test", MatchType: "default"},
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Invalid JSON",
			body:       "invalid",
			setup:      func(api *API) int64 { id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com.")); return id },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing name",
			body: poolRequest{MatchType: "default"},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing match_type",
			body: poolRequest{Name: "test"},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid match_type",
			body: poolRequest{Name: "test", MatchType: "invalid"},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "CIDR without match_value",
			body: poolRequest{Name: "test", MatchType: "cidr"},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "CIDR with invalid CIDR",
			body: poolRequest{Name: "test", MatchType: "cidr", MatchValue: "not-a-cidr"},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Negative priority",
			body: poolRequest{Name: "test", MatchType: "default", Priority: -1},
			setup: func(api *API) int64 {
				id, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupGSLBTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.poolStorage = nil
			}
			policyID := tt.setup(api)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/gslb/policies/1/pools", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.policyID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.policyID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", policyID)}}
			}

			api.createPool(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestUpdatePool(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		poolID     string
		body       interface{}
		readOnly   bool
		nilStorage bool
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name: "Valid update",
			body: poolRequest{Name: "updated-pool", MatchType: "default"},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "Update to fallback type",
			body: poolRequest{Name: "fallback", MatchType: "fallback"},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "Update to CIDR",
			body: poolRequest{Name: "cidr-pool", MatchType: "cidr", MatchValue: "192.168.0.0/16"},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "Read-only mode",
			body:       poolRequest{Name: "test", MatchType: "default"},
			readOnly:   true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Nil storage",
			body:       poolRequest{Name: "test", MatchType: "default"},
			nilStorage: true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "Invalid ID",
			poolID:     "invalid",
			body:       poolRequest{Name: "test", MatchType: "default"},
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid JSON",
			body: "invalid",
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing name",
			body: poolRequest{MatchType: "default"},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid match_type",
			body: poolRequest{Name: "test", MatchType: "invalid"},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "CIDR without value",
			body: poolRequest{Name: "test", MatchType: "cidr"},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "CIDR invalid format",
			body: poolRequest{Name: "test", MatchType: "cidr", MatchValue: "bad"},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Negative priority",
			body: poolRequest{Name: "test", MatchType: "default", Priority: -1},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupGSLBTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.poolStorage = nil
			}
			poolID := tt.setup(api)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("PUT", "/api/gslb/pools/1", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.poolID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.poolID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", poolID)}}
			}

			api.updatePool(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestDeletePool(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		poolID     string
		readOnly   bool
		nilStorage bool
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name: "Valid delete",
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
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
			poolID:     "invalid",
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Non-existent pool",
			setup:      func(api *API) int64 { return 9999 },
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupGSLBTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.poolStorage = nil
			}
			poolID := tt.setup(api)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("DELETE", "/api/gslb/pools/1", nil)

			if tt.poolID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.poolID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", poolID)}}
			}

			api.deletePool(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

// --- Member Tests ---

func TestListMembers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Valid list", func(t *testing.T) {
		api, _ := setupGSLBTestAPI(t)
		pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
		poolID, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/gslb/pools/1/members", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", poolID)}}

		api.listMembers(c)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("Nil storage", func(t *testing.T) {
		api := &API{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/gslb/pools/1/members", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: "1"}}

		api.listMembers(c)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("Invalid pool ID", func(t *testing.T) {
		api, _ := setupGSLBTestAPI(t)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/gslb/pools/invalid/members", nil)
		c.Params = gin.Params{gin.Param{Key: "id", Value: "invalid"}}

		api.listMembers(c)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestCreateMember(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		poolID     string
		body       interface{}
		readOnly   bool
		nilStorage bool
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name: "Valid member",
			body: memberRequest{Address: "1.2.3.4", Weight: 50},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "Valid IPv6 member",
			body: memberRequest{Address: "2001:db8::1", Weight: 30},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusCreated,
		},
		{
			name: "With enabled=false",
			body: memberRequest{Address: "1.2.3.4", Weight: 10, Enabled: boolPtr(false)},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "Read-only mode",
			body:       memberRequest{Address: "1.2.3.4", Weight: 50},
			readOnly:   true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Nil storage",
			body:       memberRequest{Address: "1.2.3.4", Weight: 50},
			nilStorage: true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "Invalid pool ID",
			poolID:     "invalid",
			body:       memberRequest{Address: "1.2.3.4", Weight: 50},
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid JSON",
			body: "invalid",
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing address",
			body: memberRequest{Weight: 50},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid IP address",
			body: memberRequest{Address: "not-an-ip", Weight: 50},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "IP with port",
			body: memberRequest{Address: "1.2.3.4:80", Weight: 50},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Weight too high",
			body: memberRequest{Address: "1.2.3.4", Weight: 101},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Negative weight",
			body: memberRequest{Address: "1.2.3.4", Weight: -1},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				id, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupGSLBTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.poolStorage = nil
			}
			poolID := tt.setup(api)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/gslb/pools/1/members", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.poolID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.poolID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", poolID)}}
			}

			api.createMember(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestUpdateMember(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		memberID   string
		body       interface{}
		readOnly   bool
		nilStorage bool
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name: "Valid update",
			body: memberRequest{Address: "5.6.7.8", Weight: 80},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				poolID, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				id, _ := api.poolStorage.CreateMember(newGSLBMember(poolID, "1.2.3.4", 50))
				return id
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "With enabled field",
			body: memberRequest{Address: "5.6.7.8", Weight: 80, Enabled: boolPtr(false)},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				poolID, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				id, _ := api.poolStorage.CreateMember(newGSLBMember(poolID, "1.2.3.4", 50))
				return id
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "Read-only mode",
			body:       memberRequest{Address: "1.2.3.4", Weight: 50},
			readOnly:   true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Nil storage",
			body:       memberRequest{Address: "1.2.3.4", Weight: 50},
			nilStorage: true,
			setup:      func(api *API) int64 { return 1 },
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "Invalid ID",
			memberID:   "invalid",
			body:       memberRequest{Address: "1.2.3.4", Weight: 50},
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid JSON",
			body: "invalid",
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				poolID, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				id, _ := api.poolStorage.CreateMember(newGSLBMember(poolID, "1.2.3.4", 50))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Missing address",
			body: memberRequest{Weight: 50},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				poolID, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				id, _ := api.poolStorage.CreateMember(newGSLBMember(poolID, "1.2.3.4", 50))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Invalid IP",
			body: memberRequest{Address: "not-ip", Weight: 50},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				poolID, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				id, _ := api.poolStorage.CreateMember(newGSLBMember(poolID, "1.2.3.4", 50))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Weight out of range",
			body: memberRequest{Address: "1.2.3.4", Weight: 200},
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				poolID, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				id, _ := api.poolStorage.CreateMember(newGSLBMember(poolID, "1.2.3.4", 50))
				return id
			},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "Non-existent member",
			body: memberRequest{Address: "1.2.3.4", Weight: 50},
			setup: func(api *API) int64 {
				return 9999
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupGSLBTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.poolStorage = nil
			}
			memberID := tt.setup(api)

			body, _ := json.Marshal(tt.body)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("PUT", "/api/gslb/members/1", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			if tt.memberID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.memberID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", memberID)}}
			}

			api.updateMember(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

func TestDeleteMember(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		memberID   string
		readOnly   bool
		nilStorage bool
		setup      func(api *API) int64
		wantStatus int
	}{
		{
			name: "Valid delete",
			setup: func(api *API) int64 {
				pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
				poolID, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
				id, _ := api.poolStorage.CreateMember(newGSLBMember(poolID, "1.2.3.4", 50))
				return id
			},
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
			memberID:   "invalid",
			setup:      func(api *API) int64 { return 0 },
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Non-existent member",
			setup:      func(api *API) int64 { return 9999 },
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := setupGSLBTestAPI(t)
			api.readOnly = tt.readOnly
			if tt.nilStorage {
				api.poolStorage = nil
			}
			memberID := tt.setup(api)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("DELETE", "/api/gslb/members/1", nil)

			if tt.memberID != "" {
				c.Params = gin.Params{gin.Param{Key: "id", Value: tt.memberID}}
			} else {
				c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", memberID)}}
			}

			api.deleteMember(c)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}

// --- DB Error Tests ---

func TestListPolicies_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupGSLBTestAPI(t)
	db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/gslb/policies", nil)

	api.listPolicies(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListPools_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupGSLBTestAPI(t)
	db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/gslb/policies/1/pools", nil)
	c.Params = gin.Params{gin.Param{Key: "id", Value: "1"}}

	api.listPools(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestListMembers_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupGSLBTestAPI(t)
	db.Reader.Close()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/gslb/pools/1/members", nil)
	c.Params = gin.Params{gin.Param{Key: "id", Value: "1"}}

	api.listMembers(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreatePolicy_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupGSLBTestAPI(t)
	db.Writer.Close()

	body, _ := json.Marshal(policyRequest{Name: "test", Domain: "example.com", TTL: 30})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/gslb/policies", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	api.createPolicy(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreatePool_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupGSLBTestAPI(t)
	// Create policy first, then close writer
	policyID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("test", "example.com."))
	db.Writer.Close()

	body, _ := json.Marshal(poolRequest{Name: "pool", MatchType: "default"})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/gslb/policies/1/pools", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", policyID)}}

	api.createPool(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateMember_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupGSLBTestAPI(t)
	pID, _ := api.policyStorage.CreatePolicy(newGSLBPolicy("t", "e.com."))
	poolID, _ := api.poolStorage.CreatePool(newGSLBPool(pID, "pool", "default"))
	db.Writer.Close()

	body, _ := json.Marshal(memberRequest{Address: "1.2.3.4", Weight: 50})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/gslb/pools/1/members", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{gin.Param{Key: "id", Value: fmt.Sprintf("%d", poolID)}}

	api.createMember(c)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- Helper functions ---

func gslbPolicyModel(name, domain string) model.GSLBPolicy {
	return model.GSLBPolicy{
		Name:       name,
		Domain:     domain,
		RecordType: "A",
		TTL:        30,
		Enabled:    true,
	}
}

func newGSLBPolicy(name, domain string) *model.GSLBPolicy {
	p := gslbPolicyModel(name, domain)
	return &p
}

func gslbPoolModel(policyID int64, name, matchType string) model.GSLBPool {
	return model.GSLBPool{
		PolicyID:  policyID,
		Name:      name,
		MatchType: matchType,
	}
}

func newGSLBPool(policyID int64, name, matchType string) *model.GSLBPool {
	p := gslbPoolModel(policyID, name, matchType)
	return &p
}

func gslbMemberModel(poolID int64, address string, weight int64) model.GSLBMember {
	return model.GSLBMember{
		PoolID:  poolID,
		Address: address,
		Weight:  weight,
		Enabled: true,
	}
}

func newGSLBMember(poolID int64, address string, weight int64) *model.GSLBMember {
	m := gslbMemberModel(poolID, address, weight)
	return &m
}
