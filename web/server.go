package web

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

type Server struct {
	addr   string
	http   *http.Server
	router http.Handler
}

func NewServer(listen string, port int, api *API, syncAPI *SyncAPI, serverInfoAPI *ServerInfoAPI) *Server {
	addr := fmt.Sprintf("%s:%d", listen, port)
	router := NewRouter(api, syncAPI, serverInfoAPI)

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

func (s *Server) StartAsync() (<-chan error, error) {
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", s.addr, err)
	}

	s.addr = listener.Addr().String()
	s.http.Addr = s.addr

	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	return errCh, nil
}

func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.http.Shutdown(ctx)
}

func (s *Server) Addr() string {
	return s.addr
}
