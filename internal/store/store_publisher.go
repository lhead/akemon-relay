package store

import (
	"database/sql"
	"time"
)

// Publisher represents a human account that owns agents.
type Publisher struct {
	ID          string `json:"id"`
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
	CreatedAt   int64  `json:"created_at"`
}

// GetPublisher returns a publisher by its ID.
func (s *Store) GetPublisher(id string) (*Publisher, error) {
	p := &Publisher{}
	err := s.db.QueryRow(`
		SELECT id, account_id, display_name, created_at
		FROM publishers WHERE id = ?
	`, id).Scan(&p.ID, &p.AccountID, &p.DisplayName, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// CallerInfo is returned by ResolveCallerFromToken with full identity details.
type CallerInfo struct {
	PublisherID string
	AgentID     string // non-empty when token is a legacy agent device_token
	TokenID     string // device_token.id or session.id, for auditing
}

// ResolveCallerFromToken looks up a bearer token across device_tokens and sessions.
// Returns (info, true) on a successful match, ("", false) on no match.
// Also updates last_used_at / last_used_ip for the matched token.
func (s *Store) ResolveCallerFromToken(token, clientIP string) (*CallerInfo, bool) {
	now := time.Now().Unix()

	// 1. Try device_tokens
	var info CallerInfo
	var deviceName string
	err := s.db.QueryRow(`
		SELECT id, publisher_id, device_name
		FROM device_tokens
		WHERE token_hash = ?
		  AND revoked_at IS NULL
		  AND (expires_at IS NULL OR expires_at > ?)
	`, token, now).Scan(&info.TokenID, &info.PublisherID, &deviceName)
	if err == nil {
		// Update last_used
		s.db.Exec(`UPDATE device_tokens SET last_used_at = ?, last_used_ip = ? WHERE id = ?`, now, clientIP, info.TokenID)
		// If legacy agent token, resolve agent ID
		if len(deviceName) > 7 && deviceName[:7] == "legacy:" {
			agentName := deviceName[7:]
			agent, _ := s.GetAgentByName(agentName)
			if agent != nil {
				info.AgentID = agent.ID
			}
		}
		return &info, true
	}

	// 2. Try sessions
	err = s.db.QueryRow(`
		SELECT id, publisher_id
		FROM sessions
		WHERE token_hash = ?
		  AND revoked_at IS NULL
		  AND expires_at > ?
	`, token, now).Scan(&info.TokenID, &info.PublisherID)
	if err == nil {
		s.db.Exec(`UPDATE sessions SET last_used_at = ? WHERE id = ?`, now, info.TokenID)
		return &info, true
	}

	return nil, false
}
