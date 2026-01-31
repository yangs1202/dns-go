package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRequestLogger(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{
			name:   "GET request",
			method: "GET",
			path:   "/api/zones",
			status: http.StatusOK,
		},
		{
			name:   "POST request",
			method: "POST",
			path:   "/api/zones",
			status: http.StatusCreated,
		},
		{
			name:   "DELETE request",
			method: "DELETE",
			path:   "/api/zones/1",
			status: http.StatusOK,
		},
		{
			name:   "Not Found",
			method: "GET",
			path:   "/api/notfound",
			status: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, r := gin.CreateTestContext(w)

			r.Use(requestLogger())
			r.Handle(tt.method, tt.path, func(c *gin.Context) {
				c.Status(tt.status)
			})

			req, _ := http.NewRequest(tt.method, tt.path, nil)
			c.Request = req
			r.ServeHTTP(w, req)

			assert.Equal(t, tt.status, w.Code)
		})
	}
}

func TestCorsMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		method         string
		wantStatus     int
		checkHeaders   bool
	}{
		{
			name:         "OPTIONS request",
			method:       "OPTIONS",
			wantStatus:   http.StatusNoContent,
			checkHeaders: true,
		},
		{
			name:         "GET request",
			method:       "GET",
			wantStatus:   http.StatusOK,
			checkHeaders: true,
		},
		{
			name:         "POST request",
			method:       "POST",
			wantStatus:   http.StatusOK,
			checkHeaders: true,
		},
		{
			name:         "PUT request",
			method:       "PUT",
			wantStatus:   http.StatusOK,
			checkHeaders: true,
		},
		{
			name:         "DELETE request",
			method:       "DELETE",
			wantStatus:   http.StatusOK,
			checkHeaders: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, r := gin.CreateTestContext(w)

			r.Use(corsMiddleware())
			r.Any("/test", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			req, _ := http.NewRequest(tt.method, "/test", nil)
			c.Request = req
			r.ServeHTTP(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.checkHeaders {
				assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
				assert.Equal(t, "GET, POST, PUT, DELETE, OPTIONS", w.Header().Get("Access-Control-Allow-Methods"))
				assert.Equal(t, "Content-Type, Authorization", w.Header().Get("Access-Control-Allow-Headers"))
			}
		})
	}
}

func TestCorsMiddleware_OPTIONS_Aborts(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)

	handlerCalled := false
	r.Use(corsMiddleware())
	r.OPTIONS("/test", func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("OPTIONS", "/test", nil)
	c.Request = req
	r.ServeHTTP(w, req)

	// OPTIONS 요청은 미들웨어에서 204로 중단되어야 함
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.False(t, handlerCalled, "Handler should not be called for OPTIONS request")
}

func TestCorsMiddleware_AllowedOrigins(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)

	r.Use(corsMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	c.Request = req
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}

func TestMiddleware_ChainedExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)

	// 두 미들웨어를 함께 사용
	r.Use(corsMiddleware())
	r.Use(requestLogger())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	c.Request = req
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
}
