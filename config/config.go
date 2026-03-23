package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config는 전체 서버 설정을 담는 구조체입니다
type Config struct {
	DNS      DNSConfig      `yaml:"dns"`
	Upstream UpstreamConfig `yaml:"upstream"`
	Web      WebConfig      `yaml:"web"`
	Database DatabaseConfig `yaml:"database"`
	GeoIP    GeoIPConfig    `yaml:"geoip"`
	Adblock  AdblockConfig  `yaml:"adblock"`
	Sync     SyncConfig     `yaml:"sync"`
	Logging  LoggingConfig  `yaml:"logging"`
}

// DNSConfig는 DNS 서버 설정입니다
type DNSConfig struct {
	Listen    string `yaml:"listen"`
	Port      int    `yaml:"port"`
	TCP       bool   `yaml:"tcp"`
	UDP       bool   `yaml:"udp"`
	UDPSize   int    `yaml:"udp_size"`   // EDNS0 UDP 버퍼 크기 (기본: 1232)
	NSID      string `yaml:"nsid"`       // EDNS0 NSID (Name Server Identifier)
	Version   string `yaml:"version"`    // CHAOS TXT version.bind 응답
}

// UpstreamConfig는 업스트림 리졸버 설정입니다
type UpstreamConfig struct {
	Timeout time.Duration `yaml:"timeout"`
}

// WebConfig는 웹 서버 설정입니다
type WebConfig struct {
	Listen string `yaml:"listen"`
	Port   int    `yaml:"port"`
}

// DatabaseConfig는 데이터베이스 설정입니다
type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// GeoIPConfig는 GeoIP 설정입니다
type GeoIPConfig struct {
	CityDB string `yaml:"city_db"`
}

// AdblockConfig는 광고차단 설정입니다
type AdblockConfig struct {
	Enabled       bool          `yaml:"enabled"`
	SyncInterval  time.Duration `yaml:"sync_interval"`
	BlockResponse string        `yaml:"block_response"`
}

// SyncConfig는 Primary/Secondary 동기화 설정입니다
type SyncConfig struct {
	Mode       string        `yaml:"mode"`        // "primary" | "secondary"
	PrimaryURL string        `yaml:"primary_url"` // Secondary가 연결할 Primary URL
	Interval   time.Duration `yaml:"interval"`    // 동기화 주기 (기본: 1초)
	ReadOnly   bool          `yaml:"readonly"`    // Write API 차단 여부
}

// LoggingConfig는 로깅 설정입니다
type LoggingConfig struct {
	Level    string         `yaml:"level"`
	QueryLog QueryLogConfig `yaml:"query_log"`
}

// QueryLogConfig는 DNS 쿼리 로그 설정입니다
type QueryLogConfig struct {
	Enabled       bool          `yaml:"enabled"`
	RetentionDays int           `yaml:"retention_days"`
	FlushInterval time.Duration `yaml:"flush_interval"`
	BufferSize    int           `yaml:"buffer_size"`
}

// Load는 YAML 파일에서 설정을 로드합니다
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("설정 파일 읽기 실패: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("설정 파일 파싱 실패: %w", err)
	}

	// 기본값 설정
	if cfg.DNS.Port == 0 {
		cfg.DNS.Port = 53
	}
	if cfg.DNS.UDPSize == 0 {
		cfg.DNS.UDPSize = 1232 // RFC 6891 권장 (DNSSEC 지원)
	}
	if cfg.DNS.NSID == "" {
		// 기본값: hostname 사용
		if hostname, err := os.Hostname(); err == nil {
			cfg.DNS.NSID = hostname
		} else {
			cfg.DNS.NSID = "dns-go"
		}
	}
	if cfg.DNS.Version == "" {
		cfg.DNS.Version = "DNS-Go v0.2.0" // 기본 버전
	}
	if cfg.Web.Port == 0 {
		cfg.Web.Port = 8080
	}
	if cfg.Upstream.Timeout == 0 {
		cfg.Upstream.Timeout = 5 * time.Second
	}
	if cfg.Adblock.SyncInterval == 0 {
		cfg.Adblock.SyncInterval = 1 * time.Hour
	}
	if cfg.Adblock.BlockResponse == "" {
		cfg.Adblock.BlockResponse = "0.0.0.0"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.QueryLog.RetentionDays == 0 {
		cfg.Logging.QueryLog.RetentionDays = 7
	}
	if cfg.Logging.QueryLog.FlushInterval == 0 {
		cfg.Logging.QueryLog.FlushInterval = 2 * time.Second
	}
	if cfg.Logging.QueryLog.BufferSize == 0 {
		cfg.Logging.QueryLog.BufferSize = 1000
	}
	if cfg.Sync.Mode == "" {
		cfg.Sync.Mode = "primary"
	}
	if cfg.Sync.Interval == 0 {
		cfg.Sync.Interval = 1 * time.Second // 기본 1초
	}

	return &cfg, nil
}

// Validate는 설정의 유효성을 검증합니다
func (c *Config) Validate() error {
	if !c.DNS.TCP && !c.DNS.UDP {
		return fmt.Errorf("TCP 또는 UDP 중 하나 이상 활성화되어야 합니다")
	}

	if c.DNS.Port < 1 || c.DNS.Port > 65535 {
		return fmt.Errorf("잘못된 DNS 포트: %d", c.DNS.Port)
	}

	if c.Web.Port < 1 || c.Web.Port > 65535 {
		return fmt.Errorf("잘못된 웹 포트: %d", c.Web.Port)
	}

	if c.Database.Path == "" {
		return fmt.Errorf("데이터베이스 경로가 설정되지 않았습니다")
	}

	if c.Adblock.BlockResponse != "0.0.0.0" && c.Adblock.BlockResponse != "NXDOMAIN" {
		return fmt.Errorf("잘못된 차단 응답 타입: %s (0.0.0.0 또는 NXDOMAIN)", c.Adblock.BlockResponse)
	}

	return nil
}
