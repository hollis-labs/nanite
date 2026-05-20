package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// seedWorkspaceID inserts a workspace row with a chosen ID. Distinct from
// seedWorkspaceOnce (which creates the shared `ws-test` row) — we need
// multiple workspaces for the cross-workspace deny test.
func seedWorkspaceID(t *testing.T, st *SelfToolsTransport, id string) {
	t.Helper()
	_, err := st.Store.DB.Exec(
		`INSERT OR IGNORE INTO workspaces (id, name, description, icon, sort_order, settings, created_at, updated_at)
		 VALUES (?, ?, '', '', 0, '{}', datetime('now'), datetime('now'))`,
		id, "ws "+id,
	)
	if err != nil {
		t.Fatalf("seed workspace %s: %v", id, err)
	}
}

// seedSessionInWS creates a session in a specific workspace and returns the
// short_code the store assigned.
func seedSessionInWS(t *testing.T, st *SelfToolsTransport, id, wsID string) string {
	t.Helper()
	seedWorkspaceID(t, st, wsID)
	// Insert via raw SQL so we control the workspace id; CreateSession's
	// helpers wrap nullIfEmpty around an empty string which would defeat the
	// scope check. We still need a short_code, so allocate one via the
	// store helper.
	code, err := st.Store.NextShortCode()
	if err != nil {
		t.Fatalf("next short code: %v", err)
	}
	_, err = st.Store.DB.Exec(
		`INSERT INTO sessions (id, short_code, workspace_id, title, status, metadata,
		                       last_activity, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'active', '{}', datetime('now'), datetime('now'), datetime('now'))`,
		id, code, wsID, "Session "+id,
	)
	if err != nil {
		t.Fatalf("seed session %s in ws %s: %v", id, wsID, err)
	}
	return code
}

// TestChatGet_BasicHappyPath: read a chat by short code from the same
// workspace; messages come back in chronological order with text unwrapped.
func TestChatGet_BasicHappyPath(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	code := seedSessionInWS(t, st, "sess-cg-1", "ws-test")
	seedMessage(t, s, "sess-cg-1", "user", "first message", false)
	seedMessage(t, s, "sess-cg-1", "assistant", `{"v":1,"text":"second wrapped","tier":"text"}`, false)

	ctx := WithCallerProfile(context.Background(), "ws-test", "agent-A")
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

	code := seedSessionInWS(t, st, "sess-norm", "ws-test")
	seedMessage(t, s, "sess-norm", "user", "hello", false)

	ctx := WithCallerProfile(context.Background(), "ws-test", "agent-A")
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

	_ = seedSessionInWS(t, st, "sess-uuid", "ws-test")
	seedMessage(t, s, "sess-uuid", "user", "hi", false)

	ctx := WithCallerProfile(context.Background(), "ws-test", "agent-A")
	res, err := st.CallTool(ctx, "chat_get", map[string]any{"session_id": "sess-uuid"})
	if err != nil || res.IsError {
		t.Fatalf("chat_get by session_id failed: %v / %+v", err, res)
	}
	if !strings.Contains(res.Content[0].Text, `"session_id":"sess-uuid"`) {
		t.Errorf("expected session_id in payload, got: %s", res.Content[0].Text)
	}
}

// TestChatGet_CrossWorkspaceDenied: caller in ws-A cannot read a chat in ws-B.
func TestChatGet_CrossWorkspaceDenied(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	codeB := seedSessionInWS(t, st, "sess-ws-b", "ws-other")
	seedMessage(t, s, "sess-ws-b", "user", "secret", false)

	ctx := WithCallerProfile(context.Background(), "ws-test", "agent-A")
	seedWorkspaceID(t, st, "ws-test")

	res, err := st.CallTool(ctx, "chat_get", map[string]any{"target": codeB})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("expected cross-workspace deny, got success: %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "cross-workspace") {
		t.Errorf("expected cross-workspace error message, got: %s", res.Content[0].Text)
	}
}

// TestChatGet_UnknownShortCode: friendly error, not a stack trace.
func TestChatGet_UnknownShortCode(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	ctx := WithCallerProfile(context.Background(), "ws-test", "agent-A")
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

	code := seedSessionInWS(t, st, "sess-page", "ws-test")
	for i := 0; i < 5; i++ {
		seedMessage(t, s, "sess-page", "user", "msg", false)
	}

	ctx := WithCallerProfile(context.Background(), "ws-test", "agent-A")
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

	code := seedSessionInWS(t, st, "sess-comp", "ws-test")
	seedMessage(t, s, "sess-comp", "user", "active one", false)
	seedMessage(t, s, "sess-comp", "assistant", "summary blob", true)

	ctx := WithCallerProfile(context.Background(), "ws-test", "agent-A")
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

// TestChatGet_NoCallerWorkspace_AllowsRead: when ctx has no caller profile,
// the gate is skipped (matches the H1 mux trust gate's empty-workspace
// fallback). This is the test-path / CLI-launch self-tools case.
func TestChatGet_NoCallerWorkspace_AllowsRead(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	code := seedSessionInWS(t, st, "sess-no-caller", "ws-other")
	seedMessage(t, s, "sess-no-caller", "user", "hi", false)

	// No WithCallerProfile on ctx → gate skipped.
	res, err := st.CallTool(context.Background(), "chat_get", map[string]any{"target": code})
	if err != nil || res.IsError {
		t.Fatalf("expected allow when caller workspace not stamped, got: %v / %+v", err, res)
	}
}

// TestChatSearch_CrossSessionByShortCode: search a sibling chat by short code.
func TestChatSearch_CrossSessionByShortCode(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	// Caller's session (current).
	seedSession(t, s, "sess-caller") // uses ws-test
	// Target sibling session.
	targetCode := seedSessionInWS(t, st, "sess-target", "ws-test")
	seedMessage(t, s, "sess-target", "user", "the artifact id is artifact-42", false)

	ctx := WithSessionID(WithCallerProfile(context.Background(), "ws-test", "agent-A"), "sess-caller")
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

// TestChatSearch_CrossWorkspaceDenied: cross-workspace search denied.
func TestChatSearch_CrossWorkspaceDenied(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	seedSession(t, s, "sess-caller-ws")
	codeB := seedSessionInWS(t, st, "sess-other-ws", "ws-elsewhere")
	seedMessage(t, s, "sess-other-ws", "user", "sensitive bits", false)

	ctx := WithSessionID(WithCallerProfile(context.Background(), "ws-test", "agent-A"), "sess-caller-ws")
	res, _ := st.CallTool(ctx, "chat_search", map[string]any{
		"query":  "sensitive",
		"target": codeB,
	})
	if !res.IsError {
		t.Fatalf("expected cross-workspace deny, got: %s", res.Content[0].Text)
	}
}

// TestChatSearch_TargetSameSession_NoCrossFlag: passing a target that is the
// same as the current session does NOT set cross_session=true (back-compat:
// the snippet shape stays minimal).
func TestChatSearch_TargetSameSession_NoCrossFlag(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	code := seedSessionInWS(t, st, "sess-self", "ws-test")
	seedMessage(t, s, "sess-self", "user", "needle in haystack", false)

	ctx := WithSessionID(WithCallerProfile(context.Background(), "ws-test", "agent-A"), "sess-self")
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
		"c1":                                 true,
		"c248":                               true,
		"c0":                                 true,
		"":                                   false,
		"c":                                  false,
		"C248":                               false, // caller normalises to lowercase
		"#c248":                              false, // caller strips '#'
		"abc":                                false,
		"d248":                               false,
		"c248x":                              false,
		"c248-suffix":                        false,
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
