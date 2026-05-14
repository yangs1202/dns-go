package web

import (
	"dns-go/storage"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

func (api *API) getQueryLogs(c *gin.Context) {
	if api.queryLogStorage == nil {
		respondError(c, http.StatusServiceUnavailable, "쿼리 로그가 비활성화되어 있습니다", "QUERY_LOG_DISABLED")
		return
	}

	filter := storage.QueryLogFilter{
		Domain:         c.Query("domain"),
		ClientIP:       c.Query("client_ip"),
		QueryType:      c.Query("query_type"),
		ResponseCode:   c.Query("response_code"),
		ResponseSource: c.Query("response_source"),
		Page:           parseIntParam(c, "page", 1),
		PageSize:       parseIntParam(c, "page_size", 50),
	}
	if filter.PageSize > 200 {
		filter.PageSize = 200
	}

	if start := c.Query("start_time"); start != "" {
		if t, err := time.Parse(time.RFC3339, start); err == nil {
			filter.StartTime = &t
		}
	}
	if end := c.Query("end_time"); end != "" {
		if t, err := time.Parse(time.RFC3339, end); err == nil {
			filter.EndTime = &t
		}
	}

	logs, total, err := api.queryLogStorage.Query(filter)
	if err != nil {
		respondInternalError(c, "쿼리 로그 조회 실패")
		return
	}

	totalPages := (total + int64(filter.PageSize) - 1) / int64(filter.PageSize)
	if totalPages < 0 {
		totalPages = 0
	}

	respondSuccess(c, http.StatusOK, gin.H{
		"logs":        logs,
		"total":       total,
		"page":        filter.Page,
		"page_size":   filter.PageSize,
		"total_pages": totalPages,
	})
}

func parseIntParam(c *gin.Context, key string, defaultVal int) int {
	if v := c.Query(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultVal
}
