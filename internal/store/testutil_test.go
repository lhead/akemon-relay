package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// setupTestStore opens an in-memory SQLite database, runs Migrate, and returns
// the store plus a cleanup function. Call cleanup() in a defer.
func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	s := &Store{db: db}
	if err := s.Migrate(); err != nil {
		db.Close()
		t.Fatalf("migrate: %v", err)
	}
	return s, func() { db.Close() }
}

// makeAgent creates a minimal Agent struct for test inserts.
// Call ensureAgentAccount(t, s, a) before CreateAgent to satisfy the FK.
func makeAgent(name, engine string, public bool) *Agent {
	return &Agent{
		ID:          "id-" + name,
		Name:        name,
		Engine:      engine,
		Public:      public,
		SecretHash:  "hash-" + name,
		AccessHash:  "access-" + name,
		AccountID:   "acct-" + name,
		Avatar:      "🤖",
		Description: "test agent " + name,
	}
}

// ensureAgentAccount creates the account row for a before CreateAgent is called.
func ensureAgentAccount(t *testing.T, s *Store, a *Agent) {
	t.Helper()
	if err := s.EnsureAccount(a.AccountID); err != nil {
		t.Fatalf("EnsureAccount(%s): %v", a.AccountID, err)
	}
}
