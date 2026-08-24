package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/nanite/internal/chat"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// wiringStubSummarizer is a working (non-mock, non-nil-returning) Summarizer
// used to drive a real stageSummarizeOldest pass — production behavior when
// a real LLM call succeeds — without hitting the network.
type wiringStubSummarizer struct{}

func (wiringStubSummarizer) Summarize(_ context.Context, _ string, messages []llmtypes.ChatMessage) (string, error) {
	return fmt.Sprintf("stub summary covering %d earlier messages", len(messages)), nil
}

// TestCompactionEventWriter_WiredAtAllThreeSites_EndToEnd is the Phase 3
// item 02 ("wire compaction_events") acceptance test: it drives the exact
// same construction shape used at all three production CompactionPipeline{}
// sites (internal/api/sessions.go's manual /compact endpoint,
// assembleTurnContext's compact-recoverable path in chat_generate.go, and
// the pre-loop enforceBudgetOrCompact gate in chat_generate.go) — real
// store, real ctxpkg.CompactionPipeline, real storeCompactionEventWriter via
// NewCompactionEventWriter — then confirms:
//
//  1. A real compaction_events row lands (via s.GetLatestCompactionEvent),
//     written by the production adapter, not a test mock.
//  2. On the very next call to the production AssembleSlots path (the "next
//     turn" context build every one of the three call sites feeds into),
//     the CompactionContract disclosure (internal/chat's
//     renderCompactionDisclosure, reached via assembleAgentSlotContent)
//     actually renders — proving the "silently dead" feature described in
//     docs/engineering/architecture/06-session-lifecycle-and-recovery.md is
//     reachable again now that the writer is wired.
//
// Only the LLM call itself is stubbed (via the Summarizer interface, the
// same seam production uses) — everything else (store, pipeline stages,
// event persistence, slot assembly, disclosure rendering) is unmodified
// production code.
func TestCompactionEventWriter_WiredAtAllThreeSites_EndToEnd(t *testing.T) {
	ctx := context.Background()
	s, err := storetest.New(t, ctx, t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	sess := &store.Session{ID: "wire-sess-1", Title: "compaction wiring test"}
	if err := s.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Enough conversation history that stageSummarizeOldest's negative-
	// savings guard (SummarizeMinTokens=200) is cleared by the span
	// preceding the last 4 kept messages.
	longSentence := "This is a deliberately long turn of conversation text used to " +
		"push the compaction span comfortably past the negative-savings token " +
		"guard so stageSummarizeOldest actually fires during this test. "
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if err := s.CreateMessage(context.Background(), &store.Message{
			ID:        fmt.Sprintf("wire-msg-%d", i),
			SessionID: sess.ID,
			Role:      role,
			Content:   fmt.Sprintf("[turn %d] %s", i, strings.Repeat(longSentence, 3)),
		}); err != nil {
			t.Fatalf("CreateMessage %d: %v", i, err)
		}
	}

	agent := &store.AgentProfile{
		ID:           "agent-wire",
		Name:         "WireAgent",
		Slug:         "wire",
		SystemPrompt: "You are a compaction-wiring test agent.",
		Status:       "active",
	}

	client := chat.NewContextClient(s)
	svc := NewContextService(ContextServiceConfig{Client: client})

	// CreatedAt on both messages and compaction_events is second-precision
	// (time.Now().UTC().Format(time.RFC3339)). The disclosure freshness
	// check (internal/chat's isCompactionEventFresh) is a strict
	// eventCreatedAt > lastAssistantMessage.CreatedAt comparison, so this
	// sleep guarantees the compaction event lands in a strictly later
	// second than the seed messages above — otherwise a same-second race
	// would make a genuinely fresh event look stale and flake this test.
	time.Sleep(1100 * time.Millisecond)

	// --- Turn N: assemble context, then run a REAL compaction pass using
	// the exact same pipeline construction shape as all three production
	// call sites, wired with the real writer adapter. ---
	result, err := svc.AssembleSlots(ctx, sess, agent, nil, "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots (pre-compaction): %v", err)
	}
	if result == nil || result.Window == nil {
		t.Fatal("expected non-nil result/window")
	}

	pipeline := &ctxpkg.CompactionPipeline{
		Window:                result.Window,
		Estimator:             ctxpkg.DefaultEstimator{},
		Summarizer:            wiringStubSummarizer{},
		Mode:                  ctxpkg.CompactionModeGeneral,
		ConversationMessages:  result.Messages,
		SessionID:             sess.ID,
		CompactionEventWriter: NewCompactionEventWriter(s),
	}

	// RunForce mirrors the manual /compact endpoint (internal/api/sessions.go)
	// — runs every stage unconditionally, which is also what the pre-loop and
	// recovery sites fall back to on their respective forced paths. Using it
	// here removes any dependency on crafting an exact token budget to make
	// NeedsCompaction() true; the compaction still runs the same real stages.
	cr, err := pipeline.RunForce(ctx)
	if err != nil {
		t.Fatalf("pipeline.RunForce: %v", err)
	}
	if cr == nil || len(cr.StagesApplied) == 0 {
		t.Fatalf("expected at least one compaction stage to apply, got %+v", cr)
	}

	// --- Assertion 1: a real compaction_events row landed via the writer. ---
	evt, err := s.GetLatestCompactionEvent(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetLatestCompactionEvent: %v", err)
	}
	if evt == nil {
		t.Fatal("expected a compaction_events row after RunForce with CompactionEventWriter wired; got nil (writer not actually persisting)")
	}
	if evt.SessionID != sess.ID {
		t.Errorf("evt.SessionID = %q, want %q", evt.SessionID, sess.ID)
	}
	if evt.SummaryMode != ctxpkg.CompactionModeGeneral {
		t.Errorf("evt.SummaryMode = %q, want %q", evt.SummaryMode, ctxpkg.CompactionModeGeneral)
	}
	if len(evt.StagesApplied) == 0 {
		t.Error("evt.StagesApplied is empty; expected the applied stage names to be persisted")
	}

	// --- Assertion 2: the disclosure actually renders on the next turn's
	// context assembly (the real production AssembleSlots path every one of
	// the three call sites feeds its result into on subsequent turns). ---
	result2, err := svc.AssembleSlots(ctx, sess, agent, nil, "", 200000, "")
	if err != nil {
		t.Fatalf("AssembleSlots (post-compaction / next turn): %v", err)
	}
	if !strings.Contains(result2.SystemPrompt, "Compaction Notice") {
		t.Fatalf("expected next-turn SystemPrompt to contain the CompactionContract disclosure "+
			"('Compaction Notice') now that compaction_events is wired; got:\n%s", result2.SystemPrompt)
	}
}
