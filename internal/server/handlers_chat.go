package server

import (
	"net/http"
)

func (s *Server) handleChatConversations(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		http.Error(w, `{"error":"missing agent name"}`, http.StatusBadRequest)
		return
	}
	// Only agent owner can view all conversations
	if !s.authenticateAgentOwner(w, r, agentName) {
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
	// Phase A: use ResolveCaller to get the authoritative publisher_id for owner requests,
	// so conv reads match what handleCreateAdHocOrder / handleBuyProduct write.
	// Anonymous visitors still fall back to the short-hash fingerprint.
	caller := s.ResolveCaller(r)
	var pubId string
	if caller.Kind == "owner" {
		pubId = caller.PublisherID
	} else {
		pubId = derivePublisherID(r)
	}
	convId := "pub_" + pubId
	// Product-scoped conversations: append :prod_{id} suffix
	if productID := r.URL.Query().Get("product_id"); productID != "" {
		convId += ":prod_" + productID
	}
	// Expose convId so frontend can match it against list entries
	w.Header().Set("X-Conv-Id", convId)
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

