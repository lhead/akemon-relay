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

func (s *Server) handleListRecentFailures(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	logs, err := s.relay.Store.ListRecentFailures(limit)
	if err != nil {
		jsonError(w, "failed to list failures: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if logs == nil {
		logs = []store.ExecutionLog{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (s *Server) handleCreateLesson(w http.ResponseWriter, r *http.Request) {
	targetAgent := r.PathValue("name")

	// Auth: any authenticated agent can create a lesson for another agent
	token := auth.ExtractBearer(r)
	if token == "" {
		jsonError(w, "authorization required", http.StatusUnauthorized)
		return
	}
	// Find the diagnosing agent
	allAgents, err := s.relay.Store.ListAgents()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	var diagnoserName string
	for _, a := range allAgents {
		agent, err := s.relay.Store.GetAgentByName(a.Name)
		if err != nil || agent == nil {
			continue
		}
		if auth.VerifyToken(token, agent.SecretHash) {
			diagnoserName = agent.Name
			break
		}
	}
	if diagnoserName == "" {
		jsonError(w, "invalid token", http.StatusUnauthorized)
		return
	}

	var req struct {
		Topic   string `json:"topic"`
		Content string `json:"content"`
		LogID   string `json:"log_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 50_000)).Decode(&req); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Topic == "" || req.Content == "" {
		jsonError(w, "topic and content required", http.StatusBadRequest)
		return
	}

	l := &store.Lesson{
		ID:          uuid.New().String(),
		AgentName:   targetAgent,
		Topic:       req.Topic,
		Content:     req.Content,
		DiagnosedBy: diagnoserName,
		LogID:       req.LogID,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.relay.Store.CreateLesson(l); err != nil {
		jsonError(w, "failed to create lesson: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "id": l.ID})
}

func (s *Server) handleListLessons(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	lessons, err := s.relay.Store.ListLessons(agentName, limit)
	if err != nil {
		jsonError(w, "failed to list lessons: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if lessons == nil {
		lessons = []store.Lesson{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lessons)
}

