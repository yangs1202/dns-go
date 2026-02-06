package web

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	_ "dns-go/metrics" // init()으로 메트릭 등록
)

func NewRouter(api *API, syncAPI *SyncAPI, serverInfoAPI *ServerInfoAPI) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger())
	router.Use(corsMiddleware())

	// Prometheus 메트릭
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// 테스트 페이지
	router.GET("/test/", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.File("web/test.html")
	})

	apiGroup := router.Group("/api")
	{
		// 서버 정보
		if serverInfoAPI != nil {
			apiGroup.GET("/server/info", serverInfoAPI.GetServerInfo)
		}

		apiGroup.GET("/zones", api.listZones)
		apiGroup.GET("/zones/:id", api.getZone)
		apiGroup.POST("/zones", api.createZone)
		apiGroup.PUT("/zones/:id", api.updateZone)
		apiGroup.DELETE("/zones/:id", api.deleteZone)

		apiGroup.GET("/records", api.listAllRecords)
		apiGroup.GET("/zones/:id/records", api.listRecords)
		apiGroup.POST("/zones/:id/records", api.createRecord)
		apiGroup.PUT("/records/:id", api.updateRecord)
		apiGroup.DELETE("/records/:id", api.deleteRecord)

		apiGroup.GET("/upstreams", api.listUpstreams)
		apiGroup.POST("/upstreams", api.createUpstream)
		apiGroup.PUT("/upstreams/:id", api.updateUpstream)
		apiGroup.DELETE("/upstreams/:id", api.deleteUpstream)
		apiGroup.POST("/upstreams/:id/test", api.testUpstream)

		apiGroup.GET("/cache/settings", api.getCacheSettings)
		apiGroup.PUT("/cache/settings", api.updateCacheSettings)
		apiGroup.POST("/cache/clear", api.clearCache)
		apiGroup.POST("/cache/clear/:domain", api.clearCacheDomain)
		apiGroup.GET("/cache/stats", api.getCacheStats)

		apiGroup.GET("/stats", api.getStats)

		apiGroup.GET("/gslb/policies", api.listPolicies)
		apiGroup.POST("/gslb/policies", api.createPolicy)
		apiGroup.PUT("/gslb/policies/:id", api.updatePolicy)
		apiGroup.DELETE("/gslb/policies/:id", api.deletePolicy)
		apiGroup.GET("/gslb/policies/:id/pools", api.listPools)
		apiGroup.POST("/gslb/policies/:id/pools", api.createPool)
		apiGroup.PUT("/gslb/pools/:id", api.updatePool)
		apiGroup.DELETE("/gslb/pools/:id", api.deletePool)
		apiGroup.GET("/gslb/pools/:id/members", api.listMembers)
		apiGroup.POST("/gslb/pools/:id/members", api.createMember)
		apiGroup.PUT("/gslb/members/:id", api.updateMember)
		apiGroup.DELETE("/gslb/members/:id", api.deleteMember)
		apiGroup.GET("/gslb/health", api.getHealthStatus)
		apiGroup.GET("/gslb/healthchecks", api.listHealthChecks)
		apiGroup.POST("/gslb/policies/:id/healthcheck", api.createHealthCheck)
		apiGroup.PUT("/gslb/healthchecks/:id", api.updateHealthCheck)
		apiGroup.DELETE("/gslb/healthchecks/:id", api.deleteHealthCheck)

		apiGroup.GET("/adblock/sources", api.listAdblockSources)
		apiGroup.POST("/adblock/sources", api.createAdblockSource)
		apiGroup.PUT("/adblock/sources/:id", api.updateAdblockSource)
		apiGroup.DELETE("/adblock/sources/:id", api.deleteAdblockSource)
		apiGroup.POST("/adblock/sources/:id/sync", api.syncAdblockSource)
		apiGroup.GET("/adblock/stats", api.getAdblockStats)
		apiGroup.GET("/adblock/status", api.getAdblockStatus)

		// Sync API (Primary만)
		if syncAPI != nil {
			apiGroup.GET("/sync/metadata", syncAPI.GetMetadata)
			apiGroup.GET("/sync/full", syncAPI.GetFull)
			apiGroup.GET("/sync/changes", syncAPI.GetChanges)
		}
	}

	return router
}
