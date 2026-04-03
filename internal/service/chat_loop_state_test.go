package service

import (
	"testing"
	"time"

	"github.com/hollis-labs/conduit/internal/chat"
)

func TestLoopState_ResolvedMaxTurns(t *testing.T) {
	tests := []struct {
		name        string
		constraints chat.AgentConstraints
		want        int
	}{
		{
			name:        "zero uses default 25",
			constraints: chat.AgentConstraints{},
			want:        25,
		},
		{
			name:        "explicit max turns",
			constraints: chat.AgentConstraints{MaxTurns: 50},
			want:        50,
		},
		{
			name:        "max turns clamped to hard ceiling",
			constraints: chat.AgentConstraints{MaxTurns: 200},
			want:        100, // default hard ceiling
		},
		{
			name:        "unlimited uses hard ceiling",
			constraints: chat.AgentConstraints{MaxTurns: -1},
			want:        100,
		},
		{
			name:        "unlimited with custom hard ceiling",
			constraints: chat.AgentConstraints{MaxTurns: -1, HardCeiling: 500},
			want:        500,
		},
		{
			name:        "legacy MaxIterations respected when lower",
			constraints: chat.AgentConstraints{MaxIterations: 8},
			want:        8,
		},
		{
			name:        "legacy MaxIterations ignored when higher than MaxTurns",
			constraints: chat.AgentConstraints{MaxTurns: 10, MaxIterations: 30},
			want:        10,
		},
		{
			name:        "custom hard ceiling",
			constraints: chat.AgentConstraints{HardCeiling: 50},
			want:        25, // default maxTurns < custom ceiling
		},
		{
			name:        "hard ceiling lower than max turns",
			constraints: chat.AgentConstraints{MaxTurns: 80, HardCeiling: 60},
			want:        60,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := newLoopState(tt.constraints, nil, false)
			got := ls.resolvedMaxTurns()
			if got != tt.want {
				t.Errorf("resolvedMaxTurns() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLoopState_ShouldStop_ConsecutiveFailures(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	// 3 failures should trigger stop.
	for i := 0; i < 3; i++ {
		stop, _ := ls.shouldStop()
		if stop {
			t.Fatalf("shouldStop() returned true after only %d failures", i)
		}
		ls.recordToolCall("test-tool", false) // failure
	}

	stop, reason := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should return true after 3 consecutive failures")
	}
	if reason == "" {
		t.Error("shouldStop() should return a reason")
	}

	// Reset on success.
	ls.consecutiveFailures = 0
	ls.recordToolCall("test-tool", true)
	stop, _ = ls.shouldStop()
	if stop {
		t.Error("shouldStop() should return false after success reset")
	}
}

func TestLoopState_ShouldStop_MaxTurns(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{MaxTurns: 5}, nil, false)
	ls.iteration = 5
	stop, reason := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should return true at max turns")
	}
	if reason == "" {
		t.Error("expected a reason")
	}
}

func TestLoopState_ShouldStop_HardCeiling(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{MaxTurns: -1, HardCeiling: 10}, nil, false)
	ls.iteration = 10
	stop, _ := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should return true at hard ceiling")
	}
}

func TestLoopState_ShouldStop_RetryBudget(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{RetryBudget: 1}, nil, false)
	ls.recordToolCall("tool", false) // uses the budget
	stop, reason := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should return true when retry budget exhausted")
	}
	if reason == "" {
		t.Error("expected a reason")
	}
}

func TestLoopState_ShouldStop_IdleTimeout(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{IdleTimeoutSeconds: 1}, nil, false)
	ls.lastActivity = time.Now().Add(-2 * time.Second)
	stop, reason := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should return true after idle timeout")
	}
	if reason == "" {
		t.Error("expected a reason")
	}
}

func TestLoopState_RecordToolCall_PerToolMax(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.limits.perToolMax["limited-tool"] = 2

	exhausted := ls.recordToolCall("limited-tool", true)
	if exhausted {
		t.Error("should not be exhausted after 1 call")
	}

	exhausted = ls.recordToolCall("limited-tool", true)
	if !exhausted {
		t.Error("should be exhausted after 2 calls (max=2)")
	}

	if !ls.blockedTools["limited-tool"] {
		t.Error("tool should be blocked after exhaustion")
	}
}

func TestLoopState_RecordPermissionDenial(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	ls.recordPermissionDenial()
	if ls.consecutiveFailures != 1 {
		t.Errorf("consecutiveFailures = %d, want 1", ls.consecutiveFailures)
	}

	ls.recordPermissionDenial()
	ls.recordPermissionDenial()
	stop, _ := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should trigger after 3 permission denials")
	}
}

func TestLoopState_IsToolExhausted(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.limits.perToolMax["tool-a"] = 3

	if ls.isToolExhausted("tool-a") {
		t.Error("should not be exhausted before any calls")
	}

	ls.toolCallCounts["tool-a"] = 3
	if !ls.isToolExhausted("tool-a") {
		t.Error("should be exhausted at max")
	}

	// Tool without a limit.
	if ls.isToolExhausted("tool-b") {
		t.Error("tool without limit should never be exhausted")
	}
}

func TestLoopState_ContinueWith(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.iteration = 3

	ls.continueWith(ContinueToolResults, "2 tools executed")

	if ls.lastSite != ContinueToolResults {
		t.Errorf("lastSite = %q, want %q", ls.lastSite, ContinueToolResults)
	}
	if ls.lastReason != "2 tools executed" {
		t.Errorf("lastReason = %q, want %q", ls.lastReason, "2 tools executed")
	}
}

func TestLoopState_CaptureSnapshot_DebugMode(t *testing.T) {
	// Debug mode off — no snapshots.
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	ls.captureSnapshot(ContinueToolResults, "test", 1000, 5)
	if len(ls.snapshots) != 0 {
		t.Errorf("expected 0 snapshots in non-debug mode, got %d", len(ls.snapshots))
	}

	// Debug mode on.
	ls = newLoopState(chat.AgentConstraints{}, nil, true)
	ls.iteration = 2
	ls.captureSnapshot(ContinueToolResults, "test reason", 1500, 8)
	if len(ls.snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(ls.snapshots))
	}

	snap := ls.snapshots[0]
	if snap.ContinueSite != ContinueToolResults {
		t.Errorf("snapshot site = %q, want %q", snap.ContinueSite, ContinueToolResults)
	}
	if snap.Reason != "test reason" {
		t.Errorf("snapshot reason = %q, want %q", snap.Reason, "test reason")
	}
	if snap.Iteration != 2 {
		t.Errorf("snapshot iteration = %d, want 2", snap.Iteration)
	}
	if snap.TokensUsed != 1500 {
		t.Errorf("snapshot tokens = %d, want 1500", snap.TokensUsed)
	}
	if snap.MessageCount != 8 {
		t.Errorf("snapshot messages = %d, want 8", snap.MessageCount)
	}
}

func TestLoopState_CaptureSnapshotWithTools(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, true)
	tools := []ToolCallSnapshot{
		{Name: "dev_read", Duration: 50 * time.Millisecond, Success: true},
		{Name: "dev_write", Duration: 120 * time.Millisecond, Success: false},
	}
	ls.captureSnapshotWithTools(ContinueToolResults, "2 tools", 2000, 10, tools)
	if len(ls.snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(ls.snapshots))
	}
	if len(ls.snapshots[0].ToolCalls) != 2 {
		t.Errorf("expected 2 tool call snapshots, got %d", len(ls.snapshots[0].ToolCalls))
	}
}

func TestNewLoopState_Defaults(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, []string{"tool-a", "tool-b"}, false)

	if ls.retryBudget != -1 {
		t.Errorf("retryBudget = %d, want -1", ls.retryBudget)
	}
	if !ls.loadedTools["tool-a"] || !ls.loadedTools["tool-b"] {
		t.Error("loadedTools should contain initial tools")
	}
	if ls.debugMode {
		t.Error("debugMode should be false")
	}
	if ls.limits.maxTurns != 25 {
		t.Errorf("maxTurns = %d, want 25", ls.limits.maxTurns)
	}
	if ls.limits.hardCeiling != 100 {
		t.Errorf("hardCeiling = %d, want 100", ls.limits.hardCeiling)
	}
	if ls.limits.consecutiveFailCap != 3 {
		t.Errorf("consecutiveFailCap = %d, want 3", ls.limits.consecutiveFailCap)
	}
}

func TestToolMetaInfo_ConcurrencySafe(t *testing.T) {
	svc := &toolServiceImpl{}

	tests := []struct {
		name       string
		tool       string
		wantSafe   bool
	}{
		{"read tool", "mcp__dev__dev_read", true},
		{"grep tool", "mcp__dev__dev_grep", true},
		{"glob tool", "mcp__dev__dev_glob", true},
		{"search tool", "mcp__cortex__context_search", true},
		{"web fetch", "mcp__general__web_fetch", true},
		{"web search", "mcp__general__web_search", true},
		{"write tool", "mcp__dev__dev_write", false},
		{"edit tool", "mcp__dev__dev_edit", false},
		{"bash tool", "mcp__dev__dev_bash", false},
		{"delete tool", "mcp__engine__engine_task_delete", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, _ := svc.GetToolMeta(tt.tool)
			if meta.IsConcurrencySafe != tt.wantSafe {
				t.Errorf("GetToolMeta(%q).IsConcurrencySafe = %v, want %v", tt.tool, meta.IsConcurrencySafe, tt.wantSafe)
			}
		})
	}
}
