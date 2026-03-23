package store

import (
	"database/sql"
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
}

func (s *Store) GetAgentByName(name string) (*Agent, error) {
	a := &Agent{}
	var pub int
	err := s.db.QueryRow(`
		SELECT id, name, account_id, secret_hash, access_hash, description,
		       engine, avatar, public, max_tasks,
		       first_registered, total_tasks, total_uptime_s, last_connected,
		       COALESCE(tags, ''), COALESCE(credits, 0), COALESCE(price, 1)
		FROM agents WHERE name = ?
	`, name).Scan(&a.ID, &a.Name, &a.AccountID, &a.SecretHash, &a.AccessHash,
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
		INSERT INTO agents (id, name, account_id, secret_hash, access_hash, description, engine, avatar, public, max_tasks, first_registered, tags, price)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
	_, err := s.db.Exec(`DELETE FROM agents WHERE name = ?`, name)
	return err
}

func (s *Store) UpdateAgentPrice(name string, price int) error {
	_, err := s.db.Exec(`UPDATE agents SET price = ? WHERE name = ?`, price, name)
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

// DebitAgent subtracts credits from an agent (for agent-to-agent calls).
func (s *Store) DebitAgent(agentID string, amount int) error {
	_, err := s.db.Exec(`UPDATE agents SET credits = COALESCE(credits, 0) - ? WHERE id = ?`, amount, agentID)
	return err
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
			COALESCE((SELECT AVG(t.duration_ms) FROM tasks t WHERE t.agent_id = a.id AND t.status = 'ok'), 0) as avg_ms
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
			&a.Tags, &a.Credits, &a.Price, &a.SuccessfulTasks, &avgMs); err != nil {
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
