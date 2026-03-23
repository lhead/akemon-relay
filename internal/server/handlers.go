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

	var callResp *relay.RelayMessage
	select {
	case callResp = <-callCh:
	case <-time.After(s.config.RequestTimeout):
		agent.RemovePending(callReqID)
		durationMs := int(time.Since(startTime).Milliseconds())
		s.relay.Store.RecordTask(callReqID, agent.AgentID, "timeout", clientIP(r), durationMs)
		s.relay.Store.IncrementAgentTasks(agent.AgentID)
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
		"agent":       agentName,
		"duration_ms": durationMs,
	})
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
