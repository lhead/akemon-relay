package store

import (
	"time"
)

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
		SELECT o.id, COALESCE(o.product_id, ''), COALESCE(p.name, ''), COALESCE(seller.name, o.seller_agent_name), COALESCE(seller.avatar, ''),
		       COALESCE(o.buyer_agent_id, ''), COALESCE(o.buyer_publisher_id, ''), COALESCE(NULLIF(o.buyer_name,''), buyer.name, ''), COALESCE(o.buyer_ip, ''),
		       COALESCE(o.buyer_task, ''), COALESCE(o.parent_order_id, ''),
		       COALESCE(o.deposit, 0), COALESCE(o.total_price, 0), COALESCE(o.offer_price, 0), COALESCE(o.escrow_amount, 0),
		       COALESCE(o.status, ''), COALESCE(o.result_text, ''),
		       COALESCE(o.retry_count, 0), COALESCE(o.max_retries, 5),
		       COALESCE(o.timeout_at, ''), COALESCE(o.created_at, ''), COALESCE(o.accepted_at, ''), COALESCE(o.completed_at, ''), COALESCE(o.failed_at, '')
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
			&o.BuyerAgentID, &o.BuyerPublisherID, &o.BuyerName, &o.BuyerIP,
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

