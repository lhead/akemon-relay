package server

import (
	"net/http"

	"github.com/akemon/akemon-relay/internal/auth"
)

// Caller carries the resolved identity of a request.
type Caller struct {
	Kind        string // "owner" | "anonymous"
	PublisherID string // non-empty when Kind == "owner"
	AgentID     string // non-empty when token is a legacy agent device_token
	TokenID     string // device_token.id or session.id for auditing
}

// ResolveCaller determines the caller identity from the bearer token.
//
// Resolution order:
//  1. New path: look up token in device_tokens / sessions (phase A tables).
//  2. Legacy fallback: scan agents.secret_hash via lookupAgentIDByToken —
//     guarantees zero disruption during the window between deployment and
//     the data-migration script completing.
//  3. Anonymous: no token, or token matches nothing.
func (s *Server) ResolveCaller(r *http.Request) Caller {
	token := auth.ExtractBearer(r)
	if token == "" {
		return Caller{Kind: "anonymous"}
	}

	// 1. New path: device_tokens / sessions
	if info, ok := s.relay.Store.ResolveCallerFromToken(token, clientIP(r)); ok {
		return Caller{
			Kind:        "owner",
			PublisherID: info.PublisherID,
			AgentID:     info.AgentID,
			TokenID:     info.TokenID,
		}
	}

	// 2. Legacy fallback: scan agents.secret_hash
	if agentID := s.lookupAgentIDByToken(token); agentID != "" {
		agent, _ := s.relay.Store.GetAgentByID(agentID)
		if agent != nil {
			return Caller{
				Kind:        "owner",
				PublisherID: agent.AccountID, // account_id == publisher.id post-migration
				AgentID:     agentID,
			}
		}
	}

	return Caller{Kind: "anonymous"}
}
