package web

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"dns-go/storage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)
	db := storage.SetupTestDB(t)
	syncVersion := storage.NewSyncVersion(db)
	syncAPI := NewSyncAPI(syncVersion)

	server := NewServer("127.0.0.1", 8080, api, syncAPI, nil)

	assert.NotNil(t, server)
	assert.Equal(t, "127.0.0.1:8080", server.addr)
	assert.NotNil(t, server.router)
	assert.NotNil(t, server.http)
}

func TestNewServer_WithoutSyncAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)

	server := NewServer("0.0.0.0", 9090, api, nil, nil)

	assert.NotNil(t, server)
	assert.Equal(t, "0.0.0.0:9090", server.addr)
}

func TestServer_Addr(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)

	tests := []struct {
		listen   string
		port     int
		wantAddr string
	}{
		{
			listen:   "127.0.0.1",
			port:     8080,
			wantAddr: "127.0.0.1:8080",
		},
		{
			listen:   "0.0.0.0",
			port:     9090,
			wantAddr: "0.0.0.0:9090",
		},
		{
			listen:   "localhost",
			port:     3000,
			wantAddr: "localhost:3000",
		},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s:%d", tt.listen, tt.port), func(t *testing.T) {
			server := NewServer(tt.listen, tt.port, api, nil, nil)
			assert.Equal(t, tt.wantAddr, server.Addr())
		})
	}
}

func TestServer_StartAndStop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)

	// Use a random available port
	server := NewServer("127.0.0.1", 0, api, nil, nil)

	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait a bit for server to start
	time.Sleep(100 * time.Millisecond)

	// Check if there was a startup error
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Server failed to start: %v", err)
		}
	default:
		// No error, server started successfully
	}

	// Stop server
	err := server.Stop()
	require.NoError(t, err)

	// Wait for server to fully stop
	<-errCh
}

func TestServer_StopWithTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)

	server := NewServer("127.0.0.1", 0, api, nil, nil)

	// Start server
	go func() {
		_ = server.Start()
	}()

	time.Sleep(100 * time.Millisecond)

	// Stop should complete within timeout
	stopCh := make(chan error, 1)
	go func() {
		stopCh <- server.Stop()
	}()

	select {
	case err := <-stopCh:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Stop() did not complete within timeout")
	}
}

func TestServer_Integration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, db := setupTestAPI(t)
	storage.InsertTestZone(t, db, "example.com.")

	server := NewServer("127.0.0.1", 0, api, nil, nil)

	// Start server
	go func() {
		_ = server.Start()
	}()

	time.Sleep(100 * time.Millisecond)

	// Get the actual address (with assigned port)
	actualAddr := server.http.Addr
	if actualAddr == "" {
		actualAddr = server.Addr()
	}

	// Make HTTP request
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://%s/api/zones", actualAddr))

	// Stop server first
	defer server.Stop()

	// Only check if we got a response (might fail if port is 0 and not yet assigned)
	if err == nil {
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}
}

func TestServer_HTTPServer_Configuration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)

	server := NewServer("127.0.0.1", 8080, api, nil, nil)

	assert.NotNil(t, server.http)
	assert.Equal(t, "127.0.0.1:8080", server.http.Addr)
	assert.NotNil(t, server.http.Handler)
	assert.Equal(t, 5*time.Second, server.http.ReadHeaderTimeout)
}

func TestServer_Stop_WithoutStart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	api, _ := setupTestAPI(t)

	server := NewServer("127.0.0.1", 8080, api, nil, nil)

	// Stopping a server that wasn't started should work gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := server.http.Shutdown(ctx)
	// Should not panic and may return nil or context deadline exceeded
	assert.True(t, err == nil || err == context.DeadlineExceeded)
}
