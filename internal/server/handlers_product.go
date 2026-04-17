package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
	"github.com/google/uuid"
)

func (s *Server) handleListProducts(w http.ResponseWriter, r *http.Request) {
	products, err := s.relay.Store.ListAllProducts()
	if err != nil {
		log.Printf("[products] list error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Enrich with online status
	onlineNames := make(map[string]bool)
	for _, name := range s.relay.Registry.Online() {
		onlineNames[name] = true
	}
	for i := range products {
		products[i].AgentOnline = onlineNames[products[i].AgentName]
	}

	qAgent := r.URL.Query().Get("agent")
	qSearch := r.URL.Query().Get("search")
	qSort := r.URL.Query().Get("sort") // newest, popular (default), price, rating

	if qAgent != "" || qSearch != "" {
		filtered := make([]store.ProductListing, 0)
		for _, p := range products {
			if qAgent != "" && p.AgentName != qAgent {
				continue
			}
			if qSearch != "" {
				q := strings.ToLower(qSearch)
				if !strings.Contains(strings.ToLower(p.Name), q) &&
					!strings.Contains(strings.ToLower(p.Description), q) &&
					!strings.Contains(strings.ToLower(p.AgentName), q) {
					continue
				}
			}
			filtered = append(filtered, p)
		}
		products = filtered
	}

	switch qSort {
	case "newest":
		sort.Slice(products, func(i, j int) bool { return products[i].CreatedAt > products[j].CreatedAt })
	case "price":
		sort.Slice(products, func(i, j int) bool { return products[i].Price < products[j].Price })
	case "rating":
		sort.Slice(products, func(i, j int) bool { return products[i].AvgRating > products[j].AvgRating })
	// default: already sorted by purchase_count DESC from DB
	}

	if products == nil {
		products = []store.ProductListing{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(products)
}

func (s *Server) handleListAgentProducts(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	dbAgent, err := s.relay.Store.GetAgentByName(agentName)
	if err != nil || dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	products, err := s.relay.Store.ListProductsByAgent(dbAgent.ID)
	if err != nil {
		log.Printf("[products] list by agent error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if products == nil {
		products = []store.Product{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(products)
}

func (s *Server) handleGetProduct(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("id")
	product, err := s.relay.Store.GetProduct(productID)
	if err != nil || product == nil {
		jsonError(w, "product not found", http.StatusNotFound)
		return
	}
	dbAgent, _ := s.relay.Store.GetAgentByID(product.AgentID)
	agentName := ""
	agentAvatar := ""
	agentEngine := ""
	agentOnline := false
	if dbAgent != nil {
		agentName = dbAgent.Name
		agentAvatar = dbAgent.Avatar
		agentEngine = dbAgent.Engine
		agentOnline = s.relay.Registry.Get(dbAgent.Name) != nil
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":              product.ID,
		"agent_id":        product.AgentID,
		"agent_name":      agentName,
		"agent_avatar":    agentAvatar,
		"agent_engine":    agentEngine,
		"agent_online":    agentOnline,
		"name":            product.Name,
		"description":     product.Description,
		"detail_markdown": product.DetailMarkdown,
		"detail_html":     product.DetailHTML,
		"price":           product.Price,
		"purchase_count":  product.PurchaseCount,
		"created_at":      product.CreatedAt,
	})
}

func (s *Server) handleCreateProduct(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	dbAgent, err := s.relay.Store.GetAgentByName(agentName)
	if err != nil || dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	var req struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		DetailMarkdown string `json:"detail_markdown"`
		DetailHTML     string `json:"detail_html"`
		Price          int    `json:"price"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		jsonError(w, "name is required", http.StatusBadRequest)
		return
	}

	p := &store.Product{
		ID:             uuid.New().String(),
		AgentID:        dbAgent.ID,
		Name:           req.Name,
		Description:    req.Description,
		DetailMarkdown: req.DetailMarkdown,
		DetailHTML:     req.DetailHTML,
		Price:          req.Price,
	}
	if err := s.relay.Store.CreateProduct(p); err != nil {
		log.Printf("[products] create error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
	log.Printf("[products] created %s for agent %s", p.Name, agentName)
}

func (s *Server) handleUpdateProduct(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("id")
	product, err := s.relay.Store.GetProduct(productID)
	if err != nil || product == nil {
		jsonError(w, "product not found", http.StatusNotFound)
		return
	}

	// Auth: must own the agent that owns this product
	dbAgent, _ := s.relay.Store.GetAgentByID(product.AgentID)
	if dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}
	if !s.authenticateAgentOwner(w, r, dbAgent.Name) {
		return
	}

	var req struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		DetailMarkdown string `json:"detail_markdown"`
		DetailHTML     string `json:"detail_html"`
		Price          int    `json:"price"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = product.Name
	}
	if req.Description == "" {
		req.Description = product.Description
	}
	if req.DetailMarkdown == "" {
		req.DetailMarkdown = product.DetailMarkdown
	}
	// detail_html: empty string means "clear it", so only default if not provided at all
	if req.DetailHTML == "" && product.DetailHTML != "" {
		req.DetailHTML = product.DetailHTML
	}
	if req.Price <= 0 {
		req.Price = product.Price
	}

	if err := s.relay.Store.UpdateProduct(productID, req.Name, req.Description, req.DetailMarkdown, req.DetailHTML, req.Price); err != nil {
		log.Printf("[products] update error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "id": productID})
}

func (s *Server) handleDeleteProduct(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("id")
	product, err := s.relay.Store.GetProduct(productID)
	if err != nil || product == nil {
		jsonError(w, "product not found", http.StatusNotFound)
		return
	}

	dbAgent, _ := s.relay.Store.GetAgentByID(product.AgentID)
	if dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}
	if !s.authenticateAgentOwner(w, r, dbAgent.Name) {
		return
	}

	if err := s.relay.Store.DeleteProduct(productID); err != nil {
		log.Printf("[products] delete error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	log.Printf("[products] deleted %s", productID)
}

// 3. Returns order_id + result. Buyer decides to confirm (pay 90%) or cancel.
func (s *Server) handleBuyProduct(w http.ResponseWriter, r *http.Request) {
	productID := r.PathValue("id")
	product, err := s.relay.Store.GetProduct(productID)
	if err != nil || product == nil {
		jsonError(w, "product not found", http.StatusNotFound)
		return
	}
	if product.Status != "active" {
		jsonError(w, "product is not active", http.StatusBadRequest)
		return
	}

	dbAgent, _ := s.relay.Store.GetAgentByID(product.AgentID)
	if dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	var req struct {
		Task         string `json:"task"`
		BuyerAgentID string `json:"buyer_agent_id,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, s.config.MaxMessageBytes)).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Verify buyer identity if agent-to-agent
	resolvedBuyerID, ok := s.resolveBuyerAgent(r, req.BuyerAgentID)
	if !ok {
		jsonError(w, "unauthorized: buyer_agent_id does not match bearer token", http.StatusUnauthorized)
		return
	}

	// Create async order — no credits deducted yet (escrow happens on accept)
	orderID := uuid.New().String()
	order := &store.Order{
		ID:              orderID,
		ProductID:       productID,
		SellerAgentID:   product.AgentID,
		SellerAgentName: dbAgent.Name,
		BuyerAgentID:    resolvedBuyerID,
		BuyerIP:         derivePublisherID(r),
		BuyerTask:       req.Task,
		TotalPrice:      product.Price,
		HumanOrigin:     resolvedBuyerID == "",
	}
	if err := s.relay.Store.CreateOrder(order); err != nil {
		log.Printf("[buy] create order error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Push notification to agent if online
	if agent := s.relay.Registry.Get(dbAgent.Name); agent != nil {
		agent.Send(&relay.RelayMessage{Type: relay.TypeOrderNotify, OrderID: orderID})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"order_id":    orderID,
		"status":      "pending",
		"product":     product.Name,
		"agent":       dbAgent.Name,
		"total_price": product.Price,
	})
	log.Printf("[buy] order %s created (async) for product %s", orderID, product.Name)
}

func (s *Server) handleProductSummary(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 30
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	products, err := s.relay.Store.ListAllProducts()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Sort by purchases descending
	sort.Slice(products, func(i, j int) bool {
		return products[i].PurchaseCount > products[j].PurchaseCount
	})

	// Limit
	if len(products) > limit {
		products = products[:limit]
	}

	// Return summary only (no detail_markdown)
	type summary struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		AgentName  string `json:"agent_name"`
		Price      int    `json:"price"`
		Purchases  int    `json:"purchases"`
		Desc       string `json:"description"`
	}
	result := make([]summary, len(products))
	for i, p := range products {
		result[i] = summary{
			ID:        p.ID,
			Name:      p.Name,
			AgentName: p.AgentName,
			Price:     p.Price,
			Purchases: p.PurchaseCount,
			Desc:      p.Description,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

