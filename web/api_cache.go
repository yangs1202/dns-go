package web

import (
	"net/http"

	"dns-go/model"

	"github.com/gin-gonic/gin"
)

type cacheSettingsRequest struct {
	Enabled         *bool   `json:"enabled"`
	MaxSize         *int64  `json:"max_size"`
	DefaultTTL      *int64  `json:"default_ttl"`
	MinTTL          *int64  `json:"min_ttl"`
	MaxTTL          *int64  `json:"max_ttl"`
	NegativeTTL     *int64  `json:"negative_ttl"`
	PrefetchTrigger *float64 `json:"prefetch_trigger"`
}

func (api *API) getCacheSettings(c *gin.Context) {
	settings, err := api.db.GetCacheSettings()
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, settings)
}

func (api *API) updateCacheSettings(c *gin.Context) {
	var req cacheSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}

	if req.MaxSize != nil && *req.MaxSize <= 0 {
		respondBadRequest(c, "max_size는 1 이상이어야 합니다")
		return
	}
	if req.DefaultTTL != nil && *req.DefaultTTL < 0 {
		respondBadRequest(c, "default_ttl은 0 이상이어야 합니다")
		return
	}
	if req.MinTTL != nil && *req.MinTTL < 0 {
		respondBadRequest(c, "min_ttl은 0 이상이어야 합니다")
		return
	}
	if req.MaxTTL != nil && *req.MaxTTL < 0 {
		respondBadRequest(c, "max_ttl은 0 이상이어야 합니다")
		return
	}
	if req.NegativeTTL != nil && *req.NegativeTTL < 0 {
		respondBadRequest(c, "negative_ttl은 0 이상이어야 합니다")
		return
	}
	if req.PrefetchTrigger != nil && (*req.PrefetchTrigger < 0 || *req.PrefetchTrigger > 1) {
		respondBadRequest(c, "prefetch_trigger는 0.0~1.0 사이여야 합니다")
		return
	}

	current, err := api.db.GetCacheSettings()
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	updatedSettings := toCacheSettings(req, current)
	if err := api.db.UpdateCacheSettings(updatedSettings); err != nil {
		respondBadRequest(c, err.Error())
		return
	}

	updated, err := api.db.GetCacheSettings()
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	if api.dnsHandler != nil {
		api.dnsHandler.ReconfigureCache(updated)
	}

	respondSuccess(c, http.StatusOK, updated)
}

func (api *API) clearCache(c *gin.Context) {
	if api.dnsHandler == nil {
		respondInternalError(c, "DNS 핸들러가 초기화되지 않았습니다")
		return
	}

	api.dnsHandler.GetCache().Clear()
	respondSuccess(c, http.StatusOK, gin.H{"message": "캐시 전체 초기화 완료"})
}

func (api *API) clearCacheDomain(c *gin.Context) {
	if api.dnsHandler == nil {
		respondInternalError(c, "DNS 핸들러가 초기화되지 않았습니다")
		return
	}

	domain := c.Param("domain")
	if domain == "" {
		respondBadRequest(c, "domain이 필요합니다")
		return
	}

	api.dnsHandler.GetCache().Delete(domain)
	respondSuccess(c, http.StatusOK, gin.H{"message": "도메인 캐시 무효화 완료"})
}

func (api *API) getCacheStats(c *gin.Context) {
	if api.dnsHandler == nil {
		respondInternalError(c, "DNS 핸들러가 초기화되지 않았습니다")
		return
	}

	stats := api.dnsHandler.GetCache().GetStats()
	respondSuccess(c, http.StatusOK, stats)
}

func toCacheSettings(req cacheSettingsRequest, current *model.CacheSettings) *model.CacheSettings {
	settings := *current
	if req.Enabled != nil {
		settings.Enabled = *req.Enabled
	}
	if req.MaxSize != nil {
		settings.MaxSize = *req.MaxSize
	}
	if req.DefaultTTL != nil {
		settings.DefaultTTL = *req.DefaultTTL
	}
	if req.MinTTL != nil {
		settings.MinTTL = *req.MinTTL
	}
	if req.MaxTTL != nil {
		settings.MaxTTL = *req.MaxTTL
	}
	if req.NegativeTTL != nil {
		settings.NegativeTTL = *req.NegativeTTL
	}
	if req.PrefetchTrigger != nil {
		settings.PrefetchTrigger = *req.PrefetchTrigger
	}
	return &settings
}
