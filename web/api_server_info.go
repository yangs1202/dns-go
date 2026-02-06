package web

import (
	"dns-go/config"
	"dns-go/storage"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

// ServerInfoAPI는 서버 상태 정보를 제공하는 API입니다
type ServerInfoAPI struct {
	cfg         *config.Config
	db          *storage.Database
	startTime   time.Time
	syncVersion *storage.SyncVersion
}

// NewServerInfoAPI는 ServerInfoAPI 인스턴스를 생성합니다
func NewServerInfoAPI(cfg *config.Config, db *storage.Database) *ServerInfoAPI {
	return &ServerInfoAPI{
		cfg:         cfg,
		db:          db,
		startTime:   time.Now(),
		syncVersion: storage.NewSyncVersion(db),
	}
}

// ServerInfoResponse는 서버 정보 응답 구조체입니다
type ServerInfoResponse struct {
	ServerName     string     `json:"server_name"`     // 서버 이름 (NSID 또는 hostname)
	ServerRole     string     `json:"server_role"`     // "primary" | "secondary"
	Version        string     `json:"version"`         // DNS-Go 버전
	DNSAddress     string     `json:"dns_address"`     // DNS 서버 주소
	WebAddress     string     `json:"web_address"`     // Web API 서버 주소
	Uptime         string     `json:"uptime"`          // 가동 시간 (사람이 읽기 쉬운 형식)
	UptimeSeconds  int64      `json:"uptime_seconds"`  // 가동 시간 (초)
	SyncVersion    int64      `json:"sync_version"`    // 현재 동기화 버전
	LastSyncAt     *time.Time `json:"last_sync_at"`    // 마지막 동기화 시간 (Secondary만)
	PrimaryURL     string     `json:"primary_url"`     // Primary URL (Secondary만)
	SyncInterval   string     `json:"sync_interval"`   // 동기화 주기 (Secondary만)
	ReadOnly       bool       `json:"read_only"`       // Read-Only 모드 여부
	StartedAt      time.Time  `json:"started_at"`      // 서버 시작 시간
}

// GetServerInfo는 서버 상태 정보를 반환합니다
// GET /api/server/info
func (s *ServerInfoAPI) GetServerInfo(c *gin.Context) {
	// 서버 이름 결정 (NSID 우선, 없으면 hostname)
	serverName := s.cfg.DNS.NSID
	if serverName == "" {
		if hostname, err := os.Hostname(); err == nil {
			serverName = hostname
		} else {
			serverName = "dns-go"
		}
	}

	// 동기화 버전 조회
	syncVersion, err := s.syncVersion.GetVersion()
	if err != nil {
		syncVersion = 0
	}

	// 가동 시간 계산
	uptime := time.Since(s.startTime)
	uptimeStr := formatDuration(uptime)

	// DNS 서버 주소
	dnsAddress := s.cfg.DNS.Listen
	if dnsAddress == "" {
		dnsAddress = "0.0.0.0"
	}
	dnsAddress = fmt.Sprintf("%s:%d", dnsAddress, s.cfg.DNS.Port)

	// Web API 서버 주소
	webAddress := s.cfg.Web.Listen
	if webAddress == "" {
		webAddress = "0.0.0.0"
	}
	webAddress = fmt.Sprintf("%s:%d", webAddress, s.cfg.Web.Port)

	// 응답 구조체 생성
	response := ServerInfoResponse{
		ServerName:    serverName,
		ServerRole:    s.cfg.Sync.Mode,
		Version:       "DNS-Go v0.2.0",
		DNSAddress:    dnsAddress,
		WebAddress:    webAddress,
		Uptime:        uptimeStr,
		UptimeSeconds: int64(uptime.Seconds()),
		SyncVersion:   syncVersion,
		ReadOnly:      s.cfg.Sync.ReadOnly,
		StartedAt:     s.startTime,
	}

	// Secondary 모드인 경우 추가 정보
	if s.cfg.Sync.Mode == "secondary" {
		response.PrimaryURL = s.cfg.Sync.PrimaryURL
		response.SyncInterval = s.cfg.Sync.Interval.String()

		// 마지막 동기화 시간 조회
		syncState, err := s.syncVersion.GetSyncState()
		if err == nil {
			if lastSyncAt, ok := syncState["last_sync_at"].(time.Time); ok {
				response.LastSyncAt = &lastSyncAt
			}
		}
	}

	c.JSON(http.StatusOK, response)
}

// formatDuration은 Duration을 사람이 읽기 쉬운 형식으로 변환합니다
func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %02dh %02dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%02dh %02dm %02ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%02dm %02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
