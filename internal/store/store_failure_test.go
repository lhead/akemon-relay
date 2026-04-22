package store

import (
	"testing"
	"time"
)

func TestCreateAndListFailureEvents(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	// Empty before any events
	events, err := s.ListFailureEvents24h("bob")
	if err != nil {
		t.Fatalf("ListFailureEvents24h empty: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}

	// Create a couple of events for different agents
	if err := s.CreateFailureEvent("bob", "engine_abort", "claude", "aborted by signal"); err != nil {
		t.Fatalf("CreateFailureEvent: %v", err)
	}
	if err := s.CreateFailureEvent("bob", "task_failed", "order-123", "timeout"); err != nil {
		t.Fatalf("CreateFailureEvent: %v", err)
	}
	if err := s.CreateFailureEvent("alice", "engine_abort", "codex", "crash"); err != nil {
		t.Fatalf("CreateFailureEvent alice: %v", err)
	}

	// bob should see 2
	events, err = s.ListFailureEvents24h("bob")
	if err != nil {
		t.Fatalf("ListFailureEvents24h bob: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events for bob, got %d", len(events))
	}
	// Newest first
	if events[0].Kind != "task_failed" {
		t.Errorf("expected task_failed first (newest), got %s", events[0].Kind)
	}

	// alice should see 1
	events, err = s.ListFailureEvents24h("alice")
	if err != nil {
		t.Fatalf("ListFailureEvents24h alice: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event for alice, got %d", len(events))
	}
	if events[0].AgentName != "alice" {
		t.Errorf("wrong agent_name: %s", events[0].AgentName)
	}

	// Empty agentName should return all 3
	all, err := s.ListFailureEvents24h("")
	if err != nil {
		t.Fatalf("ListFailureEvents24h all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 total events, got %d", len(all))
	}
}

func TestListFailureEvents24hCutoff(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	// Insert an event directly with a timestamp older than 24h
	old := time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO failure_events (id, agent_name, kind, label, message, created_at) VALUES ('old-event', 'bob', 'crash', 'cmd', 'msg', ?)`,
		old,
	)
	if err != nil {
		t.Fatalf("insert old event: %v", err)
	}

	events, err := s.ListFailureEvents24h("bob")
	if err != nil {
		t.Fatalf("ListFailureEvents24h: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("event older than 24h should not appear; got %d events", len(events))
	}
}
