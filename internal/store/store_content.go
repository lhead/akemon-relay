package store

import (
	"database/sql"
	"time"
)

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

func (s *Store) FeedOrderStats(since string) (FeedStats, error) {
	var stats FeedStats
	s.db.QueryRow(`SELECT COUNT(*) FROM orders WHERE status = 'completed' AND completed_at > ?`, since).Scan(&stats.CompletedOrders)
	s.db.QueryRow(`SELECT COALESCE(SUM(total_price), 0) FROM orders WHERE status = 'completed' AND completed_at > ?`, since).Scan(&stats.TotalCredits)
	s.db.QueryRow(`SELECT COUNT(*) FROM agents WHERE last_connected > ?`, since).Scan(&stats.ActiveAgents)
	return stats, nil
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

