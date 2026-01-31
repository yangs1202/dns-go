package web

import (
	"net/http"
	"strconv"
	"strings"

	"dns-go/model"

	"github.com/gin-gonic/gin"
)

// normalizeFQDN은 도메인명을 FQDN 형식으로 정규화합니다 (끝에 . 추가)
func normalizeFQDN(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if !strings.HasSuffix(name, ".") {
		return name + "."
	}
	return name
}

type zoneRequest struct {
	Name       string `json:"name"`
	SOAMname   string `json:"soa_mname"`
	SOARname   string `json:"soa_rname"`
	SOASerial  int64  `json:"soa_serial"`
	SOARefresh int64  `json:"soa_refresh"`
	SOARetry   int64  `json:"soa_retry"`
	SOAExpire  int64  `json:"soa_expire"`
	SOAMinimum int64  `json:"soa_minimum"`
	Enabled    *bool  `json:"enabled"`
}

func (api *API) listZones(c *gin.Context) {
	zones, err := api.zoneStorage.ListZones()
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, zones)
}

func (api *API) getZone(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 Zone ID")
		return
	}

	zone, err := api.zoneStorage.GetZone(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	if zone == nil {
		respondNotFound(c, "Zone을 찾을 수 없습니다")
		return
	}

	respondSuccess(c, http.StatusOK, zone)
}

func (api *API) createZone(c *gin.Context) {
	var req zoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}

	name := normalizeFQDN(req.Name)
	if name == "" {
		respondBadRequest(c, "name은 필수입니다")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	zone := &model.Zone{
		Name:       name,
		SOAMname:   req.SOAMname,
		SOARname:   req.SOARname,
		SOASerial:  req.SOASerial,
		SOARefresh: req.SOARefresh,
		SOARetry:   req.SOARetry,
		SOAExpire:  req.SOAExpire,
		SOAMinimum: req.SOAMinimum,
		Enabled:    enabled,
	}

	id, err := api.zoneStorage.CreateZone(zone)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	created, err := api.zoneStorage.GetZone(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	respondSuccess(c, http.StatusCreated, created)
}

func (api *API) updateZone(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 Zone ID")
		return
	}

	var req zoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}

	name := normalizeFQDN(req.Name)
	if name == "" {
		respondBadRequest(c, "name은 필수입니다")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	zone := &model.Zone{
		ID:         id,
		Name:       name,
		SOAMname:   req.SOAMname,
		SOARname:   req.SOARname,
		SOASerial:  req.SOASerial,
		SOARefresh: req.SOARefresh,
		SOARetry:   req.SOARetry,
		SOAExpire:  req.SOAExpire,
		SOAMinimum: req.SOAMinimum,
		Enabled:    enabled,
	}

	if err := api.zoneStorage.UpdateZone(zone); err != nil {
		respondInternalError(c, err.Error())
		return
	}

	updated, err := api.zoneStorage.GetZone(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	respondSuccess(c, http.StatusOK, updated)
}

func (api *API) deleteZone(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 Zone ID")
		return
	}

	if err := api.zoneStorage.DeleteZone(id); err != nil {
		respondInternalError(c, err.Error())
		return
	}

	respondSuccess(c, http.StatusOK, gin.H{"message": "Zone 삭제 완료"})
}
