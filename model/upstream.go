package model

import "time"

// UpstreamServer는 업스트림 리졸버 서버를 나타냅니다
type UpstreamServer struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`     // "Google DNS", "Cloudflare DNS"
	Address   string    `json:"address"`  // "8.8.8.8:53", "1.1.1.1:53"
	Protocol  string    `json:"protocol"` // "udp", "tcp", "tcp-tls"
	Priority  int64     `json:"priority"` // 낮을수록 우선순위 높음
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
