package store

import (
	"testing"
)

func TestCreateAndGetAgent(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("alice", "claude", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	got, err := s.GetAgentByName("alice")
	if err != nil {
		t.Fatalf("GetAgentByName: %v", err)
	}
	if got == nil {
		t.Fatal("expected agent, got nil")
	}
	if got.Name != "alice" {
		t.Errorf("name: want alice, got %s", got.Name)
	}
	if got.Engine != "claude" {
		t.Errorf("engine: want claude, got %s", got.Engine)
	}
	if !got.Public {
		t.Error("expected public=true")
	}
}

func TestGetAgentByID(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("bob", "codex", false)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	got, err := s.GetAgentByID(a.ID)
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if got == nil || got.ID != a.ID {
		t.Errorf("GetAgentByID returned wrong agent: %+v", got)
	}
}

func TestUpdateAgentPublic(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("carol", "gemini", false)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if err := s.UpdateAgentPublic("carol", true); err != nil {
		t.Fatalf("UpdateAgentPublic: %v", err)
	}

	got, _ := s.GetAgentByName("carol")
	if !got.Public {
		t.Error("expected public=true after update")
	}
}

func TestDeleteAgent(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("dave", "raw", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if err := s.DeleteAgent("dave"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	got, _ := s.GetAgentByName("dave")
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestMintAndSpendCredits(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("eve", "claude", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if err := s.MintCredit(a.ID, 10); err != nil {
		t.Fatalf("MintCredit: %v", err)
	}
	credits, err := s.GetAgentCredits("eve")
	if err != nil {
		t.Fatalf("GetAgentCredits: %v", err)
	}
	if credits != 10 {
		t.Errorf("credits: want 10, got %d", credits)
	}

	remaining, err := s.SpendAgentCredits("eve", 3, "test spend")
	if err != nil {
		t.Fatalf("SpendAgentCredits: %v", err)
	}
	if remaining != 7 {
		t.Errorf("remaining: want 7, got %d", remaining)
	}
}

func TestSpendCreditsInsufficient(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	a := makeAgent("frank", "claude", true)
	ensureAgentAccount(t, s, a)
	if err := s.CreateAgent(a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	_, err := s.SpendAgentCredits("frank", 100, "over-spend")
	if err == nil {
		t.Fatal("expected error for insufficient credits")
	}
}

func TestListAgents(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()

	names := []string{"g1", "g2", "g3"}
	for _, n := range names {
		a := makeAgent(n, "raw", true)
		ensureAgentAccount(t, s, a)
		if err := s.CreateAgent(a); err != nil {
			t.Fatalf("CreateAgent %s: %v", n, err)
		}
	}

	agents, err := s.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 3 {
		t.Errorf("ListAgents: want 3, got %d", len(agents))
	}
}

func TestComputeLevel(t *testing.T) {
	cases := []struct {
		tasks int
		want  int
	}{
		{0, 1},
		{1, 1},
		{4, 2},
		{9, 3},
		{100, 10},
	}
	for _, c := range cases {
		got := computeLevel(c.tasks)
		if got != c.want {
			t.Errorf("computeLevel(%d): want %d, got %d", c.tasks, c.want, got)
		}
	}
}
