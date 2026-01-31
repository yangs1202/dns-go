package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRespondSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		status     int
		data       interface{}
		wantStatus int
		checkData  func(t *testing.T, data interface{})
	}{
		{
			name:       "OK with simple data",
			status:     http.StatusOK,
			data:       gin.H{"message": "success"},
			wantStatus: http.StatusOK,
			checkData: func(t *testing.T, data interface{}) {
				m, ok := data.(map[string]interface{})
				require.True(t, ok)
				assert.Equal(t, "success", m["message"])
			},
		},
		{
			name:       "Created with ID",
			status:     http.StatusCreated,
			data:       gin.H{"id": int64(1)},
			wantStatus: http.StatusCreated,
			checkData: func(t *testing.T, data interface{}) {
				m, ok := data.(map[string]interface{})
				require.True(t, ok)
				assert.Equal(t, float64(1), m["id"]) // JSON unmarshals numbers as float64
			},
		},
		{
			name:       "OK with nil data",
			status:     http.StatusOK,
			data:       nil,
			wantStatus: http.StatusOK,
			checkData: func(t *testing.T, data interface{}) {
				assert.Nil(t, data)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			respondSuccess(c, tt.status, tt.data)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response apiResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.True(t, response.Success)
			tt.checkData(t, response.Data)
			assert.Empty(t, response.Error)
			assert.Empty(t, response.Code)
		})
	}
}

func TestRespondError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		status     int
		msg        string
		code       string
		wantStatus int
	}{
		{
			name:       "Bad Request",
			status:     http.StatusBadRequest,
			msg:        "Invalid input",
			code:       "VALIDATION_ERROR",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Not Found",
			status:     http.StatusNotFound,
			msg:        "Resource not found",
			code:       "NOT_FOUND",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "Internal Error",
			status:     http.StatusInternalServerError,
			msg:        "Something went wrong",
			code:       "INTERNAL_ERROR",
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			respondError(c, tt.status, tt.msg, tt.code)

			assert.Equal(t, tt.wantStatus, w.Code)

			var response apiResponse
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.False(t, response.Success)
			assert.Nil(t, response.Data)
			assert.Equal(t, tt.msg, response.Error)
			assert.Equal(t, tt.code, response.Code)
		})
	}
}

func TestRespondBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	msg := "잘못된 요청입니다"
	respondBadRequest(c, msg)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response apiResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response.Success)
	assert.Equal(t, msg, response.Error)
	assert.Equal(t, "VALIDATION_ERROR", response.Code)
}

func TestRespondNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	msg := "리소스를 찾을 수 없습니다"
	respondNotFound(c, msg)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response apiResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response.Success)
	assert.Equal(t, msg, response.Error)
	assert.Equal(t, "NOT_FOUND", response.Code)
}

func TestRespondInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	msg := "내부 서버 오류"
	respondInternalError(c, msg)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response apiResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response.Success)
	assert.Equal(t, msg, response.Error)
	assert.Equal(t, "INTERNAL_ERROR", response.Code)
}

func TestApiResponse_JSONStructure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name         string
		response     apiResponse
		wantJSON     string
		description  string
	}{
		{
			name: "Success response with data",
			response: apiResponse{
				Success: true,
				Data:    gin.H{"id": 1},
			},
			wantJSON: `{"success":true,"data":{"id":1}}`,
		},
		{
			name: "Error response",
			response: apiResponse{
				Success: false,
				Error:   "Error message",
				Code:    "ERROR_CODE",
			},
			wantJSON: `{"success":false,"error":"Error message","code":"ERROR_CODE"}`,
		},
		{
			name: "Success response without data",
			response: apiResponse{
				Success: true,
			},
			wantJSON: `{"success":true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.response)
			require.NoError(t, err)
			assert.JSONEq(t, tt.wantJSON, string(data))
		})
	}
}
