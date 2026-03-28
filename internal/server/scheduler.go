package server

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/akemon/akemon-relay/internal/store"
	"github.com/google/uuid"
)

// StartScheduler runs periodic tasks:
// - Every 12h: ask agents to suggest products, then create them
// - Every 12h (offset 6h): notify agents to browse and buy products
func (s *Server) StartScheduler() {
	go s.runSchedulerLoop()
	go s.runOrderTimeoutTicker()
}

const (
	schedulerStartDelay    = 2 * time.Minute
	schedulerListingCycle  = 30 * time.Minute
	schedulerShoppingCycle = 30 * time.Minute
	schedulerShoppingOffset = 15 * time.Minute
)

func (s *Server) runSchedulerLoop() {
	// Initial delay: wait for agents to connect
	time.Sleep(schedulerStartDelay)

	// Run product listing cycle immediately on startup, then periodically
	go func() {
		s.runProductListingCycle()
		ticker := time.NewTicker(schedulerListingCycle)
		defer ticker.Stop()
		for range ticker.C {
			s.runProductListingCycle()
		}
	}()

	// Run shopping cycle offset, then periodically
	go func() {
		time.Sleep(schedulerShoppingOffset)
		s.runShoppingCycle()
		ticker := time.NewTicker(schedulerShoppingCycle)
		defer ticker.Stop()
		for range ticker.C {
			s.runShoppingCycle()
		}
	}()
}

// isLLMEngine returns true for engines backed by an actual LLM
func isLLMEngine(engine string) bool {
	switch engine {
	case "claude", "codex", "opencode", "gemini":
		return true
	default:
		return false
	}
}

func (s *Server) runProductListingCycle() {
	onlineAgents := s.relay.Registry.Online()
	log.Printf("[scheduler] Product listing cycle: %d online agents", len(onlineAgents))

	for _, agentName := range onlineAgents {
		agent := s.relay.Registry.Get(agentName)
		if agent == nil || !agent.Public {
			continue
		}

		dbAgent, err := s.relay.Store.GetAgentByName(agentName)
		if err != nil || dbAgent == nil {
			continue
		}

		if !isLLMEngine(dbAgent.Engine) {
			continue
		}

		products, _ := s.relay.Store.ListProductsByAgent(dbAgent.ID)

		if len(products) == 0 {
			go s.askAgentToCreateProducts(agent, dbAgent)
		} else {
			go s.askAgentToReviewProducts(agent, dbAgent, products)
		}
	}
}

// askAgentToCreateProducts asks the agent for product ideas in JSON, then creates them via store
func (s *Server) askAgentToCreateProducts(agent *relay.ConnectedAgent, dbAgent *store.Agent) {
	// Gather market context
	allProducts, _ := s.relay.Store.ListAllProducts()
	marketInfo := ""
	if len(allProducts) > 0 {
		marketInfo = "\n\nCurrent marketplace products (your competitors):"
		for _, p := range allProducts {
			if p.AgentID == dbAgent.ID {
				continue
			}
			marketInfo += fmt.Sprintf("\n- \"%s\" by %s — %d credits, %d purchases", p.Name, p.AgentName, p.Price, p.PurchaseCount)
		}
	}

	task := fmt.Sprintf(`You are an AI agent named "%s" (engine: %s, tags: %s).
You're selling products/services on an open agent marketplace. Design 1-3 unique products.

IMPORTANT: Don't limit yourself to coding/technical products! Be creative and fun. Examples of great product ideas:
- 🔮 AI算命/占卜 Fortune Telling — fun personality readings
- ⭐ 星座分析 Horoscope Analysis — daily/weekly horoscope
- 👻 鬼故事频道 Ghost Stories — creepy original stories
- 💡 人生建议 Life Advice — wisdom and guidance
- 📖 起名大师 Name Generator — creative naming for projects/pets/babies
- 🎭 角色扮演 Roleplay — act as historical figures, fictional characters
- 📝 文案创作 Copywriting — marketing copy, social media posts
- 🎵 歌词创作 Songwriting — original lyrics in any style
Think outside the box! The marketplace needs variety, not just code tools.%s

Reply with ONLY a JSON array. Each product needs:
- "name": bilingual name. Format: "中文名 English Name" (e.g. "星座运势 Horoscope Reading")
- "description": bilingual summary. Format: one line Chinese, then " | ", then one line English
  Example: "每日星座运势分析，了解你的运气和建议 | Daily horoscope analysis with luck forecast and advice"
- "detail_markdown": a rich markdown product page. MUST be bilingual — write Chinese section first (## 中文标题), then English section (## English Title). 2-4 paragraphs each. Be creative! Use:
  - Markdown: headers, lists, bold, emoji, code blocks where relevant
  - MUST include 1-2 images using real Unsplash photos: ![description](https://images.unsplash.com/photo-XXXXX?w=600). Use photo IDs you know are real — pick images that match the product theme.
  - Make each product page visually distinct and appealing
- "price": integer 1-20 credits

Reply ONLY with the JSON array, nothing else.`, dbAgent.Name, dbAgent.Engine, dbAgent.Tags, marketInfo)

	result := s.sendTaskAndGetResult(agent, dbAgent, task)
	if result == "" {
		return
	}

	// Parse JSON products from response
	created := s.parseAndCreateProducts(result, dbAgent)
	log.Printf("[scheduler] %s: created %d products", dbAgent.Name, created)

	// Reward agent
	s.relay.Store.MintCredit(dbAgent.ID, 3)
	s.relay.Store.IncrementAgentTasks(dbAgent.ID)
}

// askAgentToReviewProducts asks agent to review existing products and suggest changes
func (s *Server) askAgentToReviewProducts(agent *relay.ConnectedAgent, dbAgent *store.Agent, products []store.Product) {
	productList := ""
	for _, p := range products {
		hasDetail := "no"
		if p.DetailMarkdown != "" {
			hasDetail = "yes"
		}
		productList += fmt.Sprintf("\n- id=%s name=\"%s\" price=%d purchases=%d has_detail_page=%s desc=\"%s\"", p.ID, p.Name, p.Price, p.PurchaseCount, hasDetail, p.Description)
	}

	// Gather competitor info
	allProducts, _ := s.relay.Store.ListAllProducts()
	competitorInfo := ""
	for _, p := range allProducts {
		if p.AgentID == dbAgent.ID {
			continue
		}
		competitorInfo += fmt.Sprintf("\n- \"%s\" by %s — %d credits, %d purchases", p.Name, p.AgentName, p.Price, p.PurchaseCount)
	}
	if competitorInfo != "" {
		competitorInfo = "\n\nCompetitor products:" + competitorInfo
	}

	task := fmt.Sprintf(`You are an AI agent named "%s". Here are your current products:%s%s

Review your products and optimize. Consider:
- Products with 0 purchases: try changing the name/price/description to be more appealing
- Products without detail pages (has_detail_page=no): add a rich detail_markdown page
- DON'T just sell coding tools! The market needs variety: fortune telling, horoscope, ghost stories, name generation, life advice, creative writing, roleplay, etc.
- If all your products are technical/code-related, DELETE some and CREATE fun/creative ones instead

Reply with ONLY a JSON object:
{
  "delete": ["id_to_delete"],
  "update": [{"id": "id1", "name": "中文名 English Name", "description": "中文描述 | English description", "detail_markdown": "## 🔮 中文标题\n\n中文内容...\n\n## 🔮 English Title\n\nEnglish content...", "price": 5}],
  "create": [{"name": "中文名 English Name", "description": "中文描述 | English description", "detail_markdown": "## ...", "price": 3}]
}

BILINGUAL FORMAT (strict):
- name: "中文名 English Name"
- description: "中文描述 | English description" (Chinese, then " | ", then English)
- detail_markdown: Write Chinese section first (## 中文标题 + Chinese paragraphs), then English section (## English Title + English paragraphs). Both sections must be complete, not mixed.

If everything truly looks good: {"keep": "all"}
Reply ONLY with JSON.`, dbAgent.Name, productList, competitorInfo)

	result := s.sendTaskAndGetResult(agent, dbAgent, task)
	if result == "" {
		return
	}

	// Parse review response
	s.applyProductReview(result, dbAgent)

	// Reward
	s.relay.Store.MintCredit(dbAgent.ID, 3)
	s.relay.Store.IncrementAgentTasks(dbAgent.ID)
}

func (s *Server) runShoppingCycle() {
	allProducts, err := s.relay.Store.ListAllProducts()
	if err != nil || len(allProducts) == 0 {
		log.Printf("[scheduler] Shopping cycle: no products available")
		return
	}

	productList := ""
	for _, p := range allProducts {
		productList += fmt.Sprintf("\n- id=%s \"%s\" by %s price=%d purchases=%d — %s",
			p.ID, p.Name, p.AgentName, p.Price, p.PurchaseCount, p.Description)
	}

	onlineAgents := s.relay.Registry.Online()
	log.Printf("[scheduler] Shopping cycle: %d online agents, %d products", len(onlineAgents), len(allProducts))

	for _, agentName := range onlineAgents {
		agent := s.relay.Registry.Get(agentName)
		if agent == nil || !agent.Public {
			continue
		}

		dbAgent, err := s.relay.Store.GetAgentByName(agentName)
		if err != nil || dbAgent == nil {
			continue
		}

		if !isLLMEngine(dbAgent.Engine) {
			continue
		}

		go s.askAgentToShop(agent, dbAgent, productList)
	}
}

func (s *Server) askAgentToShop(agent *relay.ConnectedAgent, dbAgent *store.Agent, productList string) {
	task := fmt.Sprintf(`You are "%s". Your credits: %d.

Available products on the marketplace:%s

Browse the marketplace. Would any product help you do your job better or learn something new?
Only buy products you can actually use — don't buy your own products.
Be specific in your task description so the seller can deliver quality work.

Reply with ONLY a JSON object:
- To buy: {"buy": [{"id": "product_id", "task": "specific task description"}]}
- To skip: {"buy": []}
Reply ONLY with JSON.`, dbAgent.Name, dbAgent.Credits, productList)

	result := s.sendTaskAndGetResult(agent, dbAgent, task)
	if result == "" {
		return
	}

	// Parse shopping decisions
	s.applyShoppingDecisions(result, dbAgent)

	// Reward
	s.relay.Store.MintCredit(dbAgent.ID, 3)
	s.relay.Store.IncrementAgentTasks(dbAgent.ID)
}

// --- Helpers ---

// sendTaskAndGetResult sends a task to an agent and returns the text result
func (s *Server) sendTaskAndGetResult(agent *relay.ConnectedAgent, dbAgent *store.Agent, task string) string {
	initBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1,
		"method": "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "akemon-scheduler", "version": "1.0"},
		},
	})

	initReqID := uuid.New().String()
	initCh := agent.AddPending(initReqID)
	if err := agent.Send(&relay.RelayMessage{
		Type:      relay.TypeMCPRequest,
		RequestID: initReqID,
		Method:    "POST",
		Headers:   map[string]string{"content-type": "application/json", "x-publisher-id": "scheduler"},
		Body:      initBody,
	}); err != nil {
		agent.RemovePending(initReqID)
		log.Printf("[scheduler] failed to init %s: %v", dbAgent.Name, err)
		return ""
	}

	var sessionID string
	select {
	case initResp := <-initCh:
		if initResp.ResponseHeaders != nil {
			sessionID = initResp.ResponseHeaders["mcp-session-id"]
		}
	case <-time.After(15 * time.Second):
		agent.RemovePending(initReqID)
		log.Printf("[scheduler] init timeout for %s", dbAgent.Name)
		return ""
	}

	callBody, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 2,
		"method": "tools/call",
		"params": map[string]interface{}{
			"name":      "submit_task",
			"arguments": map[string]string{"task": task},
		},
	})

	callHeaders := map[string]string{"content-type": "application/json", "x-publisher-id": "scheduler"}
	if sessionID != "" {
		callHeaders["mcp-session-id"] = sessionID
	}

	callReqID := uuid.New().String()
	callCh := agent.AddPending(callReqID)
	if err := agent.Send(&relay.RelayMessage{
		Type:      relay.TypeMCPRequest,
		RequestID: callReqID,
		SessionID: sessionID,
		Method:    "POST",
		Headers:   callHeaders,
		Body:      callBody,
	}); err != nil {
		agent.RemovePending(callReqID)
		log.Printf("[scheduler] failed to send task to %s: %v", dbAgent.Name, err)
		return ""
	}

	select {
	case resp := <-callCh:
		result := extractTextResult(resp.Body)
		log.Printf("[scheduler] %s responded (%d bytes)", dbAgent.Name, len(result))
		return result
	case <-time.After(5 * time.Minute):
		agent.RemovePending(callReqID)
		log.Printf("[scheduler] %s timed out", dbAgent.Name)
		return ""
	}
}

// extractJSON tries to find a JSON array or object in text that may contain markdown fences or extra text
func extractJSON(s string) string {
	// Strip markdown code fences
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) >= 3 {
			s = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	s = strings.TrimSpace(s)

	// Try each [ or { as potential JSON start
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '[' && c != '{' {
			continue
		}

		closeBracket := byte(']')
		if c == '{' {
			closeBracket = byte('}')
		}

		// Try to find matching close and validate JSON
		depth := 0
		for j := i; j < len(s); j++ {
			if s[j] == c {
				depth++
			} else if s[j] == closeBracket {
				depth--
				if depth == 0 {
					candidate := s[i : j+1]
					// Validate it's actually JSON
					if json.Valid([]byte(candidate)) {
						return candidate
					}
					break // this bracket pair wasn't valid JSON, try next
				}
			}
		}
	}
	return ""
}

func (s *Server) parseAndCreateProducts(response string, dbAgent *store.Agent) int {
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		log.Printf("[scheduler] %s: no JSON found in response", dbAgent.Name)
		return 0
	}

	type productSuggestion struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		DetailMarkdown string `json:"detail_markdown"`
		Price          int    `json:"price"`
	}

	var products []productSuggestion

	// Try array first
	if err := json.Unmarshal([]byte(jsonStr), &products); err != nil {
		// Try single object
		var single productSuggestion
		if err2 := json.Unmarshal([]byte(jsonStr), &single); err2 != nil {
			log.Printf("[scheduler] %s: failed to parse products JSON: %v", dbAgent.Name, err)
			return 0
		}
		products = []productSuggestion{single}
	}

	created := 0
	for _, p := range products {
		if p.Name == "" {
			continue
		}
		if p.Price <= 0 {
			p.Price = 1
		}
		if p.Price > 20 {
			p.Price = 20
		}
		product := &store.Product{
			ID:             uuid.New().String(),
			AgentID:        dbAgent.ID,
			Name:           p.Name,
			Description:    p.Description,
			DetailMarkdown: p.DetailMarkdown,
			Price:          p.Price,
		}
		if err := s.relay.Store.CreateProduct(product); err != nil {
			log.Printf("[scheduler] %s: failed to create product %s: %v", dbAgent.Name, p.Name, err)
			continue
		}
		created++
		log.Printf("[scheduler] %s: listed \"%s\" (price=%d)", dbAgent.Name, p.Name, p.Price)
	}
	return created
}

func (s *Server) applyProductReview(response string, dbAgent *store.Agent) {
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		return
	}

	var review struct {
		Keep   interface{} `json:"keep"` // can be "all" or []string
		Delete []string    `json:"delete"`
		Update []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			Description    string `json:"description"`
			DetailMarkdown string `json:"detail_markdown"`
			Price          int    `json:"price"`
		} `json:"update"`
		Create []struct {
			Name           string `json:"name"`
			Description    string `json:"description"`
			DetailMarkdown string `json:"detail_markdown"`
			Price          int    `json:"price"`
		} `json:"create"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &review); err != nil {
		log.Printf("[scheduler] %s: failed to parse review JSON: %v", dbAgent.Name, err)
		return
	}

	// Delete
	for _, id := range review.Delete {
		s.relay.Store.DeleteProduct(id)
		log.Printf("[scheduler] %s: deleted product %s", dbAgent.Name, id)
	}

	// Update
	for _, u := range review.Update {
		if u.ID == "" {
			continue
		}
		existing, _ := s.relay.Store.GetProduct(u.ID)
		if existing == nil {
			continue
		}
		name := u.Name
		if name == "" {
			name = existing.Name
		}
		desc := u.Description
		if desc == "" {
			desc = existing.Description
		}
		detail := u.DetailMarkdown
		if detail == "" {
			detail = existing.DetailMarkdown
		}
		price := u.Price
		if price <= 0 {
			price = existing.Price
		}
		s.relay.Store.UpdateProduct(u.ID, name, desc, detail, price)
		log.Printf("[scheduler] %s: updated product %s", dbAgent.Name, u.ID)
	}

	// Create new
	for _, c := range review.Create {
		if c.Name == "" {
			continue
		}
		if c.Price <= 0 {
			c.Price = 1
		}
		product := &store.Product{
			ID:             uuid.New().String(),
			AgentID:        dbAgent.ID,
			Name:           c.Name,
			Description:    c.Description,
			DetailMarkdown: c.DetailMarkdown,
			Price:          c.Price,
		}
		s.relay.Store.CreateProduct(product)
		log.Printf("[scheduler] %s: listed new \"%s\" (price=%d)", dbAgent.Name, c.Name, c.Price)
	}
}

func (s *Server) applyShoppingDecisions(response string, dbAgent *store.Agent) {
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		return
	}

	var decisions struct {
		Buy []struct {
			ID   string `json:"id"`
			Task string `json:"task"`
		} `json:"buy"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &decisions); err != nil {
		log.Printf("[scheduler] %s: failed to parse shopping JSON: %v", dbAgent.Name, err)
		return
	}

	for _, buy := range decisions.Buy {
		if buy.ID == "" {
			continue
		}
		product, _ := s.relay.Store.GetProduct(buy.ID)
		if product == nil || product.Status != "active" {
			continue
		}
		if product.AgentID == dbAgent.ID {
			continue
		}

		// Check if buyer can afford
		if dbAgent.Credits < product.Price {
			log.Printf("[scheduler] %s: can't afford %d for %s", dbAgent.Name, product.Price, product.Name)
			continue
		}

		sellerAgent, _ := s.relay.Store.GetAgentByID(product.AgentID)
		if sellerAgent == nil {
			continue
		}

		// Create async order (no immediate call, agent picks it up)
		orderID := uuid.New().String()
		order := &store.Order{
			ID:              orderID,
			ProductID:       buy.ID,
			SellerAgentID:   product.AgentID,
			SellerAgentName: sellerAgent.Name,
			BuyerAgentID:    dbAgent.ID,
			BuyerTask:       buy.Task,
			TotalPrice:      product.Price,
		}
		if err := s.relay.Store.CreateOrder(order); err != nil {
			log.Printf("[scheduler] %s: failed to create order: %v", dbAgent.Name, err)
			continue
		}

		log.Printf("[scheduler] %s placed async order %s for \"%s\" from %s (price=%d)", dbAgent.Name, orderID, product.Name, sellerAgent.Name, product.Price)
	}
}

// runOrderTimeoutTicker checks for expired orders every 60s
func (s *Server) runOrderTimeoutTicker() {
	for {
		time.Sleep(60 * time.Second)
		expired, err := s.relay.Store.FindExpiredOrders()
		if err != nil {
			log.Printf("[order-ticker] error finding expired: %v", err)
			continue
		}
		for _, order := range expired {
			if order.RetryCount < order.MaxRetries {
				continue // agent side handles retries
			}
			s.relay.Store.FailOrder(order.ID)
			if order.BuyerAgentID != "" && order.EscrowAmount > 0 {
				s.relay.Store.MintCredit(order.BuyerAgentID, order.EscrowAmount)
			}
			log.Printf("[order-ticker] %s failed (timeout, %d retries exhausted)", order.ID, order.RetryCount)
		}
	}
}
