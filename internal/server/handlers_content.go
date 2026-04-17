package server

import (
	"encoding/json"
	"net/http"

	"github.com/akemon/akemon-relay/internal/store"
)

func (s *Server) handleListGames(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	games, err := s.relay.Store.ListGames(agentName)
	if err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	if games == nil {
		games = []store.AgentGame{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(games)
}

func (s *Server) handleUpsertGame(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		HTML        string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Title == "" || req.HTML == "" {
		jsonError(w, "title and html are required", http.StatusBadRequest)
		return
	}

	if err := s.relay.Store.UpsertGame(agentName, slug, req.Title, req.Description, req.HTML); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleDeleteGame(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}
	if err := s.relay.Store.DeleteGame(agentName, slug); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleListNotes(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	notes, err := s.relay.Store.ListNotes(agentName)
	if err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	if notes == nil {
		notes = []store.AgentNote{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(notes)
}

func (s *Server) handleUpsertNote(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}
	var req struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Title == "" || req.Content == "" {
		jsonError(w, "title and content required", http.StatusBadRequest)
		return
	}
	if err := s.relay.Store.UpsertNote(agentName, slug, req.Title, req.Content); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}
	if err := s.relay.Store.DeleteNote(agentName, slug); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleListPages(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	pages, err := s.relay.Store.ListPages(agentName)
	if err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	if pages == nil {
		pages = []store.AgentPage{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pages)
}

func (s *Server) handleUpsertPage(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		HTML        string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Title == "" || req.HTML == "" {
		jsonError(w, "title and html required", http.StatusBadRequest)
		return
	}
	if err := s.relay.Store.UpsertPage(agentName, slug, req.Title, req.Description, req.HTML); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}
	if err := s.relay.Store.DeletePage(agentName, slug); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

