package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
)

// TestBuildSystemBlocks_NoSlotsNoCache asserts the simple case: a single
// system prompt becomes one TextBlockParam without cache_control when the
// plan has System=false.
func TestBuildSystemBlocks_NoSlotsNoCache(t *testing.T) {
	c := New()
	out := c.buildSystemBlocks(llmtypes.ChatRequest{SystemPrompt: "you are helpful"}, cachePlan{})
	if len(out) != 1 {
		t.Fatalf("len(out)=%d want 1", len(out))
	}
	if out[0].Text != "you are helpful" {
		t.Fatalf("text=%q want 'you are helpful'", out[0].Text)
	}
	// Marshal and confirm no cache_control marker.
	data, _ := json.Marshal(out[0])
	if strings.Contains(string(data), "cache_control") {
		t.Fatalf("did not expect cache_control without plan.System, got: %s", data)
	}
}

func TestBuildSystemBlocks_WithSystemHint(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "system"}})
	req := llmtypes.ChatRequest{SystemPrompt: "you are helpful"}
	out := c.buildSystemBlocks(req, c.planCacheMarkers(req))
	if len(out) != 1 {
		t.Fatalf("len(out)=%d want 1", len(out))
	}
	data, _ := json.Marshal(out[0])
	if !strings.Contains(string(data), `"cache_control":{`) {
		t.Fatalf("expected cache_control marker, got: %s", data)
	}
	if !strings.Contains(string(data), `"type":"ephemeral"`) {
		t.Fatalf("expected ephemeral type, got: %s", data)
	}
}

func TestBuildSystemBlocks_EmptyReturnsNil(t *testing.T) {
	c := New()
	out := c.buildSystemBlocks(llmtypes.ChatRequest{}, cachePlan{})
	if out != nil {
		t.Fatalf("expected nil for empty system + no slots, got %v", out)
	}
}

func TestBuildSystemBlocks_SlotBlocksUnchangedGetCache(t *testing.T) {
	c := New()
	// Need the "system" hint set so plan.SlotMarkers is populated
	// (slot markers are mutually gated by plan.System per cachePlan).
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "system"}})
	req := llmtypes.ChatRequest{
		SystemPrompt: "system base",
		SlotBlocks: []llmtypes.SlotBlock{
			// SlotUniversal is in stablePrefixSlotPriority — eligible to mark.
			{Name: ctxpkg.SlotUniversal, Content: "stable content", Changed: false},
			// "volatile" is Changed AND not in priority — breaks the run.
			{Name: "volatile", Content: "volatile content", Changed: true},
		},
	}
	out := c.buildSystemBlocks(req, c.planCacheMarkers(req))
	if len(out) != 3 {
		t.Fatalf("expected 3 blocks (base + 2 slots), got %d", len(out))
	}
	// Layout: [base, universal, volatile].
	// The base block carries cache_control (plan.System=true).
	// The Universal slot (unchanged, priority) gets a slot marker.
	// The volatile slot (Changed=true) does NOT.
	stable, _ := json.Marshal(out[1])
	volatile, _ := json.Marshal(out[2])
	if !strings.Contains(string(stable), "cache_control") {
		t.Fatalf("Universal slot should have cache_control, got: %s", stable)
	}
	if strings.Contains(string(volatile), "cache_control") {
		t.Fatalf("changed slot should NOT have cache_control, got: %s", volatile)
	}
}

// TestBuildSystemBlocks_MarksOnlySlotsInPlan asserts that buildSystemBlocks
// honors plan.SlotMarkers by name — only slots whose name appears in the
// list get markers; other slots (even if unchanged) stay unmarked. This
// is the load-bearing W3 (CW-20260512-0109) contract: marker placement is
// codified in cache_plan.go and consumed by name here.
func TestBuildSystemBlocks_MarksOnlySlotsInPlan(t *testing.T) {
	c := New()
	req := llmtypes.ChatRequest{
		SystemPrompt: "base",
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: "a", Content: "a-chgd", Changed: true},
			{Name: "b", Content: "b-unchgd", Changed: false},
			{Name: ctxpkg.SlotUniversal, Content: "uni", Changed: false},
			{Name: ctxpkg.SlotSystem, Content: "sys", Changed: false},
			{Name: "e", Content: "e-unchgd", Changed: false},
		},
	}
	// Plan explicitly marks Universal + System only.
	plan := cachePlan{SlotMarkers: []string{ctxpkg.SlotUniversal, ctxpkg.SlotSystem}}
	out := c.buildSystemBlocks(req, plan)
	// 1 system + 5 slot blocks = 6.
	if len(out) != 6 {
		t.Fatalf("len(out)=%d want 6", len(out))
	}
	// Layout: [base, a, b, universal, system, e].
	// Markers expected on indices 3 (universal) and 4 (system) only.
	expectMarker := map[int]bool{3: true, 4: true}
	for i := 1; i <= 5; i++ {
		data, _ := json.Marshal(out[i])
		hasMarker := strings.Contains(string(data), "cache_control")
		want := expectMarker[i]
		if hasMarker != want {
			t.Errorf("block %d cache_control=%v want %v: %s", i, hasMarker, want, data)
		}
	}
}

// TestBuildSystemBlocks_NoSlotMarkerWhenPlanEmpty asserts that with
// plan.SlotMarkers empty, no slot gets a marker regardless of Changed.
func TestBuildSystemBlocks_NoSlotMarkerWhenPlanEmpty(t *testing.T) {
	c := New()
	req := llmtypes.ChatRequest{
		SystemPrompt: "base",
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: "a", Content: "a", Changed: false},
			{Name: "b", Content: "b", Changed: false},
		},
	}
	out := c.buildSystemBlocks(req, cachePlan{})
	if len(out) != 3 {
		t.Fatalf("len(out)=%d want 3", len(out))
	}
	for i, blk := range out {
		data, _ := json.Marshal(blk)
		if strings.Contains(string(data), "cache_control") {
			t.Errorf("block %d unexpected cache_control with plan={}: %s", i, data)
		}
	}
}

// TestBuildMessages_HonorsReducedRecentMessageCount asserts that the
// builder uses plan.RecentMessages (the budget-enforced count) rather than
// the raw hint count. Set 3 recent_message hints on the client but pass a
// plan with RecentMessages=1, and only the LAST user message should be
// marked.
func TestBuildMessages_HonorsReducedRecentMessageCount(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{
		{Position: "recent_message", Index: 0},
		{Position: "recent_message", Index: 1},
		{Position: "recent_message", Index: 2},
	})
	msgs := []llmtypes.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "user", Content: "second"},
		{Role: "user", Content: "third"},
	}
	plan := cachePlan{RecentMessages: 1}
	out := c.buildMessages(msgs, plan)
	if len(out) != 3 {
		t.Fatalf("len(out)=%d want 3", len(out))
	}
	first, _ := json.Marshal(out[0])
	second, _ := json.Marshal(out[1])
	third, _ := json.Marshal(out[2])
	if strings.Contains(string(first), "cache_control") {
		t.Errorf("first user msg unexpectedly cached: %s", first)
	}
	if strings.Contains(string(second), "cache_control") {
		t.Errorf("second user msg unexpectedly cached: %s", second)
	}
	if !strings.Contains(string(third), "cache_control") {
		t.Errorf("third user msg missing cache_control: %s", third)
	}
}

func TestBuildTools_EmptyReturnsNil(t *testing.T) {
	c := New()
	out := c.buildTools(nil, cachePlan{})
	if out != nil {
		t.Fatalf("expected nil for empty tools, got %v", out)
	}
}

func TestBuildTools_NoCacheHint(t *testing.T) {
	c := New()
	tools := []llmtypes.ToolDefinition{
		{Name: "t1", Description: "first", InputSchema: map[string]any{"type": "object"}},
		{Name: "t2", Description: "second", InputSchema: map[string]any{"type": "object"}},
	}
	out := c.buildTools(tools, cachePlan{})
	if len(out) != 2 {
		t.Fatalf("len(out)=%d want 2", len(out))
	}
	for i, ut := range out {
		data, _ := json.Marshal(ut)
		if strings.Contains(string(data), "cache_control") {
			t.Fatalf("tool %d unexpected cache_control: %s", i, data)
		}
	}
}

func TestBuildTools_WithCacheHintMarksLast(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "tools"}})
	tools := []llmtypes.ToolDefinition{
		{Name: "t1", Description: "first", InputSchema: map[string]any{"type": "object"}},
		{Name: "t2", Description: "second", InputSchema: map[string]any{"type": "object"}},
	}
	out := c.buildTools(tools, c.planCacheMarkers(llmtypes.ChatRequest{}))
	if len(out) != 2 {
		t.Fatalf("len(out)=%d want 2", len(out))
	}
	first, _ := json.Marshal(out[0])
	last, _ := json.Marshal(out[1])
	if strings.Contains(string(first), "cache_control") {
		t.Fatalf("first tool should NOT have cache_control: %s", first)
	}
	if !strings.Contains(string(last), "cache_control") {
		t.Fatalf("last tool should have cache_control: %s", last)
	}
}

func TestBuildTools_StrictPassThrough(t *testing.T) {
	c := New()
	strictTrue := true
	tools := []llmtypes.ToolDefinition{
		{Name: "t1", Description: "always validated", InputSchema: map[string]any{}, Strict: &strictTrue},
		{Name: "t2", Description: "default mode", InputSchema: map[string]any{}},
	}
	out := c.buildTools(tools, cachePlan{})
	first, _ := json.Marshal(out[0])
	second, _ := json.Marshal(out[1])
	if !strings.Contains(string(first), `"strict":true`) {
		t.Fatalf("strict=true tool missing strict marker: %s", first)
	}
	if strings.Contains(string(second), `"strict"`) {
		t.Fatalf("default-strict tool should not emit strict key: %s", second)
	}
}

func TestBuildMessages_RecentMessageCacheCount(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{
		{Position: "recent_message", Index: 0},
		{Position: "recent_message", Index: 1},
	})
	msgs := []llmtypes.ChatMessage{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "answer1"},
		{Role: "user", Content: "second"},
		{Role: "assistant", Content: "answer2"},
		{Role: "user", Content: "third"},
	}
	out := c.buildMessages(msgs, c.planCacheMarkers(llmtypes.ChatRequest{}))
	if len(out) != 5 {
		t.Fatalf("len(out)=%d want 5", len(out))
	}
	// Only the last 2 user messages (indices 2 + 4) should have cache_control.
	idxThird, _ := json.Marshal(out[4])
	idxSecond, _ := json.Marshal(out[2])
	idxFirst, _ := json.Marshal(out[0])
	if !strings.Contains(string(idxThird), "cache_control") {
		t.Fatalf("third user msg missing cache_control: %s", idxThird)
	}
	if !strings.Contains(string(idxSecond), "cache_control") {
		t.Fatalf("second user msg missing cache_control: %s", idxSecond)
	}
	if strings.Contains(string(idxFirst), "cache_control") {
		t.Fatalf("first user msg should NOT have cache_control: %s", idxFirst)
	}
}

func TestBuildMessages_ToolUseAndResultRoundtrip(t *testing.T) {
	c := New()
	input := map[string]any{"query": "weather"}
	msgs := []llmtypes.ChatMessage{
		{
			Role: "assistant",
			ContentBlocks: []llmtypes.ContentBlock{
				{Type: "text", Text: "let me check"},
				{Type: "tool_use", ID: "tu_1", Name: "weather", Input: &input},
			},
		},
		{
			Role: "user",
			ContentBlocks: []llmtypes.ContentBlock{
				{Type: "tool_result", ToolUseID: "tu_1", Content: "sunny, 72F"},
			},
		},
	}
	out := c.buildMessages(msgs, cachePlan{})
	if len(out) != 2 {
		t.Fatalf("len(out)=%d want 2", len(out))
	}
	// Verify tool_use round-trips correctly.
	data, _ := json.Marshal(out[0])
	if !strings.Contains(string(data), `"type":"tool_use"`) ||
		!strings.Contains(string(data), `"id":"tu_1"`) ||
		!strings.Contains(string(data), `"name":"weather"`) {
		t.Fatalf("tool_use roundtrip failed: %s", data)
	}
	// Verify tool_result round-trips correctly.
	data, _ = json.Marshal(out[1])
	if !strings.Contains(string(data), `"type":"tool_result"`) ||
		!strings.Contains(string(data), `"tool_use_id":"tu_1"`) ||
		!strings.Contains(string(data), "sunny, 72F") {
		t.Fatalf("tool_result roundtrip failed: %s", data)
	}
}

func TestBuildMessages_ThinkingBlocksRequireSignature(t *testing.T) {
	c := New()
	msgs := []llmtypes.ChatMessage{
		{
			Role: "assistant",
			ContentBlocks: []llmtypes.ContentBlock{
				{Type: "thinking", Text: "reasoning text", Signature: "sig123"},
				{Type: "thinking", Text: "no signature"},
				{Type: "text", Text: "answer"},
			},
		},
	}
	out := c.buildMessages(msgs, cachePlan{})
	if len(out) != 1 {
		t.Fatalf("len(out)=%d want 1", len(out))
	}
	// Marshal and check thinking with signature is included; bare thinking is dropped.
	data, _ := json.Marshal(out[0])
	if !strings.Contains(string(data), "reasoning text") {
		t.Fatalf("expected signed thinking block: %s", data)
	}
	if strings.Contains(string(data), "no signature") {
		t.Fatalf("unsigned thinking block should be dropped: %s", data)
	}
}

func TestBuildMessageParams_Reasoning(t *testing.T) {
	c := New()
	c.SetAPIKey("test-key")
	cfg := llmcontracts.ReasoningConfig{
		Enabled:      true,
		BudgetTokens: 4096,
		BetasHeader:  InterleavedThinkingBetaHeader,
	}
	params := c.buildMessageParams(
		llmtypes.ChatRequest{
			Messages: []llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
		},
		"claude-sonnet-4-5-20250930",
		true, // interleavedThinking
		cfg,
	)
	data, _ := json.Marshal(params)
	if !strings.Contains(string(data), `"thinking":{`) {
		t.Fatalf("expected thinking config, got: %s", data)
	}
	if !strings.Contains(string(data), `"budget_tokens":4096`) {
		t.Fatalf("expected budget_tokens=4096, got: %s", data)
	}
}

func TestComputeCacheablePrefixBytes_NoMarkerReturnsZero(t *testing.T) {
	hints := []llmcontracts.CacheHint{{Position: "system"}}
	got := computeCacheablePrefixBytes([]byte(`{"hello":"world"}`), hints)
	if got != 0 {
		t.Fatalf("expected 0 without marker, got %d", got)
	}
}

func TestComputeCacheablePrefixBytes_NoHintsReturnsZero(t *testing.T) {
	got := computeCacheablePrefixBytes([]byte(`{"cache_control":{"type":"ephemeral"}}`), nil)
	if got != 0 {
		t.Fatalf("expected 0 without hints, got %d", got)
	}
}

func TestComputeCacheablePrefixBytes_FindsLastMarker(t *testing.T) {
	payload := []byte(`{"x":1,"cache_control":{"type":"ephemeral"},"y":2,"cache_control":{"type":"ephemeral"},"z":3}`)
	hints := []llmcontracts.CacheHint{{Position: "system"}}
	got := computeCacheablePrefixBytes(payload, hints)
	if got <= 0 {
		t.Fatalf("expected positive offset, got %d", got)
	}
	// The last marker should be near the end of the payload.
	if got < len(payload)/2 {
		t.Fatalf("expected last marker (>= half), got %d", got)
	}
}

func TestExtractSchemaProperties_HandlesMap(t *testing.T) {
	props := map[string]any{"foo": map[string]any{"type": "string"}}
	schema := map[string]any{"type": "object", "properties": props}
	got := extractSchemaProperties(schema)
	if got == nil {
		t.Fatal("expected non-nil properties")
	}
}

func TestExtractSchemaRequired_StringSlice(t *testing.T) {
	schema := map[string]any{"required": []string{"a", "b"}}
	got := extractSchemaRequired(schema)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestExtractSchemaRequired_AnySlice(t *testing.T) {
	schema := map[string]any{"required": []any{"x", "y", 42}}
	got := extractSchemaRequired(schema)
	// 42 is not a string, so should be filtered out.
	if len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("got %v", got)
	}
}

func TestResolveModel(t *testing.T) {
	// Explicit model is preserved.
	got, err := resolveModel(llmtypes.ChatRequest{Model: "explicit"})
	if err != nil {
		t.Fatalf("explicit model errored: %v", err)
	}
	if got != "explicit" {
		t.Fatalf("explicit model not preserved: %q", got)
	}

	// CW-20260526-0003: empty model now errors rather than silently
	// defaulting. Upstream callers must resolve via the store.
	if _, err := resolveModel(llmtypes.ChatRequest{}); err == nil {
		t.Fatalf("expected ErrModelRequired for empty model, got nil")
	}
}

func TestResolveMaxTokens_FallsBack(t *testing.T) {
	if got := resolveMaxTokens(llmtypes.ChatRequest{MaxTokens: 100}); got != 100 {
		t.Fatalf("explicit max_tokens not preserved: %d", got)
	}
	if got := resolveMaxTokens(llmtypes.ChatRequest{}); got != DefaultMaxTokens {
		t.Fatalf("expected default max tokens, got %d", got)
	}
}

func TestBuildMessageParams_EndToEnd_SlotsAndToolsPreserveRecentMessages(t *testing.T) {
	c := New()
	req := llmtypes.ChatRequest{
		Model: "claude-sonnet-4-20250514",
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "universal rules", Changed: false},
			{Name: ctxpkg.SlotSystem, Content: "system persona", Changed: false},
		},
		Tools: []llmtypes.ToolDefinition{
			{Name: "dev_read", Description: "read file"},
		},
		Messages: []llmtypes.ChatMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "calling tool"},
			{Role: "user", Content: "tool result output"},
		},
		CacheHints: llmcontracts.DefaultCacheStrategy(),
	}

	params := c.buildMessageParams(req, "claude-sonnet-4-20250514", false, llmcontracts.ReasoningConfig{})
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("failed to marshal params: %v", err)
	}
	markerCount := strings.Count(string(data), `"cache_control":{`)
	if markerCount != 4 { // Universal (1) + Tools (1) + 2 user messages (2) = 4
		t.Fatalf("expected exactly 4 cache markers on wire, got %d: %s", markerCount, data)
	}

	// Verify Universal has cache_control
	universalData, _ := json.Marshal(params.System[0])
	if !strings.Contains(string(universalData), "cache_control") {
		t.Fatalf("Universal slot missing cache_control: %s", universalData)
	}

	// Verify Tool has cache_control
	toolData, _ := json.Marshal(params.Tools[0])
	if !strings.Contains(string(toolData), "cache_control") {
		t.Fatalf("Tool missing cache_control: %s", toolData)
	}

	// Verify last user message has cache_control
	lastMsgData, _ := json.Marshal(params.Messages[2])
	if !strings.Contains(string(lastMsgData), "cache_control") {
		t.Fatalf("last message missing cache_control: %s", lastMsgData)
	}
}

// TestBuildSystemBlocks_MarkerCountMatchesPlannedBudget is the regression for
// the 400 "A maximum of 4 blocks with cache_control may be provided. Found 5".
//
// planCacheMarkersWithHints budgets staticCount as len(plan.SlotMarkers),
// falling back to 1 only when that is empty — i.e. the prefix block and the
// slot markers are alternatives, never both. buildSystemBlocks used to mark
// the prefix whenever plan.System was set, so a request carrying a non-empty
// extraSystemPrefix alongside two slot markers put three markers on the wire
// where the planner had counted two. Add tools and one recent message and the
// request is refused outright.
//
// The shape below is the real one: a dynamic per-turn prefix, both stable
// prefix slots unchanged, and the system hint active.
func TestBuildSystemBlocks_MarkerCountMatchesPlannedBudget(t *testing.T) {
	c := New()
	req := llmtypes.ChatRequest{
		SystemPrompt: "per-turn prefix that changes every turn",
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: ctxpkg.SlotUniversal, Content: "universal", Changed: false},
			{Name: ctxpkg.SlotSystem, Content: "system", Changed: false},
		},
	}
	plan := planCacheMarkersWithHints(req, []llmcontracts.CacheHint{{Position: "system"}})
	if len(plan.SlotMarkers) != 2 {
		t.Fatalf("precondition: SlotMarkers=%v want both stable slots", plan.SlotMarkers)
	}

	got := 0
	for _, b := range c.buildSystemBlocks(req, plan) {
		data, _ := json.Marshal(b)
		if strings.Contains(string(data), `"cache_control":{`) {
			got++
		}
	}

	// What the planner budgeted for this section.
	want := len(plan.SlotMarkers)
	if want == 0 && plan.System {
		want = 1
	}
	if got != want {
		t.Errorf("cache_control markers on the wire = %d, planner budgeted %d — "+
			"the builder must not spend markers the planner did not count", got, want)
	}
}
