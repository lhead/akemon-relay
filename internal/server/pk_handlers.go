package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/akemon/akemon-relay/internal/arena"
	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/store"
	"github.com/google/uuid"
)

// POST /v1/pk/trigger — trigger a new match (admin only)
func (s *Server) handlePKTrigger(w http.ResponseWriter, r *http.Request) {
	if s.config.AdminSecret == "" {
		jsonError(w, "admin secret not configured", http.StatusServiceUnavailable)
		return
	}
	token := auth.ExtractBearer(r)
	if token != s.config.AdminSecret {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Mode   string `json:"mode"`
		AgentA string `json:"agent_a"`
		AgentB string `json:"agent_b"`
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}

	match, err := s.arena.TriggerMatch(req.Mode, req.AgentA, req.AgentB, req.Prompt)
	if err != nil {
		code := http.StatusInternalServerError
		switch err {
		case arena.ErrNotEnoughAgents:
			code = http.StatusConflict
		case arena.ErrSameAgent:
			code = http.StatusBadRequest
		case arena.ErrAgentNotFound:
			code = http.StatusNotFound
		}
		jsonError(w, err.Error(), code)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(match)
}

// GET /v1/pk/matches — list matches
func (s *Server) handlePKMatchList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit := 20
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	matches, err := s.relay.Store.ListPKMatches(status, limit, offset)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if matches == nil {
		matches = []store.PKMatch{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(matches)
}

// GET /v1/pk/matches/{id} — match detail with rounds and votes
func (s *Server) handlePKMatchDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	match, err := s.relay.Store.GetPKMatch(id)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if match == nil {
		jsonError(w, "match not found", http.StatusNotFound)
		return
	}

	rounds, _ := s.relay.Store.ListPKRounds(id)
	votes, _ := s.relay.Store.GetPKVoteCounts(id)

	resp := map[string]any{
		"match":  match,
		"rounds": rounds,
		"votes":  votes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// POST /v1/pk/matches/{id}/vote — cast a vote
func (s *Server) handlePKVote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	match, err := s.relay.Store.GetPKMatch(id)
	if err != nil || match == nil {
		jsonError(w, "match not found", http.StatusNotFound)
		return
	}

	var req struct {
		VotedFor string `json:"voted_for"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.VotedFor != "a" && req.VotedFor != "b" {
		jsonError(w, "voted_for must be 'a' or 'b'", http.StatusBadRequest)
		return
	}

	ip := clientIP(r)
	voted, _ := s.relay.Store.HasVoted(id, ip)
	if voted {
		jsonError(w, "already voted", http.StatusConflict)
		return
	}

	vote := &store.PKVote{
		ID:       uuid.New().String(),
		MatchID:  id,
		VoterIP:  ip,
		VotedFor: req.VotedFor,
	}
	if err := s.relay.Store.CreatePKVote(vote); err != nil {
		jsonError(w, "failed to record vote", http.StatusInternalServerError)
		return
	}

	counts, _ := s.relay.Store.GetPKVoteCounts(id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(counts)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
