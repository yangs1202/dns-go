package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (api *API) getStats(c *gin.Context) {
	if api.queryStats == nil || api.dnsHandler == nil {
		respondInternalError(c, "통계가 초기화되지 않았습니다")
		return
	}

	snapshot := api.queryStats.Snapshot()
	cacheStats := api.dnsHandler.GetCache().GetStats()

	data := gin.H{
		"queries": gin.H{
			"total":         snapshot.Total,
			"l1_hits":        snapshot.L1Hits,
			"l1_misses":      snapshot.L1Misses,
			"upstream_hits":  snapshot.UpstreamHits,
		},
		"cache": cacheStats,
	}

	respondSuccess(c, http.StatusOK, data)
}
