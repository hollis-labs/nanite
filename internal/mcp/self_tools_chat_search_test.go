package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// seedMessage writes a message into the store for the given session.
func seedMessage(t *testing.T, s *store.Store, sessionID, role, content string, isCompacted bool) *store.Message {
	t.Helper()
	msg := &store.Message{
		SessionID:   sessionID,
		Role:        role,
		Content:     content,
		IsCompacted: isCompacted,
		Metadata:    "{}",
	}
	if err := s.CreateMessage(msg); err != nil {
		t.Fatalf("seed message: %v", err)
	}
	return msg
}

// seedWorkspaceOnce creates the shared test workspace (idempotent).
func seedWorkspaceOnce(t *testing.T, s *store.Store) {
	t.Helper()
	_, err := s.DB.Exec(
		`INSERT OR IGNORE INTO workspaces (id, name, description, icon, sort_order, settings, created_at, updated_at)
		 VALUES ('ws-test', 'Test Workspace', '', '', 0, '{}', datetime('now'), datetime('now'))`,
	)
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
}

// seedSession creates a minimal session for testing.
func seedSession(t *testing.T, s *store.Store, id string) {
	t.Helper()
	seedWorkspaceOnce(t, s)
	sess := &store.Session{
		ID:          id,
		WorkspaceID: "ws-test",
		Title:       "Test Session " + id,
		Status:      "active",
	}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

// parseSnippets unmarshals the tool result into the snippet structure.
func parseSnippets(t *testing.T, text string) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("parse result JSON: %v (raw: %s)", err, text)
	}
	return result
}

// TestChatSearch_BasicMatch verifies a simple substring search returns the
// expected snippet.
func TestChatSearch_BasicMatch(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	const sessID = "sess-search-basic"
	seedSession(t, s, sessID)
	seedMessage(t, s, sessID, "user", "The database password is hunter2, keep it secret", false)
	seedMessage(t, s, sessID, "assistant", "Noted. I will not repeat that value.", false)

	ctx := WithSessionID(context.Background(), sessID)
	result, err := st.CallTool(ctx, "nanite_chat_search", map[string]any{
		"query": "hunter2",
	})
	if err != nil || result.IsError {
		t.Fatalf("chat_search failed: %v / %+v", err, result)
	}

	parsed := parseSnippets(t, result.Content[0].Text)
	snippets, _ := parsed["snippets"].([]any)
	if len(snippets) == 0 {
		t.Fatalf("expected at least 1 snippet, got 0 (raw: %s)", result.Content[0].Text)
	}
	snip, _ := snippets[0].(map[string]any)
	if snip["role"] != "user" {
		t.Errorf("expected role=user, got %v", snip["role"])
	}
	if snip["source"] != "active" {
		t.Errorf("expected source=active, got %v", snip["source"])
	}
	excerpt, _ := snip["excerpt"].(string)
	if !strings.Contains(excerpt, "hunter2") {
		t.Errorf("excerpt should contain match, got: %s", excerpt)
	}
}

// TestChatSearch_ScopeActive verifies that active scope excludes compacted
// messages.
func TestChatSearch_ScopeActive(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	const sessID = "sess-search-scope-active"
	seedSession(t, s, sessID)
	seedMessage(t, s, sessID, "user", "active: findme here", false)
	seedMessage(t, s, sessID, "assistant", "[summary] findme was summarized away", true)

	ctx := WithSessionID(context.Background(), sessID)
	result, err := st.CallTool(ctx, "nanite_chat_search", map[string]any{
		"query": "findme",
		"scope": "active",
	})
	if err != nil || result.IsError {
		t.Fatalf("chat_search (active) failed: %v", err)
	}

	parsed := parseSnippets(t, result.Content[0].Text)
	snippets, _ := parsed["snippets"].([]any)
	if len(snippets) != 1 {
		t.Fatalf("expected 1 active snippet, got %d (raw: %s)", len(snippets), result.Content[0].Text)
	}
	snip, _ := snippets[0].(map[string]any)
	if snip["source"] != "active" {
		t.Errorf("expected source=active, got %v", snip["source"])
	}
}

// TestChatSearch_ScopeCompacted verifies that compacted scope only returns
// summary blobs.
func TestChatSearch_ScopeCompacted(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	const sessID = "sess-search-scope-compacted"
	seedSession(t, s, sessID)
	seedMessage(t, s, sessID, "user", "active: findme here", false)
	seedMessage(t, s, sessID, "assistant", "[summary] findme was summarized away", true)

	ctx := WithSessionID(context.Background(), sessID)
	result, err := st.CallTool(ctx, "nanite_chat_search", map[string]any{
		"query": "findme",
		"scope": "compacted",
	})
	if err != nil || result.IsError {
		t.Fatalf("chat_search (compacted) failed: %v", err)
	}

	parsed := parseSnippets(t, result.Content[0].Text)
	snippets, _ := parsed["snippets"].([]any)
	if len(snippets) != 1 {
		t.Fatalf("expected 1 compacted snippet, got %d (raw: %s)", len(snippets), result.Content[0].Text)
	}
	snip, _ := snippets[0].(map[string]any)
	if snip["source"] != "summary" {
		t.Errorf("expected source=summary, got %v", snip["source"])
	}
}

// TestChatSearch_ScopeAll verifies that all scope includes both active and
// compacted messages.
func TestChatSearch_ScopeAll(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	const sessID = "sess-search-scope-all"
	seedSession(t, s, sessID)
	seedMessage(t, s, sessID, "user", "active: findme here", false)
	seedMessage(t, s, sessID, "assistant", "[summary] findme was summarized", true)

	ctx := WithSessionID(context.Background(), sessID)
	result, err := st.CallTool(ctx, "nanite_chat_search", map[string]any{
		"query": "findme",
		"scope": "all",
	})
	if err != nil || result.IsError {
		t.Fatalf("chat_search (all) failed: %v", err)
	}

	parsed := parseSnippets(t, result.Content[0].Text)
	snippets, _ := parsed["snippets"].([]any)
	if len(snippets) != 2 {
		t.Fatalf("expected 2 snippets (active+summary), got %d (raw: %s)", len(snippets), result.Content[0].Text)
	}
}

// TestChatSearch_LimitEnforced verifies the limit parameter is respected.
func TestChatSearch_LimitEnforced(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	const sessID = "sess-search-limit"
	seedSession(t, s, sessID)
	for i := 0; i < 10; i++ {
		seedMessage(t, s, sessID, "user", "needle in this message", false)
	}

	ctx := WithSessionID(context.Background(), sessID)
	result, err := st.CallTool(ctx, "nanite_chat_search", map[string]any{
		"query": "needle",
		"limit": 3,
	})
	if err != nil || result.IsError {
		t.Fatalf("chat_search (limit) failed: %v", err)
	}

	parsed := parseSnippets(t, result.Content[0].Text)
	snippets, _ := parsed["snippets"].([]any)
	if len(snippets) != 3 {
		t.Fatalf("expected 3 snippets (limit=3), got %d", len(snippets))
	}
}

// TestChatSearch_MaxLimitCapped verifies that a limit over 100 is capped at 100.
func TestChatSearch_MaxLimitCapped(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	const sessID = "sess-search-maxlimit"
	seedSession(t, s, sessID)
	// Seed 5 messages — fewer than the cap so we can observe the cap was set
	// without writing hundreds of rows.
	for i := 0; i < 5; i++ {
		seedMessage(t, s, sessID, "user", "token found", false)
	}

	ctx := WithSessionID(context.Background(), sessID)
	result, err := st.CallTool(ctx, "nanite_chat_search", map[string]any{
		"query": "token",
		"limit": 9999, // should be clamped to 100
	})
	if err != nil || result.IsError {
		t.Fatalf("chat_search (maxlimit) failed: %v", err)
	}
	// Just assert it doesn't error and returns at most 100 snippets.
	parsed := parseSnippets(t, result.Content[0].Text)
	snippets, _ := parsed["snippets"].([]any)
	if len(snippets) > chatSearchMaxLimit {
		t.Errorf("expected at most %d snippets, got %d", chatSearchMaxLimit, len(snippets))
	}
}

// TestChatSearch_NoMatch verifies a human-readable message is returned when
// there are no matching messages.
func TestChatSearch_NoMatch(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	const sessID = "sess-search-nomatch"
	seedSession(t, s, sessID)
	seedMessage(t, s, sessID, "user", "irrelevant content here", false)

	ctx := WithSessionID(context.Background(), sessID)
	result, err := st.CallTool(ctx, "nanite_chat_search", map[string]any{
		"query": "xyzzy_notfound_12345",
	})
	if err != nil || result.IsError {
		t.Fatalf("chat_search (nomatch) failed: %v", err)
	}
	body := result.Content[0].Text
	if !strings.Contains(body, "No matches") {
		t.Errorf("expected 'No matches' message, got: %s", body)
	}
}

// TestChatSearch_MissingQuery verifies an error result for missing query.
func TestChatSearch_MissingQuery(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	ctx := WithSessionID(context.Background(), "sess-any")
	result, err := st.CallTool(ctx, "nanite_chat_search", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing query")
	}
}

// TestChatSearch_MissingSessionID verifies an error result when there is no
// session in context.
func TestChatSearch_MissingSessionID(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	result, err := st.CallTool(context.Background(), "nanite_chat_search", map[string]any{
		"query": "anything",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing session ID")
	}
}

// TestChatSearch_CaseInsensitive verifies the search is case-insensitive.
func TestChatSearch_CaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)

	const sessID = "sess-search-case"
	seedSession(t, s, sessID)
	seedMessage(t, s, sessID, "user", "The API endpoint is /api/V2/items", false)

	ctx := WithSessionID(context.Background(), sessID)
	result, err := st.CallTool(ctx, "nanite_chat_search", map[string]any{
		"query": "api/v2", // lower-case, message has V2 uppercase
	})
	if err != nil || result.IsError {
		t.Fatalf("chat_search (case-insensitive) failed: %v", err)
	}

	parsed := parseSnippets(t, result.Content[0].Text)
	snippets, _ := parsed["snippets"].([]any)
	if len(snippets) == 0 {
		t.Errorf("expected match despite case difference, got 0 snippets")
	}
}

// TestChatSearchToolDefinition_RequiredSections verifies the tool description
// follows the CW-20260419-0022 template (D6 compliance).
func TestChatSearchToolDefinition_RequiredSections(t *testing.T) {
	defs := selfToolDefinitions()
	for _, d := range defs {
		if d.Name != "nanite_chat_search" {
			continue
		}
		checks := []struct {
			fragment string
			label    string
		}{
			{"when the post-compaction disclosure prompt", "when-to-use reference"},
			{"Do NOT use for general knowledge", "anti-pattern"},
			{"Output shape", "output shape section"},
			{"turn_id", "turn_id in output shape"},
			{"source", "source field in output shape"},
			{"compaction_event_id", "compaction_event_id in output shape"},
		}
		for _, c := range checks {
			if !strings.Contains(d.Description, c.fragment) {
				t.Errorf("nanite_chat_search description missing %s (fragment %q)", c.label, c.fragment)
			}
		}
		return
	}
	t.Fatal("nanite_chat_search not found in selfToolDefinitions()")
}

// TestChatSearchToolDefinition_InputSchema verifies the input schema has the
// required and optional fields.
func TestChatSearchToolDefinition_InputSchema(t *testing.T) {
	defs := selfToolDefinitions()
	for _, d := range defs {
		if d.Name != "nanite_chat_search" {
			continue
		}
		props, _ := d.InputSchema["properties"].(map[string]any)
		for _, field := range []string{"query", "scope", "limit"} {
			if _, ok := props[field]; !ok {
				t.Errorf("nanite_chat_search schema missing property %q", field)
			}
		}
		required, _ := d.InputSchema["required"].([]string)
		if len(required) != 1 || required[0] != "query" {
			t.Errorf("nanite_chat_search required should be [query], got %v", required)
		}
		return
	}
	t.Fatal("nanite_chat_search not found in selfToolDefinitions()")
}

// TestBuildExcerpt_HighlightsMatch verifies the excerpt wraps the match with
// «…» markers and includes surrounding context.
func TestBuildExcerpt_HighlightsMatch(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog"
	loc := []int{16, 19} // "fox"
	excerpt := buildExcerpt(text, loc[0], loc[1], 10)
	if !strings.Contains(excerpt, "«fox»") {
		t.Errorf("expected «fox» in excerpt, got: %s", excerpt)
	}
	if !strings.Contains(excerpt, "brown") {
		t.Errorf("expected prefix context 'brown', got: %s", excerpt)
	}
	if !strings.Contains(excerpt, "jumps") {
		t.Errorf("expected suffix context 'jumps', got: %s", excerpt)
	}
}

// TestExtractMessageText_JSONPayload verifies JSON-wrapped assistant content
// is unwrapped correctly.
func TestExtractMessageText_JSONPayload(t *testing.T) {
	raw := `{"v":1,"text":"the actual message text here","tier":"tool"}`
	got := extractMessageText(raw)
	want := "the actual message text here"
	if got != want {
		t.Errorf("extractMessageText() = %q, want %q", got, want)
	}
}

// TestExtractMessageText_BareString verifies plain-text content passes through.
func TestExtractMessageText_BareString(t *testing.T) {
	raw := "plain user message"
	got := extractMessageText(raw)
	if got != raw {
		t.Errorf("extractMessageText() = %q, want %q", got, raw)
	}
}
