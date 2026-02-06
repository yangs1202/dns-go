package web

import (
	"dns-go/config"
	"dns-go/storage"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetServerInfo_Primary(t *testing.T) {
	// 테스트용 DB 생성
	tmpDB := "/tmp/test_server_info_primary.db"
	defer os.Remove(tmpDB)

	db, err := storage.NewDatabase(tmpDB)
	require.NoError(t, err)
	defer db.Close()

	// Primary 설정
	cfg := &config.Config{
		DNS: config.DNSConfig{
			Listen: "0.0.0.0",
			Port:   5301,
			NSID:   "dns-primary-01",
		},
		Web: config.WebConfig{
			Listen: "0.0.0.0",
			Port:   8081,
		},
		Sync: config.SyncConfig{
			Mode:     "primary",
			ReadOnly: false,
		},
	}

	// API 초기화
	api := NewServerInfoAPI(cfg, db)
	time.Sleep(1100 * time.Millisecond) // 가동 시간 테스트를 위해

	// 라우터 설정
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/server/info", api.GetServerInfo)

	// 요청 생성
	req := httptest.NewRequest(http.MethodGet, "/api/server/info", nil)
	w := httptest.NewRecorder()

	// 요청 실행
	router.ServeHTTP(w, req)

	// 응답 검증
	assert.Equal(t, http.StatusOK, w.Code)

	var response ServerInfoResponse
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// 필드 검증
	assert.Equal(t, "dns-primary-01", response.ServerName)
	assert.Equal(t, "primary", response.ServerRole)
	assert.Equal(t, "DNS-Go v0.2.0", response.Version)
	assert.Equal(t, "0.0.0.0:5301", response.DNSAddress)
	assert.Equal(t, "0.0.0.0:8081", response.WebAddress)
	assert.False(t, response.ReadOnly)
	assert.Greater(t, response.UptimeSeconds, int64(0))
	assert.NotEmpty(t, response.Uptime)
	assert.NotZero(t, response.StartedAt)
	assert.Nil(t, response.LastSyncAt)
	assert.Empty(t, response.PrimaryURL)
	assert.Empty(t, response.SyncInterval)
}

func TestGetServerInfo_Secondary(t *testing.T) {
	// 테스트용 DB 생성
	tmpDB := "/tmp/test_server_info_secondary.db"
	defer os.Remove(tmpDB)

	db, err := storage.NewDatabase(tmpDB)
	require.NoError(t, err)
	defer db.Close()

	// Secondary 설정
	cfg := &config.Config{
		DNS: config.DNSConfig{
			Listen: "0.0.0.0",
			Port:   5302,
			NSID:   "dns-secondary-01",
		},
		Web: config.WebConfig{
			Listen: "0.0.0.0",
			Port:   8082,
		},
		Sync: config.SyncConfig{
			Mode:       "secondary",
			PrimaryURL: "http://10.97.11.2:2080",
			Interval:   10 * time.Second,
			ReadOnly:   true,
		},
	}

	// 동기화 상태 초기화 (마지막 동기화 시간 설정)
	syncVersion := storage.NewSyncVersion(db)
	_, err = db.Writer.Exec(`
		UPDATE sync_state
		SET last_sync_version = 1,
		    last_sync_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`)
	require.NoError(t, err)

	// API 초기화
	api := NewServerInfoAPI(cfg, db)
	time.Sleep(1100 * time.Millisecond)

	// 라우터 설정
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/server/info", api.GetServerInfo)

	// 요청 생성
	req := httptest.NewRequest(http.MethodGet, "/api/server/info", nil)
	w := httptest.NewRecorder()

	// 요청 실행
	router.ServeHTTP(w, req)

	// 응답 검증
	assert.Equal(t, http.StatusOK, w.Code)

	var response ServerInfoResponse
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// 필드 검증
	assert.Equal(t, "dns-secondary-01", response.ServerName)
	assert.Equal(t, "secondary", response.ServerRole)
	assert.Equal(t, "DNS-Go v0.2.0", response.Version)
	assert.Equal(t, "0.0.0.0:5302", response.DNSAddress)
	assert.Equal(t, "0.0.0.0:8082", response.WebAddress)
	assert.True(t, response.ReadOnly)
	assert.Greater(t, response.UptimeSeconds, int64(0))
	assert.NotEmpty(t, response.Uptime)
	assert.NotZero(t, response.StartedAt)
	assert.Equal(t, "http://10.97.11.2:2080", response.PrimaryURL)
	assert.Equal(t, "10s", response.SyncInterval)
	assert.NotNil(t, response.LastSyncAt) // Secondary는 last_sync_at이 있어야 함

	// 동기화 버전 확인
	version, err := syncVersion.GetVersion()
	require.NoError(t, err)
	assert.Equal(t, version, response.SyncVersion)
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "Seconds only",
			duration: 45 * time.Second,
			expected: "45s",
		},
		{
			name:     "Minutes and seconds",
			duration: 3*time.Minute + 25*time.Second,
			expected: "03m 25s",
		},
		{
			name:     "Hours, minutes and seconds",
			duration: 2*time.Hour + 15*time.Minute + 30*time.Second,
			expected: "02h 15m 30s",
		},
		{
			name:     "Days, hours and minutes",
			duration: 3*24*time.Hour + 5*time.Hour + 45*time.Minute,
			expected: "3d 05h 45m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestServerName_Fallback(t *testing.T) {
	// NSID가 비어있을 때 hostname을 사용하는지 테스트
	tmpDB := "/tmp/test_server_info_hostname.db"
	defer os.Remove(tmpDB)

	db, err := storage.NewDatabase(tmpDB)
	require.NoError(t, err)
	defer db.Close()

	cfg := &config.Config{
		DNS: config.DNSConfig{
			Listen: "0.0.0.0",
			Port:   5301,
			NSID:   "", // NSID 비어있음
		},
		Web: config.WebConfig{
			Listen: "0.0.0.0",
			Port:   8081,
		},
		Sync: config.SyncConfig{
			Mode: "primary",
		},
	}

	api := NewServerInfoAPI(cfg, db)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/server/info", api.GetServerInfo)

	req := httptest.NewRequest(http.MethodGet, "/api/server/info", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response ServerInfoResponse
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// hostname 또는 "dns-go"가 설정되어야 함
	hostname, _ := os.Hostname()
	if hostname != "" {
		assert.Equal(t, hostname, response.ServerName)
	} else {
		assert.Equal(t, "dns-go", response.ServerName)
	}
}
