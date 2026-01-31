package web

import (
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dns-go/model"

	"github.com/gin-gonic/gin"
)

type adblockSourceRequest struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
}

type adblockSourceResponse struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	URL          string     `json:"url"`
	Enabled      bool       `json:"enabled"`
	LastSync     *time.Time `json:"last_sync"`     // null 가능
	LastModified *time.Time `json:"last_modified"` // null 가능
	RuleCount    int64      `json:"rule_count"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func toAdblockSourceResponse(s *model.AdblockSource) adblockSourceResponse {
	resp := adblockSourceResponse{
		ID:        s.ID,
		Name:      s.Name,
		URL:       s.URL,
		Enabled:   s.Enabled,
		RuleCount: s.RuleCount,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}

	if s.LastSync.Valid {
		resp.LastSync = &s.LastSync.Time
	}

	if s.LastModified.Valid {
		// HTTP 날짜 형식을 time.Time으로 파싱 (RFC1123)
		// 예: "Sat, 31 Jan 2026 12:12:23 GMT"
		if t, err := time.Parse(time.RFC1123, s.LastModified.String); err == nil {
			resp.LastModified = &t
		}
	}

	return resp
}

func (api *API) listAdblockSources(c *gin.Context) {
	if api.adblockStorage == nil {
		respondInternalError(c, "Adblock 스토리지가 초기화되지 않았습니다")
		return
	}
	sources, err := api.adblockStorage.ListAdblockSources()
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	responses := make([]adblockSourceResponse, len(sources))
	for i := range sources {
		responses[i] = toAdblockSourceResponse(sources[i])
	}
	respondSuccess(c, http.StatusOK, responses)
}

func (api *API) createAdblockSource(c *gin.Context) {
	if api.adblockStorage == nil {
		respondInternalError(c, "Adblock 스토리지가 초기화되지 않았습니다")
		return
	}
	var req adblockSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.URL) == "" {
		respondBadRequest(c, "name과 url은 필수입니다")
		return
	}
	parsedURL, urlErr := url.ParseRequestURI(req.URL)
	if urlErr != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		respondBadRequest(c, "url은 유효한 HTTP/HTTPS URL이어야 합니다")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	source := &model.AdblockSource{
		Name:    req.Name,
		URL:     req.URL,
		Enabled: enabled,
	}

	id, err := api.adblockStorage.CreateAdblockSource(source)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	created, err := api.adblockStorage.GetAdblockSource(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusCreated, toAdblockSourceResponse(created))
}

func (api *API) updateAdblockSource(c *gin.Context) {
	if api.adblockStorage == nil {
		respondInternalError(c, "Adblock 스토리지가 초기화되지 않았습니다")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 source ID")
		return
	}
	var req adblockSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.URL) == "" {
		respondBadRequest(c, "name과 url은 필수입니다")
		return
	}
	parsedURL, urlErr := url.ParseRequestURI(req.URL)
	if urlErr != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		respondBadRequest(c, "url은 유효한 HTTP/HTTPS URL이어야 합니다")
		return
	}
	existing, err := api.adblockStorage.GetAdblockSource(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	if existing == nil {
		respondNotFound(c, "source를 찾을 수 없습니다")
		return
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	existing.Name = req.Name
	existing.URL = req.URL
	existing.Enabled = enabled

	if err := api.adblockStorage.UpdateAdblockSource(existing); err != nil {
		respondInternalError(c, err.Error())
		return
	}
	updated, err := api.adblockStorage.GetAdblockSource(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, toAdblockSourceResponse(updated))
}

func (api *API) deleteAdblockSource(c *gin.Context) {
	if api.adblockStorage == nil {
		respondInternalError(c, "Adblock 스토리지가 초기화되지 않았습니다")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 source ID")
		return
	}
	if err := api.adblockStorage.DeleteAdblockSource(id); err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, gin.H{"message": "source 삭제 완료"})
}

func (api *API) syncAdblockSource(c *gin.Context) {
	if api.adblockSyncer == nil {
		respondInternalError(c, "Adblock syncer가 초기화되지 않았습니다")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 source ID")
		return
	}
	// 비동기로 동기화 실행 (대량 도메인 다운로드로 시간이 오래 걸림)
	go func() {
		if err := api.adblockSyncer.SyncSource(id); err != nil {
			log.Printf("[Adblock] source %d 동기화 실패: %v", id, err)
		}
	}()
	respondSuccess(c, http.StatusAccepted, gin.H{"message": "동기화 시작됨"})
}

func (api *API) getAdblockStats(c *gin.Context) {
	if api.adblockStorage == nil {
		respondInternalError(c, "Adblock 스토리지가 초기화되지 않았습니다")
		return
	}
	limit := 10
	if v := c.Query("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			limit = parsed
		}
	}
	stats, err := api.adblockStorage.GetBlockedStats(limit)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, stats)
}

func (api *API) getAdblockStatus(c *gin.Context) {
	if api.adblockStorage == nil {
		respondInternalError(c, "Adblock 스토리지가 초기화되지 않았습니다")
		return
	}
	count, err := api.adblockStorage.GetBlockedDomainCount()
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	sources, err := api.adblockStorage.ListAdblockSources()
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	latestSync := time.Time{}
	for _, src := range sources {
		if src.LastSync.Valid && src.LastSync.Time.After(latestSync) {
			latestSync = src.LastSync.Time
		}
	}
	respondSuccess(c, http.StatusOK, gin.H{
		"sources":      len(sources),
		"domain_count": count,
		"last_sync":    latestSync,
	})
}
