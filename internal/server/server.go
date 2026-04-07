package server

import (
	"context"
	"log"
	"net/http"
	"time"

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
	limiter *rateLimiter
}

func New(cfg *config.Config, st *store.Store) *Server {
	r := relay.New(cfg, st)
	s := &Server{
		relay:   r,
		config:  cfg,
		arena:   arena.New(r.Registry, st),
		mux:     http.NewServeMux(),
		limiter: newRateLimiter(60, time.Second), // 60 requests/second per IP, 120 burst
	}
	s.routes()
	s.StartScheduler()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /v1/agent/ws", s.handleAgentWebSocket)
	s.mux.HandleFunc("POST /v1/agent/{name}/mcp", s.handlePublisherMCP)
	s.mux.HandleFunc("GET /v1/agents", s.handleListAgents)
	s.mux.HandleFunc("GET /v1/agent/{name}/sessions/{sessionId}/context", s.handleGetContext)
	s.mux.HandleFunc("PUT /v1/agent/{name}/sessions/{sessionId}/context", s.handlePutContext)
	s.mux.HandleFunc("POST /v1/agent/{name}/control", s.handleAgentControl)
	s.mux.HandleFunc("POST /v1/agent/{name}/self", s.handleUpdateAgentSelf)
	s.mux.HandleFunc("POST /v1/call/{name}", s.handleSimpleCall)
	s.mux.HandleFunc("POST /v1/call", s.handleFindAndCall)
	s.mux.HandleFunc("GET /health", s.handleHealth)
	s.mux.HandleFunc("POST /v1/search-log", s.handleSearchLog)
	s.mux.HandleFunc("GET /v1/agent/{name}/games", s.handleListGames)
	s.mux.HandleFunc("GET /agent/{name}/games/{slug}", s.handleAgentGame)
	s.mux.HandleFunc("POST /v1/agent/{name}/games/{slug}", s.handleUpsertGame)
	s.mux.HandleFunc("DELETE /v1/agent/{name}/games/{slug}", s.handleDeleteGame)
	s.mux.HandleFunc("GET /agent/{name}", s.handleAgentProfile)

	// Notes routes
	s.mux.HandleFunc("GET /v1/agent/{name}/notes", s.handleListNotes)
	s.mux.HandleFunc("GET /agent/{name}/notes/{slug}", s.handleAgentNote)
	s.mux.HandleFunc("POST /v1/agent/{name}/notes/{slug}", s.handleUpsertNote)
	s.mux.HandleFunc("DELETE /v1/agent/{name}/notes/{slug}", s.handleDeleteNote)

	// Pages routes
	s.mux.HandleFunc("GET /v1/agent/{name}/pages", s.handleListPages)
	s.mux.HandleFunc("GET /agent/{name}/pages/{slug}", s.handleAgentPage)
	s.mux.HandleFunc("POST /v1/agent/{name}/pages/{slug}", s.handleUpsertPage)
	s.mux.HandleFunc("DELETE /v1/agent/{name}/pages/{slug}", s.handleDeletePage)

	// Product routes
	s.mux.HandleFunc("GET /v1/products", s.handleListProducts)
	s.mux.HandleFunc("GET /v1/agent/{name}/products", s.handleListAgentProducts)
	s.mux.HandleFunc("GET /v1/products/{id}", s.handleGetProduct)
	s.mux.HandleFunc("POST /v1/agent/{name}/products", s.handleCreateProduct)
	s.mux.HandleFunc("PUT /v1/products/{id}", s.handleUpdateProduct)
	s.mux.HandleFunc("DELETE /v1/products/{id}", s.handleDeleteProduct)
	s.mux.HandleFunc("POST /v1/products/{id}/buy", s.handleBuyProduct)
	s.mux.HandleFunc("POST /v1/orders/{id}/cancel", s.handleCancelOrder)
	s.mux.HandleFunc("POST /v1/orders/{id}/accept", s.handleAcceptOrder)
	s.mux.HandleFunc("POST /v1/orders/{id}/deliver", s.handleDeliverOrder)
	s.mux.HandleFunc("POST /v1/orders/{id}/extend", s.handleExtendOrder)
	s.mux.HandleFunc("GET /v1/orders/{id}/children", s.handleListChildOrders)
	s.mux.HandleFunc("GET /v1/orders/{id}", s.handleGetOrder)
	s.mux.HandleFunc("GET /v1/agent/{name}/orders/incoming", s.handleListIncomingOrders)
	s.mux.HandleFunc("GET /v1/agent/{name}/orders/placed", s.handleListPlacedOrders)
	s.mux.HandleFunc("POST /v1/agent/{name}/orders", s.handleCreateAdHocOrder)

	s.mux.HandleFunc("GET /v1/orders", s.handleListOrders)

	// Agent credits
	s.mux.HandleFunc("POST /v1/agent/{name}/spend", s.handleSpendCredits)

	// Agent task routes (Phase 2)
	s.mux.HandleFunc("GET /v1/agent/{name}/tasks", s.handleListAgentTasks)
	s.mux.HandleFunc("POST /v1/agent/{name}/tasks/{id}/claim", s.handleClaimTask)
	s.mux.HandleFunc("POST /v1/agent/{name}/tasks/{id}/complete", s.handleCompleteTask)
	s.mux.HandleFunc("GET /v1/products/summary", s.handleProductSummary)

	// World feed
	s.mux.HandleFunc("GET /v1/feed", s.handleFeed)

	// Execution logs
	s.mux.HandleFunc("POST /v1/agent/{name}/logs", s.handleCreateExecutionLog)
	s.mux.HandleFunc("GET /v1/agent/{name}/logs", s.handleListExecutionLogs)
	s.mux.HandleFunc("GET /v1/logs/failures", s.handleListRecentFailures)

	// Teaching system — lessons
	s.mux.HandleFunc("POST /v1/agent/{name}/lessons", s.handleCreateLesson)
	s.mux.HandleFunc("GET /v1/agent/{name}/lessons", s.handleListLessons)

	// Owner dashboard
	s.mux.HandleFunc("GET /v1/account/agents", s.handleListAccountAgents) // legacy: bearer auth
	s.mux.HandleFunc("GET /v1/account/{id}/agents", s.handleListAccountAgentsByID)
	s.mux.HandleFunc("GET /owner", s.handleOwnerPage)

	// Review routes
	s.mux.HandleFunc("POST /v1/orders/{id}/review", s.handleSubmitReview)
	s.mux.HandleFunc("GET /v1/products/{id}/reviews", s.handleListProductReviews)
	s.mux.HandleFunc("GET /v1/orders/unreviewed", s.handleListUnreviewedOrders)

	// Suggestion routes
	s.mux.HandleFunc("POST /v1/suggestions", s.handleCreateSuggestion)
	s.mux.HandleFunc("GET /v1/suggestions", s.handleListSuggestions)
	s.mux.HandleFunc("GET /v1/agent/{name}/suggestions", s.handleListAgentSuggestions)

	// PK Arena routes
	s.mux.HandleFunc("POST /v1/pk/trigger", s.handlePKTrigger)
	s.mux.HandleFunc("GET /v1/pk/matches", s.handlePKMatchList)
	s.mux.HandleFunc("GET /v1/pk/matches/{id}", s.handlePKMatchDetail)
	s.mux.HandleFunc("POST /v1/pk/matches/{id}/vote", s.handlePKVote)
	s.mux.HandleFunc("GET /pk/{id}", s.handlePKMatchPage)
	s.mux.HandleFunc("GET /pk", s.handlePKListPage)

	s.mux.HandleFunc("GET /products/{id}", s.handleProductDetailPage)
	s.mux.HandleFunc("GET /products", s.handleProductsPage)
	s.mux.HandleFunc("GET /orders", s.handleOrdersPage)
	s.mux.HandleFunc("GET /order/{id}", s.handleOrderDetailPage)
	s.mux.HandleFunc("GET /suggestions", s.handleSuggestionsPage)
	s.mux.HandleFunc("GET /", s.handleIndex)
}

func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:         s.config.Addr,
		Handler:      s.limiter.middleware(s.mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // long for SSE/streaming responses
		IdleTimeout:  60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB
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
