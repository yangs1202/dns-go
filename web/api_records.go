package web

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dns-go/model"

	"github.com/gin-gonic/gin"
)

// validRecordTypes는 허용되는 DNS 레코드 타입 목록
var validRecordTypes = map[string]bool{
	"A": true, "AAAA": true, "CNAME": true, "MX": true,
	"TXT": true, "NS": true, "SRV": true, "PTR": true, "CAA": true,
}

// validateRecordType은 레코드 타입이 유효한지 검증합니다
func validateRecordType(t string) bool {
	return validRecordTypes[strings.ToUpper(t)]
}

// validateRecordContent는 레코드 타입에 따라 content 값을 검증합니다
func validateRecordContent(recordType, content string) string {
	switch strings.ToUpper(recordType) {
	case "A":
		ip := net.ParseIP(content)
		if ip == nil || ip.To4() == nil {
			return "A 레코드의 content는 유효한 IPv4 주소여야 합니다"
		}
	case "AAAA":
		ip := net.ParseIP(content)
		if ip == nil || ip.To4() != nil {
			return "AAAA 레코드의 content는 유효한 IPv6 주소여야 합니다"
		}
	case "CNAME", "NS", "PTR":
		// 도메인명 형식 기본 검증
		name := strings.TrimSuffix(content, ".")
		if name == "" || strings.Contains(name, " ") {
			return recordType + " 레코드의 content는 유효한 도메인명이어야 합니다"
		}
	case "MX":
		// MX는 도메인명 형식
		name := strings.TrimSuffix(content, ".")
		if name == "" || strings.Contains(name, " ") {
			return "MX 레코드의 content는 유효한 도메인명이어야 합니다"
		}
	}
	return ""
}

type recordRequest struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Content  string `json:"content"`
	TTL      int64  `json:"ttl"`
	Priority int64  `json:"priority"`
	Enabled  *bool  `json:"enabled"`
}

// recordResponse는 API 응답용 Record 구조체 (마침표 제거)
type recordResponse struct {
	ID          int64         `json:"id"`
	ZoneID      int64         `json:"zone_id"`
	Zone        *zoneResponse `json:"zone,omitempty"` // Zone 정보 추가
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Content     string        `json:"content"`
	TTL         int64         `json:"ttl"`
	Priority    int64         `json:"priority"`
	Enabled     bool          `json:"enabled"`
	LastQueryAt *time.Time    `json:"last_query_at"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// toRecordResponse는 model.Record를 recordResponse로 변환합니다
func toRecordResponse(r *model.Record, zone *model.Zone) recordResponse {
	resp := recordResponse{
		ID:          r.ID,
		ZoneID:      r.ZoneID,
		Name:        removeFQDNDot(r.Name),
		Type:        r.Type,
		Content:     r.Content,
		TTL:         r.TTL,
		Priority:    r.Priority,
		Enabled:     r.Enabled,
		LastQueryAt: r.LastQueryAt,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}

	if zone != nil {
		zr := toZoneResponse(zone)
		resp.Zone = &zr
	}

	return resp
}

func (api *API) listAllRecords(c *gin.Context) {
	records, err := api.recordStorage.ListAllRecords()
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	// Zone 정보를 캐시하기 위한 맵
	zoneCache := make(map[int64]*model.Zone)

	responses := make([]recordResponse, len(records))
	for i := range records {
		// Zone 정보가 캐시에 없으면 조회
		zone, exists := zoneCache[records[i].ZoneID]
		if !exists {
			zone, err = api.zoneStorage.GetZone(records[i].ZoneID)
			if err != nil {
				// Zone 조회 실패 시에도 Record는 반환
				zone = nil
			}
			zoneCache[records[i].ZoneID] = zone
		}
		responses[i] = toRecordResponse(records[i], zone)
	}
	respondSuccess(c, http.StatusOK, responses)
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

	// 모든 레코드가 같은 Zone이므로 한 번만 조회
	var zone *model.Zone
	if len(records) > 0 {
		zone, err = api.zoneStorage.GetZone(zoneID)
		if err != nil {
			// Zone 조회 실패 시에도 Record는 반환
			zone = nil
		}
	}

	responses := make([]recordResponse, len(records))
	for i := range records {
		responses[i] = toRecordResponse(records[i], zone)
	}
	respondSuccess(c, http.StatusOK, responses)
}

func (api *API) createRecord(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Read-Only mode (Secondary server)",
		})
		return
	}

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

	// Zone 존재 여부 확인
	zone, err := api.zoneStorage.GetZone(zoneID)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	if zone == nil {
		respondNotFound(c, "Zone을 찾을 수 없습니다")
		return
	}

	name := normalizeFQDN(req.Name)
	if name == "" {
		respondBadRequest(c, "name은 필수입니다")
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		respondBadRequest(c, "type은 필수입니다")
		return
	}
	if !validateRecordType(req.Type) {
		respondBadRequest(c, "type은 A, AAAA, CNAME, MX, TXT, NS, SRV, PTR, CAA 중 하나여야 합니다")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		respondBadRequest(c, "content는 필수입니다")
		return
	}
	if msg := validateRecordContent(req.Type, req.Content); msg != "" {
		respondBadRequest(c, msg)
		return
	}
	if req.TTL < 0 {
		respondBadRequest(c, "TTL은 0 이상이어야 합니다")
		return
	}
	if req.Priority < 0 {
		respondBadRequest(c, "priority는 0 이상이어야 합니다")
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

	respondSuccess(c, http.StatusCreated, toRecordResponse(created, zone))
}

func (api *API) updateRecord(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Read-Only mode (Secondary server)",
		})
		return
	}

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

	name := normalizeFQDN(req.Name)
	if name == "" {
		respondBadRequest(c, "name은 필수입니다")
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		respondBadRequest(c, "type은 필수입니다")
		return
	}
	if !validateRecordType(req.Type) {
		respondBadRequest(c, "type은 A, AAAA, CNAME, MX, TXT, NS, SRV, PTR, CAA 중 하나여야 합니다")
		return
	}
	if strings.TrimSpace(req.Content) == "" {
		respondBadRequest(c, "content는 필수입니다")
		return
	}
	if msg := validateRecordContent(req.Type, req.Content); msg != "" {
		respondBadRequest(c, msg)
		return
	}
	if req.TTL < 0 {
		respondBadRequest(c, "TTL은 0 이상이어야 합니다")
		return
	}
	if req.Priority < 0 {
		respondBadRequest(c, "priority는 0 이상이어야 합니다")
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

	zone, err := api.zoneStorage.GetZone(updated.ZoneID)
	if err != nil {
		zone = nil
	}

	respondSuccess(c, http.StatusOK, toRecordResponse(updated, zone))
}

func (api *API) deleteRecord(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "Read-Only mode (Secondary server)",
		})
		return
	}

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
