package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/store"
)

// seedPublisherAndAgent creates the test fixtures for Bug 1 regression:
//
//	publisher P, agent A (name=my-opencode, seller agent T (name=test-seller)
func seedBug1Fixtures(t *testing.T, st *store.Store) (publisherID, agentAToken, agentASecretHash, sellerAgentID string) {
	t.Helper()
	now := time.Now().Unix()
	nowStr := time.Now().UTC().Format(time.RFC3339)

	publisherID = "publisher-uuid-1"
	agentAToken = "ak_secret_agent_a"
	sellerAgentID = "seller-uuid-1"

	var err error
	agentASecretHash, err = auth.HashToken(agentAToken)
	if err != nil {
		t.Fatalf("hash token: %v", err)
	}

	// publisher
	st.DB().Exec(`INSERT INTO publishers (id, account_id, display_name, created_at) VALUES (?,?,?,?)`,
		publisherID, "u-pub1", "Alice", now)

	// accounts (needed for agents FK)
	st.DB().Exec(`INSERT OR IGNORE INTO accounts (id, first_seen, last_active) VALUES (?,?,?)`,
		publisherID, nowStr, nowStr)
	st.DB().Exec(`INSERT OR IGNORE INTO accounts (id, first_seen, last_active) VALUES ('seller-acct',?,?)`, nowStr, nowStr)

	// agent A (buyer agent, name=my-opencode, belongs to publisherID) — real bcrypt hash
	st.DB().Exec(`INSERT INTO agents (id, name, account_id, secret_hash, access_hash, first_registered, publisher_id)
		VALUES ('agent-a','my-opencode',?,?,?,?,?)`,
		publisherID, agentASecretHash, agentASecretHash, nowStr, publisherID)

	// device_token for agent A (maps raw token → publisher)
	st.DB().Exec(`INSERT INTO device_tokens (id, publisher_id, token_hash, device_name, created_at)
		VALUES ('dt-a',?,?,?,?)`,
		publisherID, agentAToken, "legacy:my-opencode", now)

	// agent T (seller, different account, public so any authenticated user can place orders)
	st.DB().Exec(`INSERT INTO agents (id, name, account_id, secret_hash, access_hash, first_registered, publisher_id, public)
		VALUES (?,?,'seller-acct','hash-t','ahash-t',?,?,1)`,
		sellerAgentID, "test-seller", nowStr, "seller-acct")

	return
}

// TestBug1_WebChatOrderUsesPublisherID verifies that when a human user (authenticated
// via a device_token that maps to a publisher) places an ad-hoc order, the resulting
// order row has buyer_publisher_id = publisher.id and buyer_name = publisher.display_name,
// NOT buyer_agent_id = agent.id or buyer_name = "my-opencode".
func TestBug1_WebChatOrderUsesPublisherID(t *testing.T) {
	srv, st, cleanup := buildTestServer(t)
	defer cleanup()

	publisherID, token, _, _ := seedBug1Fixtures(t, st)

	body := `{"task":"hello from web chat"}`
	r := httptest.NewRequest("POST", "/v1/agent/test-seller/orders",
		strings.NewReader(body))
	r.SetPathValue("name", "test-seller")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	srv.handleCreateAdHocOrder(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		OrderID string `json:"order_id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil || resp.OrderID == "" {
		t.Fatalf("invalid response: %v / %s", err, w.Body.String())
	}

	order, err := st.GetOrder(resp.OrderID)
	if err != nil || order == nil {
		t.Fatalf("GetOrder: %v", err)
	}

	if order.BuyerPublisherID != publisherID {
		t.Errorf("buyer_publisher_id: got %q, want %q", order.BuyerPublisherID, publisherID)
	}
	if order.BuyerName != "Alice" {
		t.Errorf("buyer_name: got %q, want %q (publisher display_name)", order.BuyerName, "Alice")
	}
	if order.BuyerAgentID != "" {
		t.Errorf("buyer_agent_id should be empty for human publisher order, got %q", order.BuyerAgentID)
	}
}

// TestBug1_AgentToAgentOrderUnchanged verifies that explicit agent-to-agent orders
// (with buyer_agent_id in the request body) still go through the old path.
func TestBug1_AgentToAgentOrderUnchanged(t *testing.T) {
	srv, st, cleanup := buildTestServer(t)
	defer cleanup()

	_, token, _, _ := seedBug1Fixtures(t, st)

	// Explicit buyer_agent_id — this is agent-to-agent
	body := `{"task":"agent task","buyer_agent_id":"my-opencode"}`
	r := httptest.NewRequest("POST", "/v1/agent/test-seller/orders",
		strings.NewReader(body))
	r.SetPathValue("name", "test-seller")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)

	w := httptest.NewRecorder()
	srv.handleCreateAdHocOrder(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		OrderID string `json:"order_id"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	order, _ := st.GetOrder(resp.OrderID)
	if order == nil {
		t.Fatal("order not found")
	}
	// agent-to-agent: buyer_agent_id should be filled
	if order.BuyerAgentID == "" {
		t.Error("expected buyer_agent_id to be set for explicit agent-to-agent order")
	}
	// publisher_id should not be set in this path
	if order.BuyerPublisherID != "" {
		t.Errorf("buyer_publisher_id should be empty for agent-to-agent order, got %q", order.BuyerPublisherID)
	}
}

// TestBug1_AnonymousOrderHasNoBuyerPublisher verifies that unauthenticated (anonymous)
// requests still create orders with empty buyer_publisher_id.
func TestBug1_AnonymousOrderHasNoBuyerPublisher(t *testing.T) {
	srv, st, cleanup := buildTestServer(t)
	defer cleanup()

	_, _, _, _ = seedBug1Fixtures(t, st)

	// Make target agent public so no auth required
	st.DB().Exec(`UPDATE agents SET public = 1 WHERE name = 'test-seller'`)

	body := `{"task":"anonymous order"}`
	r := httptest.NewRequest("POST", "/v1/agent/test-seller/orders",
		strings.NewReader(body))
	r.SetPathValue("name", "test-seller")
	r.Header.Set("Content-Type", "application/json")
	// No Authorization header

	w := httptest.NewRecorder()
	srv.handleCreateAdHocOrder(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		OrderID string `json:"order_id"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	order, _ := st.GetOrder(resp.OrderID)
	if order == nil {
		t.Fatal("order not found")
	}
	if order.BuyerPublisherID != "" {
		t.Errorf("anonymous order should have empty buyer_publisher_id, got %q", order.BuyerPublisherID)
	}
}
