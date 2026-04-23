package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/gorilla/websocket"
)

func (s *Server) handleListLiveTasks(w http.ResponseWriter, r *http.Request) {
	agentName := r.PathValue("name")
	if agentName == "" {
		jsonError(w, "missing agent name", http.StatusBadRequest)
		return
	}
	if !s.authenticateAgentOwner(w, r, agentName) {
		return
	}

	includeFinished := r.URL.Query().Get("include_finished") == "true"
	out := []relay.TaskStreamInfo{}
	if agent := s.relay.Registry.Get(agentName); agent != nil {
		out = agent.ListTaskStreams(includeFinished)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) handleLiveTaskWebSocket(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("task_id")
	if taskID == "" {
		jsonError(w, "missing task id", http.StatusBadRequest)
		return
	}

	agent := s.findAgentByTask(taskID)
	if agent == nil {
		jsonError(w, "stream expired", http.StatusNotFound)
		return
	}
	if !s.authenticateAgentOwner(w, r, agent.Name) {
		return
	}

	snapshot := agent.GetTaskStream(taskID)
	if snapshot == nil {
		jsonError(w, "stream expired", http.StatusNotFound)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[live-task] websocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	session := &taskStreamSession{
		agentName:   agent.Name,
		taskID:      taskID,
		browserConn: conn,
	}
	old := s.setTaskSession(taskID, session)
	if old != nil {
		old.mu.Lock()
		old.browserConn.Close()
		old.mu.Unlock()
	}
	defer s.clearTaskSession(taskID, session)

	session.mu.Lock()
	for _, chunk := range snapshot.Chunks {
		if err := conn.WriteJSON(&relay.RelayMessage{
			Type:   relay.TypeTaskStream,
			TaskID: taskID,
			Stream: chunk.Stream,
			Chunk:  chunk.Chunk,
		}); err != nil {
			session.mu.Unlock()
			return
		}
	}
	if snapshot.Status != "running" {
		if err := conn.WriteJSON(&relay.RelayMessage{
			Type:       relay.TypeTaskEnd,
			TaskID:     taskID,
			ExitCode:   snapshot.ExitCode,
			DurationMs: snapshot.DurationMs,
		}); err == nil {
			_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "task finished"))
		}
		session.mu.Unlock()
		return
	}
	session.mu.Unlock()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (s *Server) findAgentByTask(taskID string) *relay.ConnectedAgent {
	for _, name := range s.relay.Registry.Online() {
		agent := s.relay.Registry.Get(name)
		if agent != nil && agent.GetTaskStream(taskID) != nil {
			return agent
		}
	}
	return nil
}

func (s *Server) setTaskSession(taskID string, session *taskStreamSession) *taskStreamSession {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	old := s.taskSessions[taskID]
	s.taskSessions[taskID] = session
	return old
}

func (s *Server) clearTaskSession(taskID string, session *taskStreamSession) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if cur := s.taskSessions[taskID]; cur == session {
		delete(s.taskSessions, taskID)
	}
}

func (s *Server) forwardToTaskBrowser(agentName string, msg *relay.RelayMessage) {
	if msg.TaskID == "" {
		return
	}
	s.taskMu.RLock()
	session := s.taskSessions[msg.TaskID]
	s.taskMu.RUnlock()
	if session == nil || session.agentName != agentName {
		return
	}

	session.mu.Lock()
	err := session.browserConn.WriteJSON(msg)
	if err == nil && msg.Type == relay.TypeTaskEnd {
		_ = session.browserConn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "task finished"))
	}
	session.mu.Unlock()

	if err != nil || msg.Type == relay.TypeTaskEnd {
		s.clearTaskSession(msg.TaskID, session)
		_ = session.browserConn.Close()
	}
}
