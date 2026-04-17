package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
	"github.com/google/uuid"
)

// handleAcceptOrder: seller accepts order (pending → processing), escrows buyer credits
func (s *Server) handleAcceptOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}
	if order.Status != "pending" {
		jsonError(w, "order is not pending", http.StatusBadRequest)
		return
	}

	// Auth: seller must own this order
	if !s.isOrderSeller(r, order) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Atomic escrow + accept
	price := order.TotalPrice
	if order.OfferPrice > 0 {
		price = order.OfferPrice
	}
	escrow := price
	buyerID := order.BuyerAgentID
	if order.HumanOrigin {
		escrow = 0
		buyerID = "" // skip debit for human-origin
	}
	if err := s.relay.Store.AcceptOrderWithEscrow(orderID, buyerID, price, escrow, 30); err != nil {
		if err.Error() == "insufficient credits" {
			jsonError(w, "buyer has insufficient credits", http.StatusPaymentRequired)
		} else {
			jsonError(w, "failed to accept order: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"order_id": orderID,
		"status":   "processing",
		"escrow":   escrow,
	})
	log.Printf("[order] %s accepted, escrow=%d, human_origin=%v", orderID, escrow, order.HumanOrigin)
}

// handleDeliverOrder: seller delivers result (processing → completed)
func (s *Server) handleDeliverOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}
	if order.Status != "processing" {
		jsonError(w, "order is not processing", http.StatusBadRequest)
		return
	}
	if !s.isOrderSeller(r, order) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Result string `json:"result"`
		Trace  string `json:"trace"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Atomic deliver + credit transfer + counter updates
	if err := s.relay.Store.DeliverOrderWithCredits(orderID, req.Result, order.SellerAgentID, order.EscrowAmount, order.ProductID, req.Trace); err != nil {
		jsonError(w, "failed to deliver order: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"order_id": orderID,
		"status":   "completed",
	})
	log.Printf("[order] %s delivered, seller +%d", orderID, order.EscrowAmount)
}

// handleExtendOrder: seller extends timeout
func (s *Server) handleExtendOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}
	if order.Status != "processing" {
		jsonError(w, "order is not processing", http.StatusBadRequest)
		return
	}
	if !s.isOrderSeller(r, order) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := s.relay.Store.ExtendOrderTimeout(orderID, 30); err != nil {
		jsonError(w, "failed to extend timeout", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"order_id": orderID,
	})
	log.Printf("[order] %s timeout extended +30min", orderID)
}

// handleGetOrder: get single order detail (public)
func (s *Server) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(order)
}

func (s *Server) handleListChildOrders(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	children, err := s.relay.Store.ListChildOrders(orderID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if children == nil {
		children = []*store.Order{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(children)
}

// handleListIncomingOrders: seller's pending + processing orders
func (s *Server) handleListIncomingOrders(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dbAgent, _ := s.relay.Store.GetAgentByName(name)
	if dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	orders, err := s.relay.Store.ListSellerOrders(dbAgent.ID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if orders == nil {
		orders = []store.OrderListing{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orders)
}

// handleListPlacedOrders: buyer's orders
func (s *Server) handleListPlacedOrders(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dbAgent, _ := s.relay.Store.GetAgentByName(name)
	if dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	orders, err := s.relay.Store.ListBuyerOrders(dbAgent.ID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if orders == nil {
		orders = []store.OrderListing{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orders)
}

// handleCreateAdHocOrder: agent creates an ad-hoc order to another agent (no product)
func (s *Server) handleCreateAdHocOrder(w http.ResponseWriter, r *http.Request) {
	targetName := r.PathValue("name")
	targetAgent, _ := s.relay.Store.GetAgentByName(targetName)
	if targetAgent == nil {
		jsonError(w, "target agent not found", http.StatusNotFound)
		return
	}

	// Non-public agents require a valid access token or secret token
	if !targetAgent.Public {
		token := auth.ExtractBearer(r)
		if token == "" {
			jsonError(w, "this agent is private — access key required", http.StatusUnauthorized)
			return
		}
		if !auth.VerifyToken(token, targetAgent.AccessHash) && !auth.VerifyToken(token, targetAgent.SecretHash) {
			jsonError(w, "invalid access key", http.StatusUnauthorized)
			return
		}
	}

	var req struct {
		Task          string `json:"task"`
		OfferPrice    int    `json:"offer_price"`
		BuyerAgentID  string `json:"buyer_agent_id"`
		ParentOrderID string `json:"parent_order_id,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Task == "" {
		jsonError(w, "task is required", http.StatusBadRequest)
		return
	}

	// Verify buyer identity if agent-to-agent
	resolvedBuyerID, ok := s.resolveBuyerAgent(r, req.BuyerAgentID)
	if !ok {
		jsonError(w, "unauthorized: buyer_agent_id does not match bearer token", http.StatusUnauthorized)
		return
	}

	// Use agent's default price if no offer price
	price := req.OfferPrice
	if price <= 0 {
		price = targetAgent.Price
	}

	// Determine human_origin: direct human request (no buyer agent) or inherited from parent
	humanOrigin := resolvedBuyerID == ""
	if !humanOrigin && req.ParentOrderID != "" {
		if parent, _ := s.relay.Store.GetOrder(req.ParentOrderID); parent != nil && parent.HumanOrigin {
			humanOrigin = true
		}
	}

	orderID := uuid.New().String()
	order := &store.Order{
		ID:              orderID,
		SellerAgentID:   targetAgent.ID,
		SellerAgentName: targetAgent.Name,
		BuyerAgentID:    resolvedBuyerID,
		BuyerIP:         derivePublisherID(r),
		BuyerTask:       req.Task,
		ParentOrderID:   req.ParentOrderID,
		TotalPrice:      price,
		OfferPrice:      price,
		HumanOrigin:     humanOrigin,
	}
	if err := s.relay.Store.CreateOrder(order); err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Push notification to agent if online
	if agent := s.relay.Registry.Get(targetAgent.Name); agent != nil {
		agent.Send(&relay.RelayMessage{Type: relay.TypeOrderNotify, OrderID: orderID})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"order_id": orderID,
		"status":   "pending",
		"agent":    targetAgent.Name,
	})
	log.Printf("[order] ad-hoc order %s created: %s → %s, offer=%d, human_origin=%v", orderID, resolvedBuyerID, targetAgent.Name, req.OfferPrice, humanOrigin)
}

// handleCancelOrder: cancel an order
func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}

	// Auth: require buyer or seller
	if !s.isOrderSeller(r, order) && !s.isOrderBuyer(r, order) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse optional trace from body
	var req struct {
		Trace string `json:"trace"`
	}
	json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req) // best-effort, body may be empty

	switch order.Status {
	case "pending":
		if _, err := s.relay.Store.CancelOrder(orderID); err != nil {
			jsonError(w, "failed to cancel", http.StatusInternalServerError)
			return
		}
	case "processing":
		// Cancel with race guard — only refund if we actually transitioned the status
		affected, err := s.relay.Store.CancelOrder(orderID)
		if err != nil {
			jsonError(w, "failed to cancel", http.StatusInternalServerError)
			return
		}
		if affected > 0 && order.BuyerAgentID != "" && order.EscrowAmount > 0 {
			s.relay.Store.MintCredit(order.BuyerAgentID, order.EscrowAmount)
		}
	default:
		jsonError(w, "order cannot be cancelled in state: "+order.Status, http.StatusBadRequest)
		return
	}

	// Store trace if provided
	if req.Trace != "" {
		s.relay.Store.SetOrderTrace(orderID, req.Trace)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":       true,
		"order_id": orderID,
	})
	log.Printf("[order] %s cancelled (was %s)", orderID, order.Status)
}

func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := s.relay.Store.ListRecentOrders(100)
	if err != nil {
		log.Printf("[orders] list error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if orders == nil {
		orders = []store.OrderListing{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orders)
}

