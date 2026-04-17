package server

import (
	"encoding/json"
	"net/http"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/store"
)

func (s *Server) handleListAccountAgents(w http.ResponseWriter, r *http.Request) {
	token := auth.ExtractBearer(r)
	if token == "" {
		jsonError(w, "authorization required", http.StatusUnauthorized)
		return
	}

	// Find which agent this token belongs to, then get account_id
	allAgents, err := s.relay.Store.ListAgents()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	var accountID string
	for _, a := range allAgents {
		agent, err := s.relay.Store.GetAgentByName(a.Name)
		if err != nil || agent == nil {
			continue
		}
		if auth.VerifyToken(token, agent.SecretHash) {
			accountID = agent.AccountID
			break
		}
	}
	if accountID == "" {
		jsonError(w, "invalid token", http.StatusUnauthorized)
		return
	}

	agents, err := s.relay.Store.ListAgentsByAccount(accountID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Build response with online status
	type ownerAgent struct {
		store.AgentListing
		Status string `json:"status"`
	}
	out := make([]ownerAgent, len(agents))
	for i, a := range agents {
		status := "offline"
		if s.relay.Registry.Get(a.Name) != nil {
			status = "online"
		}
		out[i] = ownerAgent{AgentListing: a, Status: status}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"account_id": accountID,
		"agents":     out,
	})
}

func (s *Server) handleListAccountAgentsByID(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("id")
	if accountID == "" {
		jsonError(w, "account id required", http.StatusBadRequest)
		return
	}

	agents, err := s.relay.Store.ListAgentsByAccount(accountID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	type ownerAgent struct {
		store.AgentListing
		Status string `json:"status"`
	}
	out := make([]ownerAgent, len(agents))
	for i, a := range agents {
		status := "offline"
		if s.relay.Registry.Get(a.Name) != nil {
			status = "online"
		}
		out[i] = ownerAgent{AgentListing: a, Status: status}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"account_id": accountID,
		"agents":     out,
	})
}

