package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/akemon/akemon-relay/internal/store"
)

// handleGetAccountMetrics returns live metrics + 24h stats for all agents in an account.
// Auth: account ID in path (same as handleListAccountAgentsByID — no secret required for
// the owner dashboard page).
func (s *Server) handleGetAccountMetrics(w http.ResponseWriter, r *http.Request) {
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

	type agentMetricsEntry struct {
		Name             string          `json:"name"`
		Online           bool            `json:"online"`
		Metrics          json.RawMessage `json:"metrics,omitempty"`
		MetricsUpdatedAt *time.Time      `json:"metrics_updated_at,omitempty"`
		Completed24h     int             `json:"completed_24h"`
		Failed24h        int             `json:"failed_24h"`
	}

	out := make([]agentMetricsEntry, 0, len(agents))
	for _, a := range agents {
		entry := agentMetricsEntry{Name: a.Name}

		if conn := s.relay.Registry.Get(a.Name); conn != nil {
			entry.Online = true
			raw, ts := conn.GetMetrics()
			if len(raw) > 0 {
				entry.Metrics = raw
				t := ts
				entry.MetricsUpdatedAt = &t
			}
		}

		// Enrich with 24h order counts from DB (non-fatal if query fails)
		dbAgent, _ := s.relay.Store.GetAgentByName(a.Name)
		if dbAgent != nil {
			entry.Completed24h, _ = s.relay.Store.CountOrdersByStatus24h(dbAgent.ID, "completed")
			entry.Failed24h, _ = s.relay.Store.CountOrdersByStatus24h(dbAgent.ID, "failed")
		}

		out = append(out, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"account_id": accountID, "agents": out})
}

// handleListAgentFailureEvents returns failure events for a specific agent in the last 24h.
// Auth: agent secret (owner-only, for programmatic access).
func (s *Server) handleListAgentFailureEvents(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		jsonError(w, "missing agent name", http.StatusBadRequest)
		return
	}
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}
	events, err := s.relay.Store.ListFailureEvents24h(agentName)
	if err != nil {
		jsonError(w, "internal error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []store.FailureEvent{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

// handleGetAccountFailureEvents returns all failure events across an account's agents (last 24h).
// Auth: account ID in path (same as owner dashboard — no secret required).
func (s *Server) handleGetAccountFailureEvents(w http.ResponseWriter, r *http.Request) {
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

	var all []store.FailureEvent
	for _, a := range agents {
		evts, err := s.relay.Store.ListFailureEvents24h(a.Name)
		if err != nil {
			continue
		}
		all = append(all, evts...)
	}
	// Sort newest-first across all agents
	for i := 0; i < len(all)-1; i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].CreatedAt > all[i].CreatedAt {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	if all == nil {
		all = []store.FailureEvent{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(all)
}
