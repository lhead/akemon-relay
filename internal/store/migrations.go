package store

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
    id          TEXT PRIMARY KEY,
    first_seen  TEXT NOT NULL,
    last_active TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agents (
    id               TEXT PRIMARY KEY,
    name             TEXT UNIQUE NOT NULL,
    account_id       TEXT NOT NULL REFERENCES accounts(id),
    secret_hash      TEXT NOT NULL,
    access_hash      TEXT NOT NULL,
    description      TEXT DEFAULT '',
    engine           TEXT DEFAULT 'claude',
    avatar           TEXT DEFAULT '',
    public           INTEGER DEFAULT 0,
    max_tasks        INTEGER DEFAULT 0,
    first_registered TEXT NOT NULL,
    total_tasks      INTEGER DEFAULT 0,
    total_uptime_s   INTEGER DEFAULT 0,
    last_connected   TEXT,
    tags             TEXT DEFAULT '',
    credits          INTEGER DEFAULT 0,
    price            INTEGER DEFAULT 1
);

CREATE TABLE IF NOT EXISTS tasks (
    id           TEXT PRIMARY KEY,
    agent_id     TEXT NOT NULL REFERENCES agents(id),
    timestamp    TEXT NOT NULL,
    duration_ms  INTEGER,
    status       TEXT NOT NULL,
    cost_credits INTEGER DEFAULT 0,
    publisher_ip TEXT,
    task_hash    TEXT,              -- SHA256 of task content (privacy-safe dedup/matching, not raw content)
    domain       TEXT DEFAULT ''    -- auto-classified domain tag (future: AI-assigned)
);

CREATE TABLE IF NOT EXISTS connections (
    id                TEXT PRIMARY KEY,
    agent_id          TEXT NOT NULL REFERENCES agents(id),
    connected_at      TEXT NOT NULL,
    disconnected_at   TEXT,
    disconnect_reason TEXT
);

CREATE INDEX IF NOT EXISTS idx_agents_account ON agents(account_id);
CREATE INDEX IF NOT EXISTS idx_agents_name ON agents(name);
CREATE INDEX IF NOT EXISTS idx_tasks_agent ON tasks(agent_id);
CREATE INDEX IF NOT EXISTS idx_connections_agent ON connections(agent_id);

-- PK Arena tables
CREATE TABLE IF NOT EXISTS pk_matches (
    id              TEXT PRIMARY KEY,
    mode            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    title           TEXT NOT NULL DEFAULT '',
    prompt          TEXT NOT NULL,
    config_json     TEXT NOT NULL DEFAULT '{}',
    agent_a_id      TEXT NOT NULL,
    agent_a_name    TEXT NOT NULL,
    agent_a_account TEXT NOT NULL,
    agent_a_engine  TEXT NOT NULL,
    agent_b_id      TEXT NOT NULL,
    agent_b_name    TEXT NOT NULL,
    agent_b_account TEXT NOT NULL,
    agent_b_engine  TEXT NOT NULL,
    winner_agent_id TEXT,
    win_reason      TEXT DEFAULT '',
    total_rounds    INTEGER NOT NULL DEFAULT 1,
    started_at      TEXT,
    finished_at     TEXT,
    created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pk_matches_created ON pk_matches(created_at);

CREATE TABLE IF NOT EXISTS pk_rounds (
    id              TEXT PRIMARY KEY,
    match_id        TEXT NOT NULL REFERENCES pk_matches(id),
    round_number    INTEGER NOT NULL,
    prompt_a        TEXT NOT NULL DEFAULT '',
    prompt_b        TEXT NOT NULL DEFAULT '',
    response_a      TEXT NOT NULL DEFAULT '',
    response_b      TEXT NOT NULL DEFAULT '',
    response_a_ms   INTEGER DEFAULT 0,
    response_b_ms   INTEGER DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending',
    created_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pk_rounds_match ON pk_rounds(match_id);

CREATE TABLE IF NOT EXISTS pk_votes (
    id          TEXT PRIMARY KEY,
    match_id    TEXT NOT NULL REFERENCES pk_matches(id),
    voter_ip    TEXT NOT NULL,
    voted_for   TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    UNIQUE(match_id, voter_ip)
);
CREATE INDEX IF NOT EXISTS idx_pk_votes_match ON pk_votes(match_id);

-- Migration: add tags column to agents (idempotent)
-- SQLite: ALTER TABLE ADD COLUMN is safe to retry (errors silently if exists)

CREATE TABLE IF NOT EXISTS session_context (
    agent_name TEXT NOT NULL,
    session_id TEXT NOT NULL,
    context    TEXT NOT NULL,
    updated_at TEXT DEFAULT (datetime('now')),
    PRIMARY KEY (agent_name, session_id)
);

CREATE TABLE IF NOT EXISTS products (
    id              TEXT PRIMARY KEY,
    agent_id        TEXT NOT NULL REFERENCES agents(id),
    name            TEXT NOT NULL,
    description     TEXT DEFAULT '',
    detail_markdown TEXT DEFAULT '',
    price           INTEGER DEFAULT 1,
    status          TEXT DEFAULT 'active',
    purchase_count  INTEGER DEFAULT 0,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_products_agent ON products(agent_id);
CREATE INDEX IF NOT EXISTS idx_products_status ON products(status);

CREATE TABLE IF NOT EXISTS orders (
    id             TEXT PRIMARY KEY,
    product_id     TEXT DEFAULT '' REFERENCES products(id),
    buyer_agent_id TEXT DEFAULT '',
    buyer_ip       TEXT DEFAULT '',
    deposit        INTEGER NOT NULL,
    total_price    INTEGER NOT NULL,
    status         TEXT DEFAULT 'pending',
    result_text    TEXT DEFAULT '',
    created_at     TEXT NOT NULL,
    completed_at   TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_orders_product ON orders(product_id);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);

CREATE TABLE IF NOT EXISTS platform_account (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    credits INTEGER DEFAULT 0
);
INSERT OR IGNORE INTO platform_account (id, credits) VALUES (1, 0);
`
