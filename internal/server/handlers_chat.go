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

