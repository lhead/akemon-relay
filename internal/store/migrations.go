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
    last_connected   TEXT
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
`
