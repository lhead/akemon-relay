package relay

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	taskStreamByteLimit  = 256 * 1024
	taskStreamChunkLimit = 1000
	taskStreamRetention  = time.Hour
)

type TaskStreamChunk struct {
	Stream string `json:"stream"`
	Chunk  string `json:"chunk"`
}

type TaskStreamInfo struct {
	TaskID     string    `json:"task_id"`
	Origin     string    `json:"origin,omitempty"`
	Cmd        string    `json:"cmd,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	DurationMs int64     `json:"duration_ms"`
	ExitCode   *int      `json:"exit_code,omitempty"`
	Status     string    `json:"status"`
}

type TaskStreamSnapshot struct {
	TaskStreamInfo
	Chunks []TaskStreamChunk `json:"chunks"`
}

type taskStream struct {
	info    TaskStreamInfo
	endedAt *time.Time
	chunks  []TaskStreamChunk
	bytes   int
}

// ConnectedAgent represents an agent with an active WebSocket connection.
type ConnectedAgent struct {
	Name             string
	AgentID          string // DB UUID
	AccountID        string
	AccessHash       string
	Public           bool
	Price            int
	Conn             *websocket.Conn
	ConnID           string // connection record ID
	LastMetrics      json.RawMessage
	MetricsUpdatedAt time.Time
	mu               sync.Mutex
	metricsMu        sync.RWMutex
	pending          map[string]chan *RelayMessage
	pendingMu        sync.Mutex
	taskMu           sync.RWMutex
	tasks            map[string]*taskStream
}

// UpdateMetrics stores the latest metrics blob sent by the agent.
func (a *ConnectedAgent) UpdateMetrics(raw json.RawMessage) {
	a.metricsMu.Lock()
	a.LastMetrics = raw
	a.MetricsUpdatedAt = time.Now()
	a.metricsMu.Unlock()
}

// GetMetrics returns a copy of the last metrics blob and the time it was received.
func (a *ConnectedAgent) GetMetrics() (json.RawMessage, time.Time) {
	a.metricsMu.RLock()
	defer a.metricsMu.RUnlock()
	return a.LastMetrics, a.MetricsUpdatedAt
}

// Send writes a message to the agent's WebSocket connection (thread-safe).
func (a *ConnectedAgent) Send(msg *RelayMessage) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Conn.WriteJSON(msg)
}

// Ping sends a WebSocket ping frame (thread-safe).
func (a *ConnectedAgent) Ping() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Conn.WriteMessage(websocket.PingMessage, nil)
}

// AddPending registers a response channel for a correlation ID.
func (a *ConnectedAgent) AddPending(requestID string) chan *RelayMessage {
	ch := make(chan *RelayMessage, 1)
	a.pendingMu.Lock()
	a.pending[requestID] = ch
	a.pendingMu.Unlock()
	return ch
}

// ResolvePending sends a response to the waiting channel and removes it.
func (a *ConnectedAgent) ResolvePending(requestID string, msg *RelayMessage) bool {
	a.pendingMu.Lock()
	ch, ok := a.pending[requestID]
	if ok {
		delete(a.pending, requestID)
	}
	a.pendingMu.Unlock()
	if ok {
		ch <- msg
	}
	return ok
}

// RemovePending removes a pending channel without sending (used on timeout/send failure).
func (a *ConnectedAgent) RemovePending(requestID string) {
	a.pendingMu.Lock()
	delete(a.pending, requestID)
	a.pendingMu.Unlock()
}

// FailAllPending sends an error to all waiting channels (called on disconnect).
func (a *ConnectedAgent) FailAllPending(errMsg string) {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	for id, ch := range a.pending {
		ch <- &RelayMessage{
			Type:      TypeMCPError,
			RequestID: id,
			Error:     errMsg,
		}
		delete(a.pending, id)
	}
}

func (a *ConnectedAgent) pruneExpiredTasksLocked(now time.Time) {
	for id, task := range a.tasks {
		if task.endedAt != nil && now.Sub(*task.endedAt) > taskStreamRetention {
			delete(a.tasks, id)
		}
	}
}

func (a *ConnectedAgent) getOrCreateTaskLocked(taskID string) *taskStream {
	task, ok := a.tasks[taskID]
	if ok {
		return task
	}
	task = &taskStream{
		info: TaskStreamInfo{
			TaskID:    taskID,
			StartedAt: time.Now().UTC(),
			Status:    "running",
		},
	}
	a.tasks[taskID] = task
	return task
}

func (a *ConnectedAgent) StartTaskStream(taskID, origin, cmd string) {
	if taskID == "" {
		return
	}
	now := time.Now().UTC()
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	a.pruneExpiredTasksLocked(now)
	a.tasks[taskID] = &taskStream{
		info: TaskStreamInfo{
			TaskID:    taskID,
			Origin:    origin,
			Cmd:       cmd,
			StartedAt: now,
			Status:    "running",
		},
	}
}

func (a *ConnectedAgent) AppendTaskChunk(taskID, stream, chunk string) {
	if taskID == "" || chunk == "" {
		return
	}
	now := time.Now().UTC()
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	a.pruneExpiredTasksLocked(now)
	task := a.getOrCreateTaskLocked(taskID)
	task.chunks = append(task.chunks, TaskStreamChunk{Stream: stream, Chunk: chunk})
	task.bytes += len(chunk)
	for len(task.chunks) > taskStreamChunkLimit || task.bytes > taskStreamByteLimit {
		task.bytes -= len(task.chunks[0].Chunk)
		task.chunks = task.chunks[1:]
	}
}

func (a *ConnectedAgent) EndTaskStream(taskID string, exitCode *int, durationMs int64) {
	if taskID == "" {
		return
	}
	now := time.Now().UTC()
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	a.pruneExpiredTasksLocked(now)
	task := a.getOrCreateTaskLocked(taskID)
	task.endedAt = &now
	task.info.Status = "done"
	task.info.ExitCode = exitCode
	task.info.DurationMs = durationMs
}

func (a *ConnectedAgent) GetTaskStream(taskID string) *TaskStreamSnapshot {
	if taskID == "" {
		return nil
	}
	now := time.Now().UTC()
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	a.pruneExpiredTasksLocked(now)
	task := a.tasks[taskID]
	if task == nil {
		return nil
	}
	info := task.info
	if task.endedAt == nil {
		info.Status = "running"
		info.DurationMs = now.Sub(info.StartedAt).Milliseconds()
	}
	chunks := make([]TaskStreamChunk, len(task.chunks))
	copy(chunks, task.chunks)
	return &TaskStreamSnapshot{
		TaskStreamInfo: info,
		Chunks:         chunks,
	}
}

func (a *ConnectedAgent) ListTaskStreams(includeFinished bool) []TaskStreamInfo {
	now := time.Now().UTC()
	a.taskMu.Lock()
	defer a.taskMu.Unlock()
	a.pruneExpiredTasksLocked(now)

	out := make([]TaskStreamInfo, 0, len(a.tasks))
	for _, task := range a.tasks {
		info := task.info
		if task.endedAt == nil {
			info.Status = "running"
			info.DurationMs = now.Sub(info.StartedAt).Milliseconds()
			out = append(out, info)
			continue
		}
		if includeFinished {
			info.Status = "done"
			out = append(out, info)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status == "running"
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out
}

// NewConnectedAgent creates a new ConnectedAgent.
func NewConnectedAgent(name, agentID, accountID, accessHash string, public bool, price int, conn *websocket.Conn, connID string) *ConnectedAgent {
	return &ConnectedAgent{
		Name:       name,
		AgentID:    agentID,
		AccountID:  accountID,
		AccessHash: accessHash,
		Public:     public,
		Price:      price,
		Conn:       conn,
		ConnID:     connID,
		pending:    make(map[string]chan *RelayMessage),
		tasks:      make(map[string]*taskStream),
	}
}
