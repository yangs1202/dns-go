package web

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dns-go/model"

	"github.com/miekg/dns"
	"github.com/gin-gonic/gin"
)

type upstreamRequest struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Protocol string `json:"protocol"`
	Priority int64  `json:"priority"`
	Enabled  *bool  `json:"enabled"`
}

func (api *API) listUpstreams(c *gin.Context) {
	servers, err := api.upstreamStorage.ListUpstreamServers()
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	respondSuccess(c, http.StatusOK, servers)
}

func (api *API) createUpstream(c *gin.Context) {
	var req upstreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}

	name := strings.TrimSpace(req.Name)
	address := strings.TrimSpace(req.Address)
	protocol := strings.ToLower(strings.TrimSpace(req.Protocol))
	if name == "" || address == "" {
		respondBadRequest(c, "name과 address는 필수입니다")
		return
	}
	if protocol == "" {
		protocol = "udp"
	}
	if protocol != "udp" && protocol != "tcp" && protocol != "tcp-tls" {
		respondBadRequest(c, "protocol은 udp, tcp, tcp-tls 중 하나여야 합니다")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	server := &model.UpstreamServer{
		Name:     name,
		Address:  address,
		Protocol: protocol,
		Priority: req.Priority,
		Enabled:  enabled,
	}

	id, err := api.upstreamStorage.CreateUpstreamServer(server)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	created, err := api.upstreamStorage.GetUpstreamServer(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	respondSuccess(c, http.StatusCreated, created)
}

func (api *API) updateUpstream(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 Upstream ID")
		return
	}

	var req upstreamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "요청 바디가 올바르지 않습니다")
		return
	}

	name := strings.TrimSpace(req.Name)
	address := strings.TrimSpace(req.Address)
	protocol := strings.ToLower(strings.TrimSpace(req.Protocol))
	if name == "" || address == "" {
		respondBadRequest(c, "name과 address는 필수입니다")
		return
	}
	if protocol == "" {
		protocol = "udp"
	}
	if protocol != "udp" && protocol != "tcp" && protocol != "tcp-tls" {
		respondBadRequest(c, "protocol은 udp, tcp, tcp-tls 중 하나여야 합니다")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	server := &model.UpstreamServer{
		ID:       id,
		Name:     name,
		Address:  address,
		Protocol: protocol,
		Priority: req.Priority,
		Enabled:  enabled,
	}

	if err := api.upstreamStorage.UpdateUpstreamServer(server); err != nil {
		respondInternalError(c, err.Error())
		return
	}

	updated, err := api.upstreamStorage.GetUpstreamServer(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}

	respondSuccess(c, http.StatusOK, updated)
}

func (api *API) deleteUpstream(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 Upstream ID")
		return
	}

	if err := api.upstreamStorage.DeleteUpstreamServer(id); err != nil {
		respondInternalError(c, err.Error())
		return
	}

	respondSuccess(c, http.StatusOK, gin.H{"message": "Upstream 삭제 완료"})
}

func (api *API) testUpstream(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		respondBadRequest(c, "잘못된 Upstream ID")
		return
	}

	server, err := api.upstreamStorage.GetUpstreamServer(id)
	if err != nil {
		respondInternalError(c, err.Error())
		return
	}
	if server == nil {
		respondNotFound(c, "Upstream 서버를 찾을 수 없습니다")
		return
	}

	start := time.Now()
	resp, err := queryUpstream(server.Address, server.Protocol)
	if err != nil {
		respondError(c, http.StatusBadGateway, err.Error(), "UPSTREAM_ERROR")
		return
	}
	latency := time.Since(start)

	respondSuccess(c, http.StatusOK, gin.H{
		"status":   "ok",
		"latency":  latency.Milliseconds(),
		"rcode":    dns.RcodeToString[resp.Rcode],
		"answers":  len(resp.Answer),
		"protocol": server.Protocol,
	})
}

func queryUpstream(address, protocol string) (*dns.Msg, error) {
	client := &dns.Client{Net: protocol}
	msg := new(dns.Msg)
	msg.SetQuestion("example.com.", dns.TypeA)

	if protocol == "tcp-tls" {
		client.Net = "tcp-tls"
	}

	if protocol == "udp" || protocol == "tcp" || protocol == "tcp-tls" {
		if host, _, err := net.SplitHostPort(address); err == nil {
			_ = host
		}
	}

	resp, _, err := client.Exchange(msg, address)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
