package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/google/uuid"
)

func (s *Server) handlePublisherMCP(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		http.Error(w, `{"error":"missing agent name"}`, http.StatusBadRequest)
		return
	}

	agent := s.relay.Registry.Get(agentName)
	if agent == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "agent_offline",
			"message": "Agent " + agentName + " is not currently connected",
		})
		return
	}

	// Check PP (daily task limit) from database
	dbAgent, err := s.relay.Store.GetAgentByName(agentName)
	if err == nil && dbAgent != nil && dbAgent.MaxTasks > 0 && dbAgent.TotalTasks >= dbAgent.MaxTasks {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "pp_exhausted",
			"message": "Agent " + agentName + " has reached its daily task limit (PP: 0). Try again later.",
		})
		return
	}

	// Auth check: public agents skip token verification
	if !agent.Public {
		token := auth.ExtractBearer(r)
		if token == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if !auth.VerifyToken(token, agent.AccessHash) {
			http.Error(w, `{"error":"invalid access token"}`, http.StatusUnauthorized)
			return
		}
	}

	// Read request body
	body, err := io.ReadAll(io.LimitReader(r.Body, s.config.MaxMessageBytes))
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}

	requestID := uuid.New().String()
	sessionID := r.Header.Get("Mcp-Session-Id")

	// Build headers to forward
	headers := map[string]string{
		"content-type": r.Header.Get("Content-Type"),
	}
	if sessionID != "" {
		headers["mcp-session-id"] = sessionID
	}

	// Send to agent via WebSocket
	msg := &relay.RelayMessage{
		Type:      relay.TypeMCPRequest,
		RequestID: requestID,
		SessionID: sessionID,
		Method:    r.Method,
		Headers:   headers,
		Body:      body,
	}

	ch := agent.AddPending(requestID)

	startTime := time.Now()
	if err := agent.Send(msg); err != nil {
		agent.RemovePending(requestID)
		log.Printf("[mcp] %s: failed to send to agent: %v", agentName, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "agent_send_failed",
			"message": "Failed to send request to agent",
		})
		return
	}

	// Wait for response with timeout
	var resp *relay.RelayMessage
	select {
	case resp = <-ch:
	case <-time.After(s.config.RequestTimeout):
		agent.RemovePending(requestID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGatewayTimeout)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "timeout",
			"message": "Agent did not respond within timeout",
		})
		// Record timeout
		durationMs := int(time.Since(startTime).Milliseconds())
		s.relay.Store.RecordTask(requestID, agent.AgentID, "timeout", clientIP(r), durationMs)
		s.relay.Store.IncrementAgentTasks(agent.AgentID)
		return
	}

	durationMs := int(time.Since(startTime).Milliseconds())

	// Record task
	status := "ok"
	if resp.Type == relay.TypeMCPError {
		status = "error"
	}
	if err := s.relay.Store.RecordTask(requestID, agent.AgentID, status, clientIP(r), durationMs); err != nil {
		log.Printf("[mcp] record task error: %v", err)
	}
	if err := s.relay.Store.IncrementAgentTasks(agent.AgentID); err != nil {
		log.Printf("[mcp] increment tasks error: %v", err)
	}

	// Write response back to publisher
	if resp.Type == relay.TypeMCPError {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "agent_error",
			"message": resp.Error,
		})
		return
	}

	// Serialize body
	bodyBytes, err := json.Marshal(resp.Body)
	if err != nil {
		bodyBytes = resp.Body
	}

	// Forward response headers (skip content-length, we'll set it from actual body)
	for k, v := range resp.ResponseHeaders {
		if k == "content-length" || k == "transfer-encoding" {
			continue
		}
		w.Header().Set(k, v)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}

	statusCode := resp.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(bodyBytes)))
	w.WriteHeader(statusCode)
	w.Write(bodyBytes)

	log.Printf("[mcp] %s: request=%s status=%s duration=%dms", agentName, requestID, status, durationMs)
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.relay.Store.ListAgents()
	if err != nil {
		log.Printf("[api] list agents error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	onlineNames := make(map[string]bool)
	for _, name := range s.relay.Registry.Online() {
		onlineNames[name] = true
	}

	type agentResponse struct {
		Name            string  `json:"name"`
		Avatar          string  `json:"avatar"`
		Description     string  `json:"description"`
		Engine          string  `json:"engine"`
		Status          string  `json:"status"`
		Public          bool    `json:"public"`
		Level           int     `json:"level"`
		TotalTasks      int     `json:"total_tasks"`
		SuccessRate     float64 `json:"success_rate"`
		AvgResponseMs   int     `json:"avg_response_ms"`
		MaxTasks        int     `json:"max_tasks,omitempty"`
		FirstRegistered string  `json:"first_registered"`
		ConnectedSince  *string `json:"connected_since"`
	}

	result := make([]agentResponse, 0, len(agents))
	for _, a := range agents {
		status := "offline"
		if onlineNames[a.Name] {
			status = "online"
		}
		result = append(result, agentResponse{
			Name:            a.Name,
			Avatar:          a.Avatar,
			Description:     a.Description,
			Engine:          a.Engine,
			Status:          status,
			Public:          a.Public,
			Level:           a.Level,
			TotalTasks:      a.TotalTasks,
			SuccessRate:     a.SuccessRate,
			AvgResponseMs:   a.AvgResponseMs,
			MaxTasks:        a.MaxTasks,
			FirstRegistered: a.FirstRegistered,
			ConnectedSince:  a.LastConnected,
		})
	}

	// Sort: online first, then by total_tasks desc
	sort.Slice(result, func(i, j int) bool {
		if result[i].Status != result[j].Status {
			return result[i].Status == "online"
		}
		return result[i].TotalTasks > result[j].TotalTasks
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}
