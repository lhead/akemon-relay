package relay

import "testing"

func TestTaskStreamLifecycle(t *testing.T) {
	agent := NewConnectedAgent("alice", "agent-1", "acct-1", "access", true, 1, nil, "conn-1")
	agent.StartTaskStream("task-1", "platform", "claude --print")
	agent.AppendTaskChunk("task-1", "stdout", "hello")
	agent.AppendTaskChunk("task-1", "stderr", "warn")
	code := 0
	agent.EndTaskStream("task-1", &code, 42)

	snap := agent.GetTaskStream("task-1")
	if snap == nil {
		t.Fatal("expected task snapshot")
	}
	if snap.TaskID != "task-1" {
		t.Fatalf("task_id = %q, want task-1", snap.TaskID)
	}
	if snap.Origin != "platform" {
		t.Fatalf("origin = %q, want platform", snap.Origin)
	}
	if snap.Cmd != "claude --print" {
		t.Fatalf("cmd = %q, want claude --print", snap.Cmd)
	}
	if snap.Status != "done" {
		t.Fatalf("status = %q, want done", snap.Status)
	}
	if snap.ExitCode == nil || *snap.ExitCode != 0 {
		t.Fatalf("exit_code = %v, want 0", snap.ExitCode)
	}
	if snap.DurationMs != 42 {
		t.Fatalf("duration_ms = %d, want 42", snap.DurationMs)
	}
	if len(snap.Chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(snap.Chunks))
	}

	list := agent.ListTaskStreams(true)
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	if list[0].TaskID != "task-1" || list[0].Status != "done" {
		t.Fatalf("unexpected list item: %+v", list[0])
	}
}

func TestTaskStreamRingTrimsOldestChunks(t *testing.T) {
	agent := NewConnectedAgent("alice", "agent-1", "acct-1", "access", true, 1, nil, "conn-1")
	agent.StartTaskStream("task-2", "platform", "claude --print")
	for i := 0; i < 1005; i++ {
		agent.AppendTaskChunk("task-2", "stdout", string(rune('a'+(i%26))))
	}

	snap := agent.GetTaskStream("task-2")
	if snap == nil {
		t.Fatal("expected task snapshot")
	}
	if len(snap.Chunks) != 1000 {
		t.Fatalf("chunks = %d, want 1000", len(snap.Chunks))
	}
}
