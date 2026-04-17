package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/akemon/akemon-relay/internal/store"
	"github.com/google/uuid"
)

func (s *Server) handleCreateSuggestion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type       string `json:"type"`
		TargetName string `json:"target_name"`
		FromAgent  string `json:"from_agent"`
		Title      string `json:"title"`
		Content    string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.Type != "platform" && body.Type != "agent" {
		jsonError(w, "type must be 'platform' or 'agent'", http.StatusBadRequest)
		return
	}
	if body.Title == "" || body.Content == "" {
		jsonError(w, "title and content required", http.StatusBadRequest)
		return
	}
	if body.FromAgent == "" {
		jsonError(w, "from_agent required", http.StatusBadRequest)
		return
	}
	sg, err := s.relay.Store.CreateSuggestion(uuid.New().String(), body.Type, body.TargetName, body.FromAgent, body.Title, body.Content)
	if err != nil {
		log.Printf("[suggestion] create error: %v", err)
		jsonError(w, "failed to create suggestion", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sg)
}

func (s *Server) handleListSuggestions(w http.ResponseWriter, r *http.Request) {
	sType := r.URL.Query().Get("type")
	target := r.URL.Query().Get("target")
	suggestions, err := s.relay.Store.ListSuggestions(sType, target, 100)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if suggestions == nil {
		suggestions = []store.Suggestion{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(suggestions)
}

func (s *Server) handleListAgentSuggestions(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	suggestions, err := s.relay.Store.ListSuggestions("agent", name, 50)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if suggestions == nil {
		suggestions = []store.Suggestion{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(suggestions)
}

