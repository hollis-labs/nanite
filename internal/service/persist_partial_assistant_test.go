package service

// Tests for CW-20260419-0019: persistPartialAssistant helper that saves a
// partial assistant message row on early-return error paths in generateResponse.
//
// CW-20260512-0002 subtodo (d): the helper additionally SKIPS the placeholder
// write when the parent session has an active subagent_runs row (status='running'),
// because subagent-caused interrupts shouldn't surface as a `[generation interrupted]`
// row in the parent's chat history.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// capturingStore is a minimal Store stub that records the last CreateMessage call.
type capturingStore struct {
	minimalStore
	lastMsg   *store.Message
	callCount int

	// lookupCallCount tracks how many times ActiveSubagentRunForParent was
	// invoked. PR #138 review #2 asserts that pre-classified call sites
	// do NOT trigger a second lookup on the hot error path.
	lookupCallCount int

	// Optional override: when non-nil, ActiveSubagentRunForParent calls
	// this instead of the embedded no-op. Lets CW-20260512-0002 (d)
	// tests inject "subagent active" classifications.
	activeRunFn func(parentSessionID string) (id, role, child string, ok bool, err error)
}

func (c *capturingStore) CreateMessage(ctx context.Context, msg *store.Message) error {
	c.lastMsg = msg
	c.callCount++
	return nil
}

func (c *capturingStore) ActiveSubagentRunForParent(ctx context.Context, parentSessionID string) (id, role, child string, ok bool, err error) {
	c.lookupCallCount++
	if c.activeRunFn != nil {
		return c.activeRunFn(parentSessionID)
	}
	return c.minimalStore.ActiveSubagentRunForParent(context.Background(), parentSessionID)
}

// TestPersistPartialAssistant_StoresRow verifies that persistPartialAssistant
// calls CreateMessage exactly once with the expected content and metadata.
func TestPersistPartialAssistant_StoresRow(t *testing.T) {
	cs := &capturingStore{}
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistant("sess-1", "msg-1", "agent-1", "partial content from stream")

	if cs.callCount != 1 {
		t.Fatalf("expected CreateMessage called once, got %d", cs.callCount)
	}
	msg := cs.lastMsg
	if msg == nil {
		t.Fatal("lastMsg is nil")
	}
	if msg.SessionID != "sess-1" {
		t.Errorf("session_id: got %q, want %q", msg.SessionID, "sess-1")
	}
	if msg.ID != "msg-1" {
		t.Errorf("id: got %q, want %q", msg.ID, "msg-1")
	}
	if msg.AgentID != "agent-1" {
		t.Errorf("agent_id: got %q, want %q", msg.AgentID, "agent-1")
	}
	if msg.Role != "assistant" {
		t.Errorf("role: got %q, want %q", msg.Role, "assistant")
	}
	// Metadata must carry had_error=true so the FE rehydration path picks it up.
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Metadata), &meta); err != nil {
		t.Fatalf("metadata is not valid JSON: %q — %v", msg.Metadata, err)
	}
	if v, ok := meta["had_error"]; !ok || v != true {
		t.Errorf("expected metadata.had_error=true, got %v (full metadata: %q)", v, msg.Metadata)
	}
	// Content must be a valid structured message JSON (not empty).
	if msg.Content == "" {
		t.Error("content must not be empty")
	}
	var structured map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Content), &structured); err != nil {
		t.Errorf("content is not valid JSON: %q", msg.Content)
	}
}

// TestPersistPartialAssistant_EmptyContentPlaceholder verifies that when no
// content was streamed, the stored row uses the "[generation interrupted]"
// placeholder rather than an empty string — so the FE always has something to
// display on refresh.
func TestPersistPartialAssistant_EmptyContentPlaceholder(t *testing.T) {
	cs := &capturingStore{}
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistant("sess-2", "msg-2", "agent-2", "")

	if cs.callCount != 1 {
		t.Fatalf("expected CreateMessage called once, got %d", cs.callCount)
	}
	// Content should contain the placeholder text in the structured message.
	if cs.lastMsg == nil || cs.lastMsg.Content == "" {
		t.Fatal("expected non-empty content for placeholder row")
	}
	var structured map[string]interface{}
	if err := json.Unmarshal([]byte(cs.lastMsg.Content), &structured); err != nil {
		t.Fatalf("content is not valid JSON: %q", cs.lastMsg.Content)
	}
	text, _ := structured["text"].(string)
	if text != "[generation interrupted]" {
		t.Errorf("expected placeholder text %q, got %q", "[generation interrupted]", text)
	}
}

// TestF4Persistence_NarrationInMetadataThinking verifies the F4 persistence
// contract: when a message is created with narration in metadata.thinking and
// final text as content, both fields survive a round-trip through a real store
// (CW-20260419-0029).
func TestF4Persistence_NarrationInMetadataThinking(t *testing.T) {
	s, err := storetest.New(t, context.Background(), t.TempDir()+"/f4_persist.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	sess := &store.Session{ID: "sess-f4", Status: "active"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	narration := "Let me look at the file...\nLet me also check the other file..."
	finalText := "Here is the answer to your question."

	// Build metadata the same way generateResponse does.
	metaJSON, err := json.Marshal(map[string]string{"thinking": narration})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	msg := &store.Message{
		ID:        "msg-f4",
		SessionID: "sess-f4",
		AgentID:   "agent-f4",
		Role:      "assistant",
		Content:   finalText,
		Metadata:  string(metaJSON),
	}
	if err := s.CreateMessage(context.Background(), msg); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}

	// Read it back and verify both fields.
	got, err := s.GetMessage(context.Background(), "msg-f4")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Content != finalText {
		t.Errorf("content: got %q, want %q", got.Content, finalText)
	}

	var meta map[string]string
	if err := json.Unmarshal([]byte(got.Metadata), &meta); err != nil {
		t.Fatalf("metadata not valid JSON: %q — %v", got.Metadata, err)
	}
	if meta["thinking"] != narration {
		t.Errorf("metadata.thinking: got %q, want %q", meta["thinking"], narration)
	}
}

// TestPersistPartialAssistant_RealStore exercises the full path against a real
// SQLite store to assert the row is actually readable after the call.  This is
// the integration-level check that covers all early-return sites that flow
// through the helper (they all call the same helper, so one integration test
// suffices for the persistence guarantee).
func TestPersistPartialAssistant_RealStore(t *testing.T) {
	s, err := storetest.New(t, context.Background(), t.TempDir()+"/persist_partial.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() {
		s.Close(context.

			// Create a session so the FK constraint in CreateMessage succeeds.
			Background())
	})

	sess := &store.Session{ID: "test-session", Status: "active"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	svc := &chatServiceImpl{store: s}
	const msgID = "test-msg-partial"
	svc.persistPartialAssistant("test-session", msgID, "test-agent", "hello from error path")

	// Verify row exists and has had_error metadata.
	msg, err := s.GetMessage(context.Background(), msgID)
	if err != nil {
		t.Fatalf("GetMessage: %v — row was not persisted", err)
	}
	if msg.Role != "assistant" {
		t.Errorf("role: got %q, want %q", msg.Role, "assistant")
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Metadata), &meta); err != nil {
		t.Fatalf("metadata is not valid JSON: %q — %v", msg.Metadata, err)
	}
	if v, ok := meta["had_error"]; !ok || v != true {
		t.Errorf("expected metadata.had_error=true; got %v (metadata: %q)", v, msg.Metadata)
	}
}

// ============================================================================
// CW-20260512-0002 subtodo (d) tests — subagent-caused suppression.
// ============================================================================

// TestPersistPartialAssistant_SuppressedWhenSubagentActive verifies that the
// placeholder row is NOT written when the parent session has an active
// subagent_runs row. Subagent-caused interrupts shouldn't show as
// `[generation interrupted]` in the parent's chat history.
func TestPersistPartialAssistant_SuppressedWhenSubagentActive(t *testing.T) {
	cs := &capturingStore{
		activeRunFn: func(string) (id, role, child string, ok bool, err error) {
			return "run-active", "planner", "child-c168", true, nil
		},
	}
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistant("sess-parent", "msg-1", "agent-1", "partial bytes that should not be persisted")

	if cs.callCount != 0 {
		t.Errorf("CreateMessage was called %d times; want 0 — placeholder must be suppressed when a subagent is active", cs.callCount)
	}
}

// TestPersistPartialAssistant_SurfacesNormallyWhenNoSubagent verifies the
// suppression gate does NOT swallow placeholder writes when there is no
// active subagent — the parent's own error paths must still get
// refresh-survival rows (CW-20260419-0019 contract).
func TestPersistPartialAssistant_SurfacesNormallyWhenNoSubagent(t *testing.T) {
	cs := &capturingStore{} // default activeRunFn returns ok=false
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistant("sess-parent", "msg-1", "agent-1", "real parent-stream failure")

	if cs.callCount != 1 {
		t.Errorf("CreateMessage was called %d times; want 1 — parent-side failures must still persist", cs.callCount)
	}
}

// TestPersistPartialAssistant_FailsOpenOnClassifierError verifies that a
// DB error in the subagent classifier does NOT silently drop the
// placeholder write — the call must fail open so genuine parent
// failures still get refresh-survival rows.
func TestPersistPartialAssistant_FailsOpenOnClassifierError(t *testing.T) {
	cs := &capturingStore{
		activeRunFn: func(string) (id, role, child string, ok bool, err error) {
			return "", "", "", false, errors.New("db connection lost")
		},
	}
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistant("sess-parent", "msg-1", "agent-1", "content")

	if cs.callCount != 1 {
		t.Errorf("CreateMessage was called %d times; want 1 — classifier DB error must fail open", cs.callCount)
	}
}

// TestSurfaceErrorOrSuppress_EmitsWhenNoSubagent verifies that when there
// is no active subagent, the helper emits both an ErrorEnvelopeDelta and
// an ErrorEvent and returns false (caller proceeds normally).
func TestSurfaceErrorOrSuppress_EmitsWhenNoSubagent(t *testing.T) {
	cs := &capturingStore{}
	svc := &chatServiceImpl{store: cs}
	ch := make(chan chat.StreamEvent, 4)

	suppressed := svc.surfaceErrorOrSuppress(ch, "sess-1", "test_site", "boom",
		map[string]interface{}{"recovery": "refused"}, "")
	close(ch)

	if suppressed {
		t.Fatal("surfaceErrorOrSuppress reported suppression with no active subagent")
	}
	var got []chat.StreamEvent
	for evt := range ch {
		got = append(got, evt)
	}
	if len(got) != 2 {
		t.Fatalf("emitted %d events, want 2 (envelope + event)", len(got))
	}
	// First is an ErrorEnvelopeDelta (type=delta with envelope content),
	// second is an ErrorEvent (type=error with structured payload).
	if got[0].Type != "delta" {
		t.Errorf("event[0].Type = %q, want \"delta\" (ErrorEnvelopeDelta)", got[0].Type)
	}
	if got[1].Type != "error" {
		t.Errorf("event[1].Type = %q, want \"error\" (ErrorEvent)", got[1].Type)
	}
	if got[1].Error != "boom" {
		t.Errorf("event[1].Error = %q, want %q", got[1].Error, "boom")
	}
}

// TestSurfaceErrorOrSuppress_SuppressesWhenSubagentActive verifies that
// when a subagent is active, NO events reach the channel and the helper
// returns true (caller short-circuits).
func TestSurfaceErrorOrSuppress_SuppressesWhenSubagentActive(t *testing.T) {
	cs := &capturingStore{
		activeRunFn: func(string) (id, role, child string, ok bool, err error) {
			return "run-active", "planner", "child-c168", true, nil
		},
	}
	svc := &chatServiceImpl{store: cs}
	ch := make(chan chat.StreamEvent, 4)

	// Site label is decoupled from the removed 5-minute deadline
	// (CW-20260512-0006); the contract is the generic suppression
	// classifier — the label is just a structured-log breadcrumb.
	suppressed := svc.surfaceErrorOrSuppress(ch, "sess-1", "provider_stream_error", "boom",
		map[string]interface{}{"recovery": "refused"}, "")
	close(ch)

	if !suppressed {
		t.Fatal("surfaceErrorOrSuppress did NOT report suppression with active subagent")
	}
	var got []chat.StreamEvent
	for evt := range ch {
		got = append(got, evt)
	}
	if len(got) != 0 {
		t.Errorf("emitted %d events, want 0 — subagent-caused surface must not reach FE", len(got))
	}
}

// ============================================================================
// PR #138 review #2 — redundant query elimination.
// ============================================================================

// TestPersistPartialAssistantPreClassified_SkipsLookup verifies that the
// pre-classified entry point writes the row WITHOUT re-querying
// ActiveSubagentRunForParent. Callers that already ran a suppression
// classification upstream save a DB round-trip per error path.
func TestPersistPartialAssistantPreClassified_SkipsLookup(t *testing.T) {
	cs := &capturingStore{}
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistantPreClassified("sess-1", "msg-1", "agent-1", "pre-classified content")

	if cs.callCount != 1 {
		t.Errorf("CreateMessage called %d times; want 1", cs.callCount)
	}
	if cs.lookupCallCount != 0 {
		t.Errorf("ActiveSubagentRunForParent called %d times; want 0 — pre-classified path must not re-query", cs.lookupCallCount)
	}
}

// TestPersistPartialAssistant_LookupOnceWhenNotPreClassified verifies the
// baseline contract: the unclassified entry point performs exactly one
// lookup. Acts as a regression guard so the helper doesn't accidentally
// double-query in future refactors.
func TestPersistPartialAssistant_LookupOnceWhenNotPreClassified(t *testing.T) {
	cs := &capturingStore{}
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistant("sess-1", "msg-1", "agent-1", "unclassified content")

	if cs.callCount != 1 {
		t.Errorf("CreateMessage called %d times; want 1", cs.callCount)
	}
	if cs.lookupCallCount != 1 {
		t.Errorf("ActiveSubagentRunForParent called %d times; want 1 — unclassified path must classify once", cs.lookupCallCount)
	}
}

// TestSurfaceErrorOrSuppress_ThenPreClassified_OneLookup is the integration
// shape that previously cost two lookups: surfaceErrorOrSuppress (returns
// false) followed by persistPartialAssistant on the error path. With the
// pre-classified wrapper there should be exactly one lookup across both
// calls. PR #138 review #2 regression test.
func TestSurfaceErrorOrSuppress_ThenPreClassified_OneLookup(t *testing.T) {
	cs := &capturingStore{}
	svc := &chatServiceImpl{store: cs}
	ch := make(chan chat.StreamEvent, 4)

	suppressed := svc.surfaceErrorOrSuppress(ch, "sess-1", "test_site", "boom",
		map[string]interface{}{"recovery": "refused"}, "partial")
	close(ch)
	if suppressed {
		t.Fatal("unexpected suppression with no active subagent")
	}

	// Caller goes on to persist; with pre-classified, no second lookup.
	svc.persistPartialAssistantPreClassified("sess-1", "msg-1", "agent-1", "partial")

	if cs.lookupCallCount != 1 {
		t.Errorf("ActiveSubagentRunForParent called %d times; want 1 — pre-classified persist must reuse the upstream classification", cs.lookupCallCount)
	}
	if cs.callCount != 1 {
		t.Errorf("CreateMessage called %d times; want 1", cs.callCount)
	}
}

// ============================================================================
// PR #139 review #2 — clean-cancel persistence path
// ============================================================================

// TestPersistPartialAssistantCanceled_NoErrorFlag verifies that the cancel-
// path helper writes a row with HasError=false in the structured content and
// no `had_error` flag in the metadata column. A deliberate stop must not
// mislabel the turn as an error.
func TestPersistPartialAssistantCanceled_NoErrorFlag(t *testing.T) {
	cs := &capturingStore{}
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistantCanceled("sess-1", "msg-1", "agent-1", "partial bytes streamed before cancel")

	if cs.callCount != 1 {
		t.Fatalf("expected CreateMessage called once, got %d", cs.callCount)
	}
	msg := cs.lastMsg
	if msg == nil {
		t.Fatal("lastMsg is nil")
	}
	// Metadata must NOT carry had_error=true. Empty metadata is acceptable;
	// non-empty metadata must not assert the flag.
	if msg.Metadata != "" {
		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(msg.Metadata), &meta); err != nil {
			t.Fatalf("metadata is not valid JSON: %q — %v", msg.Metadata, err)
		}
		if v, ok := meta["had_error"]; ok && v == true {
			t.Errorf("cancel-path metadata must not carry had_error=true; got %q", msg.Metadata)
		}
	}
	// Structured content must record HasError=false.
	var structured map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Content), &structured); err != nil {
		t.Fatalf("content is not valid JSON: %q — %v", msg.Content, err)
	}
	flags, _ := structured["flags"].(map[string]interface{})
	if v, ok := flags["has_error"]; ok && v == true {
		t.Errorf("StructuredMessage.Flags.HasError must be false for clean cancel; got flags=%v", flags)
	}
}

// TestPersistPartialAssistantCanceled_EmptyContentPlaceholder verifies that
// the cancel-path helper still writes the "[generation interrupted]"
// placeholder when nothing has been streamed, so a refresh shows the turn
// stub instead of a missing row.
func TestPersistPartialAssistantCanceled_EmptyContentPlaceholder(t *testing.T) {
	cs := &capturingStore{}
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistantCanceled("sess-1", "msg-1", "agent-1", "")

	if cs.callCount != 1 {
		t.Fatalf("expected CreateMessage called once, got %d", cs.callCount)
	}
	var structured map[string]interface{}
	if err := json.Unmarshal([]byte(cs.lastMsg.Content), &structured); err != nil {
		t.Fatalf("content is not valid JSON: %q", cs.lastMsg.Content)
	}
	text, _ := structured["text"].(string)
	if text != "[generation interrupted]" {
		t.Errorf("expected placeholder text %q, got %q", "[generation interrupted]", text)
	}
}

// TestPersistPartialAssistantCanceled_NoSubagentLookup verifies the cancel
// path does NOT classify against ActiveSubagentRunForParent. Intentional
// cancels are not "internal errors" we route through the recovery broker,
// so the suppression classifier isn't needed.
func TestPersistPartialAssistantCanceled_NoSubagentLookup(t *testing.T) {
	cs := &capturingStore{}
	svc := &chatServiceImpl{store: cs}

	svc.persistPartialAssistantCanceled("sess-1", "msg-1", "agent-1", "content")

	if cs.lookupCallCount != 0 {
		t.Errorf("ActiveSubagentRunForParent called %d times; want 0 — cancel path does not classify", cs.lookupCallCount)
	}
}

// TestPersistPartialAssistantCanceled_RealStore exercises the helper end-to-
// end against a real SQLite store. Verifies the row is readable and that
// neither HasError nor `had_error` is set.
func TestPersistPartialAssistantCanceled_RealStore(t *testing.T) {
	s, err := storetest.New(t, context.Background(), t.TempDir()+"/persist_cancel.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	sess := &store.Session{ID: "test-session", Status: "active"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	svc := &chatServiceImpl{store: s}
	const msgID = "test-msg-cancel"
	svc.persistPartialAssistantCanceled("test-session", msgID, "test-agent", "hello from cancel path")

	msg, err := s.GetMessage(context.Background(), msgID)
	if err != nil {
		t.Fatalf("GetMessage: %v — cancel-path row was not persisted", err)
	}
	if msg.Role != "assistant" {
		t.Errorf("role: got %q, want %q", msg.Role, "assistant")
	}
	// Metadata is allowed to be empty for the cancel path; if non-empty,
	// it must not assert had_error=true.
	if msg.Metadata != "" {
		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(msg.Metadata), &meta); err != nil {
			t.Fatalf("metadata is not valid JSON: %q — %v", msg.Metadata, err)
		}
		if v, ok := meta["had_error"]; ok && v == true {
			t.Errorf("cancel-path metadata must not carry had_error=true; got %q", msg.Metadata)
		}
	}
	var structured map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Content), &structured); err != nil {
		t.Fatalf("content not valid JSON: %q — %v", msg.Content, err)
	}
	flags, _ := structured["flags"].(map[string]interface{})
	if v, ok := flags["has_error"]; ok && v == true {
		t.Errorf("StructuredMessage.Flags.HasError must be false for clean cancel; got flags=%v", flags)
	}
}
