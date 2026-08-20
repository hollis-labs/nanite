package selftools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/store"
)

// seedSessionByID creates a session with a chosen ID and returns the
// short_code the store assigned. Formerly seedSessionInWS — the workspace
// dimension it varied is gone (Phase 0 item 20, retire workspaces).
func seedSessionByID(t *testing.T, st *SelfToolsTransport, id string) string {
	t.Helper()
	sess := &store.Session{ID: id, Title: "Session " + id}
	if err := st.Store.CreateSession(sess); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
	return sess.ShortCode
}

// TestChatGet_BasicHappyPath: read a chat by short code; messages come back
// in chronological order with text unwrapped.
func TestChatGet_BasicHappyPath(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	code := seedSessionByID(t, st, "sess-cg-1")
	seedMessage(t, s, "sess-cg-1", "user", "first message", false)
	seedMessage(t, s, "sess-cg-1", "assistant", `{"v":1,"text":"second wrapped","tier":"text"}`, false)

	ctx := mcp.WithCallerProfile(context.Background(), "agent-A")
	res, err := st.CallTool(ctx, "chat_get", map[string]any{"target": code})
	if err != nil || res.IsError {
		t.Fatalf("chat_get failed: %v / %+v", err, res)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &payload); err != nil {
		t.Fatalf("parse payload: %v (raw: %s)", err, res.Content[0].Text)
	}
	if payload["short_code"] != code {
		t.Errorf("short_code: got %v want %s", payload["short_code"], code)
	}
	if payload["session_id"] != "sess-cg-1" {
		t.Errorf("session_id: got %v want sess-cg-1", payload["session_id"])
	}
	msgs, _ := payload["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d (raw: %s)", len(msgs), res.Content[0].Text)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "user" || first["text"] != "first message" {
		t.Errorf("first msg wrong: %+v", first)
	}
	second, _ := msgs[1].(map[string]any)
	if second["text"] != "second wrapped" {
		t.Errorf("second msg text not unwrapped: %+v", second)
	}
}

// TestChatGet_ShortCodeNormalisation: c248, #c248, C248, #C248 all resolve.
func TestChatGet_ShortCodeNormalisation(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	code := seedSessionByID(t, st, "sess-norm")
	seedMessage(t, s, "sess-norm", "user", "hello", false)

	ctx := mcp.WithCallerProfile(context.Background(), "agent-A")
	for _, variant := range []string{code, "#" + code, strings.ToUpper(code), "#" + strings.ToUpper(code)} {
		res, err := st.CallTool(ctx, "chat_get", map[string]any{"target": variant})
		if err != nil || res.IsError {
			t.Fatalf("chat_get(%q) failed: %v / %+v", variant, err, res)
		}
		var payload map[string]any
		_ = json.Unmarshal([]byte(res.Content[0].Text), &payload)
		if payload["short_code"] != code {
			t.Errorf("variant %q: short_code=%v want %s", variant, payload["short_code"], code)
		}
	}
}

// TestChatGet_BySessionID: passing a UUID directly works.
func TestChatGet_BySessionID(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	_ = seedSessionByID(t, st, "sess-uuid")
	seedMessage(t, s, "sess-uuid", "user", "hi", false)

	ctx := mcp.WithCallerProfile(context.Background(), "agent-A")
	res, err := st.CallTool(ctx, "chat_get", map[string]any{"session_id": "sess-uuid"})
	if err != nil || res.IsError {
		t.Fatalf("chat_get by session_id failed: %v / %+v", err, res)
	}
	if !strings.Contains(res.Content[0].Text, `"session_id":"sess-uuid"`) {
		t.Errorf("expected session_id in payload, got: %s", res.Content[0].Text)
	}
}

// TestChatGet_UnknownShortCode: friendly error, not a stack trace.
func TestChatGet_UnknownShortCode(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	ctx := mcp.WithCallerProfile(context.Background(), "agent-A")
	res, _ := st.CallTool(ctx, "chat_get", map[string]any{"target": "c9999"})
	if !res.IsError {
		t.Fatal("expected error for unknown short code")
	}
	if !strings.Contains(res.Content[0].Text, "no chat found") {
		t.Errorf("expected friendly error, got: %s", res.Content[0].Text)
	}
}

// TestChatGet_MissingTarget: required-arg validation.
func TestChatGet_MissingTarget(t *testing.T) {
	st := newSelfTools(t)
	res, _ := st.CallTool(context.Background(), "chat_get", map[string]any{})
	if !res.IsError {
		t.Fatal("expected error for missing target")
	}
}

// TestChatGet_Pagination: limit + offset works against a small page.
func TestChatGet_Pagination(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	code := seedSessionByID(t, st, "sess-page")
	for i := 0; i < 5; i++ {
		seedMessage(t, s, "sess-page", "user", "msg", false)
	}

	ctx := mcp.WithCallerProfile(context.Background(), "agent-A")
	res, err := st.CallTool(ctx, "chat_get", map[string]any{
		"target": code,
		"limit":  2,
		"offset": 2,
	})
	if err != nil || res.IsError {
		t.Fatalf("chat_get paginated failed: %v / %+v", err, res)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &payload)
	msgs, _ := payload["messages"].([]any)
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}
	if total, _ := payload["total"].(float64); int(total) != 5 {
		t.Errorf("total: got %v want 5", payload["total"])
	}
	if hasMore, _ := payload["has_more"].(bool); !hasMore {
		t.Errorf("expected has_more=true at offset=2 of 5")
	}
}

// TestChatGet_IncludeCompactedFalse: compacted blobs filtered when requested.
func TestChatGet_IncludeCompactedFalse(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	code := seedSessionByID(t, st, "sess-comp")
	seedMessage(t, s, "sess-comp", "user", "active one", false)
	seedMessage(t, s, "sess-comp", "assistant", "summary blob", true)

	ctx := mcp.WithCallerProfile(context.Background(), "agent-A")
	res, err := st.CallTool(ctx, "chat_get", map[string]any{
		"target":            code,
		"include_compacted": false,
	})
	if err != nil || res.IsError {
		t.Fatalf("chat_get failed: %v / %+v", err, res)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &payload)
	msgs, _ := payload["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 (active only), got %d", len(msgs))
	}
}

// TestChatSearch_CrossSessionByShortCode: search a sibling chat by short code.
func TestChatSearch_CrossSessionByShortCode(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	// Caller's session (current).
	seedSession(t, s, "sess-caller")
	// Target sibling session.
	targetCode := seedSessionByID(t, st, "sess-target")
	seedMessage(t, s, "sess-target", "user", "the artifact id is artifact-42", false)

	ctx := mcp.WithSessionID(mcp.WithCallerProfile(context.Background(), "agent-A"), "sess-caller")
	res, err := st.CallTool(ctx, "chat_search", map[string]any{
		"query":  "artifact-42",
		"target": targetCode,
	})
	if err != nil || res.IsError {
		t.Fatalf("chat_search cross-session failed: %v / %+v", err, res)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &payload)
	if cs, _ := payload["cross_session"].(bool); !cs {
		t.Errorf("expected cross_session=true, got payload: %s", res.Content[0].Text)
	}
	if payload["short_code"] != targetCode {
		t.Errorf("payload short_code: got %v want %s", payload["short_code"], targetCode)
	}
	snippets, _ := payload["snippets"].([]any)
	if len(snippets) == 0 {
		t.Fatal("expected at least one snippet from sibling chat")
	}
	first, _ := snippets[0].(map[string]any)
	if first["session_id"] != "sess-target" {
		t.Errorf("snippet should carry target session_id, got: %+v", first)
	}
	if first["short_code"] != targetCode {
		t.Errorf("snippet should carry target short_code, got: %+v", first)
	}
}

// TestChatSearch_TargetSameSession_NoCrossFlag: passing a target that is the
// same as the current session does NOT set cross_session=true (back-compat:
// the snippet shape stays minimal).
func TestChatSearch_TargetSameSession_NoCrossFlag(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	code := seedSessionByID(t, st, "sess-self")
	seedMessage(t, s, "sess-self", "user", "needle in haystack", false)

	ctx := mcp.WithSessionID(mcp.WithCallerProfile(context.Background(), "agent-A"), "sess-self")
	res, err := st.CallTool(ctx, "chat_search", map[string]any{
		"query":  "needle",
		"target": code,
	})
	if err != nil || res.IsError {
		t.Fatalf("chat_search failed: %v / %+v", err, res)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &payload)
	if cs, ok := payload["cross_session"]; ok && cs.(bool) {
		t.Errorf("cross_session should not be set when target == current session, got: %s", res.Content[0].Text)
	}
}

// TestIsShortCode covers the small classifier directly so its edge cases stay
// pinned (UUIDs containing 'c' must NOT match; pure 'c' alone must not match).
func TestIsShortCode(t *testing.T) {
	cases := map[string]bool{
		"c1":                                   true,
		"c248":                                 true,
		"c0":                                   true,
		"":                                     false,
		"c":                                    false,
		"C248":                                 false, // caller normalises to lowercase
		"#c248":                                false, // caller strips '#'
		"abc":                                  false,
		"d248":                                 false,
		"c248x":                                false,
		"c248-suffix":                          false,
		"01926a0e-c248-7000-8000-abcdefabcdef": false,
	}
	for input, want := range cases {
		if got := isShortCode(input); got != want {
			t.Errorf("isShortCode(%q) = %v, want %v", input, got, want)
		}
	}
}

// TestChatGetToolDefinition_RequiredSections verifies the new tool ships with
// the bucket-2 description sections so future audits don't flag it.
func TestChatGetToolDefinition_RequiredSections(t *testing.T) {
	defs := selfToolDefinitions()
	for _, d := range defs {
		if d.Name != "chat_get" {
			continue
		}
		for _, want := range []string{"When to use", "When NOT to use", "Output shape", "Scope boundary"} {
			if !strings.Contains(d.Description, want) {
				t.Errorf("chat_get description missing %q section", want)
			}
		}
		return
	}
	t.Fatal("chat_get not present in selfToolDefinitions()")
}

// TestChatSearchToolDefinition_TargetSchema verifies the cross-session
// extension surfaces in the chat_search schema.
func TestChatSearchToolDefinition_TargetSchema(t *testing.T) {
	defs := selfToolDefinitions()
	for _, d := range defs {
		if d.Name != "chat_search" {
			continue
		}
		props, _ := d.InputSchema["properties"].(map[string]any)
		if _, ok := props["target"]; !ok {
			t.Errorf("chat_search schema missing `target` property (CW-20260519-0063)")
		}
		if !strings.Contains(d.Description, "short code") || !strings.Contains(d.Description, "cross_session") {
			t.Errorf("chat_search description should mention short code and cross_session flag")
		}
		return
	}
	t.Fatal("chat_search not present in selfToolDefinitions()")
}
