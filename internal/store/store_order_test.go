package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// helper to create an agent pair (buyer + seller) and return seller agent
func setupTwoAgents(t *testing.T, s *Store) (seller *Agent, buyer *Agent) {
	t.Helper()
	seller = makeAgent("seller", "claude", true)
	ensureAgentAccount(t, s, seller)
	if err := s.CreateAgent(seller); err != nil {
		t.Fatalf("CreateAgent seller: %v", err)
	}
	buyer = makeAgent("buyer", "codex", true)
	ensureAgentAccount(t, s, buyer)
	if err := s.CreateAgent(buyer); err != nil {
		t.Fatalf("CreateAgent buyer: %v", err)
	}
	return
}

func makeOrder(sellerAgent, buyerAgent *Agent) *Order {
	return &Order{
		ID:              uuid.New().String(),
		SellerAgentID:   sellerAgent.ID,
		SellerAgentName: sellerAgent.Name,
		BuyerAgentID:    buyerAgent.ID,
		BuyerTask:       "do something",
		TotalPrice:      10,
		HumanOrigin:     false,
	}
}

func TestCreateAndGetOrder(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	seller, buyer := setupTwoAgents(t, s)
	o := makeOrder(seller, buyer)

	if err := s.CreateOrder(o); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	got, err := s.GetOrder(o.ID)
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if got == nil {
		t.Fatal("expected order, got nil")
	}
	if got.Status != "pending" {
		t.Errorf("status: want pending, got %s", got.Status)
	}
	if got.SellerAgentID != seller.ID {
		t.Errorf("seller mismatch: want %s, got %s", seller.ID, got.SellerAgentID)
	}
}

func TestGetOrderNotFound(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	got, err := s.GetOrder("nonexistent-id")
	if err != nil {
		t.Fatalf("GetOrder error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown order, got %+v", got)
	}
}

func TestCancelOrder(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	seller, buyer := setupTwoAgents(t, s)
	o := makeOrder(seller, buyer)

	if err := s.CreateOrder(o); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	rowsAffected, err := s.CancelOrder(o.ID)
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if rowsAffected != 1 {
		t.Errorf("CancelOrder rowsAffected: want 1, got %d", rowsAffected)
	}

	got, _ := s.GetOrder(o.ID)
	if got.Status != "cancelled" {
		t.Errorf("status after cancel: want cancelled, got %s", got.Status)
	}
}

func TestAcceptOrderWithEscrow_HumanOrigin(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	seller, buyer := setupTwoAgents(t, s)
	o := makeOrder(seller, buyer)
	o.HumanOrigin = true // no escrow for human-origin
	if err := s.CreateOrder(o); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	// escrow=0 for human origin
	if err := s.AcceptOrderWithEscrow(o.ID, "", 10, 0, 30); err != nil {
		t.Fatalf("AcceptOrderWithEscrow: %v", err)
	}

	got, _ := s.GetOrder(o.ID)
	if got.Status != "processing" {
		t.Errorf("status after accept: want processing, got %s", got.Status)
	}
}

func TestAcceptOrderWithEscrow_AgentToBuyerDeduct(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	seller, buyer := setupTwoAgents(t, s)
	// Give buyer 50 credits
	if err := s.MintCredit(buyer.ID, 50); err != nil {
		t.Fatalf("MintCredit buyer: %v", err)
	}

	o := makeOrder(seller, buyer)
	if err := s.CreateOrder(o); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	// escrow=10 from buyer
	if err := s.AcceptOrderWithEscrow(o.ID, buyer.ID, 10, 10, 30); err != nil {
		t.Fatalf("AcceptOrderWithEscrow: %v", err)
	}

	credits, _ := s.GetAgentCredits("buyer")
	if credits != 40 {
		t.Errorf("buyer credits after escrow: want 40, got %d", credits)
	}
}

func TestDeliverOrder(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	seller, buyer := setupTwoAgents(t, s)
	if err := s.MintCredit(buyer.ID, 50); err != nil {
		t.Fatalf("MintCredit: %v", err)
	}

	o := makeOrder(seller, buyer)
	if err := s.CreateOrder(o); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if err := s.AcceptOrderWithEscrow(o.ID, buyer.ID, 10, 10, 30); err != nil {
		t.Fatalf("AcceptOrderWithEscrow: %v", err)
	}

	if err := s.DeliverOrderWithCredits(o.ID, "result text", seller.ID, 10, ""); err != nil {
		t.Fatalf("DeliverOrderWithCredits: %v", err)
	}

	got, _ := s.GetOrder(o.ID)
	if got.Status != "completed" {
		t.Errorf("status after deliver: want completed, got %s", got.Status)
	}
	if got.ResultText != "result text" {
		t.Errorf("result_text: want 'result text', got %q", got.ResultText)
	}

	// Seller should have received the escrow credits
	sellerCredits, _ := s.GetAgentCredits("seller")
	if sellerCredits != 10 {
		t.Errorf("seller credits after delivery: want 10, got %d", sellerCredits)
	}
}

func TestExtendOrderTimeout(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	seller, buyer := setupTwoAgents(t, s)
	if err := s.MintCredit(buyer.ID, 50); err != nil {
		t.Fatalf("MintCredit: %v", err)
	}

	o := makeOrder(seller, buyer)
	if err := s.CreateOrder(o); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if err := s.AcceptOrderWithEscrow(o.ID, buyer.ID, 10, 10, 30); err != nil {
		t.Fatalf("AcceptOrderWithEscrow: %v", err)
	}

	before, _ := s.GetOrder(o.ID)
	if err := s.ExtendOrderTimeout(o.ID, 60); err != nil {
		t.Fatalf("ExtendOrderTimeout: %v", err)
	}
	after, _ := s.GetOrder(o.ID)

	parseTime := func(s string) time.Time {
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
			if t2, err := time.Parse(layout, s); err == nil {
				return t2
			}
		}
		return time.Time{}
	}
	tBefore := parseTime(before.TimeoutAt)
	tAfter := parseTime(after.TimeoutAt)
	if !tAfter.After(tBefore) {
		t.Errorf("timeout_at should increase after extend: before=%s after=%s", before.TimeoutAt, after.TimeoutAt)
	}
}
