package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/store"
	"github.com/google/uuid"
)

func (s *Server) handleCreateExecutionLog(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")

	// Auth: must be the agent itself
	agent, err := s.relay.Store.GetAgentByName(agentName)
	if err != nil || agent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}
	token := auth.ExtractBearer(r)
	if token == "" || !auth.VerifyToken(token, agent.SecretHash) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Type  string `json:"type"`
		RefID string `json:"ref_id"`
		Status string `json:"status"`
		Error string `json:"error"`
		Trace string `json:"trace"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 100_000)).Decode(&req); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Type == "" || req.Status == "" {
		jsonError(w, "type and status required", http.StatusBadRequest)
		return
	}

	l := &store.ExecutionLog{
		ID:        uuid.New().String(),
		AgentID:   agent.ID,
		AgentName: agentName,
		Type:      req.Type,
		RefID:     req.RefID,
		Status:    req.Status,
		Error:     req.Error,
		Trace:     req.Trace,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.relay.Store.CreateExecutionLog(l); err != nil {
		jsonError(w, "failed to create log: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "id": l.ID})
}

func (s *Server) handleListExecutionLogs(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	status := r.URL.Query().Get("status")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	logs, err := s.relay.Store.ListExecutionLogs(agentName, status, limit)
	if err != nil {
		jsonError(w, "failed to list logs: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if logs == nil {
		logs = []store.ExecutionLog{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

