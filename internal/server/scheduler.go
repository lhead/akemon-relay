package server

import (
	"encoding/json"
	"log"
	"math/rand"
	"strings"
	"time"

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

	// Run teaching cycle: strong agents diagnose recent failures
	go func() {
		time.Sleep(schedulerStartDelay + 5*time.Minute)
		s.runTeachingCycle()
		ticker := time.NewTicker(60 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			s.runTeachingCycle()
		}
	}()
}

// isLLMEngine returns true for engines backed by an actual LLM
func isLLMEngine(engine string) bool {
	switch engine {
	case "claude", "codex", "opencode", "gemini", "raw", "aider":
		return true
	default:
		return false
	}
}

// runProductListingCycle inserts product_review or product_create tasks for each online LLM agent
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

		taskType := "product_review"
		if len(products) == 0 {
			taskType = "product_create"
		}

		// Skip if agent already has a pending task of this type
		count, _ := s.relay.Store.CountPendingTasks(dbAgent.ID, taskType)
		if count > 0 {
			continue
		}

		s.relay.Store.CreateAgentTask(&store.AgentTask{
			ID:        uuid.New().String(),
			AgentID:   dbAgent.ID,
			Type:      taskType,
			Payload:   "{}",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
		log.Printf("[scheduler] queued %s for %s", taskType, dbAgent.Name)
	}
}

// runShoppingCycle inserts shopping tasks with random product samples
func (s *Server) runShoppingCycle() {
	allProducts, err := s.relay.Store.ListAllProducts()
	if err != nil || len(allProducts) == 0 {
		log.Printf("[scheduler] Shopping cycle: no products available")
		return
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

		// Skip if agent already has a pending shopping task
		count, _ := s.relay.Store.CountPendingTasks(dbAgent.ID, "shopping")
		if count > 0 {
			continue
		}

		// Random sample of 5-8 products (excluding agent's own)
		var candidates []store.ProductListing
		for _, p := range allProducts {
			if p.AgentID != dbAgent.ID {
				candidates = append(candidates, p)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
		sampleSize := 8
		if len(candidates) < sampleSize {
			sampleSize = len(candidates)
		}
		sample := candidates[:sampleSize]

		// Embed compact product summaries so the agent doesn't have to curl
		// each product before deciding. Keeps engine context small — long
		// descriptions were previously causing opencode to hang.
		summaries := make([]map[string]interface{}, 0, len(sample))
		for _, p := range sample {
			desc := p.Description
			if len(desc) > 160 {
				desc = desc[:160] + "…"
			}
			summaries = append(summaries, map[string]interface{}{
				"id":             p.ID,
				"name":           p.Name,
				"price":          p.Price,
				"seller":         p.AgentName,
				"description":    desc,
				"avg_rating":     p.AvgRating,
				"purchase_count": p.PurchaseCount,
			})
		}
		payload, _ := json.Marshal(map[string]interface{}{"products": summaries})

		s.relay.Store.CreateAgentTask(&store.AgentTask{
			ID:        uuid.New().String(),
			AgentID:   dbAgent.ID,
			Type:      "shopping",
			Payload:   string(payload),
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
		log.Printf("[scheduler] queued shopping for %s (%d products sampled)", dbAgent.Name, sampleSize)
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
		DetailHTML     string `json:"detail_html"`
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
			DetailHTML:     p.DetailHTML,
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
			DetailHTML     string `json:"detail_html"`
			Price          int    `json:"price"`
		} `json:"update"`
		Create []struct {
			Name           string `json:"name"`
			Description    string `json:"description"`
			DetailMarkdown string `json:"detail_markdown"`
			DetailHTML     string `json:"detail_html"`
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
		detailHTML := u.DetailHTML
		if detailHTML == "" {
			detailHTML = existing.DetailHTML
		}
		price := u.Price
		if price <= 0 {
			price = existing.Price
		}
		s.relay.Store.UpdateProduct(u.ID, name, desc, detail, detailHTML, price)
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
			DetailHTML:     c.DetailHTML,
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

// runTeachingCycle finds recent failures and assigns strong agents to diagnose them
func (s *Server) runTeachingCycle() {
	failures, err := s.relay.Store.ListRecentFailures(10)
	if err != nil || len(failures) == 0 {
		return
	}

	// Find online strong agents (LLM engines, not raw)
	onlineAgents := s.relay.Registry.Online()
	var teachers []string
	for _, name := range onlineAgents {
		agent := s.relay.Registry.Get(name)
		if agent == nil || !agent.Public {
			continue
		}
		dbAgent, err := s.relay.Store.GetAgentByName(name)
		if err != nil || dbAgent == nil {
			continue
		}
		if isLLMEngine(dbAgent.Engine) {
			teachers = append(teachers, name)
		}
	}
	if len(teachers) == 0 {
		return
	}

	// Group failures by agent, only pick agents with undiagnosed failures
	failuresByAgent := make(map[string][]store.ExecutionLog)
	for _, f := range failures {
		failuresByAgent[f.AgentName] = append(failuresByAgent[f.AgentName], f)
	}

	// Pick one teacher (round-robin by random)
	teacher := teachers[rand.Intn(len(teachers))]
	teacherDB, err := s.relay.Store.GetAgentByName(teacher)
	if err != nil || teacherDB == nil {
		return
	}

	// Skip if teacher already has a pending diagnose task
	count, _ := s.relay.Store.CountPendingTasks(teacherDB.ID, "diagnose_failures")
	if count > 0 {
		return
	}

	// Build payload: recent failures (excluding teacher's own)
	var diagnosable []store.ExecutionLog
	for agentName, logs := range failuresByAgent {
		if agentName == teacher {
			continue // don't diagnose yourself
		}
		diagnosable = append(diagnosable, logs...)
	}
	if len(diagnosable) == 0 {
		return
	}

	// Limit to 5 failures per task
	if len(diagnosable) > 5 {
		diagnosable = diagnosable[:5]
	}

	payload, _ := json.Marshal(map[string]interface{}{"failures": diagnosable})

	s.relay.Store.CreateAgentTask(&store.AgentTask{
		ID:        uuid.New().String(),
		AgentID:   teacherDB.ID,
		Type:      "diagnose_failures",
		Payload:   string(payload),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	log.Printf("[scheduler] queued diagnose_failures for %s (%d failures to review)", teacher, len(diagnosable))
}

func (s *Server) applyDiagnoseLessons(response string, teacher *store.Agent) {
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		return
	}
	var result struct {
		Lessons []struct {
			AgentName string `json:"agent_name"`
			Topic     string `json:"topic"`
			Content   string `json:"content"`
		} `json:"lessons"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		log.Printf("[teaching] %s: failed to parse lessons JSON: %v", teacher.Name, err)
		return
	}
	for _, l := range result.Lessons {
		if l.AgentName == "" || l.Content == "" {
			continue
		}
		topic := l.Topic
		if topic == "" {
			topic = "general"
		}
		lesson := &store.Lesson{
			ID:          uuid.New().String(),
			AgentName:   l.AgentName,
			Topic:       topic,
			Content:     l.Content,
			DiagnosedBy: teacher.Name,
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		}
		if err := s.relay.Store.CreateLesson(lesson); err != nil {
			log.Printf("[teaching] failed to create lesson for %s: %v", l.AgentName, err)
			continue
		}
		log.Printf("[teaching] %s taught %s: %s", teacher.Name, l.AgentName, topic)
	}
}

// runOrderTimeoutTicker checks for expired orders and stale tasks every 60s
func (s *Server) runOrderTimeoutTicker() {
	for {
		time.Sleep(60 * time.Second)

		// Expire stale orders
		expired, err := s.relay.Store.FindExpiredOrders()
		if err != nil {
			log.Printf("[order-ticker] error finding expired: %v", err)
		} else {
			for _, order := range expired {
				affected, err := s.relay.Store.FailOrder(order.ID)
				if err != nil {
					log.Printf("[order-ticker] %s fail error: %v", order.ID, err)
					continue
				}
				if affected == 0 {
					log.Printf("[order-ticker] %s already transitioned, skipping refund", order.ID)
					continue
				}
				if order.BuyerAgentID != "" && order.EscrowAmount > 0 {
					if err := s.relay.Store.MintCredit(order.BuyerAgentID, order.EscrowAmount); err != nil {
						log.Printf("[order-ticker] %s refund error: %v", order.ID, err)
					}
				}
				log.Printf("[order-ticker] %s failed (timeout expired)", order.ID)
			}
		}

		// Expire stale agent tasks (pending > 1h)
		if expiredTasks, err := s.relay.Store.ExpireOldTasks(); err != nil {
			log.Printf("[task-ticker] expire error: %v", err)
		} else if expiredTasks > 0 {
			log.Printf("[task-ticker] expired %d stale tasks", expiredTasks)
		}
	}
}
