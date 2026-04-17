package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/akemon/akemon-relay/internal/store"
	"github.com/google/uuid"
)

func (s *Server) handleSubmitReview(w http.ResponseWriter, r *http.Request) {
	orderID := r.PathValue("id")
	order, err := s.relay.Store.GetOrder(orderID)
	if err != nil || order == nil {
		jsonError(w, "order not found", http.StatusNotFound)
		return
	}
	if order.Status != "completed" {
		jsonError(w, "can only review completed orders", http.StatusBadRequest)
		return
	}

	// Auth: only the buyer can review
	if !s.isOrderBuyer(r, order) {
		jsonError(w, "unauthorized: only the buyer can review", http.StatusUnauthorized)
		return
	}

	var body struct {
		Rating  int    `json:"rating"`
		Comment string `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.Rating < 1 || body.Rating > 5 {
		jsonError(w, "rating must be 1-5", http.StatusBadRequest)
		return
	}

	// Determine reviewer name from buyer agent
	reviewerName := "anonymous"
	if order.BuyerAgentID != "" {
		agent, _ := s.relay.Store.GetAgentByID(order.BuyerAgentID)
		if agent != nil {
			reviewerName = agent.Name
		}
	}

	review, err := s.relay.Store.CreateReview(uuid.New().String(), orderID, order.ProductID, reviewerName, body.Rating, body.Comment)
	if err != nil {
		log.Printf("[review] create error: %v", err)
		jsonError(w, "failed to create review (may already exist)", http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(review)
}

func (s *Server) handleListProductReviews(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("id")
	reviews, err := s.relay.Store.ListProductReviews(productID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if reviews == nil {
		reviews = []store.Review{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(reviews)
}

func (s *Server) handleListUnreviewedOrders(w http.ResponseWriter, r *http.Request) {
	buyer := r.URL.Query().Get("buyer")
	if buyer == "" {
		jsonError(w, "buyer parameter required", http.StatusBadRequest)
		return
	}
	orders, err := s.relay.Store.ListUnreviewedOrders(buyer)
	if err != nil {
		log.Printf("[review] unreviewed error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if orders == nil {
		orders = []store.OrderListing{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(orders)
}

