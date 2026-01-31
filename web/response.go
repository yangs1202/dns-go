package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type apiResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Code    string      `json:"code,omitempty"`
}

func respondSuccess(c *gin.Context, status int, data interface{}) {
	c.JSON(status, apiResponse{Success: true, Data: data})
}

func respondError(c *gin.Context, status int, msg string, code string) {
	c.JSON(status, apiResponse{Success: false, Error: msg, Code: code})
}

func respondBadRequest(c *gin.Context, msg string) {
	respondError(c, http.StatusBadRequest, msg, "VALIDATION_ERROR")
}

func respondNotFound(c *gin.Context, msg string) {
	respondError(c, http.StatusNotFound, msg, "NOT_FOUND")
}

func respondInternalError(c *gin.Context, msg string) {
	respondError(c, http.StatusInternalServerError, msg, "INTERNAL_ERROR")
}
