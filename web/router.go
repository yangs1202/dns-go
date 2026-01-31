package web

import (
	"github.com/gin-gonic/gin"
)

func NewRouter(api *API) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(requestLogger())
	router.Use(corsMiddleware())

	apiGroup := router.Group("/api")
	{
		apiGroup.GET("/zones", api.listZones)
		apiGroup.GET("/zones/:id", api.getZone)
		apiGroup.POST("/zones", api.createZone)
		apiGroup.PUT("/zones/:id", api.updateZone)
		apiGroup.DELETE("/zones/:id", api.deleteZone)

		apiGroup.GET("/zones/:id/records", api.listRecords)
		apiGroup.POST("/zones/:id/records", api.createRecord)
		apiGroup.PUT("/records/:id", api.updateRecord)
		apiGroup.DELETE("/records/:id", api.deleteRecord)

		apiGroup.GET("/upstream", api.listUpstreams)
		apiGroup.POST("/upstream", api.createUpstream)
		apiGroup.PUT("/upstream/:id", api.updateUpstream)
		apiGroup.DELETE("/upstream/:id", api.deleteUpstream)
		apiGroup.POST("/upstream/:id/test", api.testUpstream)

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
	}

	return router
}
