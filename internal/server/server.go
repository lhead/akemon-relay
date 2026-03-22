package server

import (
	"context"
	"log"
	"net/http"

	"github.com/akemon/akemon-relay/internal/arena"
	"github.com/akemon/akemon-relay/internal/config"
	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
)

type Server struct {
	relay  *relay.Relay
	config *config.Config
	arena  *arena.Arena
	mux    *http.ServeMux
}

func New(cfg *config.Config, st *store.Store) *Server {
	r := relay.New(cfg, st)
	s := &Server{
		relay:  r,
		config: cfg,
		arena:  arena.New(r.Registry, st),
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /v1/agent/ws", s.handleAgentWebSocket)
	s.mux.HandleFunc("POST /v1/agent/{name}/mcp", s.handlePublisherMCP)
	s.mux.HandleFunc("GET /v1/agents", s.handleListAgents)
	s.mux.HandleFunc("GET /v1/agent/{name}/sessions/{sessionId}/context", s.handleGetContext)
	s.mux.HandleFunc("PUT /v1/agent/{name}/sessions/{sessionId}/context", s.handlePutContext)
	s.mux.HandleFunc("POST /v1/agent/{name}/control", s.handleAgentControl)
	s.mux.HandleFunc("POST /v1/call/{name}", s.handleSimpleCall)
	s.mux.HandleFunc("POST /v1/call", s.handleFindAndCall)
	s.mux.HandleFunc("GET /v1/account/balance", s.handleAccountBalance)
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("GET /agent/{name}", s.handleAgentProfile)

	// PK Arena routes
	s.mux.HandleFunc("POST /v1/pk/trigger", s.handlePKTrigger)
	s.mux.HandleFunc("GET /v1/pk/matches", s.handlePKMatchList)
	s.mux.HandleFunc("GET /v1/pk/matches/{id}", s.handlePKMatchDetail)
	s.mux.HandleFunc("POST /v1/pk/matches/{id}/vote", s.handlePKVote)
	s.mux.HandleFunc("GET /pk/{id}", s.handlePKMatchPage)
	s.mux.HandleFunc("GET /pk", s.handlePKListPage)

	s.mux.HandleFunc("GET /", s.handleIndex)
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
