package selftools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
)

// TestHandoffStash_ResolvesSessionFromContext verifies handoff_stash uses the
// harness-assigned session id on the dispatch context when no session_id arg
// is supplied — a CLI-launch agent cannot know its own nanite session id, so
// it omits the arg and the context value carries it.
func TestHandoffStash_ResolvesSessionFromContext(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	const sessID = "sess-handoff-ctx"
	seedSession(t, s, sessID)

	ctx := mcp.WithSessionID(context.Background(), sessID)
	result, err := st.CallTool(ctx, "handoff_stash", map[string]any{
		// No session_id arg — the harness must fill it from context.
		"session_intent":   "auditing the CLI self-tools path",
		"next_step_anchor": "verify handoff round-trips",
	})
	if err != nil {
		t.Fatalf("handoff_stash: %v", err)
	}
	if result.IsError {
		t.Fatalf("handoff_stash errored: %s", result.Content[0].Text)
	}

	var resp struct {
		CacheKey  string `json:"cache_key"`
		Validated bool   `json:"validated"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &resp); err != nil {
		t.Fatalf("parse result: %v (raw: %s)", err, result.Content[0].Text)
	}
	if resp.CacheKey == "" || !resp.Validated {
		t.Fatalf("expected {cache_key, validated:true}, got: %s", result.Content[0].Text)
	}
}

// TestHandoffStash_ContextWinsOverArg verifies the dispatch context's session
// id takes precedence over an agent-supplied session_id arg — a CLI-launch
// agent that guesses a wrong id (e.g. the Claude Code SessionStart hook label)
// must not poison the write. The bogus arg would FK-fail; the context id
// (a real session row) succeeds.
func TestHandoffStash_ContextWinsOverArg(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	const sessID = "sess-handoff-ctxwins"
	seedSession(t, s, sessID)

	ctx := mcp.WithSessionID(context.Background(), sessID)
	result, err := st.CallTool(ctx, "handoff_stash", map[string]any{
		"session_id":       "session-20260518-deadbeef", // bogus hook-style label, not a sessions row
		"session_intent":   "context must override this",
		"next_step_anchor": "confirm no FK failure",
	})
	if err != nil {
		t.Fatalf("handoff_stash: %v", err)
	}
	if result.IsError {
		t.Fatalf("context id should have been used (bogus arg ignored), got error: %s", result.Content[0].Text)
	}
}

// TestHandoffStash_NoSessionAnywhere verifies a clear error when neither the
// context nor the args carry a session id.
func TestHandoffStash_NoSessionAnywhere(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	result, err := st.CallTool(context.Background(), "handoff_stash", map[string]any{
		"session_intent":   "no session",
		"next_step_anchor": "should error",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error result when no session id is resolvable")
	}
}

// TestHandoffStash_RoundTrip verifies a stash can be retrieved by
// handoff_pointers_expand, with both calls resolving the session from context.
func TestHandoffStash_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	const sessID = "sess-handoff-roundtrip"
	seedSession(t, s, sessID)
	ctx := mcp.WithSessionID(context.Background(), sessID)

	stash, err := st.CallTool(ctx, "handoff_stash", map[string]any{
		"session_intent":   "round-trip intent",
		"next_step_anchor": "round-trip anchor",
	})
	if err != nil || stash.IsError {
		t.Fatalf("handoff_stash: %v / %+v", err, stash)
	}
	var resp struct {
		CacheKey string `json:"cache_key"`
	}
	if err := json.Unmarshal([]byte(stash.Content[0].Text), &resp); err != nil {
		t.Fatalf("parse stash result: %v", err)
	}

	expand, err := st.CallTool(ctx, "handoff_pointers_expand", map[string]any{
		// session_id omitted — resolved from context.
		"cache_key": resp.CacheKey,
	})
	if err != nil || expand.IsError {
		t.Fatalf("handoff_pointers_expand: %v / %+v", err, expand)
	}
	if !strings.Contains(expand.Content[0].Text, "round-trip intent") {
		t.Errorf("expanded payload missing the stashed intent, got: %s", expand.Content[0].Text)
	}
}

// TestHandoffToolDefinitions_SessionIDOptional verifies session_id was dropped
// from the required list of both handoff tools — the harness supplies it.
func TestHandoffToolDefinitions_SessionIDOptional(t *testing.T) {
	for _, def := range []mcp.Tool{handoffStashToolDefinition(), handoffPointersExpandToolDefinition()} {
		required, _ := def.InputSchema["required"].([]string)
		for _, r := range required {
			if r == "session_id" {
				t.Errorf("%s: session_id must not be in required (harness-assigned), got required=%v", def.Name, required)
			}
		}
		props, _ := def.InputSchema["properties"].(map[string]any)
		if _, ok := props["session_id"]; !ok {
			t.Errorf("%s: session_id property should still exist (optional override)", def.Name)
		}
	}
}

// TestScratchpadTools_ClearErrorViaTransport verifies the per-turn scratchpad
// tools return an actionable error (not a bare "unknown tool") when dispatched
// through the self-tools transport — the path a CLI-launch session takes,
// where there is no chat-loop turn state.
func TestScratchpadTools_ClearErrorViaTransport(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	for _, name := range []string{"scratchpad_write", "scratchpad_read", "scratchpad_clear"} {
		result, err := st.CallTool(context.Background(), name, map[string]any{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !result.IsError {
			t.Fatalf("%s: expected an error result", name)
		}
		body := result.Content[0].Text
		if strings.Contains(body, "unknown tool") {
			t.Errorf("%s: should return an actionable per-turn error, not a bare 'unknown tool': %s", name, body)
		}
		if !strings.Contains(body, "per-turn") {
			t.Errorf("%s: error should explain the per-turn limitation, got: %s", name, body)
		}
	}
}
