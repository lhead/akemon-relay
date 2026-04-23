package store

import (
	"testing"
	"time"
)

// helper: create + register agent, then reset the migration gate so we can re-run
// with the new data in place.
func prepMigrationTest(t *testing.T, s *Store, names ...string) {
	t.Helper()
	for _, name := range names {
		a := makeAgent(name, "claude", true)
		ensureAgentAccount(t, s, a)
		if err := s.CreateAgent(a); err != nil {
			t.Fatalf("CreateAgent(%s): %v", name, err)
		}
	}
	// Reset version gate so the next MigrateIdentityPhaseA() call actually runs.
	s.resetMigration(migrationIDPhaseA)
}

func TestMigrateIdentityPhaseA_PublishersCreated(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	prepMigrationTest(t, s, "alice", "bob")

	if err := s.MigrateIdentityPhaseA(); err != nil {
		t.Fatalf("MigrateIdentityPhaseA: %v", err)
	}

	var pubCount, acctCount int
	s.db.QueryRow(`SELECT COUNT(*) FROM publishers`).Scan(&pubCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&acctCount)
	if pubCount != acctCount {
		t.Errorf("publishers=%d, accounts=%d — should be equal", pubCount, acctCount)
	}
}

func TestMigrateIdentityPhaseA_DeviceTokensCreated(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	prepMigrationTest(t, s, "agent1", "agent2")

	if err := s.MigrateIdentityPhaseA(); err != nil {
		t.Fatalf("MigrateIdentityPhaseA: %v", err)
	}

	var tokenCount, agentCount int
	s.db.QueryRow(`SELECT COUNT(*) FROM device_tokens`).Scan(&tokenCount)
	s.db.QueryRow(`SELECT COUNT(*) FROM agents WHERE secret_hash != ''`).Scan(&agentCount)
	if tokenCount != agentCount {
		t.Errorf("device_tokens=%d, agents with secret_hash=%d — should be equal", tokenCount, agentCount)
	}
}

func TestMigrateIdentityPhaseA_AgentPublisherIDFilled(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	prepMigrationTest(t, s, "test-agent")

	if err := s.MigrateIdentityPhaseA(); err != nil {
		t.Fatalf("MigrateIdentityPhaseA: %v", err)
	}

	var pubID string
	s.db.QueryRow(`SELECT COALESCE(publisher_id,'') FROM agents WHERE name = 'test-agent'`).Scan(&pubID)
	if pubID == "" {
		t.Error("agents.publisher_id should be non-empty after migration")
	}
}

// TestMigrateIdentityPhaseA_VersionGate verifies the migration does not re-run
// (data does not double) once the schema_migrations marker is written.
func TestMigrateIdentityPhaseA_VersionGate(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	prepMigrationTest(t, s, "gate-agent")

	// First run: populates data and marks migration done.
	if err := s.MigrateIdentityPhaseA(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !s.isMigrationApplied(migrationIDPhaseA) {
		t.Fatal("migration should be marked as applied after first run")
	}

	// Second run: version gate must skip — counts must not change.
	var pubsBefore, tokensBefore int
	s.db.QueryRow(`SELECT COUNT(*) FROM publishers`).Scan(&pubsBefore)
	s.db.QueryRow(`SELECT COUNT(*) FROM device_tokens`).Scan(&tokensBefore)

	if err := s.MigrateIdentityPhaseA(); err != nil {
		t.Fatalf("second run: %v", err)
	}

	var pubsAfter, tokensAfter int
	s.db.QueryRow(`SELECT COUNT(*) FROM publishers`).Scan(&pubsAfter)
	s.db.QueryRow(`SELECT COUNT(*) FROM device_tokens`).Scan(&tokensAfter)

	if pubsAfter != pubsBefore {
		t.Errorf("version gate: publishers grew from %d to %d on second run", pubsBefore, pubsAfter)
	}
	if tokensAfter != tokensBefore {
		t.Errorf("version gate: device_tokens grew from %d to %d on second run", tokensBefore, tokensAfter)
	}
}

// TestMigrateIdentityPhaseA_OrphanSkipped verifies that an agent whose account
// has been deleted does not cause a FK error — it is skipped with a warning.
func TestMigrateIdentityPhaseA_OrphanSkipped(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	// Create a normal agent (with account) and reset the version gate.
	prepMigrationTest(t, s, "healthy-agent")

	// Inject an orphan agent: temporarily disable FK so we can create an agent
	// whose account will be deleted (simulating a historical data inconsistency).
	now := time.Now().UTC().Format(time.RFC3339)
	s.db.Exec(`PRAGMA foreign_keys = OFF`)
	s.db.Exec(`INSERT OR IGNORE INTO accounts (id, first_seen, last_active) VALUES ('orphan-acct',?,?)`, now, now)
	s.db.Exec(`INSERT INTO agents (id, name, account_id, secret_hash, access_hash, first_registered)
		VALUES ('orphan-agent-id','orphan-agent','orphan-acct','orphan-hash','','2026-01-01')`)
	// Delete the account — no publisher will be created for this agent.
	s.db.Exec(`DELETE FROM accounts WHERE id = 'orphan-acct'`)
	s.db.Exec(`PRAGMA foreign_keys = ON`)

	// Migration must succeed (no error); orphan simply skipped with a warning.
	if err := s.MigrateIdentityPhaseA(); err != nil {
		t.Fatalf("migration should not fail on orphan: %v", err)
	}

	// device_token for orphan must NOT exist.
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM device_tokens WHERE token_hash = 'orphan-hash'`).Scan(&count)
	if count != 0 {
		t.Errorf("orphan device_token should not have been inserted, found %d", count)
	}

	// Healthy agent's token must exist.
	var healthy int
	s.db.QueryRow(`SELECT COUNT(*) FROM device_tokens WHERE device_name = 'legacy:healthy-agent'`).Scan(&healthy)
	if healthy != 1 {
		t.Errorf("healthy agent's device_token should exist, found %d", healthy)
	}
}
