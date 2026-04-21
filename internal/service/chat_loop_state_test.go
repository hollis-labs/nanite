package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
)

func TestLoopState_ResolvedMaxTurns(t *testing.T) {
	tests := []struct {
		name        string
		constraints chat.AgentConstraints
		want        int
	}{
		{
			name:        "zero uses default 75",
			constraints: chat.AgentConstraints{},
			want:        75,
		},
		{
			name:        "explicit max turns",
			constraints: chat.AgentConstraints{MaxTurns: 50},
			want:        50,
		},
		{
			name:        "max turns clamped to hard ceiling",
			constraints: chat.AgentConstraints{MaxTurns: 300},
			want:        200, // default hard ceiling
		},
		{
			name:        "unlimited uses hard ceiling",
			constraints: chat.AgentConstraints{MaxTurns: -1},
			want:        200,
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
			name:        "custom hard ceiling higher than default max turns",
			constraints: chat.AgentConstraints{HardCeiling: 300},
			want:        75, // default maxTurns, not bumped by higher ceiling
		},
		{
			name:        "custom hard ceiling lower than default max turns",
			constraints: chat.AgentConstraints{HardCeiling: 50},
			want:        50, // default maxTurns clamped by tighter ceiling
		},
		{
			name:        "hard ceiling lower than explicit max turns",
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

// TestLoopState_ShouldStop_SoftCapDoesNotTerminate is the CW-20260417-0485
// behavior pivot: hitting the soft consecutiveFailCap (default 3) no longer
// exits the loop. The LLM continues to receive tool_result error blocks and
// gets a chance to recover or stop gracefully.
func TestLoopState_ShouldStop_SoftCapDoesNotTerminate(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	// 3 failures — the old soft cap — should NOT stop the loop anymore.
	for i := 0; i < 3; i++ {
		ls.recordToolCall("test-tool", false)
	}
	stop, _, _ := ls.shouldStop()
	if stop {
		t.Error("shouldStop() should NOT terminate at the soft consecutive-fail threshold (3) — CW-20260417-0485")
	}

	// 9 failures still under the runaway cap (default 10) — still no stop.
	for i := 3; i < 9; i++ {
		ls.recordToolCall("test-tool", false)
	}
	stop, _, _ = ls.shouldStop()
	if stop {
		t.Errorf("shouldStop() should NOT terminate at %d failures (runaway cap is %d)",
			ls.consecutiveFailures, ls.limits.runawayFailCap)
	}
}

// TestLoopState_ShouldStop_RunawayFailures asserts the hard circuit-breaker
// trips at the runaway cap (default 10) and surfaces the structured
// TerminationCode so the chat-loop-terminated envelope can carry it.
// CW-20260417-0485.
func TestLoopState_ShouldStop_RunawayFailures(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)

	for i := 0; i < 10; i++ {
		stop, _, _ := ls.shouldStop()
		if stop {
			t.Fatalf("shouldStop() terminated at iter %d before reaching runaway cap", i)
		}
		ls.recordToolCall("test-tool", false)
	}

	stop, code, reason := ls.shouldStop()
	if !stop {
		t.Fatal("shouldStop() should terminate at runaway cap (10)")
	}
	if code != TerminationRunawayToolFailures {
		t.Errorf("termination code = %q, want %q", code, TerminationRunawayToolFailures)
	}
	if reason == "" {
		t.Error("shouldStop() should return a reason")
	}

	// Reset on success — runaway condition clears.
	ls.consecutiveFailures = 0
	ls.recordToolCall("test-tool", true)
	stop, _, _ = ls.shouldStop()
	if stop {
		t.Error("shouldStop() should return false after success reset")
	}
}

// TestLoopState_ShouldStop_CustomRunawayCap lets agent constraints override
// the runaway cap (ticket acceptance criterion — configurability kept).
func TestLoopState_ShouldStop_CustomRunawayCap(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{RunawayFailCap: 5}, nil, false)
	for i := 0; i < 5; i++ {
		ls.recordToolCall("t", false)
	}
	stop, code, _ := ls.shouldStop()
	if !stop {
		t.Fatal("shouldStop() should terminate at custom runaway cap (5)")
	}
	if code != TerminationRunawayToolFailures {
		t.Errorf("code = %q, want %q", code, TerminationRunawayToolFailures)
	}
}

func TestLoopState_ShouldStop_MaxTurns(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{MaxTurns: 5}, nil, false)
	ls.iteration = 5
	stop, code, reason := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should return true at max turns")
	}
	if code != TerminationMaxTurns {
		t.Errorf("code = %q, want %q", code, TerminationMaxTurns)
	}
	if reason == "" {
		t.Error("expected a reason")
	}
}

func TestLoopState_ShouldStop_HardCeiling(t *testing.T) {
	// MaxTurns explicitly higher than hard ceiling. resolvedMaxTurns() clamps
	// to hardCeiling, so at iter==10 both the maxTurns and hardCeiling checks
	// are true. PR #64 feedback: hardCeiling must be checked first so the
	// termination code reflects the actual constraint that tripped.
	ls := newLoopState(chat.AgentConstraints{MaxTurns: 200, HardCeiling: 10}, nil, false)
	ls.iteration = 10
	stop, code, _ := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should return true at hard ceiling")
	}
	if code != TerminationHardCeiling {
		t.Errorf("code = %q, want %q (hard_ceiling must be checked before max_turns)", code, TerminationHardCeiling)
	}
}

// TestLoopState_ShouldStop_MaxTurnsBeforeCeiling asserts that when MaxTurns is
// explicitly lower than HardCeiling, the max_turns layer still fires at the
// configured MaxTurns value (not the ceiling). Complements HardCeiling test
// to prove both codes remain reachable after the layer reorder.
func TestLoopState_ShouldStop_MaxTurnsBeforeCeiling(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{MaxTurns: 5, HardCeiling: 50}, nil, false)
	ls.iteration = 5
	stop, code, _ := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should return true at max turns")
	}
	if code != TerminationMaxTurns {
		t.Errorf("code = %q, want %q", code, TerminationMaxTurns)
	}
}

// TestLoopState_ResolveIterationLimits_ClampsRunawayCap asserts that a
// caller-supplied RunawayFailCap lower than ConsecutiveFailCap is silently
// raised to the soft cap, so the "critical" tool_warning UI signal remains
// reachable. PR #64 feedback.
func TestLoopState_ResolveIterationLimits_ClampsRunawayCap(t *testing.T) {
	c := chat.AgentConstraints{
		ConsecutiveFailCap: 5,
		RunawayFailCap:     2, // lower than soft cap — should clamp up
	}
	lim := resolveIterationLimits(c)
	if lim.runawayFailCap < lim.consecutiveFailCap {
		t.Errorf("runawayFailCap=%d should have been clamped up to >= consecutiveFailCap=%d",
			lim.runawayFailCap, lim.consecutiveFailCap)
	}
}

func TestLoopState_ShouldStop_RetryBudget(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{RetryBudget: 1}, nil, false)
	ls.recordToolCall("tool", false) // uses the budget
	stop, code, reason := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should return true when retry budget exhausted")
	}
	if code != TerminationRetryBudgetExhausted {
		t.Errorf("code = %q, want %q", code, TerminationRetryBudgetExhausted)
	}
	if reason == "" {
		t.Error("expected a reason")
	}
}

func TestLoopState_ShouldStop_IdleTimeout(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{IdleTimeoutSeconds: 1}, nil, false)
	ls.lastActivity = time.Now().Add(-2 * time.Second)
	stop, code, reason := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should return true after idle timeout")
	}
	if code != TerminationIdleTimeout {
		t.Errorf("code = %q, want %q", code, TerminationIdleTimeout)
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

	// CW-20260417-0485: 3 denials is the soft cap, NOT the terminal cap.
	// Termination now requires hitting the runaway cap (default 10).
	ls.recordPermissionDenial()
	ls.recordPermissionDenial()
	stop, _, _ := ls.shouldStop()
	if stop {
		t.Error("shouldStop() should NOT trigger at 3 denials (soft cap)")
	}

	// Drive to the runaway cap.
	for i := 3; i < 10; i++ {
		ls.recordPermissionDenial()
	}
	stop, code, _ := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should trigger at runaway cap (10)")
	}
	if code != TerminationRunawayToolFailures {
		t.Errorf("code = %q, want %q", code, TerminationRunawayToolFailures)
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
		{Name: "dev_read", DurationMs: 50, Success: true},
		{Name: "dev_write", DurationMs: 120, Success: false},
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
	if ls.limits.maxTurns != 75 {
		t.Errorf("maxTurns = %d, want 75", ls.limits.maxTurns)
	}
	if ls.limits.hardCeiling != 200 {
		t.Errorf("hardCeiling = %d, want 200", ls.limits.hardCeiling)
	}
	if ls.limits.consecutiveFailCap != 3 {
		t.Errorf("consecutiveFailCap = %d, want 3", ls.limits.consecutiveFailCap)
	}
	if ls.limits.runawayFailCap != 10 {
		t.Errorf("runawayFailCap = %d, want 10", ls.limits.runawayFailCap)
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
		{"search tool", "mcp__conduit__context_search", true},
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

func TestNewLoopState_ScratchpadInitialized(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	if ls.scratchpad == nil {
		t.Fatal("scratchpad map must be initialized, got nil")
	}
	if ls.scratchpadBytes != 0 {
		t.Errorf("scratchpadBytes must start at 0, got %d", ls.scratchpadBytes)
	}
}

func TestScratchpadWrite_BasicRoundTrip(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	if err := ls.scratchpadWrite("k", "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries, ok := ls.scratchpadRead("k")
	if !ok {
		t.Fatal("key not found after write")
	}
	if entries["k"] != "hello" {
		t.Errorf("expected 'hello', got %v", entries["k"])
	}
}

func TestScratchpadWrite_ValueTooLarge(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	big := strings.Repeat("x", scratchpadMaxValueBytes+1)
	err := ls.scratchpadWrite("k", big)
	if err == nil {
		t.Fatal("expected error for oversized value, got nil")
	}
}

func TestScratchpadWrite_TotalExceeded(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	// Each chunk is just under 8 KiB. After 8 writes, total > 64 KiB.
	chunk := strings.Repeat("y", scratchpadMaxValueBytes-10)
	for i := 0; i < 8; i++ {
		key := fmt.Sprintf("k%d", i)
		_ = ls.scratchpadWrite(key, chunk)
	}
	err := ls.scratchpadWrite("overflow", chunk)
	if err == nil {
		t.Fatal("expected total-size error, got nil")
	}
}

func TestScratchpadWrite_UpsertAdjustsByteCount(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	if err := ls.scratchpadWrite("k", strings.Repeat("a", 100)); err != nil {
		t.Fatal(err)
	}
	before := ls.scratchpadBytes
	// Overwrite with smaller value — bytes should decrease.
	if err := ls.scratchpadWrite("k", "x"); err != nil {
		t.Fatal(err)
	}
	if ls.scratchpadBytes >= before {
		t.Errorf("expected bytes to decrease on upsert, got %d (was %d)", ls.scratchpadBytes, before)
	}
}

func TestScratchpadRead_AllEntries(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	_ = ls.scratchpadWrite("a", 1)
	_ = ls.scratchpadWrite("b", 2)
	entries, ok := ls.scratchpadRead("") // empty key = read all
	if !ok {
		t.Fatal("read all should always return ok=true")
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestScratchpadRead_MissingKey(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	_, ok := ls.scratchpadRead("nope")
	if ok {
		t.Fatal("expected ok=false for missing key")
	}
}

func TestScratchpadClear_RemovesKey(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	_ = ls.scratchpadWrite("k", "v")
	bytesBefore := ls.scratchpadBytes
	cleared := ls.scratchpadClear("k")
	if !cleared {
		t.Fatal("expected cleared=true")
	}
	if ls.scratchpadBytes >= bytesBefore {
		t.Errorf("expected bytes to decrease after clear, got %d (was %d)", ls.scratchpadBytes, bytesBefore)
	}
	_, ok := ls.scratchpadRead("k")
	if ok {
		t.Fatal("key still readable after clear")
	}
}

func TestScratchpadClear_MissingKeyReturnsFalse(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{}, nil, false)
	if ls.scratchpadClear("nope") {
		t.Fatal("expected cleared=false for missing key")
	}
}

func TestScratchpad_TurnExitEviction(t *testing.T) {
	ls1 := newLoopState(chat.AgentConstraints{}, nil, false)
	_ = ls1.scratchpadWrite("k", "turn1")

	// Simulate new turn: new loopState.
	ls2 := newLoopState(chat.AgentConstraints{}, nil, false)
	_, ok := ls2.scratchpadRead("k")
	if ok {
		t.Fatal("scratchpad from prior turn must not leak into new loopState")
	}
}
