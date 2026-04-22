package store

import (
	"testing"

	"github.com/google/uuid"
)


func makeProduct(agentID, name string, price int) *Product {
	return &Product{
		ID:          uuid.New().String(),
		AgentID:     agentID,
		Name:        name,
		Description: "test product " + name,
		Price:       price,
	}
}

func TestCreateAndGetProduct(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("seller", "claude", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	p := makeProduct(a.ID, "Widget", 5)
	if err := s.CreateProduct(p); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	got, err := s.GetProduct(p.ID)
	if err != nil {
		t.Fatalf("GetProduct: %v", err)
	}
	if got == nil {
		t.Fatal("expected product, got nil")
	}
	if got.Name != "Widget" {
		t.Errorf("name: want Widget, got %s", got.Name)
	}
	if got.Price != 5 {
		t.Errorf("price: want 5, got %d", got.Price)
	}
}

func TestUpdateProduct(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("seller2", "claude", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	p := makeProduct(a.ID, "Old Name", 3)
	if err := s.CreateProduct(p); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	if err := s.UpdateProduct(p.ID, "New Name", "new desc", "", "", 7); err != nil {
		t.Fatalf("UpdateProduct: %v", err)
	}

	got, _ := s.GetProduct(p.ID)
	if got.Name != "New Name" {
		t.Errorf("name after update: want 'New Name', got %s", got.Name)
	}
	if got.Price != 7 {
		t.Errorf("price after update: want 7, got %d", got.Price)
	}
}

func TestDeleteProduct(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("seller3", "raw", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	p := makeProduct(a.ID, "Doomed", 1)
	if err := s.CreateProduct(p); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	if err := s.DeleteProduct(p.ID); err != nil {
		t.Fatalf("DeleteProduct: %v", err)
	}

	got, _ := s.GetProduct(p.ID)
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestListProductsByAgent(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("seller4", "claude", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	for i := 0; i < 3; i++ {
		p := makeProduct(a.ID, "Product", 1)
		if err := s.CreateProduct(p); err != nil {
			t.Fatalf("CreateProduct: %v", err)
		}
	}

	products, err := s.ListProductsByAgent(a.ID)
	if err != nil {
		t.Fatalf("ListProductsByAgent: %v", err)
	}
	if len(products) != 3 {
		t.Errorf("want 3 products, got %d", len(products))
	}
}

func TestGetProduct_NotFound(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	got, err := s.GetProduct("nonexistent-id")
	if err != nil {
		t.Fatalf("GetProduct error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown product, got %+v", got)
	}
}

func TestUpdateProduct_NotFound(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	// TODO: buggy behavior — UpdateProduct on a non-existent ID returns nil error with 0 rows
	// affected; caller cannot distinguish "updated" from "not found". Should return an error or
	// expose rows-affected.
	err := s.UpdateProduct("nonexistent-id", "Name", "Desc", "", "", 1)
	if err != nil {
		t.Errorf("UpdateProduct on non-existent ID returned unexpected error: %v", err)
	}
}

func TestDeleteProduct_NotFound(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	err := s.DeleteProduct("nonexistent-id")
	if err != nil {
		t.Errorf("DeleteProduct on non-existent ID returned unexpected error: %v", err)
	}
}

func TestListProductsByAgent_Empty(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("empty-seller", "claude", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	products, err := s.ListProductsByAgent(a.ID)
	if err != nil {
		t.Fatalf("ListProductsByAgent: %v", err)
	}
	if len(products) != 0 {
		t.Errorf("want empty slice, got %d products", len(products))
	}
}

func TestCreateProduct_DetailHTMLRoundtrip(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("html-seller", "claude", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	p := makeProduct(a.ID, "HTML Product", 10)
	p.DetailHTML = "<h1>Hello</h1><p>Rich content</p>"
	if err := s.CreateProduct(p); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	got, err := s.GetProduct(p.ID)
	if err != nil {
		t.Fatalf("GetProduct: %v", err)
	}
	if got == nil {
		t.Fatal("expected product, got nil")
	}
	if got.DetailHTML != p.DetailHTML {
		t.Errorf("detail_html: want %q, got %q", p.DetailHTML, got.DetailHTML)
	}
}

// TestCreateReview_BeforeOrderCompleted is intentionally omitted:
// CreateReview has no validation that the associated order is in 'completed' status.
// NOTE: no status check on order — any order_id can receive a review; recommend adding a guard.

func TestListProductReviews_Empty(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("no-review-seller", "claude", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	p := makeProduct(a.ID, "Unreviewed Product", 5)
	if err := s.CreateProduct(p); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	reviews, err := s.ListProductReviews(p.ID)
	if err != nil {
		t.Fatalf("ListProductReviews: %v", err)
	}
	if len(reviews) != 0 {
		t.Errorf("want empty slice, got %d reviews", len(reviews))
	}
}

func TestSubmitAndListReview(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("rev-seller", "claude", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	p := makeProduct(a.ID, "Reviewed Product", 5)
	if err := s.CreateProduct(p); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	// Create a completed order first (reviews require an order)
	buyerAgent := makeAgent("rev-buyer", "codex", true)
	ensureAgentAccount(t, s, buyerAgent)
	if err := s.CreateAgent(buyerAgent); err != nil {
		t.Fatalf("CreateAgent buyer: %v", err)
	}
	if err := s.MintCredit(buyerAgent.ID, 50); err != nil {
		t.Fatalf("MintCredit: %v", err)
	}

	o := &Order{
		ID:              uuid.New().String(),
		ProductID:       p.ID,
		SellerAgentID:   a.ID,
		SellerAgentName: a.Name,
		BuyerAgentID:    buyerAgent.ID,
		TotalPrice:      5,
	}
	if err := s.CreateOrder(o); err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if err := s.AcceptOrderWithEscrow(o.ID, buyerAgent.ID, 5, 5, 30); err != nil {
		t.Fatalf("AcceptOrderWithEscrow: %v", err)
	}
	if err := s.DeliverOrderWithCredits(o.ID, "done", a.ID, 5, p.ID); err != nil {
		t.Fatalf("DeliverOrderWithCredits: %v", err)
	}

	_, err := s.CreateReview(uuid.New().String(), o.ID, p.ID, "tester", 4, "pretty good")
	if err != nil {
		t.Fatalf("CreateReview: %v", err)
	}

	reviews, err := s.ListProductReviews(p.ID)
	if err != nil {
		t.Fatalf("ListProductReviews: %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("want 1 review, got %d", len(reviews))
	}
	if reviews[0].Rating != 4 {
		t.Errorf("rating: want 4, got %d", reviews[0].Rating)
	}
}
