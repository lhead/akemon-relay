package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/relay"
)

// existing agent WebSocket connection.
func (s *Server) handleTerminalWebSocket(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		http.Error(w, `{"error":"missing agent name"}`, http.StatusBadRequest)
		return
	}

	// Auth: accept token from query param (WebSocket can't set headers)
	// or from Authorization header.
	token := r.URL.Query().Get("token")
	if token == "" {
		token = auth.ExtractBearer(r)
	}
	dbAgent, err := s.relay.Store.GetAgentByName(agentName)
	if err != nil || dbAgent == nil {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}
	// Terminal requires owner (secret key) only — too dangerous for access key holders
	if token == "" {
		http.Error(w, `{"error":"authentication required — owner only"}`, http.StatusUnauthorized)
		return
	}
	if !auth.VerifyToken(token, dbAgent.SecretHash) {
		http.Error(w, `{"error":"terminal access is restricted to agent owner"}`, http.StatusForbidden)
		return
	}

	agent := s.relay.Registry.Get(agentName)
	if agent == nil {
		http.Error(w, `{"error":"agent offline"}`, http.StatusBadGateway)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[terminal] websocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Register terminal session (displace any previous)
	s.termMu.Lock()
	old := s.termSessions[agentName]
	s.termSessions[agentName] = &terminalSession{browserConn: conn}
	s.termMu.Unlock()
	if old != nil {
		old.mu.Lock()
		old.browserConn.Close()
		old.mu.Unlock()
	}

	defer func() {
		s.termMu.Lock()
		if ts := s.termSessions[agentName]; ts != nil && ts.browserConn == conn {
			delete(s.termSessions, agentName)
		}
		s.termMu.Unlock()
		agent.Send(&relay.RelayMessage{Type: relay.TypeTerminalStop})
		log.Printf("[terminal] browser disconnected from %s", agentName)
	}()

	// Read first message from browser: {cols, rows}
	_, firstMsg, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var initMsg struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	json.Unmarshal(firstMsg, &initMsg)
	if initMsg.Cols <= 0 {
		initMsg.Cols = 80
	}
	if initMsg.Rows <= 0 {
		initMsg.Rows = 24
	}

	// Tell agent to start PTY
	if err := agent.Send(&relay.RelayMessage{
		Type: relay.TypeTerminalStart,
		Cols: initMsg.Cols,
		Rows: initMsg.Rows,
	}); err != nil {
		log.Printf("[terminal] failed to send terminal_start to %s: %v", agentName, err)
		return
	}
	log.Printf("[terminal] started for %s (%dx%d)", agentName, initMsg.Cols, initMsg.Rows)

	// Relay browser messages → agent
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var msg relay.RelayMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case relay.TypeTerminalData:
			agent.Send(&msg)
		case relay.TypeTerminalResize:
			agent.Send(&msg)
		}
	}
}


// forwardToTerminalBrowser sends a terminal message from the agent to the
// connected browser WebSocket, if any.
func (s *Server) forwardToTerminalBrowser(agentName string, msg *relay.RelayMessage) {
	s.termMu.RLock()
	ts := s.termSessions[agentName]
	s.termMu.RUnlock()
	if ts == nil {
		return
	}
	ts.mu.Lock()
	ts.browserConn.WriteJSON(msg)
	ts.mu.Unlock()
}

