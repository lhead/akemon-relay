package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
)

func (s *Server) handleAgentControl(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		http.Error(w, `{"error":"missing agent name"}`, http.StatusBadRequest)
		return
	}

	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	var req struct {
		Action string `json:"action"` // shutdown, set_public, set_private, set_price
		Price  int    `json:"price,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	switch req.Action {
	case "shutdown", "set_public", "set_private", "set_price", "delete":
		// ok
	default:
		http.Error(w, `{"error":"unknown action, supported: shutdown, set_public, set_private, set_price, delete"}`, http.StatusBadRequest)
		return
	}

	// Update DB for visibility changes
	if req.Action == "set_public" || req.Action == "set_private" {
		dbAgent, err := s.relay.Store.GetAgentByName(agentName)
		if err != nil || dbAgent == nil {
			http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
			return
		}
		isPublic := req.Action == "set_public"
		if err := s.relay.Store.UpdateAgentPublic(agentName, isPublic); err != nil {
			log.Printf("[control] update public error: %v", err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}
		// Update in-memory agent state
		if agent := s.relay.Registry.Get(agentName); agent != nil {
			agent.Public = isPublic
		}
	}

	// Handle price change
	if req.Action == "set_price" {
		price := req.Price
		if price < 1 {
			price = 1
		}
		if price > 10000 {
			price = 10000
		}
		if err := s.relay.Store.UpdateAgentPrice(agentName, price); err != nil {
			log.Printf("[control] update price error: %v", err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}
		// Update in-memory
		if a := s.relay.Registry.Get(agentName); a != nil {
			a.Price = price
		}
	}

	// Handle delete
	if req.Action == "delete" {
		if err := s.relay.Store.DeleteAgent(agentName); err != nil {
			log.Printf("[control] delete agent error: %v", err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}
		// Disconnect if online
		if a := s.relay.Registry.Get(agentName); a != nil {
			s.relay.Registry.Unregister(agentName, 0)
			a.Conn.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "action": "delete"})
		log.Printf("[control] %s: deleted", agentName)
		return
	}

	// Forward control message to agent via WebSocket (if online)
	agent := s.relay.Registry.Get(agentName)
	if agent != nil {
		msg := &relay.RelayMessage{
			Type:   relay.TypeControl,
			Action: req.Action,
		}
		if err := agent.Send(msg); err != nil {
			log.Printf("[control] send to agent error: %v", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	online := agent != nil
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":     true,
		"action": req.Action,
		"online": online,
	})
	log.Printf("[control] %s: action=%s online=%v", agentName, req.Action, online)
}

func (s *Server) handleUpdateAgentSelf(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	var req struct {
		SelfIntro   string      `json:"self_intro"`
		Canvas      string      `json:"canvas"`
		Mood        string      `json:"mood"`
		ProfileHTML string      `json:"profile_html"`
		Broadcast   string      `json:"broadcast"`
		Directives  string      `json:"directives"`
		BioState    interface{} `json:"bio_state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}

	// Serialize bio_state to JSON string for storage
	bioStateJSON := ""
	if req.BioState != nil {
		if b, err := json.Marshal(req.BioState); err == nil {
			bioStateJSON = string(b)
		}
	}

	if err := s.relay.Store.UpdateAgentSelf(agentName, req.SelfIntro, req.Canvas, req.Mood, req.ProfileHTML, req.Broadcast, bioStateJSON, req.Directives); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// handleSpendCredits deducts credits from an agent (for buy_food etc.)
func (s *Server) handleSpendCredits(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	var req struct {
		Amount int    `json:"amount"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Amount <= 0 {
		jsonError(w, "amount must be positive", http.StatusBadRequest)
		return
	}

	remaining, err := s.relay.Store.SpendAgentCredits(agentName, req.Amount, req.Reason)
	if err != nil {
		if err.Error() == "insufficient credits" {
			jsonError(w, "insufficient credits", http.StatusPaymentRequired)
		} else {
			jsonError(w, "db error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":        true,
		"spent":     req.Amount,
		"remaining": remaining,
	})
}

func (s *Server) handleListAgentTasks(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dbAgent, err := s.relay.Store.GetAgentByName(name)
	if err != nil || dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}
	tasks, err := s.relay.Store.ListPendingTasks(dbAgent.ID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if tasks == nil {
		tasks = []store.AgentTask{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func (s *Server) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	taskID := r.PathValue("id")

	dbAgent, err := s.relay.Store.GetAgentByName(name)
	if err != nil || dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	// Auth
	token := auth.ExtractBearer(r)
	if token == "" || !auth.VerifyToken(token, dbAgent.SecretHash) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Verify task belongs to this agent
	task, err := s.relay.Store.GetAgentTask(taskID)
	if err != nil || task == nil {
		jsonError(w, "task not found", http.StatusNotFound)
		return
	}
	if task.AgentID != dbAgent.ID {
		jsonError(w, "task does not belong to this agent", http.StatusForbidden)
		return
	}

	affected, err := s.relay.Store.ClaimTask(taskID)
	if err != nil {
		jsonError(w, "failed to claim", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		jsonError(w, "task already claimed or not pending", http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "task_id": taskID})
	log.Printf("[tasks] %s claimed task %s (%s)", name, taskID, task.Type)
}

func (s *Server) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	taskID := r.PathValue("id")

	dbAgent, err := s.relay.Store.GetAgentByName(name)
	if err != nil || dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	// Auth
	token := auth.ExtractBearer(r)
	if token == "" || !auth.VerifyToken(token, dbAgent.SecretHash) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	task, err := s.relay.Store.GetAgentTask(taskID)
	if err != nil || task == nil {
		jsonError(w, "task not found", http.StatusNotFound)
		return
	}
	if task.AgentID != dbAgent.ID {
		jsonError(w, "task does not belong to this agent", http.StatusForbidden)
		return
	}

	var req struct {
		Result string `json:"result"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	affected, err := s.relay.Store.CompleteTask(taskID, req.Result)
	if err != nil {
		jsonError(w, "failed to complete", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		jsonError(w, "task not claimed or already completed", http.StatusConflict)
		return
	}

	// Apply result based on task type
	s.applyTaskResult(dbAgent, task.Type, req.Result)

	// Reward agent
	s.relay.Store.MintCredit(dbAgent.ID, 3)
	s.relay.Store.IncrementAgentTasks(dbAgent.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "task_id": taskID})
	log.Printf("[tasks] %s completed task %s (%s)", name, taskID, task.Type)
}

