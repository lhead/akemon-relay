package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/akemon/akemon-relay/internal/store"
)

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

