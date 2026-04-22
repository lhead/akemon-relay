package store

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// FailureEvent records an engine or task failure emitted by an agent.
type FailureEvent struct {
	ID        string
	AgentName string
	Kind      string // e.g. "engine_abort", "task_failed", "engine_crash"
	Label     string // short label, e.g. command name or task ID
	Message   string
	CreatedAt string
}

// CreateFailureEvent persists a new failure event.
func (s *Store) CreateFailureEvent(agentName, kind, label, message string) error {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO failure_events (id, agent_name, kind, label, message, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, agentName, kind, label, message, now,
	)
	return err
}

// ListFailureEvents24h returns failure events for an agent in the last 24 hours, newest first.
// Pass agentName="" to list across all agents.
func (s *Store) ListFailureEvents24h(agentName string) ([]FailureEvent, error) {
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	var rows *sql.Rows
	var err error
	if agentName == "" {
		rows, err = s.db.Query(
			`SELECT id, agent_name, kind, label, message, created_at FROM failure_events WHERE created_at >= ? ORDER BY created_at DESC`,
			cutoff,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, agent_name, kind, label, message, created_at FROM failure_events WHERE agent_name = ? AND created_at >= ? ORDER BY created_at DESC`,
			agentName, cutoff,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []FailureEvent
	for rows.Next() {
		var e FailureEvent
		if err := rows.Scan(&e.ID, &e.AgentName, &e.Kind, &e.Label, &e.Message, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
