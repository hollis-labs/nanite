package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	llmcontracts "github.com/hollis-labs/go-llm-contracts"
	llmtypes "github.com/hollis-labs/go-llm-types"
)

// TestBuildSystemBlocks_NoSlotsNoCache asserts the simple case: a single
// system prompt becomes one TextBlockParam without cache_control when the
// "system" hint is absent.
func TestBuildSystemBlocks_NoSlotsNoCache(t *testing.T) {
	c := New()
	out := c.buildSystemBlocks(llmtypes.ChatRequest{SystemPrompt: "you are helpful"})
	if len(out) != 1 {
		t.Fatalf("len(out)=%d want 1", len(out))
	}
	if out[0].Text != "you are helpful" {
		t.Fatalf("text=%q want 'you are helpful'", out[0].Text)
	}
	// Marshal and confirm no cache_control marker.
	data, _ := json.Marshal(out[0])
	if strings.Contains(string(data), "cache_control") {
		t.Fatalf("did not expect cache_control without hint, got: %s", data)
	}
}

func TestBuildSystemBlocks_WithSystemHint(t *testing.T) {
	c := New()
	c.SetCacheHints([]llmcontracts.CacheHint{{Position: "system"}})
	out := c.buildSystemBlocks(llmtypes.ChatRequest{SystemPrompt: "you are helpful"})
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
	out := c.buildSystemBlocks(llmtypes.ChatRequest{})
	if out != nil {
		t.Fatalf("expected nil for empty system + no slots, got %v", out)
	}
}

func TestBuildSystemBlocks_SlotBlocksUnchangedGetCache(t *testing.T) {
	c := New()
	out := c.buildSystemBlocks(llmtypes.ChatRequest{
		SystemPrompt: "system base",
		SlotBlocks: []llmtypes.SlotBlock{
			{Name: "stable", Content: "stable content", Changed: false},
			{Name: "volatile", Content: "volatile content", Changed: true},
		},
	})
	if len(out) != 3 {
		t.Fatalf("expected 3 blocks (base + 2 slots), got %d", len(out))
	}
	// The base ("system base") has no cache hint set, so no marker.
	// The unchanged slot (Changed=false) gets cache_control.
	// The volatile slot (Changed=true) does NOT.
	stable, _ := json.Marshal(out[1])
	volatile, _ := json.Marshal(out[2])
	if !strings.Contains(string(stable), "cache_control") {
		t.Fatalf("unchanged slot should have cache_control, got: %s", stable)
	}
	if strings.Contains(string(volatile), "cache_control") {
		t.Fatalf("changed slot should NOT have cache_control, got: %s", volatile)
	}
}

func TestBuildTools_EmptyReturnsNil(t *testing.T) {
	c := New()
	out := c.buildTools(nil)
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
	out := c.buildTools(tools)
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
	out := c.buildTools(tools)
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
	out := c.buildTools(tools)
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
	out := c.buildMessages(msgs)
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
	out := c.buildMessages(msgs)
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
	out := c.buildMessages(msgs)
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

func TestResolveModel_FallsBack(t *testing.T) {
	if got := resolveModel(llmtypes.ChatRequest{Model: "explicit"}); got != "explicit" {
		t.Fatalf("explicit model not preserved: %q", got)
	}
	if got := resolveModel(llmtypes.ChatRequest{}); got != DefaultModel {
		t.Fatalf("expected default model, got %q", got)
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
