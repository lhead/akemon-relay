package store

import (
	"database/sql"
	"log"

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

// NewFromDB wraps an existing *sql.DB (useful for testing with in-memory databases).
func NewFromDB(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying *sql.DB. Intended for tests that need direct SQL access.
func (s *Store) DB() *sql.DB {
	return s.db
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

	// Failure events table — observability: engine aborts, task failures, crash reports
	s.db.Exec(`CREATE TABLE IF NOT EXISTS failure_events (
		id TEXT PRIMARY KEY,
		agent_name TEXT NOT NULL,
		kind TEXT NOT NULL,
		label TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_failure_events_agent ON failure_events(agent_name, created_at)`)

	// Phase A identity model: publishers / device_tokens / sessions
	s.db.Exec(`CREATE TABLE IF NOT EXISTS publishers (
		id          TEXT PRIMARY KEY,
		account_id  TEXT UNIQUE NOT NULL,
		display_name TEXT DEFAULT '',
		created_at  INTEGER NOT NULL DEFAULT 0
	)`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS device_tokens (
		id           TEXT PRIMARY KEY,
		publisher_id TEXT NOT NULL REFERENCES publishers(id),
		token_hash   TEXT NOT NULL,
		device_name  TEXT DEFAULT '',
		created_at   INTEGER NOT NULL DEFAULT 0,
		last_used_at INTEGER,
		last_used_ip TEXT DEFAULT '',
		expires_at   INTEGER,
		revoked_at   INTEGER
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_device_tokens_hash ON device_tokens(token_hash) WHERE revoked_at IS NULL`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id           TEXT PRIMARY KEY,
		publisher_id TEXT NOT NULL REFERENCES publishers(id),
		token_hash   TEXT NOT NULL,
		created_at   INTEGER NOT NULL DEFAULT 0,
		expires_at   INTEGER NOT NULL DEFAULT 0,
		revoked_at   INTEGER,
		user_agent   TEXT DEFAULT '',
		last_used_at INTEGER
	)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_hash ON sessions(token_hash) WHERE revoked_at IS NULL`)
	s.db.Exec(`ALTER TABLE agents ADD COLUMN publisher_id TEXT DEFAULT ''`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_agents_publisher ON agents(publisher_id)`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN buyer_publisher_id TEXT DEFAULT ''`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_orders_buyer_pub ON orders(buyer_publisher_id)`)
	s.db.Exec(`ALTER TABLE orders ADD COLUMN buyer_name TEXT DEFAULT ''`)

	// Run one-time identity phase A data migration (idempotent)
	if err := s.MigrateIdentityPhaseA(); err != nil {
		log.Printf("[migrate] identity phase A warning: %v", err)
	}

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

// ListAgentSecrets returns (id, secret_hash) for every agent. Used to identify
// an agent by bearer token when the caller did not pass buyer_agent_id.
// Order-creation paths hit this at most once per request, and agent count stays small.

// SpendAgentCredits deducts credits by agent name. Returns remaining credits.

// MintCredit creates new credits for an agent (from human usage).
// Fixed amount, independent of agent price.

// TransferCredits moves credits between agents (agent-to-agent calls).
// Credits callee with callee's price.

// DebitAgent subtracts credits from an agent. Fails if insufficient balance.

// AgentToAgentTransfer atomically debits caller and credits callee.
// Returns the price transferred. Fails if caller has insufficient credits.

// --- Account Credits ---

// GetAnyAgentByAccount returns one agent for the given account (for secret key verification).

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

// --- Tasks ---

// --- Connections ---

// --- Session Context ---

const maxContextBytes = 8192

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

// --- Notes ---

type AgentNote struct {
	AgentName string `json:"agent_name"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	Content   string `json:"content,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
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

// --- Orders ---

type Order struct {
	ID              string `json:"id"`
	ProductID       string `json:"product_id"`
	SellerAgentID   string `json:"seller_agent_id"`
	SellerAgentName string `json:"seller_agent_name"`
	BuyerAgentID    string `json:"buyer_agent_id,omitempty"`
	BuyerPublisherID string `json:"buyer_publisher_id,omitempty"`
	BuyerName       string `json:"buyer_name,omitempty"`
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

// AcceptOrder transitions pending → processing, sets escrow and timeout

// DeliverOrder transitions processing → completed, stores result

// FailOrder transitions processing → failed, returns rows affected

// SetOrderTrace updates just the trace column on any order

// AcceptOrderWithEscrow atomically debits buyer and accepts order

// DeliverOrderWithCredits atomically delivers order, mints credits, and updates counters

// ExtendOrderTimeout adds minutes to timeout_at, capped at 24h from accepted_at

// IncrementOrderRetry bumps retry_count and extends timeout

// FindExpiredOrders returns processing orders past their timeout

// GetAgentSuccessRate returns completed and total (completed+failed) order counts

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

// CountPendingTasks returns how many pending tasks an agent has (to avoid duplicates)

type OrderListing struct {
	ID               string `json:"id"`
	ProductID        string `json:"product_id"`
	ProductName      string `json:"product_name"`
	SellerName       string `json:"seller_name"`
	SellerAvatar     string `json:"seller_avatar"`
	BuyerAgentID     string `json:"buyer_agent_id,omitempty"`
	BuyerPublisherID string `json:"buyer_publisher_id,omitempty"`
	BuyerName        string `json:"buyer_name,omitempty"`
	BuyerIP          string `json:"buyer_ip,omitempty"`
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

// ListSellerOrders returns incoming orders for a seller agent (pending + processing)

// ListBuyerOrders returns orders placed by a buyer agent

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

// ListRecentFailures returns recent failed logs across all agents (for teacher agents)

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

// --- World Feed ---

type FeedNewAgent struct {
	Name        string `json:"name"`
	Engine      string `json:"engine"`
	Description string `json:"description"`
	Registered  string `json:"registered"`
}

type FeedNewProduct struct {
	Name      string `json:"name"`
	Price     int    `json:"price"`
	AgentName string `json:"agent_name"`
	CreatedAt string `json:"created_at"`
}

type FeedCreation struct {
	Type      string `json:"type"` // game, page, note
	Title     string `json:"title"`
	AgentName string `json:"agent_name"`
	CreatedAt string `json:"created_at"`
}

type FeedStats struct {
	CompletedOrders int `json:"completed_orders"`
	TotalCredits    int `json:"total_credits_flow"`
	ActiveAgents    int `json:"active_agents"`
}

type FeedBroadcast struct {
	AgentName string `json:"agent_name"`
	Broadcast string `json:"broadcast"`
}

