package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		wantErr  bool
		validate func(*testing.T, *Config)
	}{
		{
			name: "유효한 설정",
			yaml: `
dns:
  listen: "0.0.0.0"
  port: 53
  tcp: true
  udp: true

upstream:
  timeout: 5s

web:
  listen: "0.0.0.0"
  port: 8080

database:
  path: "./test.db"

geoip:
  city_db: "./GeoLite2-City.mmdb"

adblock:
  enabled: true
  sync_interval: 1h
  block_response: "0.0.0.0"

logging:
  level: "info"
`,
			wantErr: false,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, "0.0.0.0", cfg.DNS.Listen)
				assert.Equal(t, 53, cfg.DNS.Port)
				assert.True(t, cfg.DNS.TCP)
				assert.True(t, cfg.DNS.UDP)
				assert.Equal(t, SyncModePrimary, cfg.Sync.Mode)
				assert.Equal(t, 5*time.Second, cfg.Upstream.Timeout)
				assert.Equal(t, 8080, cfg.Web.Port)
				assert.Equal(t, "./test.db", cfg.Database.Path)
				assert.True(t, cfg.Adblock.Enabled)
				assert.Equal(t, 1*time.Hour, cfg.Adblock.SyncInterval)
				assert.Equal(t, "0.0.0.0", cfg.Adblock.BlockResponse)
				assert.Equal(t, "info", cfg.Logging.Level)
				assert.Equal(t, SyncModePrimary, cfg.Sync.Mode)
			},
		},
		{
			name: "기본값 적용",
			yaml: `
database:
  path: "./test.db"
`,
			wantErr: false,
			validate: func(t *testing.T, cfg *Config) {
				assert.Equal(t, 53, cfg.DNS.Port)
				assert.Equal(t, 8080, cfg.Web.Port)
				assert.Equal(t, 5*time.Second, cfg.Upstream.Timeout)
				assert.Equal(t, 1*time.Hour, cfg.Adblock.SyncInterval)
				assert.Equal(t, "0.0.0.0", cfg.Adblock.BlockResponse)
				assert.Equal(t, "info", cfg.Logging.Level)
			},
		},
		{
			name:    "잘못된 YAML",
			yaml:    `invalid: [[[`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 임시 설정 파일 생성
			tmpDir := t.TempDir()
			cfgPath := filepath.Join(tmpDir, "config.yaml")
			err := os.WriteFile(cfgPath, []byte(tt.yaml), 0644)
			require.NoError(t, err)

			// 설정 로드
			cfg, err := Load(cfgPath)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, cfg)

			if tt.validate != nil {
				tt.validate(t, cfg)
			}
		})
	}
}

func TestLoadNonExistentFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "설정 파일 읽기 실패")
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "유효한 설정",
			config: Config{
				DNS: DNSConfig{
					Port: 53,
					UDP:  true,
				},
				Web: WebConfig{
					Port: 8080,
				},
				Database: DatabaseConfig{
					Path: "./test.db",
				},
				Adblock: AdblockConfig{
					BlockResponse: "0.0.0.0",
				},
				Sync: SyncConfig{
					Mode: SyncModePrimary,
				},
			},
			wantErr: false,
		},
		{
			name: "TCP, UDP 모두 비활성화",
			config: Config{
				DNS: DNSConfig{
					Port: 53,
					TCP:  false,
					UDP:  false,
				},
				Database: DatabaseConfig{
					Path: "./test.db",
				},
			},
			wantErr: true,
			errMsg:  "TCP 또는 UDP 중 하나 이상 활성화되어야 합니다",
		},
		{
			name: "잘못된 DNS 포트",
			config: Config{
				DNS: DNSConfig{
					Port: 0,
					UDP:  true,
				},
				Database: DatabaseConfig{
					Path: "./test.db",
				},
			},
			wantErr: true,
			errMsg:  "잘못된 DNS 포트",
		},
		{
			name: "잘못된 웹 포트",
			config: Config{
				DNS: DNSConfig{
					Port: 53,
					UDP:  true,
				},
				Web: WebConfig{
					Port: 99999,
				},
				Database: DatabaseConfig{
					Path: "./test.db",
				},
			},
			wantErr: true,
			errMsg:  "잘못된 웹 포트",
		},
		{
			name: "데이터베이스 경로 없음",
			config: Config{
				DNS: DNSConfig{
					Port: 53,
					UDP:  true,
				},
				Web: WebConfig{
					Port: 8080,
				},
			},
			wantErr: true,
			errMsg:  "데이터베이스 경로가 설정되지 않았습니다",
		},
		{
			name: "잘못된 차단 응답 타입",
			config: Config{
				DNS: DNSConfig{
					Port: 53,
					UDP:  true,
				},
				Web: WebConfig{
					Port: 8080,
				},
				Database: DatabaseConfig{
					Path: "./test.db",
				},
				Adblock: AdblockConfig{
					BlockResponse: "invalid",
				},
			},
			wantErr: true,
			errMsg:  "잘못된 차단 응답 타입",
		},
		{
			name: "NXDOMAIN 응답 타입",
			config: Config{
				DNS: DNSConfig{
					Port: 53,
					UDP:  true,
				},
				Web: WebConfig{
					Port: 8080,
				},
				Database: DatabaseConfig{
					Path: "./test.db",
				},
				Adblock: AdblockConfig{
					BlockResponse: "NXDOMAIN",
				},
				Sync: SyncConfig{
					Mode: SyncModePrimary,
				},
			},
			wantErr: false,
		},
		{
			name: "잘못된 Sync 모드",
			config: Config{
				DNS: DNSConfig{
					Port: 53,
					UDP:  true,
				},
				Web: WebConfig{
					Port: 8080,
				},
				Database: DatabaseConfig{
					Path: "./test.db",
				},
				Adblock: AdblockConfig{
					BlockResponse: "NXDOMAIN",
				},
				Sync: SyncConfig{
					Mode: "standalone",
				},
			},
			wantErr: true,
			errMsg:  "잘못된 Sync 모드",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
