package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akemon/akemon-relay/internal/auth"
	"github.com/akemon/akemon-relay/internal/relay"
	"github.com/gorilla/websocket"
)

func seedLiveTaskFixture(t *testing.T, srv *Server) (string, string, string) {
	t.Helper()

	agentName := "live-agent"
	taskID := "task-123"
	secretToken := "ak_secret_live_owner"
	accessToken := "ak_access_live_owner"
	nowStr := time.Now().UTC().Format(time.RFC3339)

	secretHash, err := auth.HashToken(secretToken)
	if err != nil {
		t.Fatalf("hash secret: %v", err)
	}
	accessHash, err := auth.HashToken(accessToken)
	if err != nil {
		t.Fatalf("hash access: %v", err)
	}

	srv.relay.Store.DB().Exec(`INSERT OR IGNORE INTO accounts (id, first_seen, last_active) VALUES ('acct-live',?,?)`, nowStr, nowStr)
	srv.relay.Store.DB().Exec(`INSERT INTO agents (id, name, account_id, secret_hash, access_hash, first_registered)
		VALUES ('agent-live-id', ?, 'acct-live', ?, ?, ?)`, agentName, secretHash, accessHash, nowStr)

	agent := relay.NewConnectedAgent(agentName, "agent-live-id", "acct-live", accessHash, true, 1, nil, "conn-live")
	if _, errStr := srv.relay.Registry.Register(agent, 0); errStr != "" {
		t.Fatalf("register agent: %s", errStr)
	}

	agent.StartTaskStream(taskID, "platform", "claude --print")
	agent.AppendTaskChunk(taskID, "stdout", "hello")
	code := 0
	agent.EndTaskStream(taskID, &code, 12)

	return agentName, secretToken, taskID
}

func TestLiveTaskStreamRejectsNonOwner(t *testing.T) {
	srv, _, cleanup := buildTestServer(t)
	defer cleanup()
	_, _, taskID := seedLiveTaskFixture(t, srv)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/live/task/{task_id}/stream", srv.handleLiveTaskWebSocket)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/v1/live/task/" + taskID + "/stream"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, http.Header{
		"Authorization": []string{"Bearer wrong-token"},
	})
	if err == nil {
		t.Fatal("expected websocket dial to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", resp)
	}
}

func TestLiveTaskStreamReplaysFinishedTaskToOwner(t *testing.T) {
	srv, _, cleanup := buildTestServer(t)
	defer cleanup()
	_, secretToken, taskID := seedLiveTaskFixture(t, srv)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/live/task/{task_id}/stream", srv.handleLiveTaskWebSocket)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/v1/live/task/" + taskID + "/stream"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, http.Header{
		"Authorization": []string{"Bearer " + secretToken},
	})
	if err != nil {
		t.Fatalf("dial: %v (resp=%v)", err, resp)
	}
	defer conn.Close()

	var chunkMsg relay.RelayMessage
	if err := conn.ReadJSON(&chunkMsg); err != nil {
		t.Fatalf("read chunk: %v", err)
	}
	if chunkMsg.Type != relay.TypeTaskStream || chunkMsg.Chunk != "hello" {
		t.Fatalf("unexpected chunk message: %+v", chunkMsg)
	}

	var endMsg relay.RelayMessage
	if err := conn.ReadJSON(&endMsg); err != nil {
		t.Fatalf("read end: %v", err)
	}
	if endMsg.Type != relay.TypeTaskEnd {
		t.Fatalf("unexpected end message: %+v", endMsg)
	}
	if endMsg.ExitCode == nil || *endMsg.ExitCode != 0 {
		t.Fatalf("exit_code = %v, want 0", endMsg.ExitCode)
	}
	if endMsg.DurationMs != 12 {
		t.Fatalf("duration_ms = %d, want 12", endMsg.DurationMs)
	}
}

func TestListLiveTasksIncludesFinishedWhenRequested(t *testing.T) {
	srv, _, cleanup := buildTestServer(t)
	defer cleanup()
	agentName, secretToken, taskID := seedLiveTaskFixture(t, srv)

	r := httptest.NewRequest("GET", "/v1/agent/"+agentName+"/live-tasks?include_finished=true", nil)
	r.SetPathValue("name", agentName)
	r.Header.Set("Authorization", "Bearer "+secretToken)
	w := httptest.NewRecorder()

	srv.handleListLiveTasks(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}

	var out []relay.TaskStreamInfo
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("tasks = %d, want 1", len(out))
	}
	if out[0].TaskID != taskID || out[0].Status != "done" {
		t.Fatalf("unexpected task summary: %+v", out[0])
	}
}
