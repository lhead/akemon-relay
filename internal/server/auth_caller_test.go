package server

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akemon/akemon-relay/internal/config"
	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
	_ "modernc.org/sqlite"
)

// buildTestServer constructs a minimal Server wired to an in-memory store.
func buildTestServer(t *testing.T) (*Server, *store.Store, func()) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := store.NewFromDB(db)
	if err := st.Migrate(); err != nil {
		db.Close()
		t.Fatalf("migrate: %v", err)
	}
	cfg := config.Default()
	r := relay.New(cfg, st)
	srv := &Server{
		relay:        r,
		config:       cfg,
		limiter:      newRateLimiter(60, time.Second),
		mux:          http.NewServeMux(),
		termSessions: make(map[string]*terminalSession),
		taskSessions: make(map[string]*taskStreamSession),
	}
	return srv, st, func() { db.Close() }
}

func req(token string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestResolveCaller_NoToken(t *testing.T) {
	srv, _, cleanup := buildTestServer(t)
	defer cleanup()

	caller := srv.ResolveCaller(req(""))
	if caller.Kind != "anonymous" {
		t.Errorf("expected anonymous, got %q", caller.Kind)
	}
}

func TestResolveCaller_ValidDeviceToken(t *testing.T) {
	srv, st, cleanup := buildTestServer(t)
	defer cleanup()

	// Seed a publisher + device_token (pre-hashed — we use the raw token as hash for test simplicity)
	now := time.Now().Unix()
	st.DB().Exec(`INSERT INTO publishers (id, account_id, display_name, created_at) VALUES ('pub1','u-pub1','Alice',?)`, now)
	st.DB().Exec(`INSERT INTO device_tokens (id, publisher_id, token_hash, device_name, created_at) VALUES ('dt1','pub1','mytoken','test-device',?)`, now)

	caller := srv.ResolveCaller(req("mytoken"))
	if caller.Kind != "owner" {
		t.Errorf("expected owner, got %q", caller.Kind)
	}
	if caller.PublisherID != "pub1" {
		t.Errorf("expected PublisherID=pub1, got %q", caller.PublisherID)
	}
}

func TestResolveCaller_RevokedToken(t *testing.T) {
	srv, st, cleanup := buildTestServer(t)
	defer cleanup()

	now := time.Now().Unix()
	st.DB().Exec(`INSERT INTO publishers (id, account_id, display_name, created_at) VALUES ('pub2','u-pub2','Bob',?)`, now)
	st.DB().Exec(`INSERT INTO device_tokens (id, publisher_id, token_hash, device_name, created_at, revoked_at) VALUES ('dt2','pub2','revokedtoken','dev',?,?)`, now, now)

	caller := srv.ResolveCaller(req("revokedtoken"))
	if caller.Kind != "anonymous" {
		t.Errorf("expected anonymous for revoked token, got %q", caller.Kind)
	}
}

func TestResolveCaller_ExpiredToken(t *testing.T) {
	srv, st, cleanup := buildTestServer(t)
	defer cleanup()

	past := time.Now().Unix() - 3600
	now := time.Now().Unix()
	st.DB().Exec(`INSERT INTO publishers (id, account_id, display_name, created_at) VALUES ('pub3','u-pub3','Carol',?)`, now)
	st.DB().Exec(`INSERT INTO device_tokens (id, publisher_id, token_hash, device_name, created_at, expires_at) VALUES ('dt3','pub3','expiredtoken','dev',?,?)`, now, past)

	caller := srv.ResolveCaller(req("expiredtoken"))
	if caller.Kind != "anonymous" {
		t.Errorf("expected anonymous for expired token, got %q", caller.Kind)
	}
}

func TestResolveCaller_UnknownToken(t *testing.T) {
	srv, _, cleanup := buildTestServer(t)
	defer cleanup()

	caller := srv.ResolveCaller(req("totally-unknown-token"))
	if caller.Kind != "anonymous" {
		t.Errorf("expected anonymous for unknown token, got %q", caller.Kind)
	}
}

func TestResolveCaller_LegacyDeviceToken(t *testing.T) {
	srv, st, cleanup := buildTestServer(t)
	defer cleanup()

	// Simulate a legacy agent token: device_name = "legacy:<agent_name>"
	now := time.Now().Unix()
	st.DB().Exec(`INSERT INTO publishers (id, account_id, display_name, created_at) VALUES ('pub4','u-pub4','Dave',?)`, now)
	st.DB().Exec(`INSERT INTO accounts (id, first_seen, last_active) VALUES ('pub4',?,?)`, "2026-01-01", "2026-01-01")
	st.DB().Exec(`INSERT INTO agents (id, name, account_id, secret_hash, access_hash, first_registered) VALUES ('agt4','my-agent','pub4','legacyhash','','2026-01-01')`)
	st.DB().Exec(`INSERT INTO device_tokens (id, publisher_id, token_hash, device_name, created_at) VALUES ('dt4','pub4','legacyhash','legacy:my-agent',?)`, now)

	caller := srv.ResolveCaller(req("legacyhash"))
	if caller.Kind != "owner" {
		t.Errorf("expected owner, got %q", caller.Kind)
	}
	if caller.AgentID != "agt4" {
		t.Errorf("expected AgentID=agt4, got %q", caller.AgentID)
	}
}
