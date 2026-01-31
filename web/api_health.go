package web

import (
	"net/http"
	"strconv"

	"dns-go/model"

	"github.com/gin-gonic/gin"
)

type healthCheckRequest struct {
	CheckType          string `json:"check_type"`
	Target             string `json:"target"`
	IntervalSec        int64  `json:"interval_sec"`
	TimeoutSec         int64  `json:"timeout_sec"`
	HealthyThreshold   int64  `json:"healthy_threshold"`
	UnhealthyThreshold int64  `json:"unhealthy_threshold"`
	Enabled            *bool  `json:"enabled"`
}

func (api *API) getHealthStatus(c *gin.Context) {
	if api.healthStatus == nil {
		respondInternalError(c, "health status가 초기화되지 않았습니다")
		return
	}

	status := make(map[int64]interface{})
	api.healthStatus.Range(func(key, value interface{}) bool {
		if id, ok := key.(int64); ok {
			status[id] = value
		}
		return true
	})

	respondSuccess(c, http.StatusOK, status)
}

func (api *API) listHealthChecks(c *gin.Context) {
	if api.healthCheckStorage == nil {
		respondInternalError(c, "health storage가 초기화되지 않았습니다")
		return
	}
	checks, err := api.healthCheckStorage.ListHealthChecks()
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, checks)
}

func (api *API) createHealthCheck(c *gin.Context) {
	if api.healthCheckStorage == nil {
		respondInternalError(c, "health storage가 초기화되지 않았습니다")
		return
	}
	policyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 정책 ID")
		return
	}

	var req healthCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	check := &model.HealthCheck{
		PolicyID:           policyID,
		CheckType:          req.CheckType,
		Target:             req.Target,
		IntervalSec:        req.IntervalSec,
		TimeoutSec:         req.TimeoutSec,
		HealthyThreshold:   req.HealthyThreshold,
		UnhealthyThreshold: req.UnhealthyThreshold,
		Enabled:            enabled,
	}

	id, err := api.healthCheckStorage.CreateHealthCheck(check)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	created, err := api.healthCheckStorage.GetHealthCheck(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	// 워커에 새 헬스체크 추가
	if api.healthWorker != nil {
		api.healthWorker.AddCheck(created)
	}

	respondSuccess(c, http.StatusCreated, created)
}

func (api *API) updateHealthCheck(c *gin.Context) {
	if api.healthCheckStorage == nil {
		respondInternalError(c, "health storage가 초기화되지 않았습니다")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 헬스체크 ID")
		return
	}
	var req healthCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}
	existing, err := api.healthCheckStorage.GetHealthCheck(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	if existing == nil {
		respondNotFound(c, "헬스체크를 찾을 수 없습니다")
		return
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	check := &model.HealthCheck{
		ID:                 id,
		PolicyID:           existing.PolicyID,
		CheckType:          req.CheckType,
		Target:             req.Target,
		IntervalSec:        req.IntervalSec,
		TimeoutSec:         req.TimeoutSec,
		HealthyThreshold:   req.HealthyThreshold,
		UnhealthyThreshold: req.UnhealthyThreshold,
		Enabled:            enabled,
	}

	if err := api.healthCheckStorage.UpdateHealthCheck(check); err != nil {
		respondInternalError(c, err.Error())
		return
	}
	updated, err := api.healthCheckStorage.GetHealthCheck(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	// 워커에 헬스체크 업데이트
	if api.healthWorker != nil {
		api.healthWorker.UpdateCheck(updated)
	}

	respondSuccess(c, http.StatusOK, updated)
}

func (api *API) deleteHealthCheck(c *gin.Context) {
	if api.healthCheckStorage == nil {
		respondInternalError(c, "health storage가 초기화되지 않았습니다")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 헬스체크 ID")
		return
	}

	// 워커에서 헬스체크 제거
	if api.healthWorker != nil {
		api.healthWorker.RemoveCheck(id)
	}

	if err := api.healthCheckStorage.DeleteHealthCheck(id); err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, gin.H{"message": "헬스체크 삭제 완료"})
}
