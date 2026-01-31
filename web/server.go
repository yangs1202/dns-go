package web

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type Server struct {
	addr   string
	http   *http.Server
	router http.Handler
}

func NewServer(listen string, port int, api *API, syncAPI *SyncAPI) *Server {
	addr := fmt.Sprintf("%s:%d", listen, port)
	router := NewRouter(api, syncAPI)

	return &Server{
		addr:   addr,
		router: router,
		http: &http.Server{
			Addr:              addr,
			Handler:           router,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

func (s *Server) Start() error {
	return s.http.ListenAndServe()
}

func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.http.Shutdown(ctx)
}

func (s *Server) Addr() string {
	return s.addr
}
