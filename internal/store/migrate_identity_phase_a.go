package store

import (
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

const migrationIDPhaseA = "identity_phase_a"

// MigrateIdentityPhaseA populates the publishers and device_tokens tables from
// the existing accounts and agents tables.
//
// Three design properties:
//  1. Version gate  — schema_migrations table records a completion marker;
//     subsequent startups skip the migration entirely.
//  2. Transaction   — all writes (publishers, agents.publisher_id, device_tokens)
//     commit atomically; a mid-migration crash leaves the DB clean.
//  3. Orphan guard  — agents whose publisher row is missing (account deleted)
//     are skipped with a warning instead of causing a silent FK failure.
func (s *Store) MigrateIdentityPhaseA() error {
	// Ensure the version-tracking table exists (idempotent DDL).
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		id         TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Version gate: skip if already applied.
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE id = ?`, migrationIDPhaseA).Scan(&count)
	if count > 0 {
		return nil
	}

	now := time.Now().Unix()

	// -----------------------------------------------------------------------
	// Read phase (outside transaction — safe to retry on crash)
	// -----------------------------------------------------------------------

	// 1. Collect account IDs.
	accountRows, err := s.db.Query(`SELECT id FROM accounts`)
	if err != nil {
		return fmt.Errorf("list accounts: %w", err)
	}
	var accountIDs []string
	for accountRows.Next() {
		var id string
		if err := accountRows.Scan(&id); err != nil {
			accountRows.Close()
			return err
		}
		accountIDs = append(accountIDs, id)
	}
	accountRows.Close()

	// 2. Collect agent rows that have a secret hash.
	type agentMigRow struct {
		id          string
		name        string
		publisherID string // COALESCE(publisher_id, account_id)
		secretHash  string
	}
	// publisher_id defaults to '' (not NULL), so use NULLIF to make COALESCE fall through.
	agentRows, err := s.db.Query(`
		SELECT id, name, COALESCE(NULLIF(publisher_id, ''), account_id), secret_hash
		FROM agents
		WHERE secret_hash != '' AND secret_hash IS NOT NULL
	`)
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}
	var agents []agentMigRow
	for agentRows.Next() {
		var a agentMigRow
		if err := agentRows.Scan(&a.id, &a.name, &a.publisherID, &a.secretHash); err != nil {
			agentRows.Close()
			return err
		}
		agents = append(agents, a)
	}
	agentRows.Close()

	// -----------------------------------------------------------------------
	// Write phase (single transaction — atomically commits or rolls back)
	// -----------------------------------------------------------------------

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration tx: %w", err)
	}
	defer tx.Rollback() // no-op if Commit succeeds

	// Step 1: accounts → publishers
	for _, id := range accountIDs {
		prefix := id
		if len(prefix) > 12 {
			prefix = id[:12]
		}
		handle := "u-" + prefix
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO publishers (id, account_id, display_name, created_at)
			VALUES (?, ?, ?, ?)
		`, id, handle, handle, now); err != nil {
			return fmt.Errorf("insert publisher %s: %w", id, err)
		}
	}

	// Step 2: backfill agents.publisher_id = account_id
	if _, err := tx.Exec(`
		UPDATE agents
		SET publisher_id = account_id
		WHERE publisher_id = '' OR publisher_id IS NULL
	`); err != nil {
		return fmt.Errorf("update agents.publisher_id: %w", err)
	}

	// Step 3: agents.secret_hash → device_tokens
	for _, a := range agents {
		// Orphan guard: skip if the publisher row doesn't exist.
		var pubExists int
		tx.QueryRow(`SELECT COUNT(*) FROM publishers WHERE id = ?`, a.publisherID).Scan(&pubExists)
		if pubExists == 0 {
			log.Printf("[migrate-phase-a] WARNING: skipping device_token for agent %q — publisher %q not found (orphan account)", a.name, a.publisherID)
			continue
		}

		// Idempotency check within the transaction.
		var exists int
		tx.QueryRow(`SELECT COUNT(*) FROM device_tokens WHERE token_hash = ?`, a.secretHash).Scan(&exists)
		if exists > 0 {
			continue
		}

		tokenID := uuid.New().String()
		if _, err := tx.Exec(`
			INSERT INTO device_tokens (id, publisher_id, token_hash, device_name, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, tokenID, a.publisherID, a.secretHash, "legacy:"+a.name, now); err != nil {
			return fmt.Errorf("insert device_token for agent %s: %w", a.name, err)
		}
	}

	// Step 4: mark migration as done (inside the same transaction).
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO schema_migrations (id, applied_at) VALUES (?, ?)
	`, migrationIDPhaseA, now); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}

	log.Printf("[migrate-phase-a] identity phase A complete (%d accounts, %d agents)", len(accountIDs), len(agents))
	return nil
}

// isMigrationApplied returns true if the given migration ID has been recorded
// in schema_migrations. Useful for tests that need to inspect migration state.
func (s *Store) isMigrationApplied(id string) bool {
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE id = ?`, id).Scan(&count)
	return count > 0
}

// resetMigration removes a migration record, allowing it to be re-run. Tests only.
func (s *Store) resetMigration(id string) {
	s.db.Exec(`DELETE FROM schema_migrations WHERE id = ?`, id)
}

