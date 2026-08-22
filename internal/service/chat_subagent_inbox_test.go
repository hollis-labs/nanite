package service

// Tests for CW-20260512-0019's turn-start injection: pending
// kind=subagent_result agent_messages should reach SlotUserContext (and
// its derived Blocks/SystemPrompt views) via evaluateAndInjectSubagentResults,
// then be Ack'd so they aren't re-injected on a later turn. Mirrors the
// approach in reminder_engine_integration_test.go — a real store-backed
// AssembleSlots result, with a small fake standing in for the
// SubagentResultInbox dependency (messaging.Service in production).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/store"
)

// fakeSubagentInbox is a small hand-rolled fake for SubagentResultInbox,
// in the same mutex-guarded-capture style as internal/subagent's
// stubPoster/recordingPoster.
type fakeSubagentInbox struct {
	mu       sync.Mutex
	msgs     []messaging.Message
	inboxErr error
	ackErr   error
	acked    []string
}

func (f *fakeSubagentInbox) Inbox(_ context.Context, _, _ string, _ messaging.InboxFilter, _, _ string) ([]messaging.Message, error) {
	if f.inboxErr != nil {
		return nil, f.inboxErr
	}
	return f.msgs, nil
}

func (f *fakeSubagentInbox) Ack(_ context.Context, _, _, msgID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acked = append(f.acked, msgID)
	return f.ackErr
}

func (f *fakeSubagentInbox) ackedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.acked...)
}

// newTestSlotResult builds a real AssembleSlots result against a fresh
// SQLite store, matching the setup reminder_engine_integration_test.go
// uses. Returns the SlotAssemblyResult ready for injection.
func newTestSlotResult(t *testing.T, sessionID string) *SlotAssemblyResult {
	t.Helper()
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.DB.Close() })

	sess := &store.Session{ID: sessionID, Title: "SubagentInboxInjectionTest"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})
	agent := &store.AgentProfile{
		ID: "agent-subagent-inbox-test", Name: "SubagentInboxTestAgent",
		Slug: "subagent-inbox-test", Status: "active", Tags: "[]", Tools: "[]",
	}
	slotResult, err := svc.AssembleSlots(context.Background(), sess, agent, []llmtypes.ToolDefinition{}, "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots: %v", err)
	}
	if slotResult.Window == nil {
		t.Fatal("expected non-nil Window in SlotAssemblyResult")
	}
	return slotResult
}

func TestEvaluateAndInjectSubagentResults_NilInbox_NoOp(t *testing.T) {
	s := &chatServiceImpl{} // subagentInbox left nil
	slotResult := newTestSlotResult(t, "sess-nil-inbox")

	got := s.evaluateAndInjectSubagentResults(context.Background(), "sess-nil-inbox", "agent-1", slotResult)
	if got != nil {
		t.Errorf("expected nil result with no subagentInbox wired, got %v", got)
	}
}

func TestEvaluateAndInjectSubagentResults_EmptyInbox_NoOp(t *testing.T) {
	fake := &fakeSubagentInbox{}
	s := &chatServiceImpl{subagentInbox: fake}
	slotResult := newTestSlotResult(t, "sess-empty-inbox")

	got := s.evaluateAndInjectSubagentResults(context.Background(), "sess-empty-inbox", "agent-1", slotResult)
	if got != nil {
		t.Errorf("expected nil result for empty inbox, got %v", got)
	}
	if len(fake.ackedIDs()) != 0 {
		t.Errorf("expected no Ack calls for empty inbox, got %v", fake.ackedIDs())
	}
}

func TestEvaluateAndInjectSubagentResults_InboxError_NoOp(t *testing.T) {
	fake := &fakeSubagentInbox{inboxErr: errors.New("boom")}
	s := &chatServiceImpl{subagentInbox: fake}
	slotResult := newTestSlotResult(t, "sess-inbox-err")

	got := s.evaluateAndInjectSubagentResults(context.Background(), "sess-inbox-err", "agent-1", slotResult)
	if got != nil {
		t.Errorf("expected nil result on inbox error, got %v", got)
	}
}

// TestEvaluateAndInjectSubagentResults_InjectsAcksAndRefreshesDerivedViews
// is the load-bearing test: pending subagent_result messages must reach
// SlotUserContext AND slotResult.Blocks/SystemPrompt (the actual LLM
// payload — see CW-20260501-0002, the reminders-path bug this mirrors),
// and must be Ack'd so a later turn doesn't re-inject them.
func TestEvaluateAndInjectSubagentResults_InjectsAcksAndRefreshesDerivedViews(t *testing.T) {
	pending := []messaging.Message{
		{ID: "msg-1", FromAgentID: "file-researcher", Body: "subagent file-researcher ended (completed): found 3 issues"},
		{ID: "msg-2", FromAgentID: "file-analyst", Body: "subagent file-analyst ended (failed): timeout"},
	}
	fake := &fakeSubagentInbox{msgs: pending}
	s := &chatServiceImpl{subagentInbox: fake}
	slotResult := newTestSlotResult(t, "sess-inject")

	preBlocks := slotResult.Blocks

	got := s.evaluateAndInjectSubagentResults(context.Background(), "sess-inject", "agent-1", slotResult)
	if len(got) != 2 {
		t.Fatalf("expected 2 pending messages returned, got %d", len(got))
	}

	// (a) Ack'd exactly once each, so a later turn won't re-inject.
	acked := fake.ackedIDs()
	if len(acked) != 2 || acked[0] != "msg-1" || acked[1] != "msg-2" {
		t.Errorf("expected both messages Ack'd in order, got %v", acked)
	}

	// (b) slotResult.Blocks (the actual LLM payload) must carry both
	// messages' content, not just the in-place Window mutation.
	blocksText := ""
	for _, b := range slotResult.Blocks {
		blocksText += b.Content
	}
	if !strings.Contains(blocksText, "found 3 issues") || !strings.Contains(blocksText, "timeout") {
		t.Errorf("slotResult.Blocks missing injected subagent results (pre-injection block count=%d, post=%d): %q",
			len(preBlocks), len(slotResult.Blocks), blocksText)
	}
	if !strings.Contains(blocksText, "<system-reminder>") || !strings.Contains(blocksText, "</system-reminder>") {
		t.Errorf("slotResult.Blocks missing <system-reminder> framing: %q", blocksText)
	}

	// (c) slotResult.SystemPrompt (legacy concat, used by budget
	// enforcement + telemetry) must also carry the injection.
	if !strings.Contains(slotResult.SystemPrompt, "found 3 issues") {
		t.Errorf("slotResult.SystemPrompt missing injected subagent results: %q", slotResult.SystemPrompt)
	}
}

func TestEvaluateAndInjectSubagentResults_AckFailure_StillReturnsPending(t *testing.T) {
	fake := &fakeSubagentInbox{
		msgs:   []messaging.Message{{ID: "msg-1", FromAgentID: "file-a", Body: "done"}},
		ackErr: errors.New("ack failed"),
	}
	s := &chatServiceImpl{subagentInbox: fake}
	slotResult := newTestSlotResult(t, "sess-ack-fail")

	got := s.evaluateAndInjectSubagentResults(context.Background(), "sess-ack-fail", "agent-1", slotResult)
	if len(got) != 1 {
		t.Fatalf("expected injection to still succeed despite Ack failure, got %d messages", len(got))
	}
}
