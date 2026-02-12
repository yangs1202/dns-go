package web

import (
	"dns-go/storage"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// SyncAPI는 Primary/Secondary 동기화 API를 제공합니다
type SyncAPI struct {
	syncVersion *storage.SyncVersion
}

// NewSyncAPI는 SyncAPI 인스턴스를 생성합니다
func NewSyncAPI(syncVersion *storage.SyncVersion) *SyncAPI {
	return &SyncAPI{
		syncVersion: syncVersion,
	}
}

// GetMetadata는 Primary의 현재 상태를 조회합니다
// GET /api/sync/metadata
func (a *SyncAPI) GetMetadata(c *gin.Context) {
	state, err := a.syncVersion.GetSyncState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "동기화 상태 조회 실패: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"version":  state["version"],
		"checksum": state["checksum"],
	})
}

// GetFull는 전체 데이터를 Export합니다
// GET /api/sync/full
func (a *SyncAPI) GetFull(c *gin.Context) {
	version, err := a.syncVersion.GetVersion()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "버전 조회 실패: " + err.Error(),
		})
		return
	}

	checksum, err := a.syncVersion.CalculateChecksum()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "체크섬 계산 실패: " + err.Error(),
		})
		return
	}

	zones, err := a.syncVersion.GetAllZones()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Zone 조회 실패: " + err.Error(),
		})
		return
	}

	records, err := a.syncVersion.GetAllRecords()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Record 조회 실패: " + err.Error(),
		})
		return
	}

	upstreams, err := a.syncVersion.GetAllUpstreams()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Upstream 조회 실패: " + err.Error(),
		})
		return
	}

	gslbPolicies, err := a.syncVersion.GetAllGSLBPolicies()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "GSLB Policy 조회 실패: " + err.Error(),
		})
		return
	}

	gslbPools, err := a.syncVersion.GetAllGSLBPools()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "GSLB Pool 조회 실패: " + err.Error(),
		})
		return
	}

	gslbMembers, err := a.syncVersion.GetAllGSLBMembers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "GSLB Member 조회 실패: " + err.Error(),
		})
		return
	}

	healthChecks, err := a.syncVersion.GetAllHealthChecks()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Health Check 조회 실패: " + err.Error(),
		})
		return
	}

	adblockSources, err := a.syncVersion.GetAllAdblockSources()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Adblock Source 조회 실패: " + err.Error(),
		})
		return
	}

	adblockDomains, err := a.syncVersion.GetAllAdblockDomains()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Adblock Domain 조회 실패: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"version":  version,
		"checksum": checksum,
		"data": gin.H{
			"zones":            zones,
			"records":          records,
			"upstream_servers": upstreams,
			"gslb_policies":    gslbPolicies,
			"gslb_pools":       gslbPools,
			"gslb_members":     gslbMembers,
			"health_checks":    healthChecks,
			"adblock_sources":  adblockSources,
			"adblock_domains":  adblockDomains,
		},
	})
}

// GetChanges는 변경사항을 조회합니다 (간단 구현: Full Sync 데이터 반환)
// GET /api/sync/changes?since_version=X
func (a *SyncAPI) GetChanges(c *gin.Context) {
	sinceVersionStr := c.Query("since_version")
	sinceVersion, _ := strconv.ParseInt(sinceVersionStr, 10, 64)

	currentVersion, err := a.syncVersion.GetVersion()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "버전 조회 실패: " + err.Error(),
		})
		return
	}

	// 버전이 같으면 변경 없음
	if sinceVersion >= currentVersion {
		c.JSON(http.StatusOK, gin.H{
			"current_version": currentVersion,
			"has_changes":     false,
		})
		return
	}

	// 변경 있음 - GetFull과 동일한 응답
	c.JSON(http.StatusOK, gin.H{
		"current_version": currentVersion,
		"has_changes":     true,
	})
}
