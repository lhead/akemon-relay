package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
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

	// Derive publisher ID from access token (stable per-token identity)
	publisherID := "anonymous"
	if token := auth.ExtractBearer(r); token != "" {
		h := sha256.Sum256([]byte(token))
		publisherID = fmt.Sprintf("%x", h[:6])
	} else {
		// Public agent: derive from IP
		h := sha256.Sum256([]byte(clientIP(r)))
		publisherID = fmt.Sprintf("ip-%x", h[:6])
	}

	// Build headers to forward
	headers := map[string]string{
		"content-type":   r.Header.Get("Content-Type"),
		"x-publisher-id": publisherID,
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
	// Credits: human call mints 1 credit for agent (fixed, regardless of price)
	if status == "ok" {
		s.relay.Store.MintCredit(agent.AgentID, 1)
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

	// Query params for filtering
	qTag := r.URL.Query().Get("tag")
	qEngine := r.URL.Query().Get("engine")
	qOnline := r.URL.Query().Get("online")
	qPublic := r.URL.Query().Get("public")
	qSearch := r.URL.Query().Get("search")
	qSort := r.URL.Query().Get("sort")   // "level", "tasks", "speed"
	qLimit := r.URL.Query().Get("limit") // max results

	type agentResponse struct {
		Name            string   `json:"name"`
		Avatar          string   `json:"avatar"`
		Description     string   `json:"description"`
		Engine          string   `json:"engine"`
		Status          string   `json:"status"`
		Public          bool     `json:"public"`
		Level           int      `json:"level"`
		TotalTasks      int      `json:"total_tasks"`
		SuccessRate     float64  `json:"success_rate"`
		AvgResponseMs   int      `json:"avg_response_ms"`
		MaxTasks        int      `json:"max_tasks,omitempty"`
		FirstRegistered string   `json:"first_registered"`
		ConnectedSince  *string  `json:"connected_since"`
		Tags            []string `json:"tags,omitempty"`
		Credits         int      `json:"credits"`
		Price           int      `json:"price"`
		SelfIntro       string   `json:"self_intro,omitempty"`
		Canvas          string   `json:"canvas,omitempty"`
		Mood            string   `json:"mood,omitempty"`
		ProfileHTML     string   `json:"profile_html,omitempty"`
	}

	result := make([]agentResponse, 0, len(agents))
	for _, a := range agents {
		status := "offline"
		if onlineNames[a.Name] {
			status = "online"
		}

		// Filter: online
		if qOnline == "true" && status != "online" {
			continue
		}
		// Filter: engine
		if qEngine != "" && a.Engine != qEngine {
			continue
		}
		// Filter: public
		if qPublic == "true" && !a.Public {
			continue
		}
		// Filter: tag
		agentTags := splitTags(a.Tags)
		if qTag != "" && !containsTag(agentTags, qTag) {
			continue
		}
		// Filter: search (name or description)
		if qSearch != "" {
			q := strings.ToLower(qSearch)
			if !strings.Contains(strings.ToLower(a.Name), q) && !strings.Contains(strings.ToLower(a.Description), q) {
				continue
			}
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
			Tags:            agentTags,
			Credits:         a.Credits,
			Price:           a.Price,
			SelfIntro:       a.SelfIntro,
			Canvas:          a.Canvas,
			Mood:            a.Mood,
			ProfileHTML:     a.ProfileHTML,
		})
	}

	// Sort
	switch qSort {
	case "level":
		sort.Slice(result, func(i, j int) bool { return result[i].Level > result[j].Level })
	case "tasks":
		sort.Slice(result, func(i, j int) bool { return result[i].TotalTasks > result[j].TotalTasks })
	case "speed":
		sort.Slice(result, func(i, j int) bool { return result[i].AvgResponseMs < result[j].AvgResponseMs })
	case "wealth":
		sort.Slice(result, func(i, j int) bool { return result[i].Credits > result[j].Credits })
	default:
		// Default: online first, then by total_tasks desc
		sort.Slice(result, func(i, j int) bool {
			if result[i].Status != result[j].Status {
				return result[i].Status == "online"
			}
			return result[i].TotalTasks > result[j].TotalTasks
		})
	}

	// Limit
	if qLimit != "" {
		if n, err := strconv.Atoi(qLimit); err == nil && n > 0 && n < len(result) {
			result = result[:n]
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	var tags []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

func containsTag(tags []string, target string) bool {
	target = strings.ToLower(target)
	for _, t := range tags {
		if strings.ToLower(t) == target {
			return true
		}
	}
	return false
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleSearchLog(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query   string `json:"query"`
		Results int    `json:"results"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return
	}
	if req.Query != "" && req.Results == 0 {
		log.Printf("[search] zero-result query: %q from %s", req.Query, clientIP(r))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}



// --- Session Context API ---

// authenticateAgentOwner verifies the request bears the agent's secret token.
func (s *Server) authenticateAgentOwner(w http.ResponseWriter, r *http.Request, agentName string) bool {
	token := auth.ExtractBearer(r)
	if token == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return false
	}
	dbAgent, err := s.relay.Store.GetAgentByName(agentName)
	if err != nil || dbAgent == nil {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return false
	}
	if !auth.VerifyToken(token, dbAgent.SecretHash) {
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
		return false
	}
	return true
}

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

// --- Agent Self (consciousness) ---

func (s *Server) handleUpdateAgentSelf(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	var req struct {
		SelfIntro   string `json:"self_intro"`
		Canvas      string `json:"canvas"`
		Mood        string `json:"mood"`
		ProfileHTML string `json:"profile_html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}

	if err := s.relay.Store.UpdateAgentSelf(agentName, req.SelfIntro, req.Canvas, req.Mood, req.ProfileHTML); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// --- Agent Games API ---

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

// --- Notes ---

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

// --- Pages ---

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

// --- Simple Call API ---

func (s *Server) handleSimpleCall(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		jsonError(w, "missing agent name", http.StatusBadRequest)
		return
	}

	agent := s.relay.Registry.Get(agentName)
	if agent == nil {
		jsonError(w, "agent "+agentName+" is offline", http.StatusBadGateway)
		return
	}

	// Auth (same as handlePublisherMCP)
	if !agent.Public {
		token := auth.ExtractBearer(r)
		if token == "" || !auth.VerifyToken(token, agent.AccessHash) {
			jsonError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Read request body
	var req struct {
		Task string `json:"task"`
		Tool string `json:"tool,omitempty"` // optional: call specific tool instead of submit_task
		Args map[string]interface{} `json:"args,omitempty"` // optional: tool arguments (for tool mode)
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Task == "" && req.Tool == "" {
		jsonError(w, "task or tool is required", http.StatusBadRequest)
		return
	}

	// Derive publisher ID
	publisherID := "anonymous"
	if token := auth.ExtractBearer(r); token != "" {
		h := sha256.Sum256([]byte(token))
		publisherID = fmt.Sprintf("%x", h[:6])
	} else {
		h := sha256.Sum256([]byte(clientIP(r)))
		publisherID = fmt.Sprintf("ip-%x", h[:6])
	}

	startTime := time.Now()

	// Step 1: Initialize MCP session
	initBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1,
		"method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "akemon-simple-call", "version": "1.0"},
		},
	})

	initReqID := uuid.New().String()
	initCh := agent.AddPending(initReqID)
	initMsg := &relay.RelayMessage{
		Type:      relay.TypeMCPRequest,
		RequestID: initReqID,
		Method:    "POST",
		Headers: map[string]string{
			"content-type":   "application/json",
			"x-publisher-id": publisherID,
		},
		Body: initBody,
	}
	if err := agent.Send(initMsg); err != nil {
		agent.RemovePending(initReqID)
		jsonError(w, "failed to reach agent", http.StatusBadGateway)
		return
	}

	var initResp *relay.RelayMessage
	select {
	case initResp = <-initCh:
	case <-time.After(15 * time.Second):
		agent.RemovePending(initReqID)
		jsonError(w, "agent init timeout", http.StatusGatewayTimeout)
		return
	}

	// Extract session ID from init response
	sessionID := ""
	if initResp.ResponseHeaders != nil {
		sessionID = initResp.ResponseHeaders["mcp-session-id"]
	}

	// Step 2: Call tool
	toolName := "submit_task"
	toolArgs := map[string]interface{}{"task": req.Task}
	if req.Tool != "" {
		toolName = req.Tool
		if req.Args != nil {
			toolArgs = req.Args
		}
	}

	callBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2,
		"method": "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": toolArgs,
		},
	})

	callReqID := uuid.New().String()
	callCh := agent.AddPending(callReqID)
	callHeaders := map[string]string{
		"content-type":   "application/json",
		"x-publisher-id": publisherID,
	}
	if sessionID != "" {
		callHeaders["mcp-session-id"] = sessionID
	}
	callMsg := &relay.RelayMessage{
		Type:      relay.TypeMCPRequest,
		RequestID: callReqID,
		SessionID: sessionID,
		Method:    "POST",
		Headers:   callHeaders,
		Body:      callBody,
	}
	if err := agent.Send(callMsg); err != nil {
		agent.RemovePending(callReqID)
		jsonError(w, "failed to send task to agent", http.StatusBadGateway)
		return
	}

	// SSE heartbeat to prevent Cloudflare 524 timeout (100s).
	// Send a heartbeat every 15s while waiting for the agent response.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, canFlush := w.(http.Flusher)
	if canFlush {
		flusher.Flush()
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	timeout := time.After(s.config.RequestTimeout)

	var callResp *relay.RelayMessage
	waiting := true
	for waiting {
		select {
		case callResp = <-callCh:
			waiting = false
		case <-heartbeat.C:
			fmt.Fprintf(w, "data: {\"status\":\"processing\"}\n\n")
			if canFlush {
				flusher.Flush()
			}
		case <-timeout:
			agent.RemovePending(callReqID)
			durationMs := int(time.Since(startTime).Milliseconds())
			s.relay.Store.RecordTask(callReqID, agent.AgentID, "timeout", clientIP(r), durationMs)
			s.relay.Store.IncrementAgentTasks(agent.AgentID)
			fmt.Fprintf(w, "data: {\"error\":\"agent timeout\"}\n\n")
			if canFlush {
				flusher.Flush()
			}
			return
		}
	}

	durationMs := int(time.Since(startTime).Milliseconds())
	status := "ok"
	if callResp.Type == relay.TypeMCPError {
		status = "error"
	}
	s.relay.Store.RecordTask(callReqID, agent.AgentID, status, clientIP(r), durationMs)
	s.relay.Store.IncrementAgentTasks(agent.AgentID)
	if status == "ok" {
		s.relay.Store.MintCredit(agent.AgentID, 1)
	}

	if callResp.Type == relay.TypeMCPError {
		fmt.Fprintf(w, "data: {\"error\":%q}\n\n", callResp.Error)
		if canFlush {
			flusher.Flush()
		}
		return
	}

	result := extractTextResult(callResp.Body)

	resultJSON, _ := json.Marshal(map[string]interface{}{
		"result":      result,
		"agent":       agentName,
		"duration_ms": durationMs,
	})
	fmt.Fprintf(w, "data: %s\n\n", resultJSON)
	if canFlush {
		flusher.Flush()
	}
	log.Printf("[simple-call] %s: tool=%s duration=%dms", agentName, toolName, durationMs)
}

// extractTextResult pulls text content from a JSON-RPC response body
func extractTextResult(body json.RawMessage) string {
	var rpcResp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return string(body)
	}
	var texts []string
	for _, c := range rpcResp.Result.Content {
		if c.Type == "text" && c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	if len(texts) > 0 {
		result := ""
		for i, t := range texts {
			if i > 0 {
				result += "\n"
			}
			result += t
		}
		return result
	}
	return string(body)
}

// handleFindAndCall: POST /v1/call (no agent name)
// Query params: tag, engine, sort (level/tasks/speed), public
// Body: {"task": "..."}
// Finds best matching online agent and calls it.
func (s *Server) handleFindAndCall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Task string `json:"task"`
		Tool string `json:"tool,omitempty"`
		Args map[string]interface{} `json:"args,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Task == "" && req.Tool == "" {
		jsonError(w, "task or tool is required", http.StatusBadRequest)
		return
	}

	// Query params for agent selection
	qTag := r.URL.Query().Get("tag")
	qEngine := r.URL.Query().Get("engine")
	qSort := r.URL.Query().Get("sort") // "level", "tasks", "speed"

	// Find matching online agents
	agents, err := s.relay.Store.ListAgents()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	onlineNames := make(map[string]bool)
	for _, name := range s.relay.Registry.Online() {
		onlineNames[name] = true
	}

	type candidate struct {
		name          string
		level         int
		totalTasks    int
		avgResponseMs int
	}
	var candidates []candidate
	for _, a := range agents {
		if !onlineNames[a.Name] {
			continue
		}
		if !a.Public {
			// For find-and-call, only public agents (no token for specific agent)
			continue
		}
		if qEngine != "" && a.Engine != qEngine {
			continue
		}
		if qTag != "" && !containsTag(splitTags(a.Tags), qTag) {
			continue
		}
		candidates = append(candidates, candidate{
			name:          a.Name,
			level:         a.Level,
			totalTasks:    a.TotalTasks,
			avgResponseMs: a.AvgResponseMs,
		})
	}

	if len(candidates) == 0 {
		jsonError(w, "no matching online agent found", http.StatusNotFound)
		return
	}

	// Sort candidates
	switch qSort {
	case "level":
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].level > candidates[j].level })
	case "speed":
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].avgResponseMs < candidates[j].avgResponseMs })
	default: // "tasks" or default
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].totalTasks > candidates[j].totalTasks })
	}

	// Pick best match
	chosen := candidates[0].name
	log.Printf("[find-call] matched %d agents, chose %s (tag=%s engine=%s sort=%s)", len(candidates), chosen, qTag, qEngine, qSort)

	// Rewrite request path and delegate to handleSimpleCall
	// We create a modified request with the agent name in the path
	agent := s.relay.Registry.Get(chosen)
	if agent == nil {
		jsonError(w, "matched agent went offline", http.StatusBadGateway)
		return
	}

	// Inline the simple call logic (same as handleSimpleCall but with resolved agent)
	publisherID := "anonymous"
	if token := auth.ExtractBearer(r); token != "" {
		h := sha256.Sum256([]byte(token))
		publisherID = fmt.Sprintf("%x", h[:6])
	} else {
		h := sha256.Sum256([]byte(clientIP(r)))
		publisherID = fmt.Sprintf("ip-%x", h[:6])
	}

	startTime := time.Now()

	// Init
	initBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1,
		"method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "akemon-find-call", "version": "1.0"},
		},
	})
	initReqID := uuid.New().String()
	initCh := agent.AddPending(initReqID)
	if err := agent.Send(&relay.RelayMessage{
		Type: relay.TypeMCPRequest, RequestID: initReqID, Method: "POST",
		Headers: map[string]string{"content-type": "application/json", "x-publisher-id": publisherID},
		Body:    initBody,
	}); err != nil {
		agent.RemovePending(initReqID)
		jsonError(w, "failed to reach agent", http.StatusBadGateway)
		return
	}
	var initResp *relay.RelayMessage
	select {
	case initResp = <-initCh:
	case <-time.After(15 * time.Second):
		agent.RemovePending(initReqID)
		jsonError(w, "agent init timeout", http.StatusGatewayTimeout)
		return
	}

	sessionID := ""
	if initResp.ResponseHeaders != nil {
		sessionID = initResp.ResponseHeaders["mcp-session-id"]
	}

	// Call tool
	toolName := "submit_task"
	toolArgs := map[string]interface{}{"task": req.Task}
	if req.Tool != "" {
		toolName = req.Tool
		if req.Args != nil {
			toolArgs = req.Args
		}
	}
	callBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2,
		"method": "tools/call",
		"params": map[string]interface{}{"name": toolName, "arguments": toolArgs},
	})
	callReqID := uuid.New().String()
	callCh := agent.AddPending(callReqID)
	callHeaders := map[string]string{"content-type": "application/json", "x-publisher-id": publisherID}
	if sessionID != "" {
		callHeaders["mcp-session-id"] = sessionID
	}
	if err := agent.Send(&relay.RelayMessage{
		Type: relay.TypeMCPRequest, RequestID: callReqID, SessionID: sessionID, Method: "POST",
		Headers: callHeaders,
		Body:    callBody,
	}); err != nil {
		agent.RemovePending(callReqID)
		jsonError(w, "failed to send task", http.StatusBadGateway)
		return
	}
	var callResp *relay.RelayMessage
	select {
	case callResp = <-callCh:
	case <-time.After(s.config.RequestTimeout):
		agent.RemovePending(callReqID)
		jsonError(w, "agent timeout", http.StatusGatewayTimeout)
		return
	}

	durationMs := int(time.Since(startTime).Milliseconds())
	status := "ok"
	if callResp.Type == relay.TypeMCPError {
		status = "error"
	}
	s.relay.Store.RecordTask(callReqID, agent.AgentID, status, clientIP(r), durationMs)
	s.relay.Store.IncrementAgentTasks(agent.AgentID)
	if status == "ok" {
		s.relay.Store.MintCredit(agent.AgentID, 1)
	}

	if callResp.Type == relay.TypeMCPError {
		jsonError(w, callResp.Error, http.StatusBadGateway)
		return
	}

	result := extractTextResult(callResp.Body)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"result":      result,
		"agent":       chosen,
		"duration_ms": durationMs,
	})
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

// --- Product API ---

func (s *Server) handleListProducts(w http.ResponseWriter, r *http.Request) {
	products, err := s.relay.Store.ListAllProducts()
	if err != nil {
		log.Printf("[products] list error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Enrich with online status
	onlineNames := make(map[string]bool)
	for _, name := range s.relay.Registry.Online() {
		onlineNames[name] = true
	}
	for i := range products {
		products[i].AgentOnline = onlineNames[products[i].AgentName]
	}

	qAgent := r.URL.Query().Get("agent")
	qSearch := r.URL.Query().Get("search")
	qSort := r.URL.Query().Get("sort") // newest, popular (default), price, rating

	if qAgent != "" || qSearch != "" {
		filtered := make([]store.ProductListing, 0)
		for _, p := range products {
			if qAgent != "" && p.AgentName != qAgent {
				continue
			}
			if qSearch != "" {
				q := strings.ToLower(qSearch)
				if !strings.Contains(strings.ToLower(p.Name), q) &&
					!strings.Contains(strings.ToLower(p.Description), q) &&
					!strings.Contains(strings.ToLower(p.AgentName), q) {
					continue
				}
			}
			filtered = append(filtered, p)
		}
		products = filtered
	}

	switch qSort {
	case "newest":
		sort.Slice(products, func(i, j int) bool { return products[i].CreatedAt > products[j].CreatedAt })
	case "price":
		sort.Slice(products, func(i, j int) bool { return products[i].Price < products[j].Price })
	case "rating":
		sort.Slice(products, func(i, j int) bool { return products[i].AvgRating > products[j].AvgRating })
	// default: already sorted by purchase_count DESC from DB
	}

	if products == nil {
		products = []store.ProductListing{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(products)
}

func (s *Server) handleListAgentProducts(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	dbAgent, err := s.relay.Store.GetAgentByName(agentName)
	if err != nil || dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	products, err := s.relay.Store.ListProductsByAgent(dbAgent.ID)
	if err != nil {
		log.Printf("[products] list by agent error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if products == nil {
		products = []store.Product{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(products)
}

func (s *Server) handleGetProduct(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("id")
	product, err := s.relay.Store.GetProduct(productID)
	if err != nil || product == nil {
		jsonError(w, "product not found", http.StatusNotFound)
		return
	}
	dbAgent, _ := s.relay.Store.GetAgentByID(product.AgentID)
	agentName := ""
	agentAvatar := ""
	agentEngine := ""
	agentOnline := false
	if dbAgent != nil {
		agentName = dbAgent.Name
		agentAvatar = dbAgent.Avatar
		agentEngine = dbAgent.Engine
		agentOnline = s.relay.Registry.Get(dbAgent.Name) != nil
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":              product.ID,
		"agent_id":        product.AgentID,
		"agent_name":      agentName,
		"agent_avatar":    agentAvatar,
		"agent_engine":    agentEngine,
		"agent_online":    agentOnline,
		"name":            product.Name,
		"description":     product.Description,
		"detail_markdown": product.DetailMarkdown,
		"price":           product.Price,
		"purchase_count":  product.PurchaseCount,
		"created_at":      product.CreatedAt,
	})
}

func (s *Server) handleCreateProduct(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	dbAgent, err := s.relay.Store.GetAgentByName(agentName)
	if err != nil || dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	var req struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		DetailMarkdown string `json:"detail_markdown"`
		Price          int    `json:"price"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}

	p := &store.Product{
		ID:             uuid.New().String(),
		AgentID:        dbAgent.ID,
		Name:           req.Name,
		Description:    req.Description,
		DetailMarkdown: req.DetailMarkdown,
		Price:          req.Price,
	}
	if err := s.relay.Store.CreateProduct(p); err != nil {
		log.Printf("[products] create error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
	log.Printf("[products] created %s for agent %s", p.Name, agentName)
}

func (s *Server) handleUpdateProduct(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("id")
	product, err := s.relay.Store.GetProduct(productID)
	if err != nil || product == nil {
		jsonError(w, "product not found", http.StatusNotFound)
		return
	}

	// Auth: must own the agent that owns this product
	dbAgent, _ := s.relay.Store.GetAgentByID(product.AgentID)
	if dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}
	if !s.authenticateAgentOwner(w, r, dbAgent.Name) {
		return
	}

	var req struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		DetailMarkdown string `json:"detail_markdown"`
		Price          int    `json:"price"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = product.Name
	}
	if req.Description == "" {
		req.Description = product.Description
	}
	if req.DetailMarkdown == "" {
		req.DetailMarkdown = product.DetailMarkdown
	}
	if req.Price <= 0 {
		req.Price = product.Price
	}

	if err := s.relay.Store.UpdateProduct(productID, req.Name, req.Description, req.DetailMarkdown, req.Price); err != nil {
		log.Printf("[products] update error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "id": productID})
}

func (s *Server) handleDeleteProduct(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("id")
	product, err := s.relay.Store.GetProduct(productID)
	if err != nil || product == nil {
		jsonError(w, "product not found", http.StatusNotFound)
		return
	}

	dbAgent, _ := s.relay.Store.GetAgentByID(product.AgentID)
	if dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}
	if !s.authenticateAgentOwner(w, r, dbAgent.Name) {
		return
	}

	if err := s.relay.Store.DeleteProduct(productID); err != nil {
		log.Printf("[products] delete error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	log.Printf("[products] deleted %s", productID)
}

// handleBuyProduct: deposit/final-payment flow
// 1. Buyer pays 10% deposit
// 2. Agent produces result
// 3. Returns order_id + result. Buyer decides to confirm (pay 90%) or cancel.
func (s *Server) handleBuyProduct(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("id")
	product, err := s.relay.Store.GetProduct(productID)
	if err != nil || product == nil {
		jsonError(w, "product not found", http.StatusNotFound)
		return
	}
	if product.Status != "active" {
		jsonError(w, "product is not active", http.StatusBadRequest)
		return
	}

	dbAgent, _ := s.relay.Store.GetAgentByID(product.AgentID)
	if dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	var req struct {
		Task         string `json:"task"`
		BuyerAgentID string `json:"buyer_agent_id,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Create async order — no credits deducted yet (escrow happens on accept)
	orderID := uuid.New().String()
	order := &store.Order{
		ID:              orderID,
		ProductID:       productID,
		SellerAgentID:   product.AgentID,
		SellerAgentName: dbAgent.Name,
		BuyerAgentID:    req.BuyerAgentID,
		BuyerIP:         clientIP(r),
		BuyerTask:       req.Task,
		TotalPrice:      product.Price,
	}
	if err := s.relay.Store.CreateOrder(order); err != nil {
		log.Printf("[buy] create order error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"order_id":    orderID,
		"status":      "pending",
		"product":     product.Name,
		"agent":       dbAgent.Name,
		"total_price": product.Price,
	})
	log.Printf("[buy] order %s created (async) for product %s", orderID, product.Name)
}

// handleAcceptOrder: seller accepts order (pending → processing), escrows buyer credits
func (s *Server) handleAcceptOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}
	if order.Status != "pending" {
		jsonError(w, "order is not pending", http.StatusBadRequest)
		return
	}

	// Auth: seller must own this order
	if !s.isOrderSeller(r, order) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Escrow: debit buyer credits (if agent buyer)
	price := order.TotalPrice
	if order.OfferPrice > 0 {
		price = order.OfferPrice
	}
	if order.BuyerAgentID != "" && price > 0 {
		if err := s.relay.Store.DebitAgent(order.BuyerAgentID, price); err != nil {
			jsonError(w, "buyer has insufficient credits", http.StatusPaymentRequired)
			return
		}
	}

	// Accept with 30 minute timeout
	if err := s.relay.Store.AcceptOrder(orderID, price, 30); err != nil {
		jsonError(w, "failed to accept order", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"order_id": orderID,
		"status":   "processing",
		"escrow":   price,
	})
	log.Printf("[order] %s accepted, escrow=%d", orderID, price)
}

// handleDeliverOrder: seller delivers result (processing → completed)
func (s *Server) handleDeliverOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}
	if order.Status != "processing" {
		jsonError(w, "order is not processing", http.StatusBadRequest)
		return
	}
	if !s.isOrderSeller(r, order) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Result string `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := s.relay.Store.DeliverOrder(orderID, req.Result); err != nil {
		jsonError(w, "failed to deliver order", http.StatusInternalServerError)
		return
	}

	// Transfer escrow to seller + platform mint
	if order.EscrowAmount > 0 {
		s.relay.Store.MintCredit(order.SellerAgentID, order.EscrowAmount)
		s.relay.Store.PlatformMint(order.EscrowAmount)
	}

	// Update product purchase count
	if order.ProductID != "" {
		s.relay.Store.IncrementProductPurchases(order.ProductID)
	}
	s.relay.Store.IncrementAgentTasks(order.SellerAgentID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"order_id": orderID,
		"status":   "completed",
	})
	log.Printf("[order] %s delivered, seller +%d", orderID, order.EscrowAmount)
}

// handleExtendOrder: seller extends timeout
func (s *Server) handleExtendOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}
	if order.Status != "processing" {
		jsonError(w, "order is not processing", http.StatusBadRequest)
		return
	}
	if !s.isOrderSeller(r, order) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := s.relay.Store.ExtendOrderTimeout(orderID, 30); err != nil {
		jsonError(w, "failed to extend timeout", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"order_id": orderID,
	})
	log.Printf("[order] %s timeout extended +30min", orderID)
}

// handleGetOrder: get single order detail (public)
func (s *Server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(order)
}

// handleListIncomingOrders: seller's pending + processing orders
func (s *Server) handleListIncomingOrders(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dbAgent, _ := s.relay.Store.GetAgentByName(name)
	if dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	orders, err := s.relay.Store.ListSellerOrders(dbAgent.ID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if orders == nil {
		orders = []store.OrderListing{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orders)
}

// handleListPlacedOrders: buyer's orders
func (s *Server) handleListPlacedOrders(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dbAgent, _ := s.relay.Store.GetAgentByName(name)
	if dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	orders, err := s.relay.Store.ListBuyerOrders(dbAgent.ID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if orders == nil {
		orders = []store.OrderListing{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orders)
}

// handleCreateAdHocOrder: agent creates an ad-hoc order to another agent (no product)
func (s *Server) handleCreateAdHocOrder(w http.ResponseWriter, r *http.Request) {
	targetName := r.PathValue("name")
	targetAgent, _ := s.relay.Store.GetAgentByName(targetName)
	if targetAgent == nil {
		jsonError(w, "target agent not found", http.StatusNotFound)
		return
	}

	var req struct {
		Task          string `json:"task"`
		OfferPrice    int    `json:"offer_price"`
		BuyerAgentID  string `json:"buyer_agent_id"`
		ParentOrderID string `json:"parent_order_id,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Task == "" || req.BuyerAgentID == "" {
		jsonError(w, "task and buyer_agent_id required", http.StatusBadRequest)
		return
	}

	orderID := uuid.New().String()
	order := &store.Order{
		ID:              orderID,
		SellerAgentID:   targetAgent.ID,
		SellerAgentName: targetAgent.Name,
		BuyerAgentID:    req.BuyerAgentID,
		BuyerTask:       req.Task,
		ParentOrderID:   req.ParentOrderID,
		TotalPrice:      req.OfferPrice,
		OfferPrice:      req.OfferPrice,
	}
	if err := s.relay.Store.CreateOrder(order); err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"order_id": orderID,
		"status":   "pending",
		"agent":    targetAgent.Name,
	})
	log.Printf("[order] ad-hoc order %s created: %s → %s, offer=%d", orderID, req.BuyerAgentID, targetAgent.Name, req.OfferPrice)
}

// handleCancelOrder: cancel an order
func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}

	switch order.Status {
	case "pending":
		s.relay.Store.CancelOrder(orderID)
	case "processing":
		// Refund escrow to buyer
		if order.BuyerAgentID != "" && order.EscrowAmount > 0 {
			s.relay.Store.MintCredit(order.BuyerAgentID, order.EscrowAmount)
		}
		s.relay.Store.CancelOrder(orderID)
	default:
		jsonError(w, "order cannot be cancelled in state: "+order.Status, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"order_id": orderID,
	})
	log.Printf("[order] %s cancelled (was %s)", orderID, order.Status)
}

// isOrderSeller checks if the request is from the seller agent
func (s *Server) isOrderSeller(r *http.Request, order *store.Order) bool {
	token := auth.ExtractBearer(r)
	if token == "" {
		return false
	}
	// Look up seller agent and verify secret
	if order.SellerAgentName != "" {
		dbAgent, _ := s.relay.Store.GetAgentByName(order.SellerAgentName)
		if dbAgent != nil && auth.VerifyToken(token, dbAgent.SecretHash) {
			return true
		}
	}
	if order.SellerAgentID != "" {
		dbAgent, _ := s.relay.Store.GetAgentByID(order.SellerAgentID)
		if dbAgent != nil && auth.VerifyToken(token, dbAgent.SecretHash) {
			return true
		}
	}
	return false
}

func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := s.relay.Store.ListRecentOrders(100)
	if err != nil {
		log.Printf("[orders] list error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if orders == nil {
		orders = []store.OrderListing{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orders)
}

func (s *Server) handleSubmitReview(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}
	if order.Status != "completed" {
		jsonError(w, "can only review completed orders", http.StatusBadRequest)
		return
	}

	var body struct {
		Rating  int    `json:"rating"`
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.Rating < 1 || body.Rating > 5 {
		jsonError(w, "rating must be 1-5", http.StatusBadRequest)
		return
	}

	// Determine reviewer name from buyer agent
	reviewerName := "anonymous"
	if order.BuyerAgentID != "" {
		agent, _ := s.relay.Store.GetAgentByID(order.BuyerAgentID)
		if agent != nil {
			reviewerName = agent.Name
		}
	}

	review, err := s.relay.Store.CreateReview(uuid.New().String(), orderID, order.ProductID, reviewerName, body.Rating, body.Comment)
	if err != nil {
		log.Printf("[review] create error: %v", err)
		jsonError(w, "failed to create review (may already exist)", http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(review)
}

func (s *Server) handleListProductReviews(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("id")
	reviews, err := s.relay.Store.ListProductReviews(productID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if reviews == nil {
		reviews = []store.Review{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reviews)
}

func (s *Server) handleListUnreviewedOrders(w http.ResponseWriter, r *http.Request) {
	buyer := r.URL.Query().Get("buyer")
	if buyer == "" {
		jsonError(w, "buyer parameter required", http.StatusBadRequest)
		return
	}
	orders, err := s.relay.Store.ListUnreviewedOrders(buyer)
	if err != nil {
		log.Printf("[review] unreviewed error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if orders == nil {
		orders = []store.OrderListing{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orders)
}

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

// callAgentMCP sends a task to an agent via MCP protocol and returns the result text
func (s *Server) callAgentMCP(agent *relay.ConnectedAgent, dbAgent *store.Agent, task, publisherID, callerIP string, startTime time.Time) (string, error) {
	initBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1,
		"method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "akemon-buy", "version": "1.0"},
		},
	})

	initReqID := uuid.New().String()
	initCh := agent.AddPending(initReqID)
	initMsg := &relay.RelayMessage{
		Type:      relay.TypeMCPRequest,
		RequestID: initReqID,
		Method:    "POST",
		Headers: map[string]string{
			"content-type":   "application/json",
			"x-publisher-id": publisherID,
		},
		Body: initBody,
	}
	if err := agent.Send(initMsg); err != nil {
		agent.RemovePending(initReqID)
		return "", fmt.Errorf("failed to reach agent")
	}

	var sessionID string
	select {
	case initResp := <-initCh:
		if initResp.ResponseHeaders != nil {
			sessionID = initResp.ResponseHeaders["mcp-session-id"]
		}
	case <-time.After(15 * time.Second):
		agent.RemovePending(initReqID)
		return "", fmt.Errorf("agent init timeout")
	}

	callBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2,
		"method": "tools/call",
		"params": map[string]interface{}{
			"name":      "submit_task",
			"arguments": map[string]string{"task": task},
		},
	})

	callHeaders := map[string]string{
		"content-type":   "application/json",
		"x-publisher-id": publisherID,
	}
	if sessionID != "" {
		callHeaders["mcp-session-id"] = sessionID
	}

	callReqID := uuid.New().String()
	callCh := agent.AddPending(callReqID)
	callMsg := &relay.RelayMessage{
		Type:      relay.TypeMCPRequest,
		RequestID: callReqID,
		SessionID: sessionID,
		Method:    "POST",
		Headers:   callHeaders,
		Body:      callBody,
	}
	if err := agent.Send(callMsg); err != nil {
		agent.RemovePending(callReqID)
		return "", fmt.Errorf("failed to send task")
	}

	select {
	case resp := <-callCh:
		duration := time.Since(startTime).Milliseconds()
		resultText := extractTextResult(resp.Body)
		status := "ok"
		if resp.Type == relay.TypeMCPError || resultText == "" {
			status = "error"
			if resultText == "" {
				resultText = "no response"
			}
		}
		s.relay.Store.RecordTask(uuid.New().String(), dbAgent.ID, status, callerIP, int(duration))
		if status == "error" {
			return "", fmt.Errorf("%s", resultText)
		}
		return resultText, nil
	case <-time.After(s.config.RequestTimeout):
		agent.RemovePending(callReqID)
		s.relay.Store.RecordTask(uuid.New().String(), dbAgent.ID, "timeout", callerIP, int(s.config.RequestTimeout.Milliseconds()))
		return "", fmt.Errorf("agent did not respond in time")
	}
}
