package store

import (
	"database/sql"
	"fmt"
	"log"
	"math"
	"time"
)

// --- Accounts ---

func (s *Store) EnsureAccount(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO accounts (id, first_seen, last_active) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET last_active = ?
	`, id, now, now, now)
	return err
}

func (s *Store) GetAgentByID(id string) (*Agent, error) {
	a := &Agent{}
	var pub int
	err := s.db.QueryRow(`
		SELECT id, name, account_id, secret_hash, access_hash, description,
		       engine, avatar, public, max_tasks,
		       first_registered, total_tasks, total_uptime_s, last_connected,
		       COALESCE(tags, ''), COALESCE(credits, 0), COALESCE(price, 1),
		       COALESCE(self_intro, ''), COALESCE(canvas, ''), COALESCE(mood, ''),
		       COALESCE(profile_html, '')
		FROM agents WHERE id = ?
	`, id).Scan(&a.ID, &a.Name, &a.AccountID, &a.SecretHash, &a.AccessHash,
		&a.Description, &a.Engine, &a.Avatar, &pub, &a.MaxTasks,
		&a.FirstRegistered, &a.TotalTasks, &a.TotalUptimeS,
		&a.LastConnected, &a.Tags, &a.Credits, &a.Price,
		&a.SelfIntro, &a.Canvas, &a.Mood, &a.ProfileHTML)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.Public = pub == 1
	return a, nil
}

func (s *Store) GetAgentByName(name string) (*Agent, error) {
	a := &Agent{}
	var pub int
	err := s.db.QueryRow(`
		SELECT id, name, account_id, secret_hash, access_hash, description,
		       engine, avatar, public, max_tasks,
		       first_registered, total_tasks, total_uptime_s, last_connected,
		       COALESCE(tags, ''), COALESCE(credits, 0), COALESCE(price, 1),
		       COALESCE(self_intro, ''), COALESCE(canvas, ''), COALESCE(mood, ''),
		       COALESCE(profile_html, '')
		FROM agents WHERE name = ?
	`, name).Scan(&a.ID, &a.Name, &a.AccountID, &a.SecretHash, &a.AccessHash,
		&a.Description, &a.Engine, &a.Avatar, &pub, &a.MaxTasks,
		&a.FirstRegistered, &a.TotalTasks, &a.TotalUptimeS,
		&a.LastConnected, &a.Tags, &a.Credits, &a.Price,
		&a.SelfIntro, &a.Canvas, &a.Mood, &a.ProfileHTML)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.Public = pub == 1
	return a, nil
}

// ListAgentSecrets returns (id, secret_hash) for every agent. Used to identify
// an agent by bearer token when the caller did not pass buyer_agent_id.
// Order-creation paths hit this at most once per request, and agent count stays small.
func (s *Store) ListAgentSecrets() ([]struct {
	ID         string
	SecretHash string
}, error) {
	rows, err := s.db.Query(`SELECT id, secret_hash FROM agents WHERE secret_hash != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct {
		ID         string
		SecretHash string
	}
	for rows.Next() {
		var r struct {
			ID         string
			SecretHash string
		}
		if err := rows.Scan(&r.ID, &r.SecretHash); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func randomAvatar(engine string) string {
	if engine == "human" {
		return ""
	}
	return avatars[time.Now().UnixNano()%int64(len(avatars))]
}

func (s *Store) CreateAgent(a *Agent) error {
	pub := 0
	if a.Public {
		pub = 1
	}
	if a.Avatar == "" {
		a.Avatar = randomAvatar(a.Engine)
	}
	price := a.Price
	if price <= 0 {
		price = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO agents (id, name, account_id, secret_hash, access_hash, description, engine, avatar, public, max_tasks, first_registered, tags, price, credits)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
	`, a.ID, a.Name, a.AccountID, a.SecretHash, a.AccessHash, a.Description, a.Engine, a.Avatar, pub, a.MaxTasks, a.FirstRegistered, a.Tags, price)
	return err
}

func (s *Store) UpdateAgentOnConnect(name, description, engine string, public bool, tags string, price int, avatar string) error {
	pub := 0
	if public {
		pub = 1
	}
	if price <= 0 {
		price = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if avatar != "" {
		_, err := s.db.Exec(`
			UPDATE agents SET description = ?, engine = ?, public = ?, last_connected = ?, tags = ?, price = ?, avatar = ? WHERE name = ?
		`, description, engine, pub, now, tags, price, avatar, name)
		return err
	}
	_, err := s.db.Exec(`
		UPDATE agents SET description = ?, engine = ?, public = ?, last_connected = ?, tags = ?, price = ? WHERE name = ?
	`, description, engine, pub, now, tags, price, name)
	return err
}

func (s *Store) UpdateAgentPublic(name string, public bool) error {
	pub := 0
	if public {
		pub = 1
	}
	_, err := s.db.Exec(`UPDATE agents SET public = ? WHERE name = ?`, pub, name)
	return err
}

func (s *Store) DeleteAgent(name string) error {
	agent, err := s.GetAgentByName(name)
	if err != nil || agent == nil {
		return fmt.Errorf("agent not found")
	}
	// Delete related records first (FK constraints)
	s.db.Exec(`DELETE FROM tasks WHERE agent_id = ?`, agent.ID)
	s.db.Exec(`DELETE FROM connections WHERE agent_id = ?`, agent.ID)
	_, err = s.db.Exec(`DELETE FROM agents WHERE id = ?`, agent.ID)
	return err
}

func (s *Store) UpdateAgentPrice(name string, price int) error {
	_, err := s.db.Exec(`UPDATE agents SET price = ? WHERE name = ?`, price, name)
	return err
}

func (s *Store) UpdateAgentSelf(name, selfIntro, canvas, mood, profileHTML, broadcast, bioState, directives string) error {
	_, err := s.db.Exec(`UPDATE agents SET self_intro = ?, canvas = ?, mood = ?, profile_html = ?,
		latest_broadcast = CASE WHEN ? != '' THEN ? ELSE latest_broadcast END,
		bio_state = CASE WHEN ? != '' THEN ? ELSE bio_state END,
		directives = CASE WHEN ? != '' THEN ? ELSE directives END
		WHERE name = ?`,
		selfIntro, canvas, mood, profileHTML,
		broadcast, broadcast,
		bioState, bioState,
		directives, directives,
		name)
	return err
}

// SpendAgentCredits deducts credits by agent name. Returns remaining credits.
func (s *Store) SpendAgentCredits(name string, amount int, reason string) (int, error) {
	res, err := s.db.Exec(`UPDATE agents SET credits = credits - ? WHERE name = ? AND credits >= ?`, amount, name, amount)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, fmt.Errorf("insufficient credits")
	}
	var remaining int
	s.db.QueryRow(`SELECT COALESCE(credits, 0) FROM agents WHERE name = ?`, name).Scan(&remaining)
	log.Printf("[credits] %s spent %d credits (%s), remaining: %d", name, amount, reason, remaining)
	return remaining, nil
}

func (s *Store) IncrementAgentTasks(agentID string) error {
	_, err := s.db.Exec(`UPDATE agents SET total_tasks = total_tasks + 1 WHERE id = ?`, agentID)
	return err
}

// MintCredit creates new credits for an agent (from human usage).
// Fixed amount, independent of agent price.
func (s *Store) MintCredit(agentID string, amount int) error {
	_, err := s.db.Exec(`UPDATE agents SET credits = COALESCE(credits, 0) + ? WHERE id = ?`, amount, agentID)
	return err
}

// TransferCredits moves credits between agents (agent-to-agent calls).
// Credits callee with callee's price.
func (s *Store) TransferCredits(calleeAgentID string) (int, error) {
	var price int
	err := s.db.QueryRow(`SELECT COALESCE(price, 1) FROM agents WHERE id = ?`, calleeAgentID).Scan(&price)
	if err != nil {
		return 0, err
	}
	_, err = s.db.Exec(`UPDATE agents SET credits = COALESCE(credits, 0) + ? WHERE id = ?`, price, calleeAgentID)
	return price, err
}

func (s *Store) GetAgentCredits(name string) (int, error) {
	var credits int
	err := s.db.QueryRow(`SELECT COALESCE(credits, 0) FROM agents WHERE name = ?`, name).Scan(&credits)
	return credits, err
}

// DebitAgent subtracts credits from an agent. Fails if insufficient balance.
func (s *Store) DebitAgent(agentID string, amount int) error {
	res, err := s.db.Exec(`UPDATE agents SET credits = credits - ? WHERE id = ? AND credits >= ?`, amount, agentID, amount)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("insufficient credits")
	}
	return nil
}

// AgentToAgentTransfer atomically debits caller and credits callee.
// Returns the price transferred. Fails if caller has insufficient credits.
func (s *Store) AgentToAgentTransfer(callerAgentID, calleeAgentID string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Get callee price
	var price int
	if err := tx.QueryRow(`SELECT COALESCE(price, 1) FROM agents WHERE id = ?`, calleeAgentID).Scan(&price); err != nil {
		return 0, fmt.Errorf("callee lookup: %w", err)
	}

	// Debit caller (fails if insufficient)
	res, err := tx.Exec(`UPDATE agents SET credits = credits - ? WHERE id = ? AND credits >= ?`, price, callerAgentID, price)
	if err != nil {
		return 0, fmt.Errorf("debit: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return 0, fmt.Errorf("insufficient credits")
	}

	// Credit callee
	if _, err := tx.Exec(`UPDATE agents SET credits = COALESCE(credits, 0) + ? WHERE id = ?`, price, calleeAgentID); err != nil {
		return 0, fmt.Errorf("credit: %w", err)
	}

	return price, tx.Commit()
}

// --- Account Credits ---

func (s *Store) GetAccountCredits(accountID string) (int, error) {
	var credits int
	err := s.db.QueryRow(`SELECT COALESCE(credits, 0) FROM accounts WHERE id = ?`, accountID).Scan(&credits)
	if err == sql.ErrNoRows {
		return 100, nil
	}
	return credits, err
}

func (s *Store) DebitAccount(accountID string, amount int) error {
	_, err := s.db.Exec(`UPDATE accounts SET credits = COALESCE(credits, 0) - ? WHERE id = ?`, amount, accountID)
	return err
}

// GetAnyAgentByAccount returns one agent for the given account (for secret key verification).
func (s *Store) GetAnyAgentByAccount(accountID string) (*Agent, error) {
	a := &Agent{}
	var pub int
	err := s.db.QueryRow(`
		SELECT id, name, account_id, secret_hash, access_hash, description,
		       engine, avatar, public, max_tasks,
		       first_registered, total_tasks, total_uptime_s, last_connected,
		       COALESCE(tags, ''), COALESCE(credits, 0), COALESCE(price, 1)
		FROM agents WHERE account_id = ? LIMIT 1
	`, accountID).Scan(&a.ID, &a.Name, &a.AccountID, &a.SecretHash, &a.AccessHash,
		&a.Description, &a.Engine, &a.Avatar, &pub, &a.MaxTasks,
		&a.FirstRegistered, &a.TotalTasks, &a.TotalUptimeS,
		&a.LastConnected, &a.Tags, &a.Credits, &a.Price)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.Public = pub == 1
	return a, nil
}

func computeLevel(successfulTasks int) int {
	if successfulTasks <= 0 {
		return 1
	}
	lv := int(math.Sqrt(float64(successfulTasks)))
	if lv < 1 {
		return 1
	}
	return lv
}

func (s *Store) ListAgents() ([]AgentListing, error) {
	rows, err := s.db.Query(`
		SELECT
			a.name, a.description, a.account_id, a.engine, a.avatar,
			a.public, a.max_tasks, a.total_tasks, a.first_registered, a.last_connected,
			COALESCE(a.tags, ''), COALESCE(a.credits, 100), COALESCE(a.price, 1),
			COALESCE((SELECT COUNT(*) FROM tasks t WHERE t.agent_id = a.id AND t.status = 'ok'), 0) as successful_tasks,
			COALESCE((SELECT AVG(t.duration_ms) FROM tasks t WHERE t.agent_id = a.id AND t.status = 'ok'), 0) as avg_ms,
			COALESCE(a.self_intro, ''), COALESCE(a.canvas, ''), COALESCE(a.mood, ''),
			COALESCE(a.profile_html, ''),
			COALESCE((SELECT COUNT(*) FROM products p WHERE p.agent_id = a.id AND p.status = 'active'), 0) as product_count,
			COALESCE(a.bio_state, '')
		FROM agents a
		ORDER BY a.total_tasks DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []AgentListing
	for rows.Next() {
		var a AgentListing
		var pub int
		var avgMs float64
		if err := rows.Scan(&a.Name, &a.Description, &a.AccountID, &a.Engine, &a.Avatar,
			&pub, &a.MaxTasks, &a.TotalTasks, &a.FirstRegistered, &a.LastConnected,
			&a.Tags, &a.Credits, &a.Price, &a.SuccessfulTasks, &avgMs,
			&a.SelfIntro, &a.Canvas, &a.Mood, &a.ProfileHTML, &a.ProductCount, &a.BioState); err != nil {
			return nil, err
		}
		a.Public = pub == 1
		a.Level = computeLevel(a.SuccessfulTasks)
		a.AvgResponseMs = int(avgMs)
		if a.TotalTasks > 0 {
			a.SuccessRate = float64(a.SuccessfulTasks) / float64(a.TotalTasks)
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func (s *Store) ListAgentsByAccount(accountID string) ([]AgentListing, error) {
	rows, err := s.db.Query(`
		SELECT
			a.name, a.description, a.account_id, a.engine, a.avatar,
			a.public, a.max_tasks, a.total_tasks, a.first_registered, a.last_connected,
			COALESCE(a.tags, ''), COALESCE(a.credits, 100), COALESCE(a.price, 1),
			COALESCE((SELECT COUNT(*) FROM tasks t WHERE t.agent_id = a.id AND t.status = 'ok'), 0) as successful_tasks,
			COALESCE((SELECT AVG(t.duration_ms) FROM tasks t WHERE t.agent_id = a.id AND t.status = 'ok'), 0) as avg_ms,
			COALESCE(a.self_intro, ''), COALESCE(a.canvas, ''), COALESCE(a.mood, ''),
			COALESCE(a.profile_html, ''),
			COALESCE((SELECT COUNT(*) FROM products p WHERE p.agent_id = a.id AND p.status = 'active'), 0) as product_count,
			COALESCE(a.bio_state, '')
		FROM agents a WHERE a.account_id = ?
		ORDER BY a.total_tasks DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []AgentListing
	for rows.Next() {
		var a AgentListing
		var pub int
		var avgMs float64
		if err := rows.Scan(&a.Name, &a.Description, &a.AccountID, &a.Engine, &a.Avatar,
			&pub, &a.MaxTasks, &a.TotalTasks, &a.FirstRegistered, &a.LastConnected,
			&a.Tags, &a.Credits, &a.Price, &a.SuccessfulTasks, &avgMs,
			&a.SelfIntro, &a.Canvas, &a.Mood, &a.ProfileHTML, &a.ProductCount, &a.BioState); err != nil {
			return nil, err
		}
		a.Public = pub == 1
		a.Level = computeLevel(a.SuccessfulTasks)
		a.AvgResponseMs = int(avgMs)
		if a.TotalTasks > 0 {
			a.SuccessRate = float64(a.SuccessfulTasks) / float64(a.TotalTasks)
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// --- Tasks ---

func (s *Store) RecordTask(id, agentID, status, publisherIP string, durationMs int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO tasks (id, agent_id, timestamp, duration_ms, status, publisher_ip)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, agentID, now, durationMs, status, publisherIP)
	return err
}

// --- Connections ---

func (s *Store) RecordConnect(id, agentID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO connections (id, agent_id, connected_at) VALUES (?, ?, ?)
	`, id, agentID, now)
	return err
}

func (s *Store) RecordDisconnect(id, reason string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE connections SET disconnected_at = ?, disconnect_reason = ? WHERE id = ?
	`, now, reason, id)
	return err
}

// GetAgentSuccessRate returns completed and total (completed+failed) order counts
func (s *Store) GetAgentSuccessRate(agentID string) (int, int, error) {
	var completed, failed int
	s.db.QueryRow(`SELECT COUNT(*) FROM orders WHERE seller_agent_id = ? AND status = 'completed'`, agentID).Scan(&completed)
	s.db.QueryRow(`SELECT COUNT(*) FROM orders WHERE seller_agent_id = ? AND status = 'failed'`, agentID).Scan(&failed)
	return completed, completed + failed, nil
}

