package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/dispatcher"
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
		// CW-20260512-0123 (SP-20260512-0011 W3): the legacy
		// `MaxIterations` agent-constraints field was removed —
		// `MaxTurns` is now the only agent-author-visible turn-count
		// knob, and the two legacy-fallback fixtures here were
		// removed because the field they exercised no longer exists.
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

// TestLoopState_ShouldStop_MaxTurnsIsSoft (CW-20260504-0001) — max_turns
// is no longer a hard terminator. Hitting it fires a one-shot soft warning
// (see TestLoopState_CheckSoftMaxTurnsWarning) and the loop continues. Only
// runaway/idle/hardCeiling/retry-budget actively terminate.
func TestLoopState_ShouldStop_MaxTurnsIsSoft(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{MaxTurns: 5}, nil, false)
	ls.iteration = 5
	stop, code, _ := ls.shouldStop()
	if stop {
		t.Errorf("shouldStop() at max_turns must NOT terminate after CW-20260504-0001 surgery; got stop=true code=%q", code)
	}
	// Sanity: the soft-warning helper fires once at the budget mark.
	fire, mt := ls.checkSoftMaxTurnsWarning()
	if !fire {
		t.Errorf("checkSoftMaxTurnsWarning should fire at iteration == maxTurns")
	}
	if mt != 5 {
		t.Errorf("soft warning maxTurns = %d, want 5", mt)
	}
}

// TestLoopState_ShouldStop_HardCeiling — hardCeiling stays the absolute
// turn-based terminator even after max_turns goes soft (CW-20260504-0001).
// The strategy planner sets MaxTurns way above hardCeiling here to prove
// that it's hardCeiling — not the (now-soft) maxTurns clamp — that fires.
func TestLoopState_ShouldStop_HardCeiling(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{MaxTurns: 200, HardCeiling: 10}, nil, false)
	ls.iteration = 10
	stop, code, _ := ls.shouldStop()
	if !stop {
		t.Error("shouldStop() should return true at hard ceiling")
	}
	if code != TerminationHardCeiling {
		t.Errorf("code = %q, want %q", code, TerminationHardCeiling)
	}
}

// TestLoopState_ShouldStop_PastMaxTurnsBeforeCeiling — even when iteration
// is well past the configured MaxTurns, the loop keeps running until
// hardCeiling (or another active terminator). CW-20260504-0001.
func TestLoopState_ShouldStop_PastMaxTurnsBeforeCeiling(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{MaxTurns: 5, HardCeiling: 50}, nil, false)
	ls.iteration = 25 // 5x past the soft budget
	stop, code, _ := ls.shouldStop()
	if stop {
		t.Errorf("shouldStop() at iter=25 (5x past MaxTurns=5, well below HardCeiling=50) must NOT terminate; got code=%q", code)
	}
}

// TestLoopState_CheckSoftMaxTurnsWarning_OneShot — the warning fires
// exactly once per generation at the budget mark, no matter how many times
// it's polled. CW-20260504-0001.
func TestLoopState_CheckSoftMaxTurnsWarning_OneShot(t *testing.T) {
	ls := newLoopState(chat.AgentConstraints{MaxTurns: 3}, nil, false)
	// Pre-budget: silent.
	ls.iteration = 2
	if fire, _ := ls.checkSoftMaxTurnsWarning(); fire {
		t.Error("warning should not fire below maxTurns")
	}
	// At budget: fires.
	ls.iteration = 3
	fire, mt := ls.checkSoftMaxTurnsWarning()
	if !fire {
		t.Fatal("warning should fire at iteration == maxTurns")
	}
	if mt != 3 {
		t.Errorf("warning maxTurns = %d, want 3", mt)
	}
	// Past budget: silent (one-shot latch).
	for i := 4; i < 10; i++ {
		ls.iteration = i
		if fire, _ := ls.checkSoftMaxTurnsWarning(); fire {
			t.Errorf("warning should fire only once; refired at iter=%d", i)
		}
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

// TestResolveIterationLimits_SubagentInactivityWindow pins CW-20260519-0073:
// a subagent dispatch resolves a tighter inactivity (liveness) timeout —
// subagentIdleTimeoutSeconds (Torque-parity, 300s of *silence*) — while
// every other caller and the omitted-caller case keep the 900s
// interactive default. The fixed 300s wall clock that used to bound
// subagent runs has been removed; this inactivity window is now the
// governing liveness signal.
func TestResolveIterationLimits_SubagentInactivityWindow(t *testing.T) {
	want := time.Duration(subagentIdleTimeoutSeconds) * time.Second
	wantDefault := time.Duration(defaultIdleTimeoutSeconds) * time.Second

	// Subagent dispatch: tight inactivity window.
	limSub := resolveIterationLimits(chat.AgentConstraints{}, dispatcher.CallerSubagent)
	if limSub.idleTimeout != want {
		t.Errorf("subagent idleTimeout = %s, want %s", limSub.idleTimeout, want)
	}

	// Chat dispatch: unchanged interactive default.
	limChat := resolveIterationLimits(chat.AgentConstraints{}, dispatcher.CallerChat)
	if limChat.idleTimeout != wantDefault {
		t.Errorf("chat idleTimeout = %s, want %s", limChat.idleTimeout, wantDefault)
	}

	// Omitted caller: defaults to the interactive window (back-compat
	// for the many test call sites that don't pass a caller).
	limOmitted := resolveIterationLimits(chat.AgentConstraints{})
	if limOmitted.idleTimeout != wantDefault {
		t.Errorf("omitted-caller idleTimeout = %s, want %s", limOmitted.idleTimeout, wantDefault)
	}

	// An explicit agent constraint still overrides the subagent default.
	limOverride := resolveIterationLimits(
		chat.AgentConstraints{IdleTimeoutSeconds: 42}, dispatcher.CallerSubagent)
	if limOverride.idleTimeout != 42*time.Second {
		t.Errorf("explicit-constraint idleTimeout = %s, want 42s", limOverride.idleTimeout)
	}
}

func TestShouldDirectReturnSubagentLiteral(t *testing.T) {
	plans := []toolPlan{{tu: chatTool("subagent_spawn")}}
	if !shouldDirectReturnSubagentLiteral(plans, "subagent_spawn", "```text\nx\n```", false) {
		t.Fatal("expected literal sync subagent result to short-circuit")
	}
	if shouldDirectReturnSubagentLiteral(plans, "subagent_spawn", "plain text", false) {
		t.Fatal("plain text should not short-circuit")
	}
	if shouldDirectReturnSubagentLiteral(plans, "dev_read", "```text\nx\n```", false) {
		t.Fatal("non-subagent tool should not short-circuit")
	}
	if shouldDirectReturnSubagentLiteral([]toolPlan{{tu: chatTool("subagent_spawn")}, {tu: chatTool("dev_read")}}, "subagent_spawn", "```text\nx\n```", false) {
		t.Fatal("multi-tool turns should not short-circuit")
	}
}

func chatTool(name string) llmtypes.ToolUseBlock {
	return llmtypes.ToolUseBlock{Name: name}
}

// CW-20260512-0123 (SP-20260512-0011 W3): TestLoopState_ShouldStop_RetryBudget
// was removed. The `RetryBudget` agent-constraints field and the
// `loopState.retryBudget` counter it drove were deleted alongside
// `MaxTimeSeconds` and `MaxIterations` — the runaway-fail-cap (Layer 1
// of shouldStop) is now the sole tool-failure terminator. The
// `TerminationRetryBudgetExhausted` code constant is retained for
// archived envelope telemetry only and is no longer emitted by the
// chat loop.

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

	// CW-20260512-0123 (SP-20260512-0011 W3): the `retryBudget`
	// counter was removed alongside the deleted `RetryBudget`
	// agent-constraints field; default check is gone.
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
		name     string
		tool     string
		wantSafe bool
	}{
		// Uniform agent-facing names per ADR-002 — no `mcp__server__` prefix.
		{"read tool", "dev_read", true},
		{"grep tool", "dev_grep", true},
		{"glob tool", "dev_glob", true},
		{"search tool", "context_search", true},
		{"web fetch", "web_fetch", true},
		{"web search", "web_search", true},
		{"write tool", "dev_write", false},
		{"edit tool", "dev_edit", false},
		{"bash tool", "dev_bash", false},
		{"delete tool", "engine_task_delete", false},
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
	if ls.scratchpadBytes != 0 {
		t.Errorf("scratchpadBytes changed on clear of missing key: got %d", ls.scratchpadBytes)
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
