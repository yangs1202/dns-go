package model

import "time"

// QueryLog는 DNS 요청/응답 로그 항목입니다
type QueryLog struct {
	ID             int64     `json:"id"`
	Timestamp      time.Time `json:"timestamp"`
	ClientIP       string    `json:"client_ip"`
	Domain         string    `json:"domain"`
	QueryType      string    `json:"query_type"`
	ResponseCode   string    `json:"response_code"`
	ResponseSource string    `json:"response_source"` // cache, upstream, zone, gslb, adblock, chaos, error, refused
	LatencyMs      float64   `json:"latency_ms"`
	ResponseData   string    `json:"response_data"`   // 응답 레코드 요약 JSON
	Protocol       string    `json:"protocol"`        // udp, tcp
	ResponseSize   int       `json:"response_size"`
	EDNSPresent    bool      `json:"edns_present"`
	EDNSVersion    int       `json:"edns_version"`
	EDNSBufferSize int       `json:"edns_buffer_size"`
}
