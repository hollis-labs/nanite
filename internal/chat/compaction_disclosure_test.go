package chat

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// newTestStoreForChat opens a fresh in-memory store with all migrations applied
// (including 030 which seeds the disclosure templates).
func newTestStoreForChat(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// writeCompactionEventForTest is a small helper so each test can fabricate an
// event row without copy-pasting the full struct.
func writeCompactionEventForTest(t *testing.T, s *store.Store, evt store.CompactionEvent) {
	t.Helper()
	if evt.ID == "" {
		evt.ID = "evt-" + evt.SessionID + "-" + evt.SummaryMode
	}
	if evt.CreatedAt == "" {
		evt.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if evt.SummaryMode == "" {
		evt.SummaryMode = "general"
	}
	if evt.EvictedCachePointers == nil {
		evt.EvictedCachePointers = []string{}
	}
	if evt.PreservedSources == nil {
		evt.PreservedSources = []string{}
	}
	if evt.StagesApplied == nil {
		evt.StagesApplied = []string{}
	}
	if err := s.WriteCompactionEvent(context.Background(), evt); err != nil {
		t.Fatalf("WriteCompactionEvent: %v", err)
	}
}

// TestRenderCompactionDisclosure_freshEventInjects asserts a fresh compaction
// event produces a non-empty disclosure containing the expected affordances
// and interpolated metadata.
func TestRenderCompactionDisclosure_freshEventInjects(t *testing.T) {
	s := newTestStoreForChat(t)

	if err := s.CreateWorkspace(&store.Workspace{ID: "ws", Name: "WS"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	stash := "stash-abc"
	startTurn := "msg-001"
	endTurn := "msg-042"
	writeCompactionEventForTest(t, s, store.CompactionEvent{
		SessionID:            sess.ID,
		SummaryMode:          "code",
		SummaryTokenCount:    321,
		HandoffStashID:       &stash,
		CoverageWindowStart:  &startTurn,
		CoverageWindowEnd:    &endTurn,
		EvictedCachePointers: []string{"ptr1", "ptr2"},
		PreservedSources:     []string{"src1"},
	})

	got := renderCompactionDisclosure(s, sess.ID)
	if got == "" {
		t.Fatal("expected non-empty disclosure for fresh compaction event")
	}
	// Mode-anchored: code variant should mention "code session".
	if !strings.Contains(got, "code session") {
		t.Errorf("expected code-mode disclosure, got: %q", got)
	}
	// Variables fully interpolated (no leftover {{...}}).
	if strings.Contains(got, "{{") {
		t.Errorf("expected all variables interpolated, got leftover {{ in: %q", got)
	}
	// Spot-check interpolated values.
	for _, want := range []string{stash, startTurn, endTurn, "321", "chat_search"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected disclosure to contain %q, missing in: %q", want, got)
		}
	}
}

// TestRenderCompactionDisclosure_modeAllVariants asserts each CompactionMode
// resolves to its own template variant. Bumps coverage on
// disclosureSlugForMode.
func TestRenderCompactionDisclosure_modeAllVariants(t *testing.T) {
	s := newTestStoreForChat(t)

	if err := s.CreateWorkspace(&store.Workspace{ID: "ws", Name: "WS"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	cases := []struct {
		mode      string
		wantSlug  string
		wantTitle string
	}{
		{"general", "compaction-disclosure-general", "Compaction Notice"},
		{"code", "compaction-disclosure-code", "code session"},
		{"plan", "compaction-disclosure-plan", "planning session"},
		{"research", "compaction-disclosure-research", "research session"},
		// Unknown modes fall back to general.
		{"unknown-mode-xyz", "compaction-disclosure-general", "Compaction Notice"},
	}

	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			sess := &store.Session{WorkspaceID: "ws"}
			if err := s.CreateSession(sess); err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			writeCompactionEventForTest(t, s, store.CompactionEvent{
				SessionID:   sess.ID,
				SummaryMode: tc.mode,
			})

			got := renderCompactionDisclosure(s, sess.ID)
			if !strings.Contains(got, tc.wantTitle) {
				t.Errorf("mode=%q: expected title fragment %q, got: %q",
					tc.mode, tc.wantTitle, got)
			}
		})
	}
}

// TestRenderCompactionDisclosure_noEventReturnsEmpty asserts a session with
// no compaction events produces no disclosure.
func TestRenderCompactionDisclosure_noEventReturnsEmpty(t *testing.T) {
	s := newTestStoreForChat(t)
	if err := s.CreateWorkspace(&store.Workspace{ID: "ws", Name: "WS"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if got := renderCompactionDisclosure(s, sess.ID); got != "" {
		t.Errorf("expected empty disclosure for session with no events, got: %q", got)
	}
}

// TestRenderCompactionDisclosure_staleEventReturnsEmpty asserts that once an
// assistant message lands AFTER the compaction event, the event is no longer
// fresh and the disclosure stops injecting.
//
// This exercises the freshness invariant: event_created_at > latest_assistant_created_at.
func TestRenderCompactionDisclosure_staleEventReturnsEmpty(t *testing.T) {
	s := newTestStoreForChat(t)
	if err := s.CreateWorkspace(&store.Workspace{ID: "ws", Name: "WS"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Compaction event at t0.
	t0 := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
	writeCompactionEventForTest(t, s, store.CompactionEvent{
		SessionID:   sess.ID,
		SummaryMode: "general",
		CreatedAt:   t0,
	})

	// Fresh: still no assistant turn.
	if got := renderCompactionDisclosure(s, sess.ID); got == "" {
		t.Fatal("expected fresh disclosure before any assistant message")
	}

	// Assistant turn at t1 > t0. Note: store.Message.CreatedAt is auto-set on
	// CreateMessage, so we don't pass it; the new row's timestamp is the
	// current wall clock, comfortably after t0.
	if err := s.CreateMessage(&store.Message{
		SessionID: sess.ID,
		Role:      "assistant",
		Content:   "ack post-compaction",
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Stale: assistant message timestamp now exceeds the event timestamp.
	if got := renderCompactionDisclosure(s, sess.ID); got != "" {
		t.Errorf("expected empty disclosure after assistant turn, got: %q", got)
	}
}

// TestRenderCompactionDisclosure_userMessageDoesNotStaleIt asserts that a USER
// message after compaction does NOT count as acknowledgment — only assistant
// messages do. This locks in the "model has acted on the disclosure" semantic.
func TestRenderCompactionDisclosure_userMessageDoesNotStaleIt(t *testing.T) {
	s := newTestStoreForChat(t)
	if err := s.CreateWorkspace(&store.Workspace{ID: "ws", Name: "WS"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	t0 := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
	writeCompactionEventForTest(t, s, store.CompactionEvent{
		SessionID:   sess.ID,
		SummaryMode: "general",
		CreatedAt:   t0,
	})

	// User message after compaction — does not count as ack.
	if err := s.CreateMessage(&store.Message{
		SessionID: sess.ID,
		Role:      "user",
		Content:   "follow-up question",
	}); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	if got := renderCompactionDisclosure(s, sess.ID); got == "" {
		t.Error("expected disclosure to remain fresh after user message (no assistant ack)")
	}
}

// TestRenderCompactionDisclosure_nullableFieldsRenderPlaceholders asserts
// that an event with no stash id / no coverage window renders the literal
// "(none)" / "(unknown)" placeholders rather than empty strings or
// un-interpolated {{...}} markers.
func TestRenderCompactionDisclosure_nullableFieldsRenderPlaceholders(t *testing.T) {
	s := newTestStoreForChat(t)
	if err := s.CreateWorkspace(&store.Workspace{ID: "ws", Name: "WS"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	writeCompactionEventForTest(t, s, store.CompactionEvent{
		SessionID:   sess.ID,
		SummaryMode: "general",
		// HandoffStashID, CoverageWindowStart, CoverageWindowEnd left nil.
	})

	got := renderCompactionDisclosure(s, sess.ID)
	if !strings.Contains(got, "(none)") {
		t.Errorf("expected (none) placeholder for missing stash id, got: %q", got)
	}
	if !strings.Contains(got, "(unknown)") {
		t.Errorf("expected (unknown) placeholder for missing coverage window, got: %q", got)
	}
	if strings.Contains(got, "{{") {
		t.Errorf("expected no un-interpolated {{...}} markers, got: %q", got)
	}
}

// TestAssembleSystemPromptFromTemplates_appendsDisclosure is the end-to-end-ish
// check: build a system prompt for a session that has a fresh compaction event
// and assert the disclosure block appears in the assembled output.
func TestAssembleSystemPromptFromTemplates_appendsDisclosure(t *testing.T) {
	s := newTestStoreForChat(t)
	if err := s.CreateWorkspace(&store.Workspace{ID: "ws", Name: "WS"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{
		Name:         "TestAgent",
		Slug:         "test",
		SystemPrompt: "You are a test agent.",
	}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	mode := &store.AgentMode{PromptAddendum: ""}
	workspace := &store.Workspace{Name: "WS", Description: "Test"}

	stash := "stash-xyz"
	writeCompactionEventForTest(t, s, store.CompactionEvent{
		SessionID:      sess.ID,
		SummaryMode:    "plan",
		HandoffStashID: &stash,
	})

	// With sessionID — disclosure should appear.
	withDisc := assembleSystemPromptFromTemplates(s, agent, mode, workspace, "", sess.ID, nil)
	if !strings.Contains(withDisc, "Compaction Notice (planning session)") {
		t.Errorf("expected planning-mode disclosure in assembled prompt, got: %q", withDisc)
	}
	if !strings.Contains(withDisc, stash) {
		t.Errorf("expected stash id %q in disclosure, got: %q", stash, withDisc)
	}

	// Without sessionID — no disclosure.
	withoutDisc := assembleSystemPromptFromTemplates(s, agent, mode, workspace, "", "", nil)
	if strings.Contains(withoutDisc, "Compaction Notice") {
		t.Errorf("expected no disclosure when sessionID empty, got: %q", withoutDisc)
	}
}

// TestAssembleAgentSlotContent_appendsDisclosure mirrors the above for the
// slot-based assembly path.
func TestAssembleAgentSlotContent_appendsDisclosure(t *testing.T) {
	s := newTestStoreForChat(t)
	if err := s.CreateWorkspace(&store.Workspace{ID: "ws", Name: "WS"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{
		Name:         "Slot",
		Slug:         "slot",
		SystemPrompt: "Slot agent prompt.",
	}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	writeCompactionEventForTest(t, s, store.CompactionEvent{
		SessionID:   sess.ID,
		SummaryMode: "research",
	})

	got := assembleAgentSlotContent(s, agent, nil, "", sess.ID)
	if !strings.Contains(got, "research session") {
		t.Errorf("expected research-mode disclosure in agent slot, got: %q", got)
	}
}

// TestRenderedDisclosureUnderTokenBudget asserts the rendered (interpolated)
// disclosure for every mode stays under the 1100-char (~300 token) ceiling
// even with realistic-length sample values.
func TestRenderedDisclosureUnderTokenBudget(t *testing.T) {
	s := newTestStoreForChat(t)
	if err := s.CreateWorkspace(&store.Workspace{ID: "ws", Name: "WS"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	stash := "01HJ8N7XK5R8M3Y6PZQWA9V2BC"          // ULID-shaped, 26 chars
	start := "turn-msg-01HJ8N7XK5R8M3Y6PZQWA9V2BC" // 33 chars
	end := "turn-msg-01HJ8N7XK5R8M3Y6PZQWA9V2BC"   // 33 chars

	for _, mode := range []string{"general", "code", "plan", "research"} {
		t.Run(mode, func(t *testing.T) {
			sess := &store.Session{WorkspaceID: "ws"}
			if err := s.CreateSession(sess); err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			writeCompactionEventForTest(t, s, store.CompactionEvent{
				SessionID:            sess.ID,
				SummaryMode:          mode,
				SummaryTokenCount:    1234,
				HandoffStashID:       &stash,
				CoverageWindowStart:  &start,
				CoverageWindowEnd:    &end,
				EvictedCachePointers: []string{"a", "b", "c", "d", "e", "f"},
				PreservedSources:     []string{"x", "y", "z", "w"},
			})

			rendered := renderCompactionDisclosure(s, sess.ID)
			if rendered == "" {
				t.Fatalf("mode=%q produced empty disclosure", mode)
			}
			const ceiling = 1100
			if got := len(rendered); got > ceiling {
				t.Errorf("mode=%q rendered disclosure exceeds char ceiling: %d > %d (~300 tokens)",
					mode, got, ceiling)
			}
		})
	}
}
