package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
	"github.com/google/uuid"
)

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

// checkAgentAccess verifies access for private agents. Public agents always pass.
// Returns true if access is granted; writes 401 and returns false otherwise.
func (s *Server) checkAgentAccess(w http.ResponseWriter, r *http.Request, agentName string) bool {
	dbAgent, err := s.relay.Store.GetAgentByName(agentName)
	if err != nil || dbAgent == nil {
		return true // agent not in DB → allow (it may be online but unregistered edge case)
	}
	if dbAgent.Public {
		return true
	}
	token := auth.ExtractBearer(r)
	if token == "" {
		jsonError(w, "this agent is private — access key required", http.StatusUnauthorized)
		return false
	}
	if !auth.VerifyToken(token, dbAgent.AccessHash) && !auth.VerifyToken(token, dbAgent.SecretHash) {
		jsonError(w, "invalid access key", http.StatusUnauthorized)
		return false
	}
	return true
}

// derivePublisherID computes the publisher identity from request credentials.
func derivePublisherID(r *http.Request) string {
	if token := auth.ExtractBearer(r); token != "" {
		h := sha256.Sum256([]byte(token))
		return fmt.Sprintf("%x", h[:6])
	}
	h := sha256.Sum256([]byte(clientIP(r)))
	return fmt.Sprintf("ip-%x", h[:6])
}

// proxyAgentSelfAPI forwards a GET request to an online agent's /self/* endpoint via WebSocket.
func (s *Server) proxyAgentSelfAPI(w http.ResponseWriter, agentName, path string) {
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

	requestID := uuid.New().String()
	msg := &relay.RelayMessage{
		Type:      relay.TypeMCPRequest,
		RequestID: requestID,
		Method:    "GET",
		Path:      path,
		Headers:   map[string]string{"content-type": "application/json"},
	}

	ch := agent.AddPending(requestID)
	if err := agent.Send(msg); err != nil {
		agent.RemovePending(requestID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": "agent_send_failed"})
		return
	}

	var resp *relay.RelayMessage
	select {
	case resp = <-ch:
	case <-time.After(10 * time.Second):
		agent.RemovePending(requestID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGatewayTimeout)
		json.NewEncoder(w).Encode(map[string]string{"error": "timeout"})
		return
	}

	if resp.Type == relay.TypeMCPError {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": resp.Error})
		return
	}

	bodyBytes, err := json.Marshal(resp.Body)
	if err != nil {
		bodyBytes = resp.Body
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(bodyBytes)
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

// resolveBuyerAgent resolves buyer_agent_id (which may be a DB UUID or agent name)
// and verifies the bearer token matches. Returns (resolved_db_id, true) on success.
// If buyer_agent_id is empty, falls back to identifying the buyer by bearer token
// so agent-to-agent purchases made without passing buyer_agent_id (MCP buy_product,
// raw curl) still attribute the order to the right agent. If no token is present,
// returns ("", true) — genuine human buyer.
func (s *Server) resolveBuyerAgent(r *http.Request, buyerAgentID string) (string, bool) {
	token := auth.ExtractBearer(r)

	if buyerAgentID == "" {
		if token == "" {
			return "", true // anonymous human buyer
		}
		if id := s.lookupAgentIDByToken(token); id != "" {
			return id, true
		}
		return "", true // token doesn't match any agent — treat as human
	}

	if token == "" {
		return "", false
	}
	// Try by ID first, then by name
	dbAgent, _ := s.relay.Store.GetAgentByID(buyerAgentID)
	if dbAgent == nil {
		dbAgent, _ = s.relay.Store.GetAgentByName(buyerAgentID)
	}
	if dbAgent == nil {
		return "", false
	}
	if !auth.VerifyToken(token, dbAgent.SecretHash) {
		return "", false
	}
	return dbAgent.ID, true
}

// lookupAgentIDByToken scans agent secret hashes for one that matches the bearer
// token. Called only when the client omitted buyer_agent_id. Returns "" if nothing
// matches (treat as human).
func (s *Server) lookupAgentIDByToken(token string) string {
	rows, err := s.relay.Store.ListAgentSecrets()
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if auth.VerifyToken(token, r.SecretHash) {
			return r.ID
		}
	}
	return ""
}

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

func (s *Server) isOrderBuyer(r *http.Request, order *store.Order) bool {
	token := auth.ExtractBearer(r)
	if token == "" {
		return false
	}
	if order.BuyerAgentID != "" {
		dbAgent, _ := s.relay.Store.GetAgentByID(order.BuyerAgentID)
		if dbAgent != nil && auth.VerifyToken(token, dbAgent.SecretHash) {
			return true
		}
	}
	return false
}

// applyTaskResult routes completed task results to existing action handlers
func (s *Server) applyTaskResult(dbAgent *store.Agent, taskType, result string) {
	switch taskType {
	case "product_review":
		s.applyProductReview(result, dbAgent)
	case "product_create":
		s.parseAndCreateProducts(result, dbAgent)
	case "shopping":
		s.applyShoppingDecisions(result, dbAgent)
	case "diagnose_failures":
		s.applyDiagnoseLessons(result, dbAgent)
	default:
		log.Printf("[tasks] unknown task type: %s", taskType)
	}
}

// --- Auth Check ---

func (s *Server) handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	token := auth.ExtractBearer(r)
	if token == "" {
		// Not logged in — return anonymous identity from IP
		pubID := derivePublisherID(r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"role": "anonymous",
			"id":   pubID,
		})
		return
	}

	pubID := func() string {
		h := sha256.Sum256([]byte(token))
		return fmt.Sprintf("%x", h[:6])
	}()

	// Check if token matches any agent's secret key
	allAgents, err := s.relay.Store.ListAgents()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	var accountID string
	var ownedAgents []string
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

	if accountID != "" {
		// Owner — list all agents under this account
		acctAgents, _ := s.relay.Store.ListAgentsByAccount(accountID)
		for _, a := range acctAgents {
			ownedAgents = append(ownedAgents, a.Name)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"role":       "owner",
			"id":         pubID,
			"account_id": accountID,
			"agents":     ownedAgents,
		})
		return
	}

	// Valid token but not an owner — regular user
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"role": "user",
		"id":   pubID,
	})
}
