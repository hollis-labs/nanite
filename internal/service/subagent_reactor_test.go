package service

// Tests for CW-20260520-0001 (Layer 2 — "harness reacts"): policy
// resolution order and the completion reactor's trigger/skip decisions.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// fakeSessionEventWriter records WriteSessionEvent calls so tests can
// assert the triggered_by provenance marker (the ticket's "every
// harness-triggered turn has a triggered_by provenance marker" criterion).
type fakeSessionEventWriter struct {
	mu     sync.Mutex
	events []struct{ sessionID, eventType, payloadJSON string }
}

func (f *fakeSessionEventWriter) WriteSessionEvent(_ context.Context, sessionID, eventType, _, payloadJSON string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, struct{ sessionID, eventType, payloadJSON string }{sessionID, eventType, payloadJSON})
}

func (f *fakeSessionEventWriter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// --- resolveSubagentCompletionPolicy ---

func TestResolveSubagentCompletionPolicy_SessionOverrideWins(t *testing.T) {
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1", Metadata: `{"subagent_completion_policy":"auto_summarize"}`},
		}},
		agents: &stubAgentService{agent: &store.AgentProfile{
			Constraints: `{"subagent_completion_policy":"render_and_wait"}`,
		}},
	}
	got := svc.resolveSubagentCompletionPolicy(context.Background(), "sess-1")
	if got != chat.SubagentPolicyAutoSummarize {
		t.Errorf("got %q, want session override %q", got, chat.SubagentPolicyAutoSummarize)
	}
}

func TestResolveSubagentCompletionPolicy_SessionOverrideSkipsAgentResolution(t *testing.T) {
	agents := &stubAgentService{agent: &store.AgentProfile{
		Constraints: `{"subagent_completion_policy":"render_and_wait"}`,
	}}
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1", Metadata: `{"subagent_completion_policy":"auto_summarize"}`},
		}},
		agents: agents,
	}
	got := svc.resolveSubagentCompletionPolicy(context.Background(), "sess-1")
	if got != chat.SubagentPolicyAutoSummarize {
		t.Errorf("got %q, want session override %q", got, chat.SubagentPolicyAutoSummarize)
	}
	if agents.resolveForSessionCalls != 0 {
		t.Errorf("session override should not require mutating ResolveForSession; got %d calls", agents.resolveForSessionCalls)
	}
}

func TestResolveSubagentCompletionPolicy_FallsBackToAgentDefault(t *testing.T) {
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1"}, // no metadata override
		}},
		agents: &stubAgentService{agent: &store.AgentProfile{
			Constraints: `{"subagent_completion_policy":"auto_summarize"}`,
		}},
	}
	got := svc.resolveSubagentCompletionPolicy(context.Background(), "sess-1")
	if got != chat.SubagentPolicyAutoSummarize {
		t.Errorf("got %q, want agent-profile default %q", got, chat.SubagentPolicyAutoSummarize)
	}
}

func TestResolveSubagentCompletionPolicy_GlobalDefaultWhenUnset(t *testing.T) {
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1"},
		}},
		agents: &stubAgentService{agent: &store.AgentProfile{}}, // empty constraints
	}
	got := svc.resolveSubagentCompletionPolicy(context.Background(), "sess-1")
	if got != chat.SubagentPolicyRenderAndWait {
		t.Errorf("got %q, want global default %q", got, chat.SubagentPolicyRenderAndWait)
	}
}

// TestResolveSubagentCompletionPolicy_UnrecognizedSessionOverride_FallsThrough
// is the regression test for PR #247's review comment: a typo'd or stale
// session-level policy value must not be trusted verbatim — it should be
// ignored (with a warning) and resolution should fall through to the
// agent-profile default rather than silently disabling auto_summarize.
func TestResolveSubagentCompletionPolicy_UnrecognizedSessionOverride_FallsThrough(t *testing.T) {
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1", Metadata: `{"subagent_completion_policy":"auto-summarise"}`}, // typo/variant, not a real value
		}},
		agents: &stubAgentService{agent: &store.AgentProfile{
			Constraints: `{"subagent_completion_policy":"auto_summarize"}`,
		}},
	}
	got := svc.resolveSubagentCompletionPolicy(context.Background(), "sess-1")
	if got != chat.SubagentPolicyAutoSummarize {
		t.Errorf("got %q, want fall-through to the valid agent-profile default %q", got, chat.SubagentPolicyAutoSummarize)
	}
}

// TestResolveSubagentCompletionPolicy_UnrecognizedAtEveryTier_FallsBackToGlobalDefault
// covers the case where every tier is unrecognized garbage — resolution
// must land on the safe global default, never return the garbage string.
func TestResolveSubagentCompletionPolicy_UnrecognizedAtEveryTier_FallsBackToGlobalDefault(t *testing.T) {
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1", Metadata: `{"subagent_completion_policy":"bogus"}`},
		}},
		agents: &stubAgentService{agent: &store.AgentProfile{
			Constraints: `{"subagent_completion_policy":"also-bogus"}`,
		}},
	}
	got := svc.resolveSubagentCompletionPolicy(context.Background(), "sess-1")
	if got != chat.SubagentPolicyRenderAndWait {
		t.Errorf("got %q, want global default %q", got, chat.SubagentPolicyRenderAndWait)
	}
}

func TestResolveSubagentCompletionPolicy_UnknownSession_FallsBackSafely(t *testing.T) {
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{}}, // Get errors
		agents:   &stubAgentService{agent: &store.AgentProfile{}},
	}
	got := svc.resolveSubagentCompletionPolicy(context.Background(), "sess-missing")
	if got != chat.SubagentPolicyRenderAndWait {
		t.Errorf("got %q, want safe fallback %q", got, chat.SubagentPolicyRenderAndWait)
	}
}

// --- subagentCompletionReactor.ReactToCompletion ---

func newReactorTestService(t *testing.T, agentConstraints string) (*chatServiceImpl, *capturingStore, *fakeSessionEventWriter) {
	t.Helper()
	cs := &capturingStore{}
	sw := &fakeSessionEventWriter{}
	svc := &chatServiceImpl{
		store:   cs,
		streams: NewStreamManager(),
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1"},
		}},
		agents: &stubAgentService{agent: &store.AgentProfile{
			Constraints: agentConstraints,
		}},
		sessionEventWriter: sw,
		activeGen:          make(map[string]*inFlightGen),
		lifecycle:          lifecycle.NewManager("test.subagent-reactor"),
	}
	return svc, cs, sw
}

func TestReactToCompletion_RenderAndWait_NoTrigger(t *testing.T) {
	svc, cs, sw := newReactorTestService(t, `{"subagent_completion_policy":"render_and_wait"}`)
	r := &subagentCompletionReactor{chat: svc}

	r.ReactToCompletion(context.Background(), &subagent.Run{ID: "run-1", ParentSessionID: "sess-1"}, "msg-1")

	if cs.callCount != 0 {
		t.Errorf("expected no harness-triggered message for render_and_wait, got %d CreateMessage calls", cs.callCount)
	}
	if sw.count() != 0 {
		t.Errorf("expected no session_events row for render_and_wait, got %d", sw.count())
	}
}

func TestReactToCompletion_AutoSummarize_Triggers(t *testing.T) {
	svc, cs, sw := newReactorTestService(t, `{"subagent_completion_policy":"auto_summarize"}`)
	r := &subagentCompletionReactor{chat: svc}

	r.ReactToCompletion(context.Background(), &subagent.Run{ID: "run-1", ParentSessionID: "sess-1"}, "msg-1")

	if cs.callCount != 1 {
		t.Fatalf("expected exactly 1 harness-triggered message for auto_summarize, got %d", cs.callCount)
	}
	if cs.lastMsg.SessionID != "sess-1" || cs.lastMsg.Role != "user" {
		t.Errorf("unexpected message: session=%q role=%q", cs.lastMsg.SessionID, cs.lastMsg.Role)
	}
	if !strings.Contains(cs.lastMsg.Metadata, `"source":"harness"`) || !strings.Contains(cs.lastMsg.Metadata, `"triggered_by":"subagent_completion"`) {
		t.Errorf("expected harness/triggered_by provenance in message metadata, got %q", cs.lastMsg.Metadata)
	}

	// Give the async launchGeneration goroutine (and its synchronous
	// WriteSessionEvent call inside TriggerHarnessTurn) a moment — the
	// event write itself happens synchronously in TriggerHarnessTurn
	// before it returns, so this should already be true, but a short
	// poll keeps the test robust against any future scheduling change.
	deadline := time.Now().Add(500 * time.Millisecond)
	for sw.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if sw.count() != 1 {
		t.Fatalf("expected exactly 1 session_events row, got %d", sw.count())
	}
	if sw.events[0].eventType != "harness_triggered_turn" {
		t.Errorf("eventType = %q, want harness_triggered_turn", sw.events[0].eventType)
	}
	if !strings.Contains(sw.events[0].payloadJSON, "subagent_completion") || !strings.Contains(sw.events[0].payloadJSON, "run-1") {
		t.Errorf("expected triggered_by+run_id in payload, got %q", sw.events[0].payloadJSON)
	}
}

func TestReactToCompletion_AutoSummarize_SkipsWhenSessionBusy(t *testing.T) {
	svc, cs, _ := newReactorTestService(t, `{"subagent_completion_policy":"auto_summarize"}`)
	svc.activeGen["sess-1"] = &inFlightGen{msgID: "in-flight", cancel: func() {}}
	r := &subagentCompletionReactor{chat: svc}

	r.ReactToCompletion(context.Background(), &subagent.Run{ID: "run-1", ParentSessionID: "sess-1"}, "msg-1")

	if cs.callCount != 0 {
		t.Errorf("expected no harness trigger while session busy, got %d CreateMessage calls", cs.callCount)
	}
}

func TestReactToCompletion_NilRunOrEmptyParentSession_NoOp(t *testing.T) {
	svc, cs, _ := newReactorTestService(t, `{"subagent_completion_policy":"auto_summarize"}`)
	r := &subagentCompletionReactor{chat: svc}

	r.ReactToCompletion(context.Background(), nil, "msg-1")
	r.ReactToCompletion(context.Background(), &subagent.Run{ID: "run-1"}, "msg-1") // empty ParentSessionID

	if cs.callCount != 0 {
		t.Errorf("expected no harness trigger for nil run / empty parent session, got %d CreateMessage calls", cs.callCount)
	}
}
