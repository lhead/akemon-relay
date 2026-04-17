package store

import (
	"log"
	"time"
)

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

