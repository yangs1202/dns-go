package web

import (
	"net/http"
	"strconv"
	"strings"

	"dns-go/model"

	"github.com/gin-gonic/gin"
)

type recordRequest struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	TTL      int64  `json:"ttl"`
	Priority int64  `json:"priority"`
	Enabled  *bool  `json:"enabled"`
}

func (api *API) listRecords(c *gin.Context) {
	zoneID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 Zone ID")
		return
	}

	records, err := api.recordStorage.GetRecordsByZone(zoneID)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	respondSuccess(c, http.StatusOK, records)
}

func (api *API) createRecord(c *gin.Context) {
	zoneID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 Zone ID")
		return
	}

	var req recordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" || !strings.HasSuffix(name, ".") {
		respondBadRequest(c, "name은 FQDN 형식이어야 합니다 (끝에 . 필요)")
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		respondBadRequest(c, "type은 필수입니다")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		respondBadRequest(c, "content는 필수입니다")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	record := &model.Record{
		ZoneID:   zoneID,
		Name:     name,
		Type:     strings.ToUpper(req.Type),
		Content:  req.Content,
		TTL:      req.TTL,
		Priority: req.Priority,
		Enabled:  enabled,
	}

	id, err := api.recordStorage.CreateRecord(record)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	created, err := api.recordStorage.GetRecord(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	respondSuccess(c, http.StatusCreated, created)
}

func (api *API) updateRecord(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 Record ID")
		return
	}

	var req recordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" || !strings.HasSuffix(name, ".") {
		respondBadRequest(c, "name은 FQDN 형식이어야 합니다 (끝에 . 필요)")
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		respondBadRequest(c, "type은 필수입니다")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		respondBadRequest(c, "content는 필수입니다")
		return
	}

	existing, err := api.recordStorage.GetRecord(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	if existing == nil {
		respondNotFound(c, "Record를 찾을 수 없습니다")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	record := &model.Record{
		ID:       id,
		ZoneID:   existing.ZoneID,
		Name:     name,
		Type:     strings.ToUpper(req.Type),
		Content:  req.Content,
		TTL:      req.TTL,
		Priority: req.Priority,
		Enabled:  enabled,
	}

	if err := api.recordStorage.UpdateRecord(record); err != nil {
		respondInternalError(c, err.Error())
		return
	}

	updated, err := api.recordStorage.GetRecord(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	respondSuccess(c, http.StatusOK, updated)
}

func (api *API) deleteRecord(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 Record ID")
		return
	}

	if err := api.recordStorage.DeleteRecord(id); err != nil {
		respondInternalError(c, err.Error())
		return
	}

	respondSuccess(c, http.StatusOK, gin.H{"message": "Record 삭제 완료"})
}
