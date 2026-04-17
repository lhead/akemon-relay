package server

import (
	"io"
	"log"
	"net/http"
)

func (s *Server) handleGetContext(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	sessionID := r.PathValue("sessionId")
	if agentName == "" || sessionID == "" {
		http.Error(w, `{"error":"missing parameters"}`, http.StatusBadRequest)
		return
	}

	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	ctx, err := s.relay.Store.GetContext(agentName, sessionID)
	if err != nil {
		log.Printf("[context] GET error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(ctx))
}

func (s *Server) handlePutContext(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	sessionID := r.PathValue("sessionId")
	if agentName == "" || sessionID == "" {
		http.Error(w, `{"error":"missing parameters"}`, http.StatusBadRequest)
		return
	}

	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8192+1))
	if err != nil {
		http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
		return
	}
	if len(body) > 8192 {
		http.Error(w, `{"error":"context too large (max 8KB)"}`, http.StatusRequestEntityTooLarge)
		return
	}

	if err := s.relay.Store.PutContext(agentName, sessionID, string(body)); err != nil {
		log.Printf("[context] PUT error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

