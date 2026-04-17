package store

import (
	"database/sql"
	"time"
)

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

