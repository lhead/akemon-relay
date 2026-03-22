package store

import (
	"database/sql"
	"time"
)

// --- PK Matches ---

type PKMatch struct {
	ID            string  `json:"id"`
	Mode          string  `json:"mode"`
	Status        string  `json:"status"`
	Title         string  `json:"title"`
	Prompt        string  `json:"prompt"`
	ConfigJSON    string  `json:"config_json"`
	AgentAID      string  `json:"agent_a_id"`
	AgentAName    string  `json:"agent_a_name"`
	AgentAAccount string  `json:"agent_a_account"`
	AgentAEngine  string  `json:"agent_a_engine"`
	AgentBID      string  `json:"agent_b_id"`
	AgentBName    string  `json:"agent_b_name"`
	AgentBAccount string  `json:"agent_b_account"`
	AgentBEngine  string  `json:"agent_b_engine"`
	WinnerAgentID *string `json:"winner_agent_id"`
	WinReason     string  `json:"win_reason"`
	TotalRounds   int     `json:"total_rounds"`
	StartedAt     *string `json:"started_at"`
	FinishedAt    *string `json:"finished_at"`
	CreatedAt     string  `json:"created_at"`
}

func (s *Store) CreatePKMatch(m *PKMatch) error {
	m.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO pk_matches (id, mode, status, title, prompt, config_json,
			agent_a_id, agent_a_name, agent_a_account, agent_a_engine,
			agent_b_id, agent_b_name, agent_b_account, agent_b_engine,
			total_rounds, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.ID, m.Mode, m.Status, m.Title, m.Prompt, m.ConfigJSON,
		m.AgentAID, m.AgentAName, m.AgentAAccount, m.AgentAEngine,
		m.AgentBID, m.AgentBName, m.AgentBAccount, m.AgentBEngine,
		m.TotalRounds, m.CreatedAt)
	return err
}

func (s *Store) GetPKMatch(id string) (*PKMatch, error) {
	m := &PKMatch{}
	err := s.db.QueryRow(`
		SELECT id, mode, status, title, prompt, config_json,
			agent_a_id, agent_a_name, agent_a_account, agent_a_engine,
			agent_b_id, agent_b_name, agent_b_account, agent_b_engine,
			winner_agent_id, win_reason, total_rounds, started_at, finished_at, created_at
		FROM pk_matches WHERE id = ?
	`, id).Scan(&m.ID, &m.Mode, &m.Status, &m.Title, &m.Prompt, &m.ConfigJSON,
		&m.AgentAID, &m.AgentAName, &m.AgentAAccount, &m.AgentAEngine,
		&m.AgentBID, &m.AgentBName, &m.AgentBAccount, &m.AgentBEngine,
		&m.WinnerAgentID, &m.WinReason, &m.TotalRounds, &m.StartedAt, &m.FinishedAt, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

func (s *Store) ListPKMatches(status string, limit, offset int) ([]PKMatch, error) {
	query := `SELECT id, mode, status, title, prompt, config_json,
		agent_a_id, agent_a_name, agent_a_account, agent_a_engine,
		agent_b_id, agent_b_name, agent_b_account, agent_b_engine,
		winner_agent_id, win_reason, total_rounds, started_at, finished_at, created_at
		FROM pk_matches`
	var args []any
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []PKMatch
	for rows.Next() {
		var m PKMatch
		if err := rows.Scan(&m.ID, &m.Mode, &m.Status, &m.Title, &m.Prompt, &m.ConfigJSON,
			&m.AgentAID, &m.AgentAName, &m.AgentAAccount, &m.AgentAEngine,
			&m.AgentBID, &m.AgentBName, &m.AgentBAccount, &m.AgentBEngine,
			&m.WinnerAgentID, &m.WinReason, &m.TotalRounds, &m.StartedAt, &m.FinishedAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}

func (s *Store) UpdatePKMatchStatus(id, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if status == "in_progress" {
		_, err := s.db.Exec(`UPDATE pk_matches SET status = ?, started_at = ? WHERE id = ?`, status, now, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE pk_matches SET status = ? WHERE id = ?`, status, id)
	return err
}

func (s *Store) FinishPKMatch(id string, winnerID *string, winReason string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE pk_matches SET status = 'completed', winner_agent_id = ?, win_reason = ?, finished_at = ? WHERE id = ?
	`, winnerID, winReason, now, id)
	return err
}

func (s *Store) AbortPKMatch(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`UPDATE pk_matches SET status = 'aborted', finished_at = ? WHERE id = ?`, now, id)
	return err
}

// --- PK Rounds ---

type PKRound struct {
	ID          string `json:"id"`
	MatchID     string `json:"match_id"`
	RoundNumber int    `json:"round_number"`
	PromptA     string `json:"prompt_a"`
	PromptB     string `json:"prompt_b"`
	ResponseA   string `json:"response_a"`
	ResponseB   string `json:"response_b"`
	ResponseAMs int    `json:"response_a_ms"`
	ResponseBMs int    `json:"response_b_ms"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

func (s *Store) CreatePKRound(r *PKRound) error {
	r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO pk_rounds (id, match_id, round_number, prompt_a, prompt_b, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, r.ID, r.MatchID, r.RoundNumber, r.PromptA, r.PromptB, r.Status, r.CreatedAt)
	return err
}

func (s *Store) UpdatePKRound(id, responseA, responseB string, aMs, bMs int) error {
	_, err := s.db.Exec(`
		UPDATE pk_rounds SET response_a = ?, response_b = ?, response_a_ms = ?, response_b_ms = ?, status = 'completed' WHERE id = ?
	`, responseA, responseB, aMs, bMs, id)
	return err
}

func (s *Store) ListPKRounds(matchID string) ([]PKRound, error) {
	rows, err := s.db.Query(`
		SELECT id, match_id, round_number, prompt_a, prompt_b, response_a, response_b,
			response_a_ms, response_b_ms, status, created_at
		FROM pk_rounds WHERE match_id = ? ORDER BY round_number ASC
	`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rounds []PKRound
	for rows.Next() {
		var r PKRound
		if err := rows.Scan(&r.ID, &r.MatchID, &r.RoundNumber, &r.PromptA, &r.PromptB,
			&r.ResponseA, &r.ResponseB, &r.ResponseAMs, &r.ResponseBMs, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		rounds = append(rounds, r)
	}
	return rounds, rows.Err()
}

// --- PK Votes ---

type PKVote struct {
	ID        string `json:"id"`
	MatchID   string `json:"match_id"`
	VoterIP   string `json:"voter_ip"`
	VotedFor  string `json:"voted_for"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) CreatePKVote(v *PKVote) error {
	v.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		INSERT INTO pk_votes (id, match_id, voter_ip, voted_for, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, v.ID, v.MatchID, v.VoterIP, v.VotedFor, v.CreatedAt)
	return err
}

type PKVoteCounts struct {
	VotesA int `json:"votes_a"`
	VotesB int `json:"votes_b"`
}

func (s *Store) GetPKVoteCounts(matchID string) (PKVoteCounts, error) {
	var c PKVoteCounts
	err := s.db.QueryRow(`
		SELECT
			COALESCE(SUM(CASE WHEN voted_for = 'a' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN voted_for = 'b' THEN 1 ELSE 0 END), 0)
		FROM pk_votes WHERE match_id = ?
	`, matchID).Scan(&c.VotesA, &c.VotesB)
	return c, err
}

func (s *Store) HasVoted(matchID, voterIP string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM pk_votes WHERE match_id = ? AND voter_ip = ?`, matchID, voterIP).Scan(&count)
	return count > 0, err
}
