package server

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func joinTags(tags []string) string {
	var clean []string
	for _, t := range tags {
		t = strings.TrimSpace(strings.ToLower(t))
		if t != "" {
			clean = append(clean, t)
		}
	}
	return strings.Join(clean, ",")
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

func (s *Server) handleAgentWebSocket(w http.ResponseWriter, r *http.Request) {
	secretToken := auth.ExtractBearer(r)
	if secretToken == "" {
		http.Error(w, `{"error":"missing Authorization header"}`, http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade error: %v", err)
		return
	}
	defer conn.Close()

	conn.SetReadLimit(s.config.MaxMessageBytes)

	// Registration must arrive within 30 seconds
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Read registration message
	_, rawMsg, err := conn.ReadMessage()
	if err != nil {
		log.Printf("[ws] read registration error: %v", err)
		return
	}

	var regMsg relay.RelayMessage
	if err := json.Unmarshal(rawMsg, &regMsg); err != nil {
		sendError(conn, "invalid registration message")
		return
	}

	name := regMsg.Name
	accountID := regMsg.AccountID

	if name == "" || accountID == "" {
		sendError(conn, "name and account_id are required")
		return
	}

	if !namePattern.MatchString(name) {
		sendError(conn, "invalid name: must be 3-40 chars, lowercase letters/numbers/hyphens, cannot start or end with hyphen")
		return
	}

	// Check if agent exists in DB
	existing, err := s.relay.Store.GetAgentByName(name)
	if err != nil {
		log.Printf("[ws] db error: %v", err)
		sendError(conn, "internal error")
		return
	}

	var agentID string
	var accessHash string

	if existing == nil {
		// New agent: hash the secret token and store
		secretHash, err := auth.HashToken(secretToken)
		if err != nil {
			sendError(conn, "internal error")
			return
		}
		// For new agents, we need the access token hash too.
		// The client sends it as a separate field, or we generate a placeholder.
		// In V1, the access_hash is stored when the agent first registers.
		// The client must provide it in the registration message headers.
		accessToken := regMsg.Headers["access_token"]
		if accessToken == "" {
			sendError(conn, "access_token required in headers for first registration")
			return
		}
		ah, err := auth.HashToken(accessToken)
		if err != nil {
			sendError(conn, "internal error")
			return
		}
		accessHash = ah

		agentID = uuid.New().String()
		now := time.Now().UTC().Format(time.RFC3339)

		if err := s.relay.Store.EnsureAccount(accountID); err != nil {
			log.Printf("[ws] ensure account error: %v", err)
			sendError(conn, "internal error")
			return
		}

		engine := regMsg.Engine
		if engine == "" {
			engine = "claude"
		}
		regPrice := regMsg.Price
		if regPrice <= 0 {
			regPrice = 1
		}
		tags := joinTags(regMsg.Tags)
		if err := s.relay.Store.CreateAgent(&store.Agent{
			ID:              agentID,
			Name:            name,
			AccountID:       accountID,
			SecretHash:      secretHash,
			AccessHash:      accessHash,
			Description:     regMsg.Description,
			Engine:          engine,
			Avatar:          regMsg.Avatar,
			Public:          regMsg.Public,
			FirstRegistered: now,
			Tags:            tags,
			Price:           regPrice,
		}); err != nil {
			log.Printf("[ws] create agent error: %v", err)
			sendError(conn, "failed to register agent (name may be taken)")
			return
		}
		log.Printf("[ws] new agent registered: %s (account=%s)", name, accountID)
	} else {
		// Existing agent: verify secret and account
		if existing.AccountID != accountID {
			sendError(conn, "name already registered by another account")
			return
		}
		if !auth.VerifyToken(secretToken, existing.SecretHash) {
			sendError(conn, "invalid secret token")
			return
		}
		agentID = existing.ID
		accessHash = existing.AccessHash

		// Update description/engine/public on reconnect
		reconnectEngine := regMsg.Engine
		if reconnectEngine == "" {
			reconnectEngine = "claude"
		}
		reconnectTags := joinTags(regMsg.Tags)
		reconnectPrice := regMsg.Price
		if reconnectPrice <= 0 {
			reconnectPrice = 1
		}
		if err := s.relay.Store.UpdateAgentOnConnect(name, regMsg.Description, reconnectEngine, regMsg.Public, reconnectTags, reconnectPrice, regMsg.Avatar); err != nil {
			log.Printf("[ws] update agent error: %v", err)
		}
		log.Printf("[ws] agent reconnected: %s", name)
	}

	connID := uuid.New().String()
	if err := s.relay.Store.RecordConnect(connID, agentID); err != nil {
		log.Printf("[ws] record connect error: %v", err)
	}

	// Get price from DB
	agentPrice := 1
	if dbAg, err := s.relay.Store.GetAgentByName(name); err == nil && dbAg != nil {
		agentPrice = dbAg.Price
		if agentPrice <= 0 {
			agentPrice = 1
		}
	}
	agent := relay.NewConnectedAgent(name, agentID, accountID, accessHash, regMsg.Public, agentPrice, conn, connID)

	// Register in memory
	displaced, errMsg := s.relay.Registry.Register(agent, s.config.GracePeriod)
	if errMsg != "" {
		sendError(conn, errMsg)
		return
	}
	if displaced != nil {
		log.Printf("[ws] agent displaced: %s (old connection closed by same-account reconnect)", name)
		displaced.FailAllPending("displaced by reconnect")
		displaced.Conn.Close()
	}

	// Clear registration deadline
	conn.SetReadDeadline(time.Time{})

	// Send registered confirmation
	conn.WriteJSON(&relay.RelayMessage{
		Type: relay.TypeRegistered,
		Name: name,
	})

	// Start heartbeat and message loop
	s.agentLoop(agent)
}

func (s *Server) agentLoop(agent *ConnectedAgent) {
	cfg := s.config
	pingTicker := time.NewTicker(cfg.PingInterval)
	defer pingTicker.Stop()

	missedPongs := 0
	pongCh := make(chan struct{}, 1)

	agent.Conn.SetPongHandler(func(string) error {
		missedPongs = 0
		select {
		case pongCh <- struct{}{}:
		default:
		}
		return nil
	})

	// Read loop in separate goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, rawMsg, err := agent.Conn.ReadMessage()
			if err != nil {
				return
			}

			var msg relay.RelayMessage
			if err := json.Unmarshal(rawMsg, &msg); err != nil {
				log.Printf("[ws] %s: invalid message: %v", agent.Name, err)
				continue
			}

			switch msg.Type {
			case relay.TypeMCPResponse, relay.TypeMCPError:
				if msg.RequestID != "" {
					agent.ResolvePending(msg.RequestID, &msg)
				}
			case relay.TypeControlAck:
				log.Printf("[ws] %s: control_ack action=%s", agent.Name, msg.Action)
			case relay.TypeSetPrice:
				newPrice := msg.Price
				if newPrice < 1 {
					newPrice = 1
				}
				if newPrice > 10000 {
					newPrice = 10000
				}
				agent.Price = newPrice
				if err := s.relay.Store.UpdateAgentPrice(agent.Name, newPrice); err != nil {
					log.Printf("[ws] %s: set_price db error: %v", agent.Name, err)
				} else {
					log.Printf("[ws] %s: price updated to %d", agent.Name, newPrice)
				}
			case relay.TypeAgentCall:
				// Agent wants to call another agent
				s.handleAgentCallFromWS(agent, &msg)
			case relay.TypeAgentCallResult:
				// Agent returning result to a caller
				s.routeAgentCallResult(agent, &msg)
			default:
				log.Printf("[ws] %s: unexpected message type: %s", agent.Name, msg.Type)
			}
		}
	}()

	disconnectReason := "connection_closed"

	defer func() {
		agent.FailAllPending("agent disconnected")
		s.relay.Registry.Unregister(agent.Name, cfg.GracePeriod)
		if err := s.relay.Store.RecordDisconnect(agent.ConnID, disconnectReason); err != nil {
			log.Printf("[ws] record disconnect error: %v", err)
		}
		log.Printf("[ws] agent disconnected: %s (%s)", agent.Name, disconnectReason)
	}()

	for {
		select {
		case <-done:
			return
		case <-pingTicker.C:
			if err := agent.Ping(); err != nil {
				return
			}
			missedPongs++
			if missedPongs >= cfg.MaxMissedPongs {
				disconnectReason = "pong_timeout"
				return
			}
		}
	}
}

type ConnectedAgent = relay.ConnectedAgent

func (s *Server) handleAgentCallFromWS(caller *ConnectedAgent, msg *relay.RelayMessage) {
	target := msg.Target
	if target == "" || msg.CallID == "" {
		log.Printf("[agent_call] %s: missing target or call_id", caller.Name)
		return
	}

	targetAgent := s.relay.Registry.Get(target)
	if targetAgent == nil {
		// Target offline — send error back to caller
		errMsg := &relay.RelayMessage{
			Type:   relay.TypeAgentCallResult,
			CallID: msg.CallID,
			Caller: target,
			Result: "[error] Agent " + target + " is offline",
		}
		caller.Send(errMsg)
		return
	}

	// Forward to target, stamping caller name
	fwd := &relay.RelayMessage{
		Type:   relay.TypeAgentCall,
		CallID: msg.CallID,
		Caller: caller.Name,
		Task:   msg.Task,
	}
	if err := targetAgent.Send(fwd); err != nil {
		log.Printf("[agent_call] %s→%s: send failed: %v", caller.Name, target, err)
		errMsg := &relay.RelayMessage{
			Type:   relay.TypeAgentCallResult,
			CallID: msg.CallID,
			Caller: target,
			Result: "[error] Failed to reach agent " + target,
		}
		caller.Send(errMsg)
		return
	}
	log.Printf("[agent_call] %s → %s (call_id=%s)", caller.Name, target, msg.CallID)
}

func (s *Server) routeAgentCallResult(responder *ConnectedAgent, msg *relay.RelayMessage) {
	callerName := msg.Caller
	if callerName == "" || msg.CallID == "" {
		return
	}

	callerAgent := s.relay.Registry.Get(callerName)
	if callerAgent == nil {
		log.Printf("[agent_call_result] caller %s is offline, dropping result", callerName)
		return
	}

	fwd := &relay.RelayMessage{
		Type:   relay.TypeAgentCallResult,
		CallID: msg.CallID,
		Caller: responder.Name,
		Result: msg.Result,
	}
	if err := callerAgent.Send(fwd); err != nil {
		log.Printf("[agent_call_result] send to %s failed: %v", callerName, err)
	}

	// Credits: atomic debit caller + credit callee (skip on error result)
	result := msg.Result
	isError := strings.HasPrefix(result, "[error]")
	if !isError {
		price, err := s.relay.Store.AgentToAgentTransfer(callerAgent.AgentID, responder.AgentID)
		if err != nil {
			log.Printf("[credits] agent-to-agent transfer failed (%s → %s): %v", callerName, responder.Name, err)
		} else {
			log.Printf("[credits] agent %s paid %d to %s", callerName, price, responder.Name)
		}
	}

	log.Printf("[agent_call_result] %s → %s (call_id=%s)", responder.Name, callerName, msg.CallID)
}

func sendError(conn *websocket.Conn, msg string) {
	conn.WriteJSON(&relay.RelayMessage{
		Type:  relay.TypeError,
		Error: msg,
	})
	conn.Close()
}
