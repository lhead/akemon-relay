package store

import (
	"database/sql"
	"fmt"
	"log"
	"math"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Idempotent column additions for existing databases
	s.db.Exec(`ALTER TABLE agents ADD COLUMN tags TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE agents ADD COLUMN credits INTEGER DEFAULT 0`)
	s.db.Exec(`ALTER TABLE agents ADD COLUMN price INTEGER DEFAULT 1`)
	s.db.Exec(`ALTER TABLE accounts ADD COLUMN credits INTEGER DEFAULT 100`)
	s.db.Exec(`ALTER TABLE products ADD COLUMN detail_markdown TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE products ADD COLUMN detail_html TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE agents ADD COLUMN self_intro TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE agents ADD COLUMN canvas TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE agents ADD COLUMN mood TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE agents ADD COLUMN profile_html TEXT DEFAULT ''`)

	// Games table
	s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_games (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		agent_name TEXT NOT NULL,
		slug TEXT NOT NULL,
		title TEXT NOT NULL,
		description TEXT DEFAULT '',
		html TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(agent_name, slug)
	)`)

	// Reviews table
	s.db.Exec(`CREATE TABLE IF NOT EXISTS reviews (
		id             TEXT PRIMARY KEY,
		order_id       TEXT NOT NULL UNIQUE,
		product_id     TEXT NOT NULL,
		reviewer_name  TEXT NOT NULL,
		rating         INTEGER NOT NULL CHECK(rating >= 1 AND rating <= 5),
		comment        TEXT DEFAULT '',
		created_at     TEXT NOT NULL
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_reviews_product ON reviews(product_id)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_reviews_order ON reviews(order_id)`)

	// Suggestions table
	s.db.Exec(`CREATE TABLE IF NOT EXISTS suggestions (
		id          TEXT PRIMARY KEY,
		type        TEXT NOT NULL,
		target_name TEXT DEFAULT '',
		from_agent  TEXT NOT NULL,
		title       TEXT NOT NULL,
		content     TEXT NOT NULL,
		created_at  TEXT NOT NULL
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_suggestions_type ON suggestions(type)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_suggestions_target ON suggestions(target_name)`)

	// Notes table
	s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_notes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		agent_name TEXT NOT NULL,
		slug TEXT NOT NULL,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(agent_name, slug)
	)`)

	// Pages table
	s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_pages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		agent_name TEXT NOT NULL,
		slug TEXT NOT NULL,
		title TEXT NOT NULL,
		description TEXT DEFAULT '',
		html TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(agent_name, slug)
	)`)

	// Drop NOT NULL + foreign key on product_id (ad-hoc orders have no product)
	s.migrateOrdersDropProductNotNull()

	// Order system v2 — async orders
	s.db.Exec(`ALTER TABLE orders ADD COLUMN seller_agent_id TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN seller_agent_name TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN parent_order_id TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN buyer_task TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN offer_price INTEGER DEFAULT 0`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN escrow_amount INTEGER DEFAULT 0`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN retry_count INTEGER DEFAULT 0`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN max_retries INTEGER DEFAULT 5`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN timeout_at TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN accepted_at TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN failed_at TEXT DEFAULT ''`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_orders_seller ON orders(seller_agent_id)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_orders_parent ON orders(parent_order_id)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_orders_timeout ON orders(timeout_at)`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN human_origin INTEGER DEFAULT 0`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN trace TEXT DEFAULT ''`)
	// Migrate old pending orders: those with results → completed, without → cancelled
	s.db.Exec(`UPDATE orders SET status = 'completed', completed_at = created_at WHERE status = 'pending' AND result_text != '' AND result_text IS NOT NULL`)
	s.db.Exec(`UPDATE orders SET status = 'cancelled', completed_at = created_at WHERE status = 'pending' AND (result_text = '' OR result_text IS NULL)`)

	// Agent tasks table (Phase 2 — relay writes, agent polls)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_tasks (
		id TEXT PRIMARY KEY,
		agent_id TEXT NOT NULL,
		type TEXT NOT NULL,
		payload TEXT DEFAULT '{}',
		status TEXT DEFAULT 'pending',
		result TEXT DEFAULT '',
		created_at TEXT NOT NULL,
		claimed_at TEXT DEFAULT '',
		completed_at TEXT DEFAULT ''
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_tasks_agent ON agent_tasks(agent_id, status)`)

	// Execution logs — trace every task execution for observability & teaching
	s.db.Exec(`CREATE TABLE IF NOT EXISTS execution_logs (
		id TEXT PRIMARY KEY,
		agent_id TEXT NOT NULL,
		agent_name TEXT NOT NULL,
		type TEXT NOT NULL,
		ref_id TEXT DEFAULT '',
		status TEXT NOT NULL,
		error TEXT DEFAULT '',
		trace TEXT DEFAULT '',
		created_at TEXT NOT NULL
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_exlogs_agent ON execution_logs(agent_name, created_at)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_exlogs_status ON execution_logs(agent_name, status)`)

	// Broadcast field on agents
	s.db.Exec(`ALTER TABLE agents ADD COLUMN latest_broadcast TEXT DEFAULT ''`)

	// Bio-state + directives (pushed from agent harness)
	s.db.Exec(`ALTER TABLE agents ADD COLUMN bio_state TEXT DEFAULT ''`)
	s.db.Exec(`ALTER TABLE agents ADD COLUMN directives TEXT DEFAULT ''`)

	// Lessons table — teaching system: strong agents diagnose weak agent failures
	s.db.Exec(`CREATE TABLE IF NOT EXISTS lessons (
		id TEXT PRIMARY KEY,
		agent_name TEXT NOT NULL,
		topic TEXT NOT NULL,
		content TEXT NOT NULL,
		diagnosed_by TEXT NOT NULL,
		log_id TEXT DEFAULT '',
		created_at TEXT NOT NULL
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_lessons_agent ON lessons(agent_name, created_at)`)

	return nil
}

// migrateOrdersDropProductNotNull recreates the orders table without NOT NULL on product_id.
// SQLite doesn't support ALTER COLUMN, so we use the standard recreate pattern.
func (s *Store) migrateOrdersDropProductNotNull() {
	// Check if product_id still has NOT NULL constraint
	var hasNotNull bool
	rows, err := s.db.Query(`PRAGMA table_info(orders)`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dfltValue interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dfltValue, &pk); err != nil {
			continue
		}
		if name == "product_id" && notnull == 1 {
			hasNotNull = true
		}
	}
	if !hasNotNull {
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("[migrate] failed to begin transaction: %v", err)
		return
	}
	defer tx.Rollback()

	// Must disable foreign keys inside the migration (can't change mid-transaction on some drivers)
	if _, err := tx.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		log.Printf("[migrate] failed to disable foreign keys: %v", err)
		return
	}
	if _, err := tx.Exec(`CREATE TABLE orders_new AS SELECT * FROM orders`); err != nil {
		log.Printf("[migrate] failed to copy orders to orders_new: %v", err)
		return
	}
	if _, err := tx.Exec(`DROP TABLE orders`); err != nil {
		log.Printf("[migrate] failed to drop old orders table: %v", err)
		return
	}
	if _, err := tx.Exec(`CREATE TABLE orders (
		id             TEXT PRIMARY KEY,
		product_id     TEXT DEFAULT '' REFERENCES products(id),
		buyer_agent_id TEXT DEFAULT '',
		buyer_ip       TEXT DEFAULT '',
		deposit        INTEGER NOT NULL DEFAULT 0,
		total_price    INTEGER NOT NULL DEFAULT 0,
		status         TEXT DEFAULT 'pending',
		result_text    TEXT DEFAULT '',
		created_at     TEXT NOT NULL DEFAULT '',
		completed_at   TEXT DEFAULT '',
		seller_agent_id   TEXT DEFAULT '',
		seller_agent_name TEXT DEFAULT '',
		parent_order_id   TEXT DEFAULT '',
		buyer_task        TEXT DEFAULT '',
		offer_price       INTEGER DEFAULT 0,
		escrow_amount     INTEGER DEFAULT 0,
		retry_count       INTEGER DEFAULT 0,
		max_retries       INTEGER DEFAULT 5,
		timeout_at        TEXT DEFAULT '',
		accepted_at       TEXT DEFAULT '',
		failed_at         TEXT DEFAULT '',
		human_origin      INTEGER DEFAULT 0
	)`); err != nil {
		log.Printf("[migrate] failed to create new orders table: %v", err)
		return
	}
	if _, err := tx.Exec(`INSERT INTO orders SELECT *, 0 FROM orders_new`); err != nil {
		log.Printf("[migrate] failed to copy data into new orders table: %v", err)
		return
	}
	if _, err := tx.Exec(`DROP TABLE orders_new`); err != nil {
		log.Printf("[migrate] failed to drop orders_new: %v", err)
		return
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_orders_product ON orders(product_id)`); err != nil {
		log.Printf("[migrate] failed to create idx_orders_product: %v", err)
		return
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status)`); err != nil {
		log.Printf("[migrate] failed to create idx_orders_status: %v", err)
		return
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_orders_seller ON orders(seller_agent_id)`); err != nil {
		log.Printf("[migrate] failed to create idx_orders_seller: %v", err)
		return
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_orders_parent ON orders(parent_order_id)`); err != nil {
		log.Printf("[migrate] failed to create idx_orders_parent: %v", err)
		return
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_orders_timeout ON orders(timeout_at)`); err != nil {
		log.Printf("[migrate] failed to create idx_orders_timeout: %v", err)
		return
	}
	if _, err := tx.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		log.Printf("[migrate] failed to re-enable foreign keys: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("[migrate] failed to commit orders migration: %v", err)
		return
	}
	log.Printf("[migrate] successfully dropped NOT NULL on orders.product_id")
}

// --- Accounts ---

func (s *Store) EnsureAccount(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO accounts (id, first_seen, last_active) VALUES (?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET last_active = ?
	`, id, now, now, now)
	return err
}

// --- Agents ---

var avatars = []string{
	// 哺乳类
	"🐺", "🦊", "🦁", "🐯", "🐆", "🐻", "🐼", "🦄", "🐗", "🦘", "🦝", "🦬", "🦣",
	// 鸟类
	"🦅", "🦉", "🦚", "🦜", "🐧", "🦩", "🦢",
	// 龙/爬虫
	"🐉", "🐲", "🐊", "🦎", "🐢", "🐍", "🦕", "🦖",
	// 海洋
	"🐙", "🦑", "🐳", "🐬", "🦈", "🐡", "🦞", "🦐",
	// 虫类
	"🦋", "🐝", "🦂", "🐌",
	// 植物
	"🌵", "🌻", "🍀", "🌴", "🌲", "🍄", "🌸", "🌾",
}

type Agent struct {
	ID              string
	Name            string
	AccountID       string
	SecretHash      string
	AccessHash      string
	Description     string
	Engine          string
	Avatar          string
	Public          bool
	MaxTasks        int
	FirstRegistered string
	TotalTasks      int
	TotalUptimeS    int
	LastConnected   *string
	Tags            string // comma-separated
	Credits         int
	Price           int
	SelfIntro       string
	Canvas          string
	Mood            string
	ProfileHTML     string
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

type AgentListing struct {
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	AccountID       string  `json:"account_id"`
	Engine          string  `json:"engine"`
	Avatar          string  `json:"avatar"`
	Public          bool    `json:"public"`
	MaxTasks        int     `json:"max_tasks"`
	TotalTasks      int     `json:"total_tasks"`
	SuccessfulTasks int     `json:"successful_tasks"`
	Level           int     `json:"level"`
	SuccessRate     float64 `json:"success_rate"`
	AvgResponseMs   int     `json:"avg_response_ms"`
	FirstRegistered string  `json:"first_registered"`
	LastConnected   *string `json:"last_connected"`
	Tags            string  `json:"tags"`
	Credits         int     `json:"credits"`
	Price           int     `json:"price"`
	SelfIntro       string  `json:"self_intro"`
	Canvas          string  `json:"canvas"`
	Mood            string  `json:"mood"`
	ProfileHTML     string  `json:"profile_html"`
	ProductCount    int     `json:"product_count"`
	BioState        string  `json:"bio_state,omitempty"`
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

// --- Session Context ---

const maxContextBytes = 8192

func (s *Store) GetContext(agentName, sessionID string) (string, error) {
	var ctx string
	err := s.db.QueryRow(`
		SELECT context FROM session_context WHERE agent_name = ? AND session_id = ?
	`, agentName, sessionID).Scan(&ctx)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return ctx, err
}

func (s *Store) PutContext(agentName, sessionID, context string) error {
	if len(context) > maxContextBytes {
		context = context[len(context)-maxContextBytes:]
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO session_context (agent_name, session_id, context, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(agent_name, session_id) DO UPDATE SET context = ?, updated_at = ?
	`, agentName, sessionID, context, now, context, now)
	return err
}

// --- Products ---

type Product struct {
	ID             string `json:"id"`
	AgentID        string `json:"agent_id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	DetailMarkdown string `json:"detail_markdown,omitempty"`
	DetailHTML     string `json:"detail_html,omitempty"`
	Price          int    `json:"price"`
	Status         string `json:"status"`
	PurchaseCount  int    `json:"purchase_count"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type ProductListing struct {
	ID             string  `json:"id"`
	AgentID        string  `json:"agent_id"`
	AgentName      string  `json:"agent_name"`
	AgentAvatar    string  `json:"agent_avatar"`
	AgentEngine    string  `json:"agent_engine"`
	AgentOnline    bool    `json:"agent_online,omitempty"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	DetailMarkdown string  `json:"detail_markdown,omitempty"`
	Price          int     `json:"price"`
	PurchaseCount  int     `json:"purchase_count"`
	AvgRating      float64 `json:"avg_rating"`
	ReviewCount    int     `json:"review_count"`
	CreatedAt      string  `json:"created_at"`
}

func (s *Store) CreateProduct(p *Product) error {
	if p.Price <= 0 {
		p.Price = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO products (id, agent_id, name, description, detail_markdown, detail_html, price, status, purchase_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'active', 0, ?, ?)
	`, p.ID, p.AgentID, p.Name, p.Description, p.DetailMarkdown, p.DetailHTML, p.Price, now, now)
	return err
}

func (s *Store) ListProductsByAgent(agentID string) ([]Product, error) {
	rows, err := s.db.Query(`
		SELECT id, agent_id, name, description, COALESCE(detail_markdown, ''), price, status, purchase_count, created_at, updated_at
		FROM products WHERE agent_id = ? AND status = 'active' ORDER BY created_at DESC
	`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.AgentID, &p.Name, &p.Description, &p.DetailMarkdown, &p.Price, &p.Status, &p.PurchaseCount, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, rows.Err()
}

func (s *Store) ListAllProducts() ([]ProductListing, error) {
	rows, err := s.db.Query(`
		SELECT p.id, p.agent_id, a.name, a.avatar, a.engine,
		       p.name, p.description, COALESCE(p.detail_markdown, ''), p.price, p.purchase_count,
		       COALESCE((SELECT AVG(r.rating) FROM reviews r WHERE r.product_id = p.id), 0),
		       COALESCE((SELECT COUNT(*) FROM reviews r WHERE r.product_id = p.id), 0),
		       p.created_at
		FROM products p
		JOIN agents a ON a.id = p.agent_id
		WHERE p.status = 'active'
		ORDER BY p.purchase_count DESC, p.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []ProductListing
	for rows.Next() {
		var p ProductListing
		if err := rows.Scan(&p.ID, &p.AgentID, &p.AgentName, &p.AgentAvatar, &p.AgentEngine,
			&p.Name, &p.Description, &p.DetailMarkdown, &p.Price, &p.PurchaseCount,
			&p.AvgRating, &p.ReviewCount, &p.CreatedAt); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, rows.Err()
}

func (s *Store) GetProduct(id string) (*Product, error) {
	p := &Product{}
	err := s.db.QueryRow(`
		SELECT id, agent_id, name, description, COALESCE(detail_markdown, ''), COALESCE(detail_html, ''), price, status, purchase_count, created_at, updated_at
		FROM products WHERE id = ?
	`, id).Scan(&p.ID, &p.AgentID, &p.Name, &p.Description, &p.DetailMarkdown, &p.DetailHTML, &p.Price, &p.Status, &p.PurchaseCount, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) UpdateProduct(id, name, description, detailMarkdown, detailHTML string, price int) error {
	if price <= 0 {
		price = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE products SET name = ?, description = ?, detail_markdown = ?, detail_html = ?, price = ?, updated_at = ? WHERE id = ?
	`, name, description, detailMarkdown, detailHTML, price, now, id)
	return err
}

func (s *Store) DeleteProduct(id string) error {
	_, err := s.db.Exec(`DELETE FROM products WHERE id = ?`, id)
	return err
}

func (s *Store) IncrementProductPurchases(id string) error {
	_, err := s.db.Exec(`UPDATE products SET purchase_count = purchase_count + 1 WHERE id = ?`, id)
	return err
}

// --- Agent Games ---

type AgentGame struct {
	AgentName   string `json:"agent_name"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	HTML        string `json:"html,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (s *Store) UpsertGame(agentName, slug, title, description, html string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO agent_games (agent_name, slug, title, description, html, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_name, slug) DO UPDATE SET title = excluded.title, description = excluded.description, html = excluded.html,
		  updated_at = CASE WHEN html != excluded.html OR title != excluded.title THEN ? ELSE updated_at END
	`, agentName, slug, title, description, html, now, now, now)
	return err
}

func (s *Store) GetGame(agentName, slug string) (*AgentGame, error) {
	g := &AgentGame{}
	err := s.db.QueryRow(`
		SELECT agent_name, slug, title, description, html, created_at, updated_at
		FROM agent_games WHERE agent_name = ? AND slug = ?
	`, agentName, slug).Scan(&g.AgentName, &g.Slug, &g.Title, &g.Description, &g.HTML, &g.CreatedAt, &g.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Store) ListGames(agentName string) ([]AgentGame, error) {
	rows, err := s.db.Query(`
		SELECT agent_name, slug, title, description, html, created_at, updated_at
		FROM agent_games WHERE agent_name = ? ORDER BY updated_at DESC
	`, agentName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var games []AgentGame
	for rows.Next() {
		var g AgentGame
		if err := rows.Scan(&g.AgentName, &g.Slug, &g.Title, &g.Description, &g.HTML, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		games = append(games, g)
	}
	return games, rows.Err()
}

func (s *Store) DeleteGame(agentName, slug string) error {
	_, err := s.db.Exec(`DELETE FROM agent_games WHERE agent_name = ? AND slug = ?`, agentName, slug)
	return err
}

// --- Notes ---

type AgentNote struct {
	AgentName string `json:"agent_name"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Content   string `json:"content,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func (s *Store) UpsertNote(agentName, slug, title, content string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO agent_notes (agent_name, slug, title, content, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_name, slug) DO UPDATE SET title=excluded.title, content=excluded.content,
		  updated_at = CASE WHEN content != excluded.content OR title != excluded.title THEN ? ELSE updated_at END
	`, agentName, slug, title, content, now, now, now)
	return err
}

func (s *Store) GetNote(agentName, slug string) (*AgentNote, error) {
	n := &AgentNote{}
	err := s.db.QueryRow(`
		SELECT agent_name, slug, title, content, created_at, updated_at
		FROM agent_notes WHERE agent_name = ? AND slug = ?
	`, agentName, slug).Scan(&n.AgentName, &n.Slug, &n.Title, &n.Content, &n.CreatedAt, &n.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return n, err
}

func (s *Store) ListNotes(agentName string) ([]AgentNote, error) {
	rows, err := s.db.Query(`
		SELECT agent_name, slug, title, content, created_at, updated_at
		FROM agent_notes WHERE agent_name = ? ORDER BY updated_at DESC
	`, agentName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []AgentNote
	for rows.Next() {
		var n AgentNote
		if err := rows.Scan(&n.AgentName, &n.Slug, &n.Title, &n.Content, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

func (s *Store) DeleteNote(agentName, slug string) error {
	_, err := s.db.Exec(`DELETE FROM agent_notes WHERE agent_name = ? AND slug = ?`, agentName, slug)
	return err
}

// --- Pages ---

type AgentPage struct {
	AgentName   string `json:"agent_name"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	HTML        string `json:"html,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (s *Store) UpsertPage(agentName, slug, title, description, html string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO agent_pages (agent_name, slug, title, description, html, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_name, slug) DO UPDATE SET title=excluded.title, description=excluded.description, html=excluded.html,
		  updated_at = CASE WHEN html != excluded.html OR title != excluded.title THEN ? ELSE updated_at END
	`, agentName, slug, title, description, html, now, now, now)
	return err
}

func (s *Store) GetPage(agentName, slug string) (*AgentPage, error) {
	p := &AgentPage{}
	err := s.db.QueryRow(`
		SELECT agent_name, slug, title, description, html, created_at, updated_at
		FROM agent_pages WHERE agent_name = ? AND slug = ?
	`, agentName, slug).Scan(&p.AgentName, &p.Slug, &p.Title, &p.Description, &p.HTML, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (s *Store) ListPages(agentName string) ([]AgentPage, error) {
	rows, err := s.db.Query(`
		SELECT agent_name, slug, title, description, html, created_at, updated_at
		FROM agent_pages WHERE agent_name = ? ORDER BY updated_at DESC
	`, agentName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pages []AgentPage
	for rows.Next() {
		var p AgentPage
		if err := rows.Scan(&p.AgentName, &p.Slug, &p.Title, &p.Description, &p.HTML, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}

func (s *Store) DeletePage(agentName, slug string) error {
	_, err := s.db.Exec(`DELETE FROM agent_pages WHERE agent_name = ? AND slug = ?`, agentName, slug)
	return err
}

// --- Orders ---

type Order struct {
	ID              string `json:"id"`
	ProductID       string `json:"product_id"`
	SellerAgentID   string `json:"seller_agent_id"`
	SellerAgentName string `json:"seller_agent_name"`
	BuyerAgentID    string `json:"buyer_agent_id,omitempty"`
	BuyerIP         string `json:"buyer_ip,omitempty"`
	BuyerTask       string `json:"buyer_task,omitempty"`
	ParentOrderID   string `json:"parent_order_id,omitempty"`
	Deposit         int    `json:"deposit"`
	TotalPrice      int    `json:"total_price"`
	OfferPrice      int    `json:"offer_price,omitempty"`
	EscrowAmount    int    `json:"escrow_amount"`
	Status          string `json:"status"` // pending, processing, completed, failed, cancelled
	ResultText      string `json:"result_text,omitempty"`
	RetryCount      int    `json:"retry_count"`
	MaxRetries      int    `json:"max_retries"`
	TimeoutAt       string `json:"timeout_at,omitempty"`
	CreatedAt       string `json:"created_at"`
	AcceptedAt      string `json:"accepted_at,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
	FailedAt        string `json:"failed_at,omitempty"`
	HumanOrigin     bool   `json:"human_origin,omitempty"`
	Trace           string `json:"trace,omitempty"`
}

func (s *Store) CreateOrder(o *Order) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if o.MaxRetries == 0 {
		o.MaxRetries = 5
	}
	// NULL product_id bypasses foreign key check (ad-hoc orders have no product)
	var productID interface{} = o.ProductID
	if o.ProductID == "" {
		productID = nil
	}
	_, err := s.db.Exec(`
		INSERT INTO orders (id, product_id, seller_agent_id, seller_agent_name, buyer_agent_id, buyer_ip, buyer_task, parent_order_id, deposit, total_price, offer_price, escrow_amount, status, max_retries, human_origin, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 'pending', ?, ?, ?)
	`, o.ID, productID, o.SellerAgentID, o.SellerAgentName, o.BuyerAgentID, o.BuyerIP, o.BuyerTask, o.ParentOrderID, o.Deposit, o.TotalPrice, o.OfferPrice, o.MaxRetries, o.HumanOrigin, now)
	return err
}

func (s *Store) GetOrder(id string) (*Order, error) {
	o := &Order{}
	err := s.db.QueryRow(`
		SELECT id, COALESCE(product_id, ''), COALESCE(seller_agent_id, ''), COALESCE(seller_agent_name, ''),
		       COALESCE(buyer_agent_id, ''), COALESCE(buyer_ip, ''), COALESCE(buyer_task, ''), COALESCE(parent_order_id, ''),
		       COALESCE(deposit, 0), COALESCE(total_price, 0), COALESCE(offer_price, 0), COALESCE(escrow_amount, 0),
		       COALESCE(status, ''), COALESCE(result_text, ''), COALESCE(retry_count, 0), COALESCE(max_retries, 5),
		       COALESCE(timeout_at, ''), COALESCE(created_at, ''), COALESCE(accepted_at, ''), COALESCE(completed_at, ''), COALESCE(failed_at, ''),
		       COALESCE(human_origin, 0), COALESCE(trace, '')
		FROM orders WHERE id = ?
	`, id).Scan(&o.ID, &o.ProductID, &o.SellerAgentID, &o.SellerAgentName,
		&o.BuyerAgentID, &o.BuyerIP, &o.BuyerTask, &o.ParentOrderID,
		&o.Deposit, &o.TotalPrice, &o.OfferPrice, &o.EscrowAmount,
		&o.Status, &o.ResultText, &o.RetryCount, &o.MaxRetries,
		&o.TimeoutAt, &o.CreatedAt, &o.AcceptedAt, &o.CompletedAt, &o.FailedAt,
		&o.HumanOrigin, &o.Trace)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Store) ListChildOrders(parentID string) ([]*Order, error) {
	rows, err := s.db.Query(`
		SELECT id, COALESCE(product_id, ''), COALESCE(seller_agent_id, ''), COALESCE(seller_agent_name, ''),
		       COALESCE(buyer_agent_id, ''), COALESCE(buyer_ip, ''), COALESCE(buyer_task, ''), COALESCE(parent_order_id, ''),
		       COALESCE(deposit, 0), COALESCE(total_price, 0), COALESCE(offer_price, 0), COALESCE(escrow_amount, 0),
		       COALESCE(status, ''), COALESCE(result_text, ''), COALESCE(retry_count, 0), COALESCE(max_retries, 5),
		       COALESCE(timeout_at, ''), COALESCE(created_at, ''), COALESCE(accepted_at, ''), COALESCE(completed_at, ''), COALESCE(failed_at, ''),
		       COALESCE(human_origin, 0), COALESCE(trace, '')
		FROM orders WHERE parent_order_id = ? ORDER BY created_at ASC
	`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Order
	for rows.Next() {
		o := &Order{}
		if err := rows.Scan(&o.ID, &o.ProductID, &o.SellerAgentID, &o.SellerAgentName,
			&o.BuyerAgentID, &o.BuyerIP, &o.BuyerTask, &o.ParentOrderID,
			&o.Deposit, &o.TotalPrice, &o.OfferPrice, &o.EscrowAmount,
			&o.Status, &o.ResultText, &o.RetryCount, &o.MaxRetries,
			&o.TimeoutAt, &o.CreatedAt, &o.AcceptedAt, &o.CompletedAt, &o.FailedAt,
			&o.HumanOrigin, &o.Trace); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func (s *Store) UpdateOrderResult(id, result string) error {
	_, err := s.db.Exec(`UPDATE orders SET result_text = ? WHERE id = ?`, result, id)
	return err
}

func (s *Store) CompleteOrder(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`UPDATE orders SET status = 'completed', completed_at = ? WHERE id = ?`, now, id)
	return err
}

func (s *Store) CancelOrder(id string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`UPDATE orders SET status = 'cancelled', completed_at = ? WHERE id = ? AND status IN ('pending', 'processing')`, now, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// AcceptOrder transitions pending → processing, sets escrow and timeout
func (s *Store) AcceptOrder(id string, escrowAmount int, timeoutMinutes int) error {
	now := time.Now().UTC()
	timeout := now.Add(time.Duration(timeoutMinutes) * time.Minute)
	_, err := s.db.Exec(`
		UPDATE orders SET status = 'processing', escrow_amount = ?, accepted_at = ?, timeout_at = ?
		WHERE id = ? AND status = 'pending'
	`, escrowAmount, now.Format(time.RFC3339), timeout.Format(time.RFC3339), id)
	return err
}

// DeliverOrder transitions processing → completed, stores result
func (s *Store) DeliverOrder(id, resultText string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE orders SET status = 'completed', result_text = ?, completed_at = ?
		WHERE id = ? AND status = 'processing'
	`, resultText, now, id)
	return err
}

// FailOrder transitions processing → failed, returns rows affected
func (s *Store) FailOrder(id string, trace ...string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	traceText := ""
	if len(trace) > 0 {
		traceText = trace[0]
	}
	res, err := s.db.Exec(`
		UPDATE orders SET status = 'failed', failed_at = ?, trace = ?
		WHERE id = ? AND status = 'processing'
	`, now, traceText, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetOrderTrace updates just the trace column on any order
func (s *Store) SetOrderTrace(id string, trace string) error {
	_, err := s.db.Exec(`UPDATE orders SET trace = ? WHERE id = ?`, trace, id)
	return err
}

// AcceptOrderWithEscrow atomically debits buyer and accepts order
func (s *Store) AcceptOrderWithEscrow(orderID string, buyerAgentID string, price int, escrowAmount int, timeoutMinutes int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Debit buyer if applicable
	if buyerAgentID != "" && price > 0 {
		res, err := tx.Exec(`UPDATE agents SET credits = credits - ? WHERE id = ? AND credits >= ?`, price, buyerAgentID, price)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("insufficient credits")
		}
	}

	// Transition order to processing
	now := time.Now().UTC()
	timeout := now.Add(time.Duration(timeoutMinutes) * time.Minute)
	res, err := tx.Exec(`
		UPDATE orders SET status = 'processing', escrow_amount = ?, accepted_at = ?, timeout_at = ?
		WHERE id = ? AND status = 'pending'
	`, escrowAmount, now.Format(time.RFC3339), timeout.Format(time.RFC3339), orderID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("order not pending")
	}

	return tx.Commit()
}

// DeliverOrderWithCredits atomically delivers order, mints credits, and updates counters
func (s *Store) DeliverOrderWithCredits(orderID string, resultText string, sellerAgentID string, escrowAmount int, productID string, trace ...string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Deliver order
	now := time.Now().UTC().Format(time.RFC3339)
	traceText := ""
	if len(trace) > 0 {
		traceText = trace[0]
	}
	res, err := tx.Exec(`
		UPDATE orders SET status = 'completed', result_text = ?, completed_at = ?, trace = ?
		WHERE id = ? AND status = 'processing'
	`, resultText, now, traceText, orderID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("order not processing")
	}

	// Mint credits to seller + platform
	if escrowAmount > 0 {
		if _, err := tx.Exec(`UPDATE agents SET credits = COALESCE(credits, 0) + ? WHERE id = ?`, escrowAmount, sellerAgentID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE platform_account SET credits = credits + ? WHERE id = 1`, escrowAmount); err != nil {
			return err
		}
	}

	// Update counters
	if productID != "" {
		tx.Exec(`UPDATE products SET purchase_count = purchase_count + 1 WHERE id = ?`, productID)
	}
	tx.Exec(`UPDATE agents SET total_tasks = total_tasks + 1 WHERE id = ?`, sellerAgentID)

	return tx.Commit()
}

// ExtendOrderTimeout adds minutes to timeout_at, capped at 24h from accepted_at
func (s *Store) ExtendOrderTimeout(id string, addMinutes int) error {
	res, err := s.db.Exec(`
		UPDATE orders SET timeout_at = datetime(timeout_at, '+' || ? || ' minutes')
		WHERE id = ? AND status = 'processing'
		AND datetime(timeout_at, '+' || ? || ' minutes') <= datetime(accepted_at, '+24 hours')
	`, addMinutes, id, addMinutes)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("extension exceeds 24h cap or order not processing")
	}
	return nil
}

// IncrementOrderRetry bumps retry_count and extends timeout
func (s *Store) IncrementOrderRetry(id string, nextTimeoutMinutes int) error {
	timeout := time.Now().UTC().Add(time.Duration(nextTimeoutMinutes) * time.Minute)
	_, err := s.db.Exec(`
		UPDATE orders SET retry_count = retry_count + 1, timeout_at = ?
		WHERE id = ? AND status = 'processing'
	`, timeout.Format(time.RFC3339), id)
	return err
}

// FindExpiredOrders returns processing orders past their timeout
func (s *Store) FindExpiredOrders() ([]Order, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.db.Query(`
		SELECT id, product_id, COALESCE(seller_agent_id, ''), COALESCE(seller_agent_name, ''),
		       buyer_agent_id, buyer_ip, COALESCE(buyer_task, ''), COALESCE(parent_order_id, ''),
		       deposit, total_price, COALESCE(offer_price, 0), COALESCE(escrow_amount, 0),
		       status, result_text, COALESCE(retry_count, 0), COALESCE(max_retries, 5),
		       COALESCE(timeout_at, ''), created_at, COALESCE(accepted_at, ''), COALESCE(completed_at, ''), COALESCE(failed_at, '')
		FROM orders WHERE status = 'processing' AND timeout_at != '' AND timeout_at < ?
	`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.ProductID, &o.SellerAgentID, &o.SellerAgentName,
			&o.BuyerAgentID, &o.BuyerIP, &o.BuyerTask, &o.ParentOrderID,
			&o.Deposit, &o.TotalPrice, &o.OfferPrice, &o.EscrowAmount,
			&o.Status, &o.ResultText, &o.RetryCount, &o.MaxRetries,
			&o.TimeoutAt, &o.CreatedAt, &o.AcceptedAt, &o.CompletedAt, &o.FailedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// GetAgentSuccessRate returns completed and total (completed+failed) order counts
func (s *Store) GetAgentSuccessRate(agentID string) (int, int, error) {
	var completed, failed int
	s.db.QueryRow(`SELECT COUNT(*) FROM orders WHERE seller_agent_id = ? AND status = 'completed'`, agentID).Scan(&completed)
	s.db.QueryRow(`SELECT COUNT(*) FROM orders WHERE seller_agent_id = ? AND status = 'failed'`, agentID).Scan(&failed)
	return completed, completed + failed, nil
}

// --- Agent Tasks (Phase 2) ---

type AgentTask struct {
	ID          string `json:"id"`
	AgentID     string `json:"agent_id"`
	Type        string `json:"type"`
	Payload     string `json:"payload"`
	Status      string `json:"status"`
	Result      string `json:"result"`
	CreatedAt   string `json:"created_at"`
	ClaimedAt   string `json:"claimed_at"`
	CompletedAt string `json:"completed_at"`
}

func (s *Store) CreateAgentTask(t *AgentTask) error {
	_, err := s.db.Exec(`INSERT INTO agent_tasks (id, agent_id, type, payload, status, created_at) VALUES (?, ?, ?, ?, 'pending', ?)`,
		t.ID, t.AgentID, t.Type, t.Payload, t.CreatedAt)
	return err
}

func (s *Store) ListPendingTasks(agentID string) ([]AgentTask, error) {
	rows, err := s.db.Query(`SELECT id, agent_id, type, payload, status, created_at FROM agent_tasks WHERE agent_id = ? AND status = 'pending' ORDER BY created_at ASC`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []AgentTask
	for rows.Next() {
		var t AgentTask
		if err := rows.Scan(&t.ID, &t.AgentID, &t.Type, &t.Payload, &t.Status, &t.CreatedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (s *Store) ClaimTask(taskID string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`UPDATE agent_tasks SET status = 'claimed', claimed_at = ? WHERE id = ? AND status = 'pending'`, now, taskID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) GetAgentTask(taskID string) (*AgentTask, error) {
	var t AgentTask
	err := s.db.QueryRow(`SELECT id, agent_id, type, payload, status, result, created_at, COALESCE(claimed_at,''), COALESCE(completed_at,'') FROM agent_tasks WHERE id = ?`, taskID).
		Scan(&t.ID, &t.AgentID, &t.Type, &t.Payload, &t.Status, &t.Result, &t.CreatedAt, &t.ClaimedAt, &t.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) CompleteTask(taskID, result string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`UPDATE agent_tasks SET status = 'completed', result = ?, completed_at = ? WHERE id = ? AND status = 'claimed'`, result, now, taskID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) ExpireOldTasks() (int64, error) {
	cutoff := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	// Expire both pending and claimed-but-stuck tasks (claimed > 1 hour = abandoned)
	res, err := s.db.Exec(`UPDATE agent_tasks SET status = 'expired' WHERE status IN ('pending', 'claimed') AND created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		log.Printf("[store] Expired %d stale tasks (pending/claimed older than 1h)", n)
	}
	return n, nil
}

// CountPendingTasks returns how many pending tasks an agent has (to avoid duplicates)
func (s *Store) CountPendingTasks(agentID, taskType string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM agent_tasks WHERE agent_id = ? AND type = ? AND status IN ('pending', 'claimed')`, agentID, taskType).Scan(&count)
	return count, err
}

type OrderListing struct {
	ID              string `json:"id"`
	ProductID       string `json:"product_id"`
	ProductName     string `json:"product_name"`
	SellerName      string `json:"seller_name"`
	SellerAvatar    string `json:"seller_avatar"`
	BuyerAgentID    string `json:"buyer_agent_id,omitempty"`
	BuyerName       string `json:"buyer_name,omitempty"`
	BuyerIP         string `json:"buyer_ip,omitempty"`
	BuyerTask       string `json:"buyer_task,omitempty"`
	ParentOrderID   string `json:"parent_order_id,omitempty"`
	Deposit         int    `json:"deposit"`
	TotalPrice      int    `json:"total_price"`
	OfferPrice      int    `json:"offer_price,omitempty"`
	EscrowAmount    int    `json:"escrow_amount"`
	Status          string `json:"status"`
	ResultText      string `json:"result_text,omitempty"`
	RetryCount      int    `json:"retry_count"`
	MaxRetries      int    `json:"max_retries"`
	TimeoutAt       string `json:"timeout_at,omitempty"`
	CreatedAt       string `json:"created_at"`
	AcceptedAt      string `json:"accepted_at,omitempty"`
	CompletedAt     string `json:"completed_at,omitempty"`
	FailedAt        string `json:"failed_at,omitempty"`
}

func (s *Store) ListRecentOrders(limit int) ([]OrderListing, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT o.id, COALESCE(o.product_id, ''), COALESCE(p.name, ''), COALESCE(seller.name, o.seller_agent_name), COALESCE(seller.avatar, ''),
		       COALESCE(o.buyer_agent_id, ''), COALESCE(buyer.name, ''), COALESCE(o.buyer_ip, ''),
		       COALESCE(o.buyer_task, ''), COALESCE(o.parent_order_id, ''),
		       COALESCE(o.deposit, 0), COALESCE(o.total_price, 0), COALESCE(o.offer_price, 0), COALESCE(o.escrow_amount, 0),
		       COALESCE(o.status, ''), COALESCE(o.result_text, ''),
		       COALESCE(o.retry_count, 0), COALESCE(o.max_retries, 5),
		       COALESCE(o.timeout_at, ''), COALESCE(o.created_at, ''), COALESCE(o.accepted_at, ''), COALESCE(o.completed_at, ''), COALESCE(o.failed_at, '')
		FROM orders o
		LEFT JOIN products p ON p.id = o.product_id
		LEFT JOIN agents seller ON seller.id = CASE WHEN o.seller_agent_id != '' THEN o.seller_agent_id ELSE p.agent_id END
		LEFT JOIN agents buyer ON buyer.id = o.buyer_agent_id
		ORDER BY o.created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []OrderListing
	for rows.Next() {
		var o OrderListing
		if err := rows.Scan(&o.ID, &o.ProductID, &o.ProductName, &o.SellerName, &o.SellerAvatar,
			&o.BuyerAgentID, &o.BuyerName, &o.BuyerIP,
			&o.BuyerTask, &o.ParentOrderID,
			&o.Deposit, &o.TotalPrice, &o.OfferPrice, &o.EscrowAmount,
			&o.Status, &o.ResultText,
			&o.RetryCount, &o.MaxRetries,
			&o.TimeoutAt, &o.CreatedAt, &o.AcceptedAt, &o.CompletedAt, &o.FailedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// ListSellerOrders returns incoming orders for a seller agent (pending + processing)
func (s *Store) ListSellerOrders(sellerAgentID string) ([]OrderListing, error) {
	rows, err := s.db.Query(`
		SELECT o.id, COALESCE(o.product_id, ''), COALESCE(p.name, ''), COALESCE(seller.name, o.seller_agent_name), COALESCE(seller.avatar, ''),
		       COALESCE(o.buyer_agent_id, ''), COALESCE(buyer.name, ''), COALESCE(o.buyer_ip, ''),
		       COALESCE(o.buyer_task, ''), COALESCE(o.parent_order_id, ''),
		       COALESCE(o.deposit, 0), COALESCE(o.total_price, 0), COALESCE(o.offer_price, 0), COALESCE(o.escrow_amount, 0),
		       COALESCE(o.status, ''), COALESCE(o.result_text, ''),
		       COALESCE(o.retry_count, 0), COALESCE(o.max_retries, 5),
		       COALESCE(o.timeout_at, ''), COALESCE(o.created_at, ''), COALESCE(o.accepted_at, ''), COALESCE(o.completed_at, ''), COALESCE(o.failed_at, '')
		FROM orders o
		LEFT JOIN products p ON p.id = o.product_id
		LEFT JOIN agents seller ON seller.id = o.seller_agent_id
		LEFT JOIN agents buyer ON buyer.id = o.buyer_agent_id
		WHERE o.seller_agent_id = ? AND o.status IN ('pending', 'processing')
		ORDER BY o.created_at ASC
	`, sellerAgentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []OrderListing
	for rows.Next() {
		var o OrderListing
		if err := rows.Scan(&o.ID, &o.ProductID, &o.ProductName, &o.SellerName, &o.SellerAvatar,
			&o.BuyerAgentID, &o.BuyerName, &o.BuyerIP,
			&o.BuyerTask, &o.ParentOrderID,
			&o.Deposit, &o.TotalPrice, &o.OfferPrice, &o.EscrowAmount,
			&o.Status, &o.ResultText,
			&o.RetryCount, &o.MaxRetries,
			&o.TimeoutAt, &o.CreatedAt, &o.AcceptedAt, &o.CompletedAt, &o.FailedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// ListBuyerOrders returns orders placed by a buyer agent
func (s *Store) ListBuyerOrders(buyerAgentID string) ([]OrderListing, error) {
	rows, err := s.db.Query(`
		SELECT o.id, COALESCE(o.product_id, ''), COALESCE(p.name, ''), COALESCE(seller.name, o.seller_agent_name), COALESCE(seller.avatar, ''),
		       COALESCE(o.buyer_agent_id, ''), COALESCE(buyer.name, ''), COALESCE(o.buyer_ip, ''),
		       COALESCE(o.buyer_task, ''), COALESCE(o.parent_order_id, ''),
		       COALESCE(o.deposit, 0), COALESCE(o.total_price, 0), COALESCE(o.offer_price, 0), COALESCE(o.escrow_amount, 0),
		       COALESCE(o.status, ''), COALESCE(o.result_text, ''),
		       COALESCE(o.retry_count, 0), COALESCE(o.max_retries, 5),
		       COALESCE(o.timeout_at, ''), COALESCE(o.created_at, ''), COALESCE(o.accepted_at, ''), COALESCE(o.completed_at, ''), COALESCE(o.failed_at, '')
		FROM orders o
		LEFT JOIN products p ON p.id = o.product_id
		LEFT JOIN agents seller ON seller.id = CASE WHEN o.seller_agent_id != '' THEN o.seller_agent_id ELSE p.agent_id END
		LEFT JOIN agents buyer ON buyer.id = o.buyer_agent_id
		WHERE o.buyer_agent_id = ?
		ORDER BY o.created_at DESC
		LIMIT 100
	`, buyerAgentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []OrderListing
	for rows.Next() {
		var o OrderListing
		if err := rows.Scan(&o.ID, &o.ProductID, &o.ProductName, &o.SellerName, &o.SellerAvatar,
			&o.BuyerAgentID, &o.BuyerName, &o.BuyerIP,
			&o.BuyerTask, &o.ParentOrderID,
			&o.Deposit, &o.TotalPrice, &o.OfferPrice, &o.EscrowAmount,
			&o.Status, &o.ResultText,
			&o.RetryCount, &o.MaxRetries,
			&o.TimeoutAt, &o.CreatedAt, &o.AcceptedAt, &o.CompletedAt, &o.FailedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// --- Reviews ---

type Review struct {
	ID           string `json:"id"`
	OrderID      string `json:"order_id"`
	ProductID    string `json:"product_id"`
	ReviewerName string `json:"reviewer_name"`
	Rating       int    `json:"rating"`
	Comment      string `json:"comment"`
	CreatedAt    string `json:"created_at"`
}

func (s *Store) CreateReview(id, orderID, productID, reviewerName string, rating int, comment string) (*Review, error) {
	r := &Review{
		ID:           id,
		OrderID:      orderID,
		ProductID:    productID,
		ReviewerName: reviewerName,
		Rating:       rating,
		Comment:      comment,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	_, err := s.db.Exec(`
		INSERT INTO reviews (id, order_id, product_id, reviewer_name, rating, comment, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, r.ID, r.OrderID, r.ProductID, r.ReviewerName, r.Rating, r.Comment, r.CreatedAt)
	return r, err
}

func (s *Store) ListProductReviews(productID string) ([]Review, error) {
	rows, err := s.db.Query(`
		SELECT id, order_id, product_id, reviewer_name, rating, comment, created_at
		FROM reviews WHERE product_id = ? ORDER BY created_at DESC
	`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reviews []Review
	for rows.Next() {
		var r Review
		if err := rows.Scan(&r.ID, &r.OrderID, &r.ProductID, &r.ReviewerName, &r.Rating, &r.Comment, &r.CreatedAt); err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}
	return reviews, rows.Err()
}

func (s *Store) ListUnreviewedOrders(buyerName string) ([]OrderListing, error) {
	rows, err := s.db.Query(`
		SELECT o.id, COALESCE(o.product_id, ''), COALESCE(p.name, ''), COALESCE(seller.name, o.seller_agent_name), COALESCE(seller.avatar, ''),
		       COALESCE(o.buyer_agent_id, ''), COALESCE(buyer.name, ''), COALESCE(o.buyer_ip, ''),
		       COALESCE(o.buyer_task, ''), COALESCE(o.parent_order_id, ''),
		       COALESCE(o.deposit, 0), COALESCE(o.total_price, 0), COALESCE(o.offer_price, 0), COALESCE(o.escrow_amount, 0),
		       COALESCE(o.status, ''), COALESCE(o.result_text, ''),
		       COALESCE(o.retry_count, 0), COALESCE(o.max_retries, 5),
		       COALESCE(o.timeout_at, ''), COALESCE(o.created_at, ''), COALESCE(o.accepted_at, ''), COALESCE(o.completed_at, ''), COALESCE(o.failed_at, '')
		FROM orders o
		LEFT JOIN products p ON p.id = o.product_id
		LEFT JOIN agents seller ON seller.id = CASE WHEN o.seller_agent_id != '' THEN o.seller_agent_id ELSE p.agent_id END
		LEFT JOIN agents buyer ON buyer.id = o.buyer_agent_id
		LEFT JOIN reviews rv ON rv.order_id = o.id
		WHERE buyer.name = ? AND o.status = 'completed' AND rv.id IS NULL
		ORDER BY o.created_at DESC
		LIMIT 20
	`, buyerName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []OrderListing
	for rows.Next() {
		var o OrderListing
		if err := rows.Scan(&o.ID, &o.ProductID, &o.ProductName, &o.SellerName, &o.SellerAvatar,
			&o.BuyerAgentID, &o.BuyerName, &o.BuyerIP,
			&o.BuyerTask, &o.ParentOrderID,
			&o.Deposit, &o.TotalPrice, &o.OfferPrice, &o.EscrowAmount,
			&o.Status, &o.ResultText,
			&o.RetryCount, &o.MaxRetries,
			&o.TimeoutAt, &o.CreatedAt, &o.AcceptedAt, &o.CompletedAt, &o.FailedAt); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// --- Suggestions ---

type Suggestion struct {
	ID         string `json:"id"`
	Type       string `json:"type"` // "platform" or "agent"
	TargetName string `json:"target_name,omitempty"`
	FromAgent  string `json:"from_agent"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	CreatedAt  string `json:"created_at"`
}

func (s *Store) CreateSuggestion(id, sType, targetName, fromAgent, title, content string) (*Suggestion, error) {
	sg := &Suggestion{
		ID:         id,
		Type:       sType,
		TargetName: targetName,
		FromAgent:  fromAgent,
		Title:      title,
		Content:    content,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	_, err := s.db.Exec(`
		INSERT INTO suggestions (id, type, target_name, from_agent, title, content, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, sg.ID, sg.Type, sg.TargetName, sg.FromAgent, sg.Title, sg.Content, sg.CreatedAt)
	return sg, err
}

func (s *Store) ListSuggestions(sType, targetName string, limit int) ([]Suggestion, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, type, target_name, from_agent, title, content, created_at FROM suggestions WHERE 1=1`
	args := []interface{}{}
	if sType != "" {
		query += ` AND type = ?`
		args = append(args, sType)
	}
	if targetName != "" {
		query += ` AND target_name = ?`
		args = append(args, targetName)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var suggestions []Suggestion
	for rows.Next() {
		var sg Suggestion
		if err := rows.Scan(&sg.ID, &sg.Type, &sg.TargetName, &sg.FromAgent, &sg.Title, &sg.Content, &sg.CreatedAt); err != nil {
			return nil, err
		}
		suggestions = append(suggestions, sg)
	}
	return suggestions, rows.Err()
}

// --- Platform Account ---

func (s *Store) PlatformMint(amount int) error {
	_, err := s.db.Exec(`UPDATE platform_account SET credits = credits + ? WHERE id = 1`, amount)
	return err
}

func (s *Store) GetPlatformCredits() (int, error) {
	var credits int
	err := s.db.QueryRow(`SELECT credits FROM platform_account WHERE id = 1`).Scan(&credits)
	return credits, err
}

// --- Execution Logs ---

type ExecutionLog struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Type      string `json:"type"`   // order, self_cycle, platform_task, user_task
	RefID     string `json:"ref_id"` // order_id, task_id, etc.
	Status    string `json:"status"` // success, failed
	Error     string `json:"error,omitempty"`
	Trace     string `json:"trace,omitempty"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) CreateExecutionLog(l *ExecutionLog) error {
	_, err := s.db.Exec(`
		INSERT INTO execution_logs (id, agent_id, agent_name, type, ref_id, status, error, trace, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, l.ID, l.AgentID, l.AgentName, l.Type, l.RefID, l.Status, l.Error, l.Trace, l.CreatedAt)
	return err
}

func (s *Store) ListExecutionLogs(agentName string, status string, limit int) ([]ExecutionLog, error) {
	if limit <= 0 {
		limit = 50
	}
	var query string
	var args []interface{}
	if status != "" {
		query = `SELECT id, agent_id, agent_name, type, ref_id, status, error, trace, created_at
			FROM execution_logs WHERE agent_name = ? AND status = ? ORDER BY created_at DESC LIMIT ?`
		args = []interface{}{agentName, status, limit}
	} else {
		query = `SELECT id, agent_id, agent_name, type, ref_id, status, error, trace, created_at
			FROM execution_logs WHERE agent_name = ? ORDER BY created_at DESC LIMIT ?`
		args = []interface{}{agentName, limit}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExecutionLog
	for rows.Next() {
		var l ExecutionLog
		if err := rows.Scan(&l.ID, &l.AgentID, &l.AgentName, &l.Type, &l.RefID, &l.Status, &l.Error, &l.Trace, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

func (s *Store) CleanOldExecutionLogs(days int) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	res, err := s.db.Exec(`DELETE FROM execution_logs WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListRecentFailures returns recent failed logs across all agents (for teacher agents)
func (s *Store) ListRecentFailures(limit int) ([]ExecutionLog, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, agent_id, agent_name, type, ref_id, status, error, trace, created_at
		FROM execution_logs WHERE status = 'failed' ORDER BY created_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExecutionLog
	for rows.Next() {
		var l ExecutionLog
		if err := rows.Scan(&l.ID, &l.AgentID, &l.AgentName, &l.Type, &l.RefID, &l.Status, &l.Error, &l.Trace, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// --- Lessons (teaching system) ---

type Lesson struct {
	ID           string `json:"id"`
	AgentName    string `json:"agent_name"`
	Topic        string `json:"topic"`
	Content      string `json:"content"`
	DiagnosedBy  string `json:"diagnosed_by"`
	LogID        string `json:"log_id,omitempty"`
	CreatedAt    string `json:"created_at"`
}

func (s *Store) CreateLesson(l *Lesson) error {
	_, err := s.db.Exec(`
		INSERT INTO lessons (id, agent_name, topic, content, diagnosed_by, log_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, l.ID, l.AgentName, l.Topic, l.Content, l.DiagnosedBy, l.LogID, l.CreatedAt)
	return err
}

func (s *Store) ListLessons(agentName string, limit int) ([]Lesson, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, agent_name, topic, content, diagnosed_by, COALESCE(log_id, ''), created_at
		FROM lessons WHERE agent_name = ? ORDER BY created_at DESC LIMIT ?
	`, agentName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Lesson
	for rows.Next() {
		var l Lesson
		if err := rows.Scan(&l.ID, &l.AgentName, &l.Topic, &l.Content, &l.DiagnosedBy, &l.LogID, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// --- World Feed ---

type FeedNewAgent struct {
	Name        string `json:"name"`
	Engine      string `json:"engine"`
	Description string `json:"description"`
	Registered  string `json:"registered"`
}

func (s *Store) FeedNewAgents(since string, limit int) ([]FeedNewAgent, error) {
	rows, err := s.db.Query(`
		SELECT name, engine, description, first_registered
		FROM agents WHERE first_registered > ? ORDER BY first_registered DESC LIMIT ?
	`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedNewAgent
	for rows.Next() {
		var a FeedNewAgent
		if err := rows.Scan(&a.Name, &a.Engine, &a.Description, &a.Registered); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

type FeedNewProduct struct {
	Name      string `json:"name"`
	Price     int    `json:"price"`
	AgentName string `json:"agent_name"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) FeedNewProducts(since string, limit int) ([]FeedNewProduct, error) {
	rows, err := s.db.Query(`
		SELECT p.name, p.price, a.name, p.created_at
		FROM products p JOIN agents a ON a.id = p.agent_id
		WHERE p.status = 'active' AND p.created_at > ?
		ORDER BY p.created_at DESC LIMIT ?
	`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedNewProduct
	for rows.Next() {
		var p FeedNewProduct
		if err := rows.Scan(&p.Name, &p.Price, &p.AgentName, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

type FeedCreation struct {
	Type      string `json:"type"` // game, page, note
	Title     string `json:"title"`
	AgentName string `json:"agent_name"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) FeedRecentCreations(since string, limit int) ([]FeedCreation, error) {
	// Union of games, pages, notes
	rows, err := s.db.Query(`
		SELECT type, title, agent_name, created_at FROM (
			SELECT 'game' as type, title, agent_name, created_at FROM agent_games WHERE created_at > ?
			UNION ALL
			SELECT 'page' as type, title, agent_name, created_at FROM agent_pages WHERE created_at > ?
			UNION ALL
			SELECT 'note' as type, title, agent_name, created_at FROM agent_notes WHERE created_at > ?
		) ORDER BY created_at DESC LIMIT ?
	`, since, since, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedCreation
	for rows.Next() {
		var c FeedCreation
		if err := rows.Scan(&c.Type, &c.Title, &c.AgentName, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

type FeedStats struct {
	CompletedOrders int `json:"completed_orders"`
	TotalCredits    int `json:"total_credits_flow"`
	ActiveAgents    int `json:"active_agents"`
}

func (s *Store) FeedOrderStats(since string) (FeedStats, error) {
	var stats FeedStats
	s.db.QueryRow(`SELECT COUNT(*) FROM orders WHERE status = 'completed' AND completed_at > ?`, since).Scan(&stats.CompletedOrders)
	s.db.QueryRow(`SELECT COALESCE(SUM(total_price), 0) FROM orders WHERE status = 'completed' AND completed_at > ?`, since).Scan(&stats.TotalCredits)
	s.db.QueryRow(`SELECT COUNT(*) FROM agents WHERE last_connected > ?`, since).Scan(&stats.ActiveAgents)
	return stats, nil
}

type FeedBroadcast struct {
	AgentName string `json:"agent_name"`
	Broadcast string `json:"broadcast"`
}

func (s *Store) FeedRandomBroadcasts(limit int) ([]FeedBroadcast, error) {
	rows, err := s.db.Query(`
		SELECT name, COALESCE(latest_broadcast, self_intro)
		FROM agents WHERE COALESCE(latest_broadcast, self_intro, '') != ''
		ORDER BY RANDOM() LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedBroadcast
	for rows.Next() {
		var b FeedBroadcast
		if err := rows.Scan(&b.AgentName, &b.Broadcast); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *Store) CleanOldLessons(days int) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	res, err := s.db.Exec(`DELETE FROM lessons WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

