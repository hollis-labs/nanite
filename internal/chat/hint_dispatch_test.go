package chat

// hint_dispatch_test.go — Unit tests for think-block v2 dynamic dispatch (F5 / CW-20260420-0022).
//
// Tests cover:
//   - ThinkToolBlockWithDispatch: flag combinations → v0 / v1 / v2 paths
//   - ThinkToolBlockDynamic: peer succeeds, peer fails, empty response, over-budget
//   - PeerQuery dispatch payload shape (request fields)
//   - Token budget guard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// -------------------------------------------------------------------
// Stub dispatcher implementations
// -------------------------------------------------------------------

// okDispatcher simulates a successful peer that returns the given ids.
type okDispatcher struct {
	ids             []string
	capturedPayload string
}

func (d *okDispatcher) Dispatch(_ context.Context, payload string) (string, error) {
	d.capturedPayload = payload
	b, _ := json.Marshal(d.ids)
	return string(b), nil
}

// errDispatcher simulates a peer that always fails.
type errDispatcher struct{}

func (d *errDispatcher) Dispatch(_ context.Context, _ string) (string, error) {
	return "", errors.New("peer unavailable")
}

// emptyDispatcher simulates a peer that returns an empty array.
type emptyDispatcher struct{}

func (d *emptyDispatcher) Dispatch(_ context.Context, _ string) (string, error) {
	return "[]", nil
}

// badJSONDispatcher simulates a peer that returns invalid JSON.
type badJSONDispatcher struct{}

func (d *badJSONDispatcher) Dispatch(_ context.Context, _ string) (string, error) {
	return "not json", nil
}

// proseWrappedDispatcher returns hint IDs embedded in prose (tests extraction).
type proseWrappedDispatcher struct{}

func (d *proseWrappedDispatcher) Dispatch(_ context.Context, _ string) (string, error) {
	return `Here are the hints: ["scratchpad","memory_recall"] — done.`, nil
}

// -------------------------------------------------------------------
// ThinkToolBlockWithDispatch tests
// -------------------------------------------------------------------

// TestThinkToolBlockWithDispatch_V0WhenV1Off verifies that v0 is returned when
// NANITE_THINK_BLOCK_V1=false, regardless of v2 or dispatcher.
func TestThinkToolBlockWithDispatch_V0WhenV1Off(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V1", "false")
	t.Setenv("NANITE_THINK_BLOCK_V2_ENABLED", "true")

	disp := &okDispatcher{ids: []string{"scratchpad"}}
	got := ThinkToolBlockWithDispatch(context.Background(), disp, "hello", "small", "")
	if got != thinkToolBlock {
		t.Errorf("expected v0 block when V1=false; got:\n%s", got)
	}
}

// TestThinkToolBlockWithDispatch_V1WhenV2Off verifies v1 static is returned
// when V1=true and V2=false (the normal default path).
func TestThinkToolBlockWithDispatch_V1WhenV2Off(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V1", "true")
	t.Setenv("NANITE_THINK_BLOCK_V2_ENABLED", "false")

	disp := &okDispatcher{ids: []string{"scratchpad"}}
	got := ThinkToolBlockWithDispatch(context.Background(), disp, "hello", "small", "")
	if got != thinkToolBlockV1 {
		t.Errorf("expected v1 block when V2=false; got:\n%s", got)
	}
}

// TestThinkToolBlockWithDispatch_V1WhenDispatcherNil verifies v1 static is
// returned when V2=true but dispatcher is nil.
func TestThinkToolBlockWithDispatch_V1WhenDispatcherNil(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V1", "true")
	t.Setenv("NANITE_THINK_BLOCK_V2_ENABLED", "true")

	got := ThinkToolBlockWithDispatch(context.Background(), nil, "hello", "small", "")
	if got != thinkToolBlockV1 {
		t.Errorf("expected v1 block when dispatcher is nil; got:\n%s", got)
	}
}

// TestThinkToolBlockWithDispatch_V2WhenPeerSucceeds verifies dynamic block
// is returned when V1=true, V2=true, and peer succeeds.
func TestThinkToolBlockWithDispatch_V2WhenPeerSucceeds(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V1", "true")
	t.Setenv("NANITE_THINK_BLOCK_V2_ENABLED", "true")

	disp := &okDispatcher{ids: []string{"scratchpad", "memory_recall"}}
	got := ThinkToolBlockWithDispatch(context.Background(), disp, "write a plan", "large", "planner-mention")
	// Should NOT be the static v1 block (it's dynamic).
	if got == thinkToolBlockV1 {
		t.Error("expected dynamic block but got v1 static")
	}
	// Should contain scratchpad body content.
	if !strings.Contains(strings.ToLower(got), "scratchpad") {
		t.Errorf("expected scratchpad content in dynamic block; got:\n%s", got)
	}
}

// -------------------------------------------------------------------
// ThinkToolBlockDynamic fallback path tests
// -------------------------------------------------------------------

// TestThinkToolBlockDynamic_NilDispatcher verifies nil dispatcher → v1 fallback.
func TestThinkToolBlockDynamic_NilDispatcher(t *testing.T) {
	got := ThinkToolBlockDynamic(context.Background(), nil, "hello", "small", "")
	if got != thinkToolBlockV1 {
		t.Error("expected v1 fallback for nil dispatcher")
	}
}

// TestThinkToolBlockDynamic_PeerError verifies peer error → v1 fallback.
func TestThinkToolBlockDynamic_PeerError(t *testing.T) {
	got := ThinkToolBlockDynamic(context.Background(), &errDispatcher{}, "hello", "small", "")
	if got != thinkToolBlockV1 {
		t.Error("expected v1 fallback when peer errors")
	}
}

// TestThinkToolBlockDynamic_EmptyResponse verifies empty array → v1 fallback.
func TestThinkToolBlockDynamic_EmptyResponse(t *testing.T) {
	got := ThinkToolBlockDynamic(context.Background(), &emptyDispatcher{}, "hello", "small", "")
	if got != thinkToolBlockV1 {
		t.Error("expected v1 fallback when peer returns empty array")
	}
}

// TestThinkToolBlockDynamic_BadJSON verifies non-parseable response → v1 fallback.
func TestThinkToolBlockDynamic_BadJSON(t *testing.T) {
	got := ThinkToolBlockDynamic(context.Background(), &badJSONDispatcher{}, "hello", "small", "")
	if got != thinkToolBlockV1 {
		t.Error("expected v1 fallback when peer returns bad JSON")
	}
}

// TestThinkToolBlockDynamic_ProseWrappedIDs verifies extraction of IDs from
// prose-wrapped JSON (peer adds surrounding text).
func TestThinkToolBlockDynamic_ProseWrappedIDs(t *testing.T) {
	got := ThinkToolBlockDynamic(context.Background(), &proseWrappedDispatcher{}, "hello", "small", "")
	// Should succeed — ids extracted from prose.
	if got == thinkToolBlockV1 {
		t.Error("expected dynamic block from prose-wrapped response; got v1 fallback")
	}
	if !strings.Contains(strings.ToLower(got), "scratchpad") {
		t.Errorf("expected scratchpad in prose-extracted block; got:\n%s", got)
	}
}

// TestThinkToolBlockDynamic_UnknownIDsFallback verifies all-unknown IDs → v1 fallback.
func TestThinkToolBlockDynamic_UnknownIDsFallback(t *testing.T) {
	disp := &okDispatcher{ids: []string{"does-not-exist", "also-missing"}}
	got := ThinkToolBlockDynamic(context.Background(), disp, "hello", "small", "")
	if got != thinkToolBlockV1 {
		t.Error("expected v1 fallback when all peer IDs are unknown in catalog")
	}
}

// -------------------------------------------------------------------
// PeerQuery dispatch payload shape tests
// -------------------------------------------------------------------

// TestDispatchPayload_Shape verifies the peer request carries the required fields.
func TestDispatchPayload_Shape(t *testing.T) {
	disp := &okDispatcher{ids: []string{"scratchpad"}}
	t.Setenv("NANITE_THINK_BLOCK_V2_ENABLED", "true")
	t.Setenv("NANITE_THINK_BLOCK_V1", "true")

	_ = ThinkToolBlockDynamic(context.Background(), disp, "build a feature", "large", "worker-execute")

	if disp.capturedPayload == "" {
		t.Fatal("no payload captured — Dispatch was not called")
	}

	var req struct {
		UserInput   string               `json:"user_input"`
		ScopeTier   string               `json:"scope_tier"`
		ReflexMatch string               `json:"reflex_match"`
		HintCatalog []HintCatalogSummary `json:"hint_catalog"`
	}
	if err := json.Unmarshal([]byte(disp.capturedPayload), &req); err != nil {
		t.Fatalf("failed to parse captured payload: %v\npayload: %s", err, disp.capturedPayload)
	}

	if req.UserInput != "build a feature" {
		t.Errorf("user_input = %q, want %q", req.UserInput, "build a feature")
	}
	if req.ScopeTier != "large" {
		t.Errorf("scope_tier = %q, want %q", req.ScopeTier, "large")
	}
	if req.ReflexMatch != "worker-execute" {
		t.Errorf("reflex_match = %q, want %q", req.ReflexMatch, "worker-execute")
	}
	if len(req.HintCatalog) == 0 {
		t.Error("hint_catalog is empty in dispatch payload")
	}
	// Verify catalog summary has required fields.
	for _, s := range req.HintCatalog {
		if s.ID == "" {
			t.Errorf("catalog entry missing id: %+v", s)
		}
		if s.Body == "" {
			t.Errorf("catalog entry %q missing body", s.ID)
		}
	}
}

// -------------------------------------------------------------------
// Token budget guard tests
// -------------------------------------------------------------------

// TestGuardTokenBudget_UnderBudget verifies a short block passes through unchanged.
func TestGuardTokenBudget_UnderBudget(t *testing.T) {
	selected := []Hint{
		{ID: "a", Affordance: "A", Body: "Short body A."},
	}
	block := renderDynamicBlock(selected)
	result := guardTokenBudget(block, selected)
	if result != block {
		t.Error("guardTokenBudget changed an under-budget block")
	}
}

// TestGuardTokenBudget_OverBudget verifies an over-budget block is truncated
// to fit within 200 tokens.
func TestGuardTokenBudget_OverBudget(t *testing.T) {
	// Build a block that exceeds 200 tokens by using many large hints.
	// Each body is ~60 chars = ~15 tokens; 15 hints × 15 = ~225 tokens.
	selected := make([]Hint, 15)
	for i := 0; i < 15; i++ {
		selected[i] = Hint{
			ID:         "h",
			Affordance: "X",
			Body:       "This is a longer hint body text that consumes tokens for testing purposes.",
		}
	}
	block := renderDynamicBlock(selected)
	content := strings.TrimLeft(block, "\n")
	if EstimateTokens(content) <= thinkBlockMaxTokens {
		t.Skip("test hints didn't exceed budget — adjust hint body length")
	}

	result := guardTokenBudget(block, selected)
	resultContent := strings.TrimLeft(result, "\n")
	if EstimateTokens(resultContent) > thinkBlockMaxTokens {
		t.Errorf("guardTokenBudget did not reduce block to ≤%d tokens; got %d",
			thinkBlockMaxTokens, EstimateTokens(resultContent))
	}
}

// TestThinkToolBlockDynamic_TokenBudget verifies the full dynamic block
// rendered from real catalog entries stays within 200 tokens.
func TestThinkToolBlockDynamic_TokenBudget(t *testing.T) {
	// Return all catalog IDs from the peer — max possible size.
	catalog := BuiltinHints()
	if len(catalog) == 0 {
		t.Skip("hint catalog empty — skipping budget test")
	}
	ids := make([]string, len(catalog))
	for i, h := range catalog {
		ids[i] = h.ID
	}

	disp := &okDispatcher{ids: ids}
	got := ThinkToolBlockDynamic(context.Background(), disp, "large open task", "open", "")
	if got == thinkToolBlockV1 {
		t.Skip("dynamic dispatch fell back to v1 — budget test cannot run")
	}

	content := strings.TrimLeft(got, "\n")
	tokens := EstimateTokens(content)
	if tokens > thinkBlockMaxTokens {
		t.Errorf("dynamic block exceeded %d token budget: %d tokens\n%s",
			thinkBlockMaxTokens, tokens, got)
	}
	t.Logf("dynamic block token estimate: %d tokens (%d chars)", tokens, len(content))
}

// -------------------------------------------------------------------
// IsThinkBlockV2Enabled tests
// -------------------------------------------------------------------

// TestIsThinkBlockV2Enabled_DefaultOff verifies the flag defaults to OFF.
func TestIsThinkBlockV2Enabled_DefaultOff(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V2_ENABLED", "")
	if IsThinkBlockV2Enabled() {
		t.Error("IsThinkBlockV2Enabled() should return false when unset (default OFF)")
	}
}

// TestIsThinkBlockV2Enabled_True verifies "true" activates v2.
func TestIsThinkBlockV2Enabled_True(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V2_ENABLED", "true")
	if !IsThinkBlockV2Enabled() {
		t.Error("IsThinkBlockV2Enabled() should return true when set to 'true'")
	}
}

// TestIsThinkBlockV2Enabled_One verifies "1" activates v2.
func TestIsThinkBlockV2Enabled_One(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V2_ENABLED", "1")
	if !IsThinkBlockV2Enabled() {
		t.Error("IsThinkBlockV2Enabled() should return true when set to '1'")
	}
}

// TestIsThinkBlockV2Enabled_False verifies "false" keeps v2 off.
func TestIsThinkBlockV2Enabled_False(t *testing.T) {
	t.Setenv("NANITE_THINK_BLOCK_V2_ENABLED", "false")
	if IsThinkBlockV2Enabled() {
		t.Error("IsThinkBlockV2Enabled() should return false when set to 'false'")
	}
}
