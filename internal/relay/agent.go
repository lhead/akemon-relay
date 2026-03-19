package relay

import (
	"sync"

	"github.com/gorilla/websocket"
)

// ConnectedAgent represents an agent with an active WebSocket connection.
type ConnectedAgent struct {
	Name         string
	AgentID      string // DB UUID
	AccountID    string
	AccessHash   string
	Public       bool
	Conn         *websocket.Conn
	ConnID       string // connection record ID
	mu           sync.Mutex
	pending      map[string]chan *RelayMessage
	pendingMu    sync.Mutex
}

// Send writes a message to the agent's WebSocket connection (thread-safe).
func (a *ConnectedAgent) Send(msg *RelayMessage) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Conn.WriteJSON(msg)
}

// Ping sends a WebSocket ping frame (thread-safe).
func (a *ConnectedAgent) Ping() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Conn.WriteMessage(websocket.PingMessage, nil)
}

// AddPending registers a response channel for a correlation ID.
func (a *ConnectedAgent) AddPending(requestID string) chan *RelayMessage {
	ch := make(chan *RelayMessage, 1)
	a.pendingMu.Lock()
	a.pending[requestID] = ch
	a.pendingMu.Unlock()
	return ch
}

// ResolvePending sends a response to the waiting channel and removes it.
func (a *ConnectedAgent) ResolvePending(requestID string, msg *RelayMessage) bool {
	a.pendingMu.Lock()
	ch, ok := a.pending[requestID]
	if ok {
		delete(a.pending, requestID)
	}
	a.pendingMu.Unlock()
	if ok {
		ch <- msg
	}
	return ok
}

// RemovePending removes a pending channel without sending (used on timeout/send failure).
func (a *ConnectedAgent) RemovePending(requestID string) {
	a.pendingMu.Lock()
	delete(a.pending, requestID)
	a.pendingMu.Unlock()
}

// FailAllPending sends an error to all waiting channels (called on disconnect).
func (a *ConnectedAgent) FailAllPending(errMsg string) {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	for id, ch := range a.pending {
		ch <- &RelayMessage{
			Type:      TypeMCPError,
			RequestID: id,
			Error:     errMsg,
		}
		delete(a.pending, id)
	}
}

// NewConnectedAgent creates a new ConnectedAgent.
func NewConnectedAgent(name, agentID, accountID, accessHash string, public bool, conn *websocket.Conn, connID string) *ConnectedAgent {
	return &ConnectedAgent{
		Name:       name,
		AgentID:    agentID,
		AccountID:  accountID,
		AccessHash: accessHash,
		Public:     public,
		Conn:       conn,
		ConnID:     connID,
		pending:    make(map[string]chan *RelayMessage),
	}
}
