package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"dns-go/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestRunStartsAndStopsPrimary(t *testing.T) {
	tmpDir := t.TempDir()
	dnsPort := freeTCPPort(t)
	webPort := freeTCPPort(t)
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "dns-go.db")

	configYAML := `
dns:
  listen: "127.0.0.1"
  port: ` + strconv.Itoa(dnsPort) + `
  tcp: true
  udp: false
  nsid: "test-nsid"
  version: "DNS-Go Test"
upstream:
  timeout: 50ms
web:
  listen: "127.0.0.1"
  port: ` + strconv.Itoa(webPort) + `
database:
  path: "` + dbPath + `"
geoip:
  city_db: "` + filepath.Join(tmpDir, "missing.mmdb") + `"
adblock:
  enabled: false
  sync_interval: 24h
  block_response: "0.0.0.0"
sync:
  mode: "primary"
  readonly: true
logging:
  level: "info"
  query_log:
    enabled: true
    retention_days: 1
    flush_interval: 1h
    buffer_size: 10
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(configYAML), 0644))

	err := run(cfgPath, func(<-chan os.Signal) os.Signal {
		return syscall.SIGTERM
	})
	require.NoError(t, err)

	_, statErr := os.Stat(dbPath)
	assert.NoError(t, statErr)
}

func TestRunStartsAndStopsSecondary(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "secondary.yaml")
	dbPath := filepath.Join(tmpDir, "secondary.db")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
dns:
  listen: "127.0.0.1"
  port: `+strconv.Itoa(freeTCPPort(t))+`
  tcp: true
  udp: false
web:
  listen: "127.0.0.1"
  port: `+strconv.Itoa(freeTCPPort(t))+`
database:
  path: "`+dbPath+`"
adblock:
  enabled: false
  sync_interval: 24h
  block_response: "NXDOMAIN"
sync:
  mode: "secondary"
  primary_url: "http://127.0.0.1:1"
  interval: 24h
  readonly: true
logging:
  query_log:
    enabled: false
`), 0644))

	err := run(cfgPath, func(<-chan os.Signal) os.Signal {
		return syscall.SIGTERM
	})
	require.NoError(t, err)
}

func TestRunConfigErrors(t *testing.T) {
	err := run("/missing/config.yaml", func(<-chan os.Signal) os.Signal { return syscall.SIGTERM })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "설정 로드 실패")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "invalid.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
dns:
  tcp: false
  udp: false
database:
  path: "`+filepath.Join(tmpDir, "db.sqlite")+`"
`), 0644))

	err = run(cfgPath, func(<-chan os.Signal) os.Signal { return syscall.SIGTERM })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "설정 검증 실패")

	dirPath := t.TempDir()
	cfgPath = filepath.Join(t.TempDir(), "db-error.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
dns:
  tcp: true
  udp: false
database:
  path: "`+dirPath+`"
adblock:
  block_response: "0.0.0.0"
`), 0644))
	err = run(cfgPath, func(<-chan os.Signal) os.Signal { return syscall.SIGTERM })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "데이터베이스 연결 실패")
}

func TestRunDNSServerStartError(t *testing.T) {
	tmpDir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "dns-go.db")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
dns:
  listen: "127.0.0.1"
  port: `+strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)+`
  tcp: true
  udp: false
web:
  listen: "127.0.0.1"
  port: `+strconv.Itoa(freeTCPPort(t))+`
database:
  path: "`+dbPath+`"
adblock:
  block_response: "0.0.0.0"
sync:
  mode: "primary"
`), 0644))

	err = run(cfgPath, func(<-chan os.Signal) os.Signal {
		return syscall.SIGTERM
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DNS 서버 시작 실패")
}

func TestInitDBSeedsDefaultsAndIsIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "init.db")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
database:
  path: "`+dbPath+`"
`), 0644))

	require.NoError(t, initDB(cfgPath))
	require.NoError(t, initDB(cfgPath))

	db, err := storage.NewDatabase(dbPath)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	var upstreamCount int
	require.NoError(t, db.Reader.QueryRow("SELECT COUNT(*) FROM upstream_servers").Scan(&upstreamCount))
	assert.Equal(t, 3, upstreamCount)

	var sourceCount int
	require.NoError(t, db.Reader.QueryRow("SELECT COUNT(*) FROM adblock_sources").Scan(&sourceCount))
	assert.Equal(t, 1, sourceCount)
}

func TestInitDBLoadError(t *testing.T) {
	err := initDB("/missing/config.yaml")
	require.Error(t, err)
}

func TestWaitForShutdownSignal(t *testing.T) {
	ch := make(chan os.Signal, 1)
	ch <- syscall.SIGINT

	assert.Equal(t, syscall.SIGINT, waitForShutdownSignal(ch))
}
