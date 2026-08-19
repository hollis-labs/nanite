package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	ctxpkg "github.com/hollis-labs/nanite/internal/context"
	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/store"
)

// fakeEventEmitter captures EmitPreCompact and EmitPostCompact calls for
// assertion. All other methods are no-ops.
type fakeEventEmitter struct {
	mu            sync.Mutex
	pre           []fakeCompactEvent
	post          []fakeCompactEvent
	agentAssigned []fakeAgentAssignedEvent
}

type fakeAgentAssignedEvent struct {
	sessionID string
	agentID   string
	mode      string
}

type fakeCompactEvent struct {
	sessionID     string
	messageCount  int
	reason        string   // pre only
	tokensSaved   int      // post only
	stagesApplied []string // post only
}

func (f *fakeEventEmitter) EmitPreCompact(_ context.Context, sessionID string, messageCount int, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pre = append(f.pre, fakeCompactEvent{sessionID: sessionID, messageCount: messageCount, reason: reason})
}
func (f *fakeEventEmitter) EmitPostCompact(_ context.Context, sessionID string, tokensSaved int, stagesApplied []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.post = append(f.post, fakeCompactEvent{sessionID: sessionID, tokensSaved: tokensSaved, stagesApplied: stagesApplied})
}
func (f *fakeEventEmitter) EmitSessionStart(_ context.Context, _, _, _, _ string) {}
func (f *fakeEventEmitter) EmitSessionEnd(_ context.Context, _ string)            {}
func (f *fakeEventEmitter) EmitAgentAssigned(_ context.Context, sessionID, agentID, mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agentAssigned = append(f.agentAssigned, fakeAgentAssignedEvent{sessionID: sessionID, agentID: agentID, mode: mode})
}
func (f *fakeEventEmitter) EmitResponseComplete(_ context.Context, _, _, _ string, _, _ int) {
}
func (f *fakeEventEmitter) EmitToolCall(_ context.Context, _, _ string, _ bool, _ int) {}
func (f *fakeEventEmitter) EmitToolFailed(_ context.Context, _, _ string, _ any, _ string) {
}
func (f *fakeEventEmitter) EmitRateLimitHit(_ context.Context, _, _ string, _ time.Duration) {
}
func (f *fakeEventEmitter) EmitCircuitBreakerTripped(_ context.Context, _, _ string) {}
func (f *fakeEventEmitter) EmitContextBudgetExceeded(_ context.Context, _ string, _, _ int) {
}
func (f *fakeEventEmitter) EmitError(_ context.Context, _, _, _ string)                    {}
func (f *fakeEventEmitter) EmitMessageReceived(_ context.Context, _, _, _ string, _ int64) {}

// TestRecoverFromContextOverflow_DisabledFlag covers the plan §T9 requirement
// that UserSettings.ContextOverflowRecovery=false produces a no-op — callers
// see ok=false and fall through to the normal error path.
func TestRecoverFromContextOverflow_DisabledFlag(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := s.Seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.UpdateUserSettings(&store.UserSettings{ContextOverflowRecovery: false}); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	svc := &chatServiceImpl{store: s}

	// A minimal result with a window so the helper has something to reach for
	// before short-circuiting on the flag.
	cw := ctxpkg.NewContextWindow(200000, ctxpkg.DefaultEstimator{})
	result := &SlotAssemblyResult{Window: cw}

	ch := make(chan struct{}, 0) // unused
	_ = ch

	_, _, ok := svc.recoverFromContextOverflow(
		context.Background(),
		"sess-1",
		result,
		&store.AgentProfile{ID: "a1"},
		[]llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
		nil,
		nil, // nil stream channel — helper must tolerate
		"prompt is too long", compactTriggerContextOverflow,
	)
	if ok {
		t.Fatalf("recovery should be a no-op when flag is disabled; got ok=true")
	}
}

// TestRecoverFromContextOverflow_NoSummarizerSkips covers the D9/T9 nil-summarizer
// fallback. If the provider registry has no summarizer, we can't compact, so
// recovery reports unsuccessful and the caller surfaces the original error.
func TestRecoverFromContextOverflow_NoSummarizerSkips(t *testing.T) {
	s, err := store.New(context.Background(), t.TempDir()+"/test.db")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := s.Seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Flag enabled, but no provider.Registry is wired → buildSummarizer returns nil.
	if err := s.UpdateUserSettings(&store.UserSettings{ContextOverflowRecovery: true}); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	svc := &chatServiceImpl{store: s} // providers left nil

	cw := ctxpkg.NewContextWindow(200000, ctxpkg.DefaultEstimator{})
	result := &SlotAssemblyResult{Window: cw}

	_, _, ok := svc.recoverFromContextOverflow(
		context.Background(),
		"sess-1",
		result,
		&store.AgentProfile{ID: "a1"},
		[]llmtypes.ChatMessage{{Role: "user", Content: "hi"}},
		nil,
		nil,
		"prompt is too long", compactTriggerContextOverflow,
	)
	if ok {
		t.Fatalf("recovery should be skipped when no summarizer is available; got ok=true")
	}
}

// TestRecoverFromContextOverflow_NilResult covers defensive nil-safety.
func TestRecoverFromContextOverflow_NilResult(t *testing.T) {
	svc := &chatServiceImpl{}
	_, _, ok := svc.recoverFromContextOverflow(
		context.Background(),
		"s",
		nil,
		&store.AgentProfile{},
		nil, nil, nil, "prompt is too long", compactTriggerContextOverflow,
	)
	if ok {
		t.Fatal("nil result should not succeed")
	}
}

// TestFakeEventEmitter_PreBeforePost asserts that the fakeEventEmitter records
// pre-compact before post-compact when called in sequence — validates the fake
// itself so ordering assertions in integration tests are trustworthy.
func TestFakeEventEmitter_PreBeforePost(t *testing.T) {
	fe := &fakeEventEmitter{}
	ctx := context.Background()

	fe.EmitPreCompact(ctx, "sess-1", 10, compactTriggerContextOverflow)
	fe.EmitPostCompact(ctx, "sess-1", 500, []string{"drop_enrichment", "summarize_oldest"})

	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.pre) != 1 {
		t.Fatalf("pre count = %d, want 1", len(fe.pre))
	}
	if len(fe.post) != 1 {
		t.Fatalf("post count = %d, want 1", len(fe.post))
	}
	// Pre captures trigger_kind in reason field.
	if fe.pre[0].reason != compactTriggerContextOverflow {
		t.Errorf("pre[0].reason = %q, want %q", fe.pre[0].reason, compactTriggerContextOverflow)
	}
	// Post carries tokensSaved and stagesApplied.
	if fe.post[0].tokensSaved != 500 {
		t.Errorf("post[0].tokensSaved = %d, want 500", fe.post[0].tokensSaved)
	}
	if len(fe.post[0].stagesApplied) != 2 {
		t.Errorf("post[0].stagesApplied len = %d, want 2", len(fe.post[0].stagesApplied))
	}
}

// TestRecoverFromContextOverflow_WithEventsNilResult verifies that wiring a
// fakeEventEmitter into chatServiceImpl does not panic on nil-result early exit
// (EmitPreCompact is not called before result nil-check).
func TestRecoverFromContextOverflow_WithEventsNilResult(t *testing.T) {
	fe := &fakeEventEmitter{}
	svc := &chatServiceImpl{events: fe}
	_, _, ok := svc.recoverFromContextOverflow(
		context.Background(),
		"sess-1",
		nil, // nil result — should return false before any emit
		&store.AgentProfile{},
		nil, nil, nil,
		"prompt is too long", compactTriggerContextOverflow,
	)
	if ok {
		t.Fatal("nil result should not succeed")
	}
	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.pre) != 0 {
		t.Errorf("pre events emitted before nil-result guard: %v", fe.pre)
	}
}

// TestCompositeEmitter_SessionEventsOrdering checks that EmitPreCompact writes
// a context_pre_compact row and EmitPostCompact writes a context_post_compact
// row to the session_events table via the captureSessionEventWriter, with
// trigger_kind carried in the pre-compact payload and stages_applied in the
// post-compact payload.
func TestCompositeEmitter_SessionEventsOrdering(t *testing.T) {
	writer := &captureSessionEventWriter{}
	emitter := NewCompositeEmitter(nil, nil).WithSessionWriter(writer)

	ctx := context.Background()
	emitter.EmitPreCompact(ctx, "sess-2", 7, compactTriggerContextOverflow)
	emitter.EmitPostCompact(ctx, "sess-2", 1200, []string{"drop_enrichment", "dedupe_tool_results"})

	// Give background goroutines time to complete (fire-and-forget pattern).
	// safego.Go launches goroutines immediately; 100ms is ample for in-process
	// work with no I/O (captureSessionEventWriter is in-memory).
	time.Sleep(100 * time.Millisecond)

	evts := writer.events()
	if len(evts) < 2 {
		t.Fatalf("session_events written = %d, want >= 2", len(evts))
	}
	// Pre-compact comes first.
	if evts[0].EventType != messaging.EventContextPreCompact {
		t.Errorf("evts[0].EventType = %q, want %q", evts[0].EventType, messaging.EventContextPreCompact)
	}
	// trigger_kind is in pre-compact payload.
	if !strings.Contains(evts[0].PayloadJSON, compactTriggerContextOverflow) {
		t.Errorf("pre-compact payload missing trigger_kind: %s", evts[0].PayloadJSON)
	}
	// Post-compact comes second.
	if evts[1].EventType != messaging.EventContextPostCompact {
		t.Errorf("evts[1].EventType = %q, want %q", evts[1].EventType, messaging.EventContextPostCompact)
	}
	// stages_applied is in post-compact payload.
	if !strings.Contains(evts[1].PayloadJSON, "drop_enrichment") {
		t.Errorf("post-compact payload missing stages: %s", evts[1].PayloadJSON)
	}
}
