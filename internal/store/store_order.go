package store

import (
	"database/sql"
	"fmt"
	"time"
)

func (s *Store) CreateOrder(o *Order) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if o.MaxRetries == 0 {
		o.MaxRetries = 5
	}
	// NULL product_id bypasses foreign key check (ad-hoc orders have no product)
	var productID interface{} = o.ProductID
	if o.ProductID == "" {
		productID = nil
	}
	_, err := s.db.Exec(`
		INSERT INTO orders (id, product_id, seller_agent_id, seller_agent_name, buyer_agent_id, buyer_publisher_id, buyer_name, buyer_ip, buyer_task, parent_order_id, deposit, total_price, offer_price, escrow_amount, status, max_retries, human_origin, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 'pending', ?, ?, ?)
	`, o.ID, productID, o.SellerAgentID, o.SellerAgentName, o.BuyerAgentID, o.BuyerPublisherID, o.BuyerName, o.BuyerIP, o.BuyerTask, o.ParentOrderID, o.Deposit, o.TotalPrice, o.OfferPrice, o.MaxRetries, o.HumanOrigin, now)
	return err
}

func (s *Store) GetOrder(id string) (*Order, error) {
	o := &Order{}
	err := s.db.QueryRow(`
		SELECT id, COALESCE(product_id, ''), COALESCE(seller_agent_id, ''), COALESCE(seller_agent_name, ''),
		       COALESCE(buyer_agent_id, ''), COALESCE(buyer_publisher_id, ''), COALESCE(buyer_name, ''), COALESCE(buyer_ip, ''), COALESCE(buyer_task, ''), COALESCE(parent_order_id, ''),
		       COALESCE(deposit, 0), COALESCE(total_price, 0), COALESCE(offer_price, 0), COALESCE(escrow_amount, 0),
		       COALESCE(status, ''), COALESCE(result_text, ''), COALESCE(retry_count, 0), COALESCE(max_retries, 5),
		       COALESCE(timeout_at, ''), COALESCE(created_at, ''), COALESCE(accepted_at, ''), COALESCE(completed_at, ''), COALESCE(failed_at, ''),
		       COALESCE(human_origin, 0), COALESCE(trace, '')
		FROM orders WHERE id = ?
	`, id).Scan(&o.ID, &o.ProductID, &o.SellerAgentID, &o.SellerAgentName,
		&o.BuyerAgentID, &o.BuyerPublisherID, &o.BuyerName, &o.BuyerIP, &o.BuyerTask, &o.ParentOrderID,
		&o.Deposit, &o.TotalPrice, &o.OfferPrice, &o.EscrowAmount,
		&o.Status, &o.ResultText, &o.RetryCount, &o.MaxRetries,
		&o.TimeoutAt, &o.CreatedAt, &o.AcceptedAt, &o.CompletedAt, &o.FailedAt,
		&o.HumanOrigin, &o.Trace)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return o, nil
}

func (s *Store) ListChildOrders(parentID string) ([]*Order, error) {
	rows, err := s.db.Query(`
		SELECT id, COALESCE(product_id, ''), COALESCE(seller_agent_id, ''), COALESCE(seller_agent_name, ''),
		       COALESCE(buyer_agent_id, ''), COALESCE(buyer_publisher_id, ''), COALESCE(buyer_name, ''), COALESCE(buyer_ip, ''), COALESCE(buyer_task, ''), COALESCE(parent_order_id, ''),
		       COALESCE(deposit, 0), COALESCE(total_price, 0), COALESCE(offer_price, 0), COALESCE(escrow_amount, 0),
		       COALESCE(status, ''), COALESCE(result_text, ''), COALESCE(retry_count, 0), COALESCE(max_retries, 5),
		       COALESCE(timeout_at, ''), COALESCE(created_at, ''), COALESCE(accepted_at, ''), COALESCE(completed_at, ''), COALESCE(failed_at, ''),
		       COALESCE(human_origin, 0), COALESCE(trace, '')
		FROM orders WHERE parent_order_id = ? ORDER BY created_at ASC
	`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Order
	for rows.Next() {
		o := &Order{}
		if err := rows.Scan(&o.ID, &o.ProductID, &o.SellerAgentID, &o.SellerAgentName,
			&o.BuyerAgentID, &o.BuyerPublisherID, &o.BuyerName, &o.BuyerIP, &o.BuyerTask, &o.ParentOrderID,
			&o.Deposit, &o.TotalPrice, &o.OfferPrice, &o.EscrowAmount,
			&o.Status, &o.ResultText, &o.RetryCount, &o.MaxRetries,
			&o.TimeoutAt, &o.CreatedAt, &o.AcceptedAt, &o.CompletedAt, &o.FailedAt,
			&o.HumanOrigin, &o.Trace); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
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

func (s *Store) CancelOrder(id string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`UPDATE orders SET status = 'cancelled', completed_at = ? WHERE id = ? AND status IN ('pending', 'processing')`, now, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
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

// FailOrder transitions processing → failed, returns rows affected
func (s *Store) FailOrder(id string, trace ...string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	traceText := ""
	if len(trace) > 0 {
		traceText = trace[0]
	}
	res, err := s.db.Exec(`
		UPDATE orders SET status = 'failed', failed_at = ?, trace = ?
		WHERE id = ? AND status = 'processing'
	`, now, traceText, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetOrderTrace updates just the trace column on any order
func (s *Store) SetOrderTrace(id string, trace string) error {
	_, err := s.db.Exec(`UPDATE orders SET trace = ? WHERE id = ?`, trace, id)
	return err
}

// AcceptOrderWithEscrow atomically debits buyer and accepts order
func (s *Store) AcceptOrderWithEscrow(orderID string, buyerAgentID string, price int, escrowAmount int, timeoutMinutes int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Debit buyer if applicable
	if buyerAgentID != "" && price > 0 {
		res, err := tx.Exec(`UPDATE agents SET credits = credits - ? WHERE id = ? AND credits >= ?`, price, buyerAgentID, price)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return fmt.Errorf("insufficient credits")
		}
	}

	// Transition order to processing
	now := time.Now().UTC()
	timeout := now.Add(time.Duration(timeoutMinutes) * time.Minute)
	res, err := tx.Exec(`
		UPDATE orders SET status = 'processing', escrow_amount = ?, accepted_at = ?, timeout_at = ?
		WHERE id = ? AND status = 'pending'
	`, escrowAmount, now.Format(time.RFC3339), timeout.Format(time.RFC3339), orderID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("order not pending")
	}

	return tx.Commit()
}

// DeliverOrderWithCredits atomically delivers order, mints credits, and updates counters
func (s *Store) DeliverOrderWithCredits(orderID string, resultText string, sellerAgentID string, escrowAmount int, productID string, trace ...string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Deliver order
	now := time.Now().UTC().Format(time.RFC3339)
	traceText := ""
	if len(trace) > 0 {
		traceText = trace[0]
	}
	res, err := tx.Exec(`
		UPDATE orders SET status = 'completed', result_text = ?, completed_at = ?, trace = ?
		WHERE id = ? AND status = 'processing'
	`, resultText, now, traceText, orderID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("order not processing")
	}

	// Mint credits to seller + platform
	if escrowAmount > 0 {
		if _, err := tx.Exec(`UPDATE agents SET credits = COALESCE(credits, 0) + ? WHERE id = ?`, escrowAmount, sellerAgentID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE platform_account SET credits = credits + ? WHERE id = 1`, escrowAmount); err != nil {
			return err
		}
	}

	// Update counters
	if productID != "" {
		tx.Exec(`UPDATE products SET purchase_count = purchase_count + 1 WHERE id = ?`, productID)
	}
	tx.Exec(`UPDATE agents SET total_tasks = total_tasks + 1 WHERE id = ?`, sellerAgentID)

	return tx.Commit()
}

// ExtendOrderTimeout adds minutes to timeout_at, capped at 24h from accepted_at
func (s *Store) ExtendOrderTimeout(id string, addMinutes int) error {
	res, err := s.db.Exec(`
		UPDATE orders SET timeout_at = datetime(timeout_at, '+' || ? || ' minutes')
		WHERE id = ? AND status = 'processing'
		AND datetime(timeout_at, '+' || ? || ' minutes') <= datetime(accepted_at, '+24 hours')
	`, addMinutes, id, addMinutes)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("extension exceeds 24h cap or order not processing")
	}
	return nil
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

func (s *Store) ListRecentOrders(limit int) ([]OrderListing, error) {
	if limit <= 0 {
		limit = 50
	}
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

// ListSellerOrders returns incoming orders for a seller agent (pending + processing)
func (s *Store) ListSellerOrders(sellerAgentID string) ([]OrderListing, error) {
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

// CountOrdersByStatus24h returns the number of orders for a seller agent with the given status
// that were updated (completed_at or failed_at) within the last 24 hours.
func (s *Store) CountOrdersByStatus24h(sellerAgentID, status string) (int, error) {
	cutoff := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	var col string
	switch status {
	case "completed":
		col = "completed_at"
	case "failed":
		col = "failed_at"
	default:
		col = "created_at"
	}
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM orders WHERE seller_agent_id = ? AND status = ? AND "+col+" >= ?",
		sellerAgentID, status, cutoff,
	).Scan(&count)
	return count, err
}

// ListBuyerOrders returns orders placed by a buyer agent
func (s *Store) ListBuyerOrders(buyerAgentID string) ([]OrderListing, error) {
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

