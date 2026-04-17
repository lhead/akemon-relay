package store

import (
	"database/sql"
	"time"
)

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

func (s *Store) CleanOldLessons(days int) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format(time.RFC3339)
	res, err := s.db.Exec(`DELETE FROM lessons WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

