package relay

import "encoding/json"

// Message types exchanged over WebSocket between relay and agent.
const (
	TypeMCPRequest  = "mcp_request"
	TypeMCPResponse = "mcp_response"
	TypeMCPError    = "mcp_error"
	TypePing        = "ping"
	TypePong        = "pong"
	TypeRegistered  = "registered"
	TypeError       = "error"
)

// RelayMessage is the envelope for all WebSocket communication.
type RelayMessage struct {
	Type      string            `json:"type"`
	RequestID string            `json:"request_id,omitempty"`
	SessionID string            `json:"session_id,omitempty"`
	Method    string            `json:"method,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      json.RawMessage   `json:"body,omitempty"`

	// Response fields
	StatusCode      int               `json:"status_code,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`

	// Error field
	Error string `json:"error,omitempty"`

	// Registration fields (sent by agent on connect)
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	AccountID   string `json:"account_id,omitempty"`
	Public      bool   `json:"public,omitempty"`
	Engine      string `json:"engine,omitempty"`
}
