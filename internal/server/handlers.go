package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
	"github.com/google/uuid"
)

// --- Session Context API ---

func (s *Server) handleGetContext(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	sessionID := r.PathValue("sessionId")
	if agentName == "" || sessionID == "" {
		http.Error(w, `{"error":"missing parameters"}`, http.StatusBadRequest)
		return
	}

	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	ctx, err := s.relay.Store.GetContext(agentName, sessionID)
	if err != nil {
		log.Printf("[context] GET error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(ctx))
}

func (s *Server) handleChatConversations(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		http.Error(w, `{"error":"missing agent name"}`, http.StatusBadRequest)
		return
	}
	s.proxyAgentSelfAPI(w, agentName, "/self/conversations")
}

func (s *Server) handleChatMine(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		http.Error(w, `{"error":"missing agent name"}`, http.StatusBadRequest)
		return
	}
	// Private agents require access or secret token
	if !s.checkAgentAccess(w, r, agentName) {
		return
	}
	pubId := derivePublisherID(r)
	convId := "pub_" + pubId
	// Product-scoped conversations: append :prod_{id} suffix
	if productID := r.URL.Query().Get("product_id"); productID != "" {
		convId += ":prod_" + productID
	}
	s.proxyAgentSelfAPI(w, agentName, "/self/conversation/"+convId)
}

func (s *Server) handleChatConversation(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	convId := r.PathValue("convId")
	if agentName == "" || convId == "" {
		http.Error(w, `{"error":"missing parameters"}`, http.StatusBadRequest)
		return
	}
	s.proxyAgentSelfAPI(w, agentName, "/self/conversation/"+convId)
}

func (s *Server) handleAgentControl(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		http.Error(w, `{"error":"missing agent name"}`, http.StatusBadRequest)
		return
	}

	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	var req struct {
		Action string `json:"action"` // shutdown, set_public, set_private, set_price
		Price  int    `json:"price,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	switch req.Action {
	case "shutdown", "set_public", "set_private", "set_price", "delete":
		// ok
	default:
		http.Error(w, `{"error":"unknown action, supported: shutdown, set_public, set_private, set_price, delete"}`, http.StatusBadRequest)
		return
	}

	// Update DB for visibility changes
	if req.Action == "set_public" || req.Action == "set_private" {
		dbAgent, err := s.relay.Store.GetAgentByName(agentName)
		if err != nil || dbAgent == nil {
			http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
			return
		}
		isPublic := req.Action == "set_public"
		if err := s.relay.Store.UpdateAgentPublic(agentName, isPublic); err != nil {
			log.Printf("[control] update public error: %v", err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}
		// Update in-memory agent state
		if agent := s.relay.Registry.Get(agentName); agent != nil {
			agent.Public = isPublic
		}
	}

	// Handle price change
	if req.Action == "set_price" {
		price := req.Price
		if price < 1 {
			price = 1
		}
		if price > 10000 {
			price = 10000
		}
		if err := s.relay.Store.UpdateAgentPrice(agentName, price); err != nil {
			log.Printf("[control] update price error: %v", err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}
		// Update in-memory
		if a := s.relay.Registry.Get(agentName); a != nil {
			a.Price = price
		}
	}

	// Handle delete
	if req.Action == "delete" {
		if err := s.relay.Store.DeleteAgent(agentName); err != nil {
			log.Printf("[control] delete agent error: %v", err)
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}
		// Disconnect if online
		if a := s.relay.Registry.Get(agentName); a != nil {
			s.relay.Registry.Unregister(agentName, 0)
			a.Conn.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "action": "delete"})
		log.Printf("[control] %s: deleted", agentName)
		return
	}

	// Forward control message to agent via WebSocket (if online)
	agent := s.relay.Registry.Get(agentName)
	if agent != nil {
		msg := &relay.RelayMessage{
			Type:   relay.TypeControl,
			Action: req.Action,
		}
		if err := agent.Send(msg); err != nil {
			log.Printf("[control] send to agent error: %v", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	online := agent != nil
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":     true,
		"action": req.Action,
		"online": online,
	})
	log.Printf("[control] %s: action=%s online=%v", agentName, req.Action, online)
}

// --- Agent Self (consciousness) ---

func (s *Server) handleUpdateAgentSelf(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	var req struct {
		SelfIntro   string      `json:"self_intro"`
		Canvas      string      `json:"canvas"`
		Mood        string      `json:"mood"`
		ProfileHTML string      `json:"profile_html"`
		Broadcast   string      `json:"broadcast"`
		Directives  string      `json:"directives"`
		BioState    interface{} `json:"bio_state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}

	// Serialize bio_state to JSON string for storage
	bioStateJSON := ""
	if req.BioState != nil {
		if b, err := json.Marshal(req.BioState); err == nil {
			bioStateJSON = string(b)
		}
	}

	if err := s.relay.Store.UpdateAgentSelf(agentName, req.SelfIntro, req.Canvas, req.Mood, req.ProfileHTML, req.Broadcast, bioStateJSON, req.Directives); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// handleSpendCredits deducts credits from an agent (for buy_food etc.)
func (s *Server) handleSpendCredits(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	var req struct {
		Amount int    `json:"amount"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Amount <= 0 {
		jsonError(w, "amount must be positive", http.StatusBadRequest)
		return
	}

	remaining, err := s.relay.Store.SpendAgentCredits(agentName, req.Amount, req.Reason)
	if err != nil {
		if err.Error() == "insufficient credits" {
			jsonError(w, "insufficient credits", http.StatusPaymentRequired)
		} else {
			jsonError(w, "db error", http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":        true,
		"spent":     req.Amount,
		"remaining": remaining,
	})
}

// --- Agent Games API ---

func (s *Server) handleListGames(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	games, err := s.relay.Store.ListGames(agentName)
	if err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	if games == nil {
		games = []store.AgentGame{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(games)
}

func (s *Server) handleUpsertGame(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		HTML        string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Title == "" || req.HTML == "" {
		jsonError(w, "title and html are required", http.StatusBadRequest)
		return
	}

	if err := s.relay.Store.UpsertGame(agentName, slug, req.Title, req.Description, req.HTML); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleDeleteGame(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}
	if err := s.relay.Store.DeleteGame(agentName, slug); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// --- Notes ---

func (s *Server) handleListNotes(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	notes, err := s.relay.Store.ListNotes(agentName)
	if err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	if notes == nil {
		notes = []store.AgentNote{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(notes)
}

func (s *Server) handleUpsertNote(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}
	var req struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Title == "" || req.Content == "" {
		jsonError(w, "title and content required", http.StatusBadRequest)
		return
	}
	if err := s.relay.Store.UpsertNote(agentName, slug, req.Title, req.Content); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}
	if err := s.relay.Store.DeleteNote(agentName, slug); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// --- Pages ---

func (s *Server) handleListPages(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	pages, err := s.relay.Store.ListPages(agentName)
	if err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	if pages == nil {
		pages = []store.AgentPage{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pages)
}

func (s *Server) handleUpsertPage(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		HTML        string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Title == "" || req.HTML == "" {
		jsonError(w, "title and html required", http.StatusBadRequest)
		return
	}
	if err := s.relay.Store.UpsertPage(agentName, slug, req.Title, req.Description, req.HTML); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

func (s *Server) handleDeletePage(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	slug := r.PathValue("slug")
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}
	if err := s.relay.Store.DeletePage(agentName, slug); err != nil {
		jsonError(w, "db error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}


func (s *Server) handlePutContext(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	sessionID := r.PathValue("sessionId")
	if agentName == "" || sessionID == "" {
		http.Error(w, `{"error":"missing parameters"}`, http.StatusBadRequest)
		return
	}

	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8192+1))
	if err != nil {
		http.Error(w, `{"error":"failed to read body"}`, http.StatusBadRequest)
		return
	}
	if len(body) > 8192 {
		http.Error(w, `{"error":"context too large (max 8KB)"}`, http.StatusRequestEntityTooLarge)
		return
	}

	if err := s.relay.Store.PutContext(agentName, sessionID, string(body)); err != nil {
		log.Printf("[context] PUT error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// --- Product API ---

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

// handleBuyProduct: deposit/final-payment flow
// 1. Buyer pays 10% deposit
// 2. Agent produces result
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

// --- Agent Tasks (Phase 2) ---

func (s *Server) handleListAgentTasks(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dbAgent, err := s.relay.Store.GetAgentByName(name)
	if err != nil || dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}
	tasks, err := s.relay.Store.ListPendingTasks(dbAgent.ID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if tasks == nil {
		tasks = []store.AgentTask{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func (s *Server) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	taskID := r.PathValue("id")

	dbAgent, err := s.relay.Store.GetAgentByName(name)
	if err != nil || dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	// Auth
	token := auth.ExtractBearer(r)
	if token == "" || !auth.VerifyToken(token, dbAgent.SecretHash) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Verify task belongs to this agent
	task, err := s.relay.Store.GetAgentTask(taskID)
	if err != nil || task == nil {
		jsonError(w, "task not found", http.StatusNotFound)
		return
	}
	if task.AgentID != dbAgent.ID {
		jsonError(w, "task does not belong to this agent", http.StatusForbidden)
		return
	}

	affected, err := s.relay.Store.ClaimTask(taskID)
	if err != nil {
		jsonError(w, "failed to claim", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		jsonError(w, "task already claimed or not pending", http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "task_id": taskID})
	log.Printf("[tasks] %s claimed task %s (%s)", name, taskID, task.Type)
}

func (s *Server) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	taskID := r.PathValue("id")

	dbAgent, err := s.relay.Store.GetAgentByName(name)
	if err != nil || dbAgent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}

	// Auth
	token := auth.ExtractBearer(r)
	if token == "" || !auth.VerifyToken(token, dbAgent.SecretHash) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	task, err := s.relay.Store.GetAgentTask(taskID)
	if err != nil || task == nil {
		jsonError(w, "task not found", http.StatusNotFound)
		return
	}
	if task.AgentID != dbAgent.ID {
		jsonError(w, "task does not belong to this agent", http.StatusForbidden)
		return
	}

	var req struct {
		Result string `json:"result"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}

	affected, err := s.relay.Store.CompleteTask(taskID, req.Result)
	if err != nil {
		jsonError(w, "failed to complete", http.StatusInternalServerError)
		return
	}
	if affected == 0 {
		jsonError(w, "task not claimed or already completed", http.StatusConflict)
		return
	}

	// Apply result based on task type
	s.applyTaskResult(dbAgent, task.Type, req.Result)

	// Reward agent
	s.relay.Store.MintCredit(dbAgent.ID, 3)
	s.relay.Store.IncrementAgentTasks(dbAgent.ID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "task_id": taskID})
	log.Printf("[tasks] %s completed task %s (%s)", name, taskID, task.Type)
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

func (s *Server) handleCreateSuggestion(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type       string `json:"type"`
		TargetName string `json:"target_name"`
		FromAgent  string `json:"from_agent"`
		Title      string `json:"title"`
		Content    string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.Type != "platform" && body.Type != "agent" {
		jsonError(w, "type must be 'platform' or 'agent'", http.StatusBadRequest)
		return
	}
	if body.Title == "" || body.Content == "" {
		jsonError(w, "title and content required", http.StatusBadRequest)
		return
	}
	if body.FromAgent == "" {
		jsonError(w, "from_agent required", http.StatusBadRequest)
		return
	}
	sg, err := s.relay.Store.CreateSuggestion(uuid.New().String(), body.Type, body.TargetName, body.FromAgent, body.Title, body.Content)
	if err != nil {
		log.Printf("[suggestion] create error: %v", err)
		jsonError(w, "failed to create suggestion", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sg)
}

func (s *Server) handleListSuggestions(w http.ResponseWriter, r *http.Request) {
	sType := r.URL.Query().Get("type")
	target := r.URL.Query().Get("target")
	suggestions, err := s.relay.Store.ListSuggestions(sType, target, 100)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if suggestions == nil {
		suggestions = []store.Suggestion{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(suggestions)
}

func (s *Server) handleListAgentSuggestions(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	suggestions, err := s.relay.Store.ListSuggestions("agent", name, 50)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if suggestions == nil {
		suggestions = []store.Suggestion{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(suggestions)
}

// --- Execution Logs ---

func (s *Server) handleCreateExecutionLog(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")

	// Auth: must be the agent itself
	agent, err := s.relay.Store.GetAgentByName(agentName)
	if err != nil || agent == nil {
		jsonError(w, "agent not found", http.StatusNotFound)
		return
	}
	token := auth.ExtractBearer(r)
	if token == "" || !auth.VerifyToken(token, agent.SecretHash) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Type  string `json:"type"`
		RefID string `json:"ref_id"`
		Status string `json:"status"`
		Error string `json:"error"`
		Trace string `json:"trace"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 100_000)).Decode(&req); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Type == "" || req.Status == "" {
		jsonError(w, "type and status required", http.StatusBadRequest)
		return
	}

	l := &store.ExecutionLog{
		ID:        uuid.New().String(),
		AgentID:   agent.ID,
		AgentName: agentName,
		Type:      req.Type,
		RefID:     req.RefID,
		Status:    req.Status,
		Error:     req.Error,
		Trace:     req.Trace,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.relay.Store.CreateExecutionLog(l); err != nil {
		jsonError(w, "failed to create log: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "id": l.ID})
}

func (s *Server) handleListExecutionLogs(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	status := r.URL.Query().Get("status")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	logs, err := s.relay.Store.ListExecutionLogs(agentName, status, limit)
	if err != nil {
		jsonError(w, "failed to list logs: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if logs == nil {
		logs = []store.ExecutionLog{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

// --- Owner Dashboard ---

func (s *Server) handleListAccountAgents(w http.ResponseWriter, r *http.Request) {
	token := auth.ExtractBearer(r)
	if token == "" {
		jsonError(w, "authorization required", http.StatusUnauthorized)
		return
	}

	// Find which agent this token belongs to, then get account_id
	allAgents, err := s.relay.Store.ListAgents()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	var accountID string
	for _, a := range allAgents {
		agent, err := s.relay.Store.GetAgentByName(a.Name)
		if err != nil || agent == nil {
			continue
		}
		if auth.VerifyToken(token, agent.SecretHash) {
			accountID = agent.AccountID
			break
		}
	}
	if accountID == "" {
		jsonError(w, "invalid token", http.StatusUnauthorized)
		return
	}

	agents, err := s.relay.Store.ListAgentsByAccount(accountID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Build response with online status
	type ownerAgent struct {
		store.AgentListing
		Status string `json:"status"`
	}
	out := make([]ownerAgent, len(agents))
	for i, a := range agents {
		status := "offline"
		if s.relay.Registry.Get(a.Name) != nil {
			status = "online"
		}
		out[i] = ownerAgent{AgentListing: a, Status: status}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"account_id": accountID,
		"agents":     out,
	})
}

// --- World Feed ---

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	since := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)

	newAgents, _ := s.relay.Store.FeedNewAgents(since, 10)
	newProducts, _ := s.relay.Store.FeedNewProducts(since, 10)
	creations, _ := s.relay.Store.FeedRecentCreations(since, 10)
	stats, _ := s.relay.Store.FeedOrderStats(since)
	broadcasts, _ := s.relay.Store.FeedRandomBroadcasts(5)

	// Online agents count
	onlineNames := s.relay.Registry.Online()
	stats.ActiveAgents = len(onlineNames)

	if newAgents == nil {
		newAgents = []store.FeedNewAgent{}
	}
	if newProducts == nil {
		newProducts = []store.FeedNewProduct{}
	}
	if creations == nil {
		creations = []store.FeedCreation{}
	}
	if broadcasts == nil {
		broadcasts = []store.FeedBroadcast{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"since":        since,
		"new_agents":   newAgents,
		"new_products": newProducts,
		"creations":    creations,
		"stats":        stats,
		"broadcasts":   broadcasts,
	})
}

// --- Teaching System ---

func (s *Server) handleListRecentFailures(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	logs, err := s.relay.Store.ListRecentFailures(limit)
	if err != nil {
		jsonError(w, "failed to list failures: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if logs == nil {
		logs = []store.ExecutionLog{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (s *Server) handleCreateLesson(w http.ResponseWriter, r *http.Request) {
	targetAgent := r.PathValue("name")

	// Auth: any authenticated agent can create a lesson for another agent
	token := auth.ExtractBearer(r)
	if token == "" {
		jsonError(w, "authorization required", http.StatusUnauthorized)
		return
	}
	// Find the diagnosing agent
	allAgents, err := s.relay.Store.ListAgents()
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	var diagnoserName string
	for _, a := range allAgents {
		agent, err := s.relay.Store.GetAgentByName(a.Name)
		if err != nil || agent == nil {
			continue
		}
		if auth.VerifyToken(token, agent.SecretHash) {
			diagnoserName = agent.Name
			break
		}
	}
	if diagnoserName == "" {
		jsonError(w, "invalid token", http.StatusUnauthorized)
		return
	}

	var req struct {
		Topic   string `json:"topic"`
		Content string `json:"content"`
		LogID   string `json:"log_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 50_000)).Decode(&req); err != nil {
		jsonError(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Topic == "" || req.Content == "" {
		jsonError(w, "topic and content required", http.StatusBadRequest)
		return
	}

	l := &store.Lesson{
		ID:          uuid.New().String(),
		AgentName:   targetAgent,
		Topic:       req.Topic,
		Content:     req.Content,
		DiagnosedBy: diagnoserName,
		LogID:       req.LogID,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.relay.Store.CreateLesson(l); err != nil {
		jsonError(w, "failed to create lesson: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "id": l.ID})
}

func (s *Server) handleListLessons(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	lessons, err := s.relay.Store.ListLessons(agentName, limit)
	if err != nil {
		jsonError(w, "failed to list lessons: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if lessons == nil {
		lessons = []store.Lesson{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(lessons)
}

func (s *Server) handleListAccountAgentsByID(w http.ResponseWriter, r *http.Request) {
	accountID := r.PathValue("id")
	if accountID == "" {
		jsonError(w, "account id required", http.StatusBadRequest)
		return
	}

	agents, err := s.relay.Store.ListAgentsByAccount(accountID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	type ownerAgent struct {
		store.AgentListing
		Status string `json:"status"`
	}
	out := make([]ownerAgent, len(agents))
	for i, a := range agents {
		status := "offline"
		if s.relay.Registry.Get(a.Name) != nil {
			status = "online"
		}
		out[i] = ownerAgent{AgentListing: a, Status: status}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"account_id": accountID,
		"agents":     out,
	})
}

// handleTerminalWebSocket upgrades the browser connection to WebSocket and
// proxies terminal I/O between the browser and the agent's PTY via the
// existing agent WebSocket connection.
func (s *Server) handleTerminalWebSocket(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		http.Error(w, `{"error":"missing agent name"}`, http.StatusBadRequest)
		return
	}

	// Auth: accept token from query param (WebSocket can't set headers)
	// or from Authorization header.
	token := r.URL.Query().Get("token")
	if token == "" {
		token = auth.ExtractBearer(r)
	}
	dbAgent, err := s.relay.Store.GetAgentByName(agentName)
	if err != nil || dbAgent == nil {
		http.Error(w, `{"error":"agent not found"}`, http.StatusNotFound)
		return
	}
	// Terminal requires owner (secret key) only — too dangerous for access key holders
	if token == "" {
		http.Error(w, `{"error":"authentication required — owner only"}`, http.StatusUnauthorized)
		return
	}
	if !auth.VerifyToken(token, dbAgent.SecretHash) {
		http.Error(w, `{"error":"terminal access is restricted to agent owner"}`, http.StatusForbidden)
		return
	}

	agent := s.relay.Registry.Get(agentName)
	if agent == nil {
		http.Error(w, `{"error":"agent offline"}`, http.StatusBadGateway)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[terminal] websocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Register terminal session (displace any previous)
	s.termMu.Lock()
	old := s.termSessions[agentName]
	s.termSessions[agentName] = &terminalSession{browserConn: conn}
	s.termMu.Unlock()
	if old != nil {
		old.mu.Lock()
		old.browserConn.Close()
		old.mu.Unlock()
	}

	defer func() {
		s.termMu.Lock()
		if ts := s.termSessions[agentName]; ts != nil && ts.browserConn == conn {
			delete(s.termSessions, agentName)
		}
		s.termMu.Unlock()
		agent.Send(&relay.RelayMessage{Type: relay.TypeTerminalStop})
		log.Printf("[terminal] browser disconnected from %s", agentName)
	}()

	// Read first message from browser: {cols, rows}
	_, firstMsg, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var initMsg struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	json.Unmarshal(firstMsg, &initMsg)
	if initMsg.Cols <= 0 {
		initMsg.Cols = 80
	}
	if initMsg.Rows <= 0 {
		initMsg.Rows = 24
	}

	// Tell agent to start PTY
	if err := agent.Send(&relay.RelayMessage{
		Type: relay.TypeTerminalStart,
		Cols: initMsg.Cols,
		Rows: initMsg.Rows,
	}); err != nil {
		log.Printf("[terminal] failed to send terminal_start to %s: %v", agentName, err)
		return
	}
	log.Printf("[terminal] started for %s (%dx%d)", agentName, initMsg.Cols, initMsg.Rows)

	// Relay browser messages → agent
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var msg relay.RelayMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case relay.TypeTerminalData:
			agent.Send(&msg)
		case relay.TypeTerminalResize:
			agent.Send(&msg)
		}
	}
}

// forwardToTerminalBrowser sends a terminal message from the agent to the
// connected browser WebSocket, if any.
func (s *Server) forwardToTerminalBrowser(agentName string, msg *relay.RelayMessage) {
	s.termMu.RLock()
	ts := s.termSessions[agentName]
	s.termMu.RUnlock()
	if ts == nil {
		return
	}
	ts.mu.Lock()
	ts.browserConn.WriteJSON(msg)
	ts.mu.Unlock()
}
