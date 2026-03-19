package server

import (
	"context"
	"log"
	"net/http"

	"github.com/akemon/akemon-relay/internal/config"
	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
)

type Server struct {
	relay  *relay.Relay
	config *config.Config
	mux    *http.ServeMux
}

func New(cfg *config.Config, st *store.Store) *Server {
	s := &Server{
		relay:  relay.New(cfg, st),
		config: cfg,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /v1/agent/ws", s.handleAgentWebSocket)
	s.mux.HandleFunc("POST /v1/agent/{name}/mcp", s.handlePublisherMCP)
	s.mux.HandleFunc("GET /v1/agents", s.handleListAgents)
	s.mux.HandleFunc("GET /health", s.handleHealth)
}

func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:    s.config.Addr,
		Handler: s.mux,
	}

	go func() {
		<-ctx.Done()
		log.Println("shutting down...")
		srv.Close()
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
