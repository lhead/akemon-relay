package store

import (
	"database/sql"
	"fmt"
	"math"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
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
	// Migrate old pending orders: those with results → completed, without → cancelled
	s.db.Exec(`UPDATE orders SET status = 'completed', completed_at = created_at WHERE status = 'pending' AND result_text != '' AND result_text IS NOT NULL`)
	s.db.Exec(`UPDATE orders SET status = 'cancelled', completed_at = created_at WHERE status = 'pending' AND (result_text = '' OR result_text IS NULL)`)

	return nil
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

func (s *Store) UpdateAgentOnConnect(name, description, engine string, public bool, tags string, price int) error {
	pub := 0
	if public {
		pub = 1
	}
	if price <= 0 {
		price = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
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

func (s *Store) UpdateAgentSelf(name, selfIntro, canvas, mood, profileHTML string) error {
	_, err := s.db.Exec(`UPDATE agents SET self_intro = ?, canvas = ?, mood = ?, profile_html = ? WHERE name = ?`,
		selfIntro, canvas, mood, profileHTML, name)
	return err
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
			COALESCE((SELECT COUNT(*) FROM products p WHERE p.agent_id = a.id AND p.status = 'active'), 0) as product_count
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
			&a.SelfIntro, &a.Canvas, &a.Mood, &a.ProfileHTML, &a.ProductCount); err != nil {
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
		INSERT INTO products (id, agent_id, name, description, detail_markdown, price, status, purchase_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'active', 0, ?, ?)
	`, p.ID, p.AgentID, p.Name, p.Description, p.DetailMarkdown, p.Price, now, now)
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
		SELECT id, agent_id, name, description, COALESCE(detail_markdown, ''), price, status, purchase_count, created_at, updated_at
		FROM products WHERE id = ?
	`, id).Scan(&p.ID, &p.AgentID, &p.Name, &p.Description, &p.DetailMarkdown, &p.Price, &p.Status, &p.PurchaseCount, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) UpdateProduct(id, name, description, detailMarkdown string, price int) error {
	if price <= 0 {
		price = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE products SET name = ?, description = ?, detail_markdown = ?, price = ?, updated_at = ? WHERE id = ?
	`, name, description, detailMarkdown, price, now, id)
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
		ON CONFLICT(agent_name, slug) DO UPDATE SET title = ?, description = ?, html = ?, updated_at = ?
	`, agentName, slug, title, description, html, now, now, title, description, html, now)
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
		SELECT agent_name, slug, title, description, created_at, updated_at
		FROM agent_games WHERE agent_name = ? ORDER BY created_at ASC
	`, agentName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var games []AgentGame
	for rows.Next() {
		var g AgentGame
		if err := rows.Scan(&g.AgentName, &g.Slug, &g.Title, &g.Description, &g.CreatedAt, &g.UpdatedAt); err != nil {
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
		ON CONFLICT(agent_name, slug) DO UPDATE SET title=excluded.title, content=excluded.content, updated_at=excluded.updated_at
	`, agentName, slug, title, content, now, now)
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
		SELECT agent_name, slug, title, '', created_at, updated_at
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
		ON CONFLICT(agent_name, slug) DO UPDATE SET title=excluded.title, description=excluded.description, html=excluded.html, updated_at=excluded.updated_at
	`, agentName, slug, title, description, html, now, now)
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
		SELECT agent_name, slug, title, description, '', created_at, updated_at
		FROM agent_pages WHERE agent_name = ? ORDER BY created_at ASC
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
}

func (s *Store) CreateOrder(o *Order) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if o.MaxRetries == 0 {
		o.MaxRetries = 5
	}
	_, err := s.db.Exec(`
		INSERT INTO orders (id, product_id, seller_agent_id, seller_agent_name, buyer_agent_id, buyer_ip, buyer_task, parent_order_id, deposit, total_price, offer_price, escrow_amount, status, max_retries, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 'pending', ?, ?)
	`, o.ID, o.ProductID, o.SellerAgentID, o.SellerAgentName, o.BuyerAgentID, o.BuyerIP, o.BuyerTask, o.ParentOrderID, o.Deposit, o.TotalPrice, o.OfferPrice, o.MaxRetries, now)
	return err
}

func (s *Store) GetOrder(id string) (*Order, error) {
	o := &Order{}
	err := s.db.QueryRow(`
		SELECT id, product_id, COALESCE(seller_agent_id, ''), COALESCE(seller_agent_name, ''),
		       buyer_agent_id, buyer_ip, COALESCE(buyer_task, ''), COALESCE(parent_order_id, ''),
		       deposit, total_price, COALESCE(offer_price, 0), COALESCE(escrow_amount, 0),
		       status, result_text, COALESCE(retry_count, 0), COALESCE(max_retries, 5),
		       COALESCE(timeout_at, ''), created_at, COALESCE(accepted_at, ''), COALESCE(completed_at, ''), COALESCE(failed_at, '')
		FROM orders WHERE id = ?
	`, id).Scan(&o.ID, &o.ProductID, &o.SellerAgentID, &o.SellerAgentName,
		&o.BuyerAgentID, &o.BuyerIP, &o.BuyerTask, &o.ParentOrderID,
		&o.Deposit, &o.TotalPrice, &o.OfferPrice, &o.EscrowAmount,
		&o.Status, &o.ResultText, &o.RetryCount, &o.MaxRetries,
		&o.TimeoutAt, &o.CreatedAt, &o.AcceptedAt, &o.CompletedAt, &o.FailedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return o, nil
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

func (s *Store) CancelOrder(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`UPDATE orders SET status = 'cancelled', completed_at = ? WHERE id = ?`, now, id)
	return err
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

// FailOrder transitions processing → failed
func (s *Store) FailOrder(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE orders SET status = 'failed', failed_at = ?
		WHERE id = ? AND status = 'processing'
	`, now, id)
	return err
}

// ExtendOrderTimeout adds minutes to timeout_at (max 24h from accepted_at)
func (s *Store) ExtendOrderTimeout(id string, addMinutes int) error {
	_, err := s.db.Exec(`
		UPDATE orders SET timeout_at = datetime(timeout_at, '+' || ? || ' minutes')
		WHERE id = ? AND status = 'processing'
	`, addMinutes, id)
	return err
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
		SELECT o.id, o.product_id, COALESCE(p.name, ''), COALESCE(seller.name, o.seller_agent_name), COALESCE(seller.avatar, ''),
		       o.buyer_agent_id, COALESCE(buyer.name, ''), o.buyer_ip,
		       COALESCE(o.buyer_task, ''), COALESCE(o.parent_order_id, ''),
		       o.deposit, o.total_price, COALESCE(o.offer_price, 0), COALESCE(o.escrow_amount, 0),
		       o.status, o.result_text,
		       COALESCE(o.retry_count, 0), COALESCE(o.max_retries, 5),
		       COALESCE(o.timeout_at, ''), o.created_at, COALESCE(o.accepted_at, ''), COALESCE(o.completed_at, ''), COALESCE(o.failed_at, '')
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
		SELECT o.id, o.product_id, COALESCE(p.name, ''), COALESCE(seller.name, o.seller_agent_name), COALESCE(seller.avatar, ''),
		       o.buyer_agent_id, COALESCE(buyer.name, ''), o.buyer_ip,
		       COALESCE(o.buyer_task, ''), COALESCE(o.parent_order_id, ''),
		       o.deposit, o.total_price, COALESCE(o.offer_price, 0), COALESCE(o.escrow_amount, 0),
		       o.status, o.result_text,
		       COALESCE(o.retry_count, 0), COALESCE(o.max_retries, 5),
		       COALESCE(o.timeout_at, ''), o.created_at, COALESCE(o.accepted_at, ''), COALESCE(o.completed_at, ''), COALESCE(o.failed_at, '')
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
		SELECT o.id, o.product_id, COALESCE(p.name, ''), COALESCE(seller.name, o.seller_agent_name), COALESCE(seller.avatar, ''),
		       o.buyer_agent_id, COALESCE(buyer.name, ''), o.buyer_ip,
		       COALESCE(o.buyer_task, ''), COALESCE(o.parent_order_id, ''),
		       o.deposit, o.total_price, COALESCE(o.offer_price, 0), COALESCE(o.escrow_amount, 0),
		       o.status, o.result_text,
		       COALESCE(o.retry_count, 0), COALESCE(o.max_retries, 5),
		       COALESCE(o.timeout_at, ''), o.created_at, COALESCE(o.accepted_at, ''), COALESCE(o.completed_at, ''), COALESCE(o.failed_at, '')
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
		SELECT o.id, o.product_id, COALESCE(p.name, ''), COALESCE(seller.name, o.seller_agent_name), COALESCE(seller.avatar, ''),
		       o.buyer_agent_id, COALESCE(buyer.name, ''), o.buyer_ip,
		       COALESCE(o.buyer_task, ''), COALESCE(o.parent_order_id, ''),
		       o.deposit, o.total_price, COALESCE(o.offer_price, 0), COALESCE(o.escrow_amount, 0),
		       o.status, o.result_text,
		       COALESCE(o.retry_count, 0), COALESCE(o.max_retries, 5),
		       COALESCE(o.timeout_at, ''), o.created_at, COALESCE(o.accepted_at, ''), COALESCE(o.completed_at, ''), COALESCE(o.failed_at, '')
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

