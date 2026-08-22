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
	t.Cleanup(func() {
		s.Close(context.Background(

		// mustCreateAgent, mustCreateSkill, and mustAssignSkill were originally
		// defined in skill_list_mode_test.go, deleted by Phase 0 item 21 ("Cut
		// Modes, in full") along with the mode-filter tests it covered — moved here
		// since skill_list_loadhint_test.go and skill_broker_wire_test.go still use
		// them independently of mode filtering.
		))
	})
	return s
}

func mustCreateAgent(t *testing.T, s *store.Store, slug string) *store.AgentProfile {
	t.Helper()
	a := &store.AgentProfile{
		Slug:        slug,
		Name:        slug,
		Description: "test agent",
		Source:      "user",
	}
	if err := s.CreateAgent(context.Background(), a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return a
}

func mustCreateSkill(t *testing.T, s *store.Store, sk *store.Skill) *store.Skill {
	t.Helper()
	if err := s.CreateSkill(context.Background(), sk); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	return sk
}

func mustAssignSkill(t *testing.T, s *store.Store, agentID, skillID string) {
	t.Helper()
	if err := s.AssignSkillToAgent(context.Background(), agentID, skillID, ""); err != nil {
		t.Fatalf("AssignSkillToAgent: %v", err)
	}
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

	sess := &store.Session{}
	if err := s.CreateSession(context.Background(), sess); err != nil {
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
	// Unified message: no mode-branching, always the same title.
	if !strings.Contains(got, "## Compaction Notice") {
		t.Errorf("expected unified disclosure title, got: %q", got)
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

// TestRenderCompactionDisclosure_modeIsIrrelevant asserts that
// SummaryMode no longer selects a template variant (Phase 0 item 29
// collapsed the four mode-branched disclosure templates into one hardcoded
// message) — every mode string, including unknown ones, produces the same
// unified disclosure body.
func TestRenderCompactionDisclosure_modeIsIrrelevant(t *testing.T) {
	s := newTestStoreForChat(t)

	modes := []string{"general", "code", "plan", "research", "unknown-mode-xyz", ""}
	var rendered []string
	for _, mode := range modes {
		sess := &store.Session{}
		if err := s.CreateSession(context.Background(), sess); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		writeCompactionEventForTest(t, s, store.CompactionEvent{
			SessionID:   sess.ID,
			SummaryMode: mode,
		})

		got := renderCompactionDisclosure(s, sess.ID)
		if !strings.Contains(got, "## Compaction Notice") {
			t.Errorf("mode=%q: expected unified disclosure title, got: %q", mode, got)
		}
		rendered = append(rendered, got)
	}

	for i := 1; i < len(rendered); i++ {
		if rendered[i] != rendered[0] {
			t.Errorf("expected identical disclosure body across modes (mode-independent), mode[%d] differs:\n%q\nvs\n%q",
				i, rendered[i], rendered[0])
		}
	}
}

// TestRenderCompactionDisclosure_noEventReturnsEmpty asserts a session with
// no compaction events produces no disclosure.
func TestRenderCompactionDisclosure_noEventReturnsEmpty(t *testing.T) {
	s := newTestStoreForChat(t)
	sess := &store.Session{}
	if err := s.CreateSession(context.Background(), sess); err != nil {
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
	sess := &store.Session{}
	if err := s.CreateSession(context.Background(), sess); err != nil {
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
	if err := s.CreateMessage(context.Background(), &store.Message{
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
	sess := &store.Session{}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	t0 := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
	writeCompactionEventForTest(t, s, store.CompactionEvent{
		SessionID:   sess.ID,
		SummaryMode: "general",
		CreatedAt:   t0,
	})

	// User message after compaction — does not count as ack.
	if err := s.CreateMessage(context.Background(), &store.Message{
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
	sess := &store.Session{}
	if err := s.CreateSession(context.Background(), sess); err != nil {
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

// TestAssembleAgentSlotContent_appendsDisclosure asserts the disclosure block
// appears in the slot-based agent-slot assembly output for a session with a
// fresh compaction event. The legacy-path equivalent of this test
// (assembleSystemPromptFromTemplates) was removed along with that function
// when the legacy AssembleContext path was deleted (CW-20260814-0005).
func TestAssembleAgentSlotContent_appendsDisclosure(t *testing.T) {
	s := newTestStoreForChat(t)
	sess := &store.Session{}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{
		Name:         "Slot",
		Slug:         "slot",
		SystemPrompt: "Slot agent prompt.",
	}
	if err := s.CreateAgent(context.Background(), agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	writeCompactionEventForTest(t, s, store.CompactionEvent{
		SessionID:   sess.ID,
		SummaryMode: "research",
	})

	got := assembleAgentSlotContent(s, agent, "", sess.ID)
	if !strings.Contains(got, "## Compaction Notice") {
		t.Errorf("expected unified disclosure in agent slot, got: %q", got)
	}
}

// TestRenderedDisclosureUnderTokenBudget asserts the rendered (interpolated)
// disclosure for every mode stays under the 1100-char (~300 token) ceiling
// even with realistic-length sample values.
func TestRenderedDisclosureUnderTokenBudget(t *testing.T) {
	s := newTestStoreForChat(t)

	stash := "01HJ8N7XK5R8M3Y6PZQWA9V2BC"          // ULID-shaped, 26 chars
	start := "turn-msg-01HJ8N7XK5R8M3Y6PZQWA9V2BC" // 33 chars
	end := "turn-msg-01HJ8N7XK5R8M3Y6PZQWA9V2BC"   // 33 chars

	for _, mode := range []string{"general", "code", "plan", "research"} {
		t.Run(mode, func(t *testing.T) {
			sess := &store.Session{}
			if err := s.CreateSession(context.Background(), sess); err != nil {
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

// TestInterpolateDisclosure_syntheticEvent is a direct unit test against the
// hardcoded compactionDisclosureTemplate (Phase 0 item 29), exercising
// interpolateDisclosure with a synthetic *store.CompactionEvent rather than
// round-tripping through the store. Originally this was the only regression
// guard for the relocated content, since compaction_events had no production
// writer wired at the time (renderCompactionDisclosure couldn't be triggered
// end-to-end by a real compaction). The writer is now wired at all three
// production CompactionPipeline{} construction sites (Phase 3 item 02,
// TASKS/phase-3/02-wire-compaction-events.md) — see
// internal/service.TestCompactionEventWriter_WiredAtAllThreeSites_EndToEnd
// for the real end-to-end coverage (real store, real pipeline, real writer,
// real disclosure render on the next turn). This test is kept as a direct,
// synthetic-input unit test of interpolateDisclosure's template formatting.
func TestInterpolateDisclosure_syntheticEvent(t *testing.T) {
	stash := "handoff-xyz"
	start := "msg-010"
	end := "msg-099"
	evt := &store.CompactionEvent{
		ID:                   "evt-synthetic-1",
		SessionID:            "sess-synthetic-1",
		SummaryMode:          "general",
		SummaryTokenCount:    512,
		HandoffStashID:       &stash,
		CoverageWindowStart:  &start,
		CoverageWindowEnd:    &end,
		EvictedCachePointers: []string{"p1", "p2", "p3"},
		PreservedSources:     []string{"s1"},
	}

	got := interpolateDisclosure(evt)

	// Title + section headers (unified message, no mode branching).
	for _, want := range []string{
		"## Compaction Notice",
		"**Preserved:**",
		"**Lost:**",
		"**Recovery:**",
		"**Handoff stash id:**",
		"chat_search",
		"**Summary metadata:**",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected disclosure to contain %q, got: %q", want, got)
		}
	}

	// Interpolated values.
	for _, want := range []string{stash, start, end, "512", "3", "1"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected disclosure to contain interpolated value %q, got: %q", want, got)
		}
	}

	// No leftover placeholders.
	if strings.Contains(got, "{{") || strings.Contains(got, "%s") || strings.Contains(got, "%d") {
		t.Errorf("expected fully interpolated disclosure, got leftover placeholder in: %q", got)
	}
}

// TestInterpolateDisclosure_nilFieldsSynthetic asserts the nullable-field
// placeholder behavior directly against interpolateDisclosure (as opposed to
// TestRenderCompactionDisclosure_nullableFieldsRenderPlaceholders, which
// exercises the same behavior through the store round-trip).
func TestInterpolateDisclosure_nilFieldsSynthetic(t *testing.T) {
	evt := &store.CompactionEvent{
		ID:          "evt-synthetic-2",
		SessionID:   "sess-synthetic-2",
		SummaryMode: "general",
		// HandoffStashID, CoverageWindowStart, CoverageWindowEnd left nil.
	}

	got := interpolateDisclosure(evt)
	if !strings.Contains(got, "(none)") {
		t.Errorf("expected (none) placeholder for missing stash id, got: %q", got)
	}
	if !strings.Contains(got, "(unknown)") {
		t.Errorf("expected (unknown) placeholder for missing coverage window, got: %q", got)
	}
}
