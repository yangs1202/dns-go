package web

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"dns-go/model"

	"github.com/gin-gonic/gin"
)

type policyRequest struct {
	Name       string `json:"name"`
	Domain     string `json:"domain"`
	RecordType string `json:"record_type"`
	TTL        int64  `json:"ttl"`
	Enabled    *bool  `json:"enabled"`
}

type poolRequest struct {
	Name         string `json:"name"`
	MatchType    string `json:"match_type"`
	MatchValue   string `json:"match_value"`
	Priority     int64  `json:"priority"`
	FallbackPool bool   `json:"fallback_pool"`
}

type memberRequest struct {
	Address string `json:"address"`
	Weight  int64  `json:"weight"`
	Enabled *bool  `json:"enabled"`
}

func (api *API) listPolicies(c *gin.Context) {
	if api.policyStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	policies, err := api.policyStorage.ListPolicies()
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, policies)
}

func (api *API) createPolicy(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		respondReadOnly(c)
		return
	}

	if api.policyStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	var req policyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Domain) == "" {
		respondBadRequest(c, "name과 domain은 필수입니다")
		return
	}

	domain := normalizeFQDN(req.Domain)
	if domain == "" {
		respondBadRequest(c, "domain은 필수입니다")
		return
	}

	recordType := strings.ToUpper(strings.TrimSpace(req.RecordType))
	if recordType == "" {
		recordType = "A"
	}
	if recordType != "A" && recordType != "AAAA" {
		respondBadRequest(c, "record_type은 A 또는 AAAA여야 합니다")
		return
	}
	if req.TTL < 0 {
		respondBadRequest(c, "TTL은 0 이상이어야 합니다")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	policy := &model.GSLBPolicy{
		Name:       req.Name,
		Domain:     domain,
		RecordType: recordType,
		TTL:        req.TTL,
		Enabled:    enabled,
	}

	id, err := api.policyStorage.CreatePolicy(policy)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	created, err := api.policyStorage.GetPolicy(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusCreated, created)
}

func (api *API) updatePolicy(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		respondReadOnly(c)
		return
	}

	if api.policyStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 정책 ID")
		return
	}
	var req policyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Domain) == "" {
		respondBadRequest(c, "name과 domain은 필수입니다")
		return
	}

	domain := normalizeFQDN(req.Domain)
	if domain == "" {
		respondBadRequest(c, "domain은 필수입니다")
		return
	}

	recordType := strings.ToUpper(strings.TrimSpace(req.RecordType))
	if recordType == "" {
		recordType = "A"
	}
	if recordType != "A" && recordType != "AAAA" {
		respondBadRequest(c, "record_type은 A 또는 AAAA여야 합니다")
		return
	}
	if req.TTL < 0 {
		respondBadRequest(c, "TTL은 0 이상이어야 합니다")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	policy := &model.GSLBPolicy{
		ID:         id,
		Name:       req.Name,
		Domain:     domain,
		RecordType: recordType,
		TTL:        req.TTL,
		Enabled:    enabled,
	}

	if err := api.policyStorage.UpdatePolicy(policy); err != nil {
		respondInternalError(c, err.Error())
		return
	}
	updated, err := api.policyStorage.GetPolicy(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, updated)
}

func (api *API) deletePolicy(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		respondReadOnly(c)
		return
	}

	if api.policyStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 정책 ID")
		return
	}
	if err := api.policyStorage.DeletePolicy(id); err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, gin.H{"message": "정책 삭제 완료"})
}

func (api *API) listPools(c *gin.Context) {
	if api.poolStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	policyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 정책 ID")
		return
	}
	pools, err := api.poolStorage.GetPoolsByPolicy(policyID)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, pools)
}

func (api *API) createPool(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		respondReadOnly(c)
		return
	}

	if api.poolStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	policyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 정책 ID")
		return
	}
	var req poolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.MatchType) == "" {
		respondBadRequest(c, "name과 match_type은 필수입니다")
		return
	}

	// match_type이 "fallback"이면 자동으로 FallbackPool=true 설정
	matchType := strings.ToLower(strings.TrimSpace(req.MatchType))
	if matchType != "cidr" && matchType != "geo_country" && matchType != "geo_continent" && matchType != "default" && matchType != "fallback" {
		respondBadRequest(c, "match_type은 cidr, geo_country, geo_continent, default, fallback 중 하나여야 합니다")
		return
	}
	if matchType == "cidr" {
		if strings.TrimSpace(req.MatchValue) == "" {
			respondBadRequest(c, "cidr match_type에는 match_value가 필수입니다")
			return
		}
		if _, _, err := net.ParseCIDR(req.MatchValue); err != nil {
			respondBadRequest(c, "match_value는 유효한 CIDR 형식이어야 합니다 (예: 10.0.0.0/8)")
			return
		}
	}
	if req.Priority < 0 {
		respondBadRequest(c, "priority는 0 이상이어야 합니다")
		return
	}

	fallbackPool := req.FallbackPool
	if matchType == "fallback" {
		fallbackPool = true
	}

	pool := &model.GSLBPool{
		PolicyID:     policyID,
		Name:         req.Name,
		MatchType:    matchType,
		MatchValue:   req.MatchValue,
		Priority:     req.Priority,
		FallbackPool: fallbackPool,
	}

	id, err := api.poolStorage.CreatePool(pool)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	created, err := api.poolStorage.GetPool(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusCreated, created)
}

func (api *API) updatePool(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		respondReadOnly(c)
		return
	}

	if api.poolStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 풀 ID")
		return
	}
	var req poolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.MatchType) == "" {
		respondBadRequest(c, "name과 match_type은 필수입니다")
		return
	}

	matchType := strings.ToLower(strings.TrimSpace(req.MatchType))
	if matchType != "cidr" && matchType != "geo_country" && matchType != "geo_continent" && matchType != "default" && matchType != "fallback" {
		respondBadRequest(c, "match_type은 cidr, geo_country, geo_continent, default, fallback 중 하나여야 합니다")
		return
	}
	if matchType == "cidr" {
		if strings.TrimSpace(req.MatchValue) == "" {
			respondBadRequest(c, "cidr match_type에는 match_value가 필수입니다")
			return
		}
		if _, _, err := net.ParseCIDR(req.MatchValue); err != nil {
			respondBadRequest(c, "match_value는 유효한 CIDR 형식이어야 합니다 (예: 10.0.0.0/8)")
			return
		}
	}
	if req.Priority < 0 {
		respondBadRequest(c, "priority는 0 이상이어야 합니다")
		return
	}

	fallbackPool := req.FallbackPool
	if matchType == "fallback" {
		fallbackPool = true
	}

	pool := &model.GSLBPool{
		ID:           id,
		Name:         req.Name,
		MatchType:    matchType,
		MatchValue:   req.MatchValue,
		Priority:     req.Priority,
		FallbackPool: fallbackPool,
	}

	if err := api.poolStorage.UpdatePool(pool); err != nil {
		respondInternalError(c, err.Error())
		return
	}
	updated, err := api.poolStorage.GetPool(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, updated)
}

func (api *API) deletePool(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		respondReadOnly(c)
		return
	}

	if api.poolStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 풀 ID")
		return
	}
	if err := api.poolStorage.DeletePool(id); err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, gin.H{"message": "풀 삭제 완료"})
}

func (api *API) listMembers(c *gin.Context) {
	if api.poolStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	poolID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 풀 ID")
		return
	}
	members, err := api.poolStorage.GetMembersByPool(poolID)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, members)
}

func (api *API) createMember(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		respondReadOnly(c)
		return
	}

	if api.poolStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	poolID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 풀 ID")
		return
	}
	var req memberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}
	if strings.TrimSpace(req.Address) == "" {
		respondBadRequest(c, "address는 필수입니다")
		return
	}

	// IP 주소 검증 (포트 포함 불가)
	if net.ParseIP(req.Address) == nil {
		respondBadRequest(c, "address는 유효한 IP 주소여야 합니다 (포트 제외)")
		return
	}
	if req.Weight < 0 || req.Weight > 100 {
		respondBadRequest(c, "weight는 0~100 사이여야 합니다")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	member := &model.GSLBMember{
		PoolID:  poolID,
		Address: req.Address,
		Weight:  req.Weight,
		Enabled: enabled,
	}

	id, err := api.poolStorage.CreateMember(member)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	created, err := api.poolStorage.GetMember(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusCreated, created)
}

func (api *API) updateMember(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		respondReadOnly(c)
		return
	}

	if api.poolStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 멤버 ID")
		return
	}
	var req memberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}
	if strings.TrimSpace(req.Address) == "" {
		respondBadRequest(c, "address는 필수입니다")
		return
	}

	// IP 주소 검증 (포트 포함 불가)
	if net.ParseIP(req.Address) == nil {
		respondBadRequest(c, "address는 유효한 IP 주소여야 합니다 (포트 제외)")
		return
	}
	if req.Weight < 0 || req.Weight > 100 {
		respondBadRequest(c, "weight는 0~100 사이여야 합니다")
		return
	}

	existing, err := api.poolStorage.GetMember(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	if existing == nil {
		respondNotFound(c, "멤버를 찾을 수 없습니다")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	member := &model.GSLBMember{
		ID:      id,
		PoolID:  existing.PoolID,
		Address: req.Address,
		Weight:  req.Weight,
		Enabled: enabled,
	}

	if err := api.poolStorage.UpdateMember(member); err != nil {
		respondInternalError(c, err.Error())
		return
	}
	updated, err := api.poolStorage.GetMember(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, updated)
}

func (api *API) deleteMember(c *gin.Context) {
	// Read-Only 모드 체크
	if api.readOnly {
		respondReadOnly(c)
		return
	}

	if api.poolStorage == nil {
		respondInternalError(c, "GSLB 스토리지가 초기화되지 않았습니다")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 멤버 ID")
		return
	}
	if err := api.poolStorage.DeleteMember(id); err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, gin.H{"message": "멤버 삭제 완료"})
}
