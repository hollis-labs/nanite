package service

// Tests for CW-20260816-0065: resolveMessageWakePolicy's tiering (mirrors
// resolveSubagentCompletionPolicy's tests, but the global default is
// inverted — auto_summarize, not render_and_wait) and
// messagingWakeReactor.ReactToMessage's trigger/skip decisions (mirrors
// subagentCompletionReactor.ReactToCompletion's tests).

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/messaging"
	"github.com/hollis-labs/nanite/internal/store"
)

// --- resolveMessageWakePolicy ---

func TestResolveMessageWakePolicy_SessionOverrideWins(t *testing.T) {
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1", Metadata: `{"message_wake_policy":"render_and_wait"}`},
		}},
		agents: &stubAgentService{agent: &store.AgentProfile{
			Constraints: `{"message_wake_policy":"auto_summarize"}`,
		}},
	}
	got := svc.resolveMessageWakePolicy(context.Background(), "sess-1")
	if got != chat.SubagentPolicyRenderAndWait {
		t.Errorf("got %q, want session override %q", got, chat.SubagentPolicyRenderAndWait)
	}
}

func TestResolveMessageWakePolicy_FallsBackToAgentDefault(t *testing.T) {
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1"}, // no metadata override
		}},
		agents: &stubAgentService{agent: &store.AgentProfile{
			Constraints: `{"message_wake_policy":"render_and_wait"}`,
		}},
	}
	got := svc.resolveMessageWakePolicy(context.Background(), "sess-1")
	if got != chat.SubagentPolicyRenderAndWait {
		t.Errorf("got %q, want agent-profile default %q", got, chat.SubagentPolicyRenderAndWait)
	}
}

// TestResolveMessageWakePolicy_GlobalDefaultWhenUnset is the key
// asymmetry vs. resolveSubagentCompletionPolicy: unlike a subagent
// completion (which still reaches the model via the kind=subagent_result
// turn-start injection even under render_and_wait), a generic A2A
// message has no equivalent fallback delivery path, so the global
// default here is auto_summarize, not render_and_wait.
func TestResolveMessageWakePolicy_GlobalDefaultWhenUnset(t *testing.T) {
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1"},
		}},
		agents: &stubAgentService{agent: &store.AgentProfile{}}, // empty constraints
	}
	got := svc.resolveMessageWakePolicy(context.Background(), "sess-1")
	if got != chat.SubagentPolicyAutoSummarize {
		t.Errorf("got %q, want global default %q", got, chat.SubagentPolicyAutoSummarize)
	}
}

func TestResolveMessageWakePolicy_UnrecognizedSessionOverride_FallsThrough(t *testing.T) {
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1", Metadata: `{"message_wake_policy":"auto-summarize"}`}, // typo/variant
		}},
		agents: &stubAgentService{agent: &store.AgentProfile{
			Constraints: `{"message_wake_policy":"render_and_wait"}`,
		}},
	}
	got := svc.resolveMessageWakePolicy(context.Background(), "sess-1")
	if got != chat.SubagentPolicyRenderAndWait {
		t.Errorf("got %q, want fall-through to the valid agent-profile default %q", got, chat.SubagentPolicyRenderAndWait)
	}
}

// TestResolveMessageWakePolicy_UsesReadOnlyAgentResolution is the
// regression test for a code-review finding: resolveMessageWakePolicy runs
// from a fire-and-forget goroutine on every eligible A2A SendMessage
// (internal/messaging/service.go), so its agent-profile-default tier must
// use the non-mutating ResolveForSessionReadOnly rather than
// ResolveForSession — the latter auto-assigns a session_agents row and
// emits AgentAssigned as a side effect for any session with no existing
// binding, which a read-only "what policy applies here" check should
// never trigger. See internal/service/agent_test.go's
// TestAgentService_ResolveForSessionReadOnly_NoAutoAssign for the
// corresponding proof at the AgentService implementation level.
func TestResolveMessageWakePolicy_UsesReadOnlyAgentResolution(t *testing.T) {
	agents := &stubAgentService{agent: &store.AgentProfile{
		Constraints: `{"message_wake_policy":"render_and_wait"}`,
	}}
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{
			"sess-1": {ID: "sess-1"}, // no metadata override, forces the agent-profile tier
		}},
		agents: agents,
	}

	svc.resolveMessageWakePolicy(context.Background(), "sess-1")

	if agents.resolveForSessionReadOnlyCalls != 1 {
		t.Errorf("expected exactly 1 ResolveForSessionReadOnly call, got %d", agents.resolveForSessionReadOnlyCalls)
	}
	if agents.resolveForSessionCalls != 0 {
		t.Errorf("resolveMessageWakePolicy must not use the mutating ResolveForSession (auto-assigns session_agents + emits AgentAssigned); got %d calls", agents.resolveForSessionCalls)
	}
}

func TestResolveMessageWakePolicy_UnknownSession_FallsBackSafely(t *testing.T) {
	svc := &chatServiceImpl{
		sessions: &stubSessionService{sessions: map[string]*store.Session{}}, // Get errors
		agents:   &stubAgentService{agent: &store.AgentProfile{}},
	}
	got := svc.resolveMessageWakePolicy(context.Background(), "sess-missing")
	if got != chat.SubagentPolicyAutoSummarize {
		t.Errorf("got %q, want safe fallback %q", got, chat.SubagentPolicyAutoSummarize)
	}
}

// --- messagingWakeReactor.ReactToMessage ---

func newMessagingReactorTestService(t *testing.T, agentConstraints string) (*chatServiceImpl, *capturingStore, *fakeSessionEventWriter) {
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
		lifecycle:          lifecycle.NewManager("test.messaging-reactor"),
	}
	return svc, cs, sw
}

func TestReactToMessage_RenderAndWait_NoTrigger(t *testing.T) {
	svc, cs, sw := newMessagingReactorTestService(t, `{"message_wake_policy":"render_and_wait"}`)
	r := &messagingWakeReactor{chat: svc}

	r.ReactToMessage(context.Background(), &messaging.Message{ID: "msg-1", ToSessionID: "sess-1", FromAgentID: "peer", Body: "hi"})

	if cs.callCount != 0 {
		t.Errorf("expected no wake-triggered message for render_and_wait, got %d CreateMessage calls", cs.callCount)
	}
	if sw.count() != 0 {
		t.Errorf("expected no session_events row for render_and_wait, got %d", sw.count())
	}
}

func TestReactToMessage_DefaultPolicy_Triggers(t *testing.T) {
	// No message_wake_policy set anywhere — resolves to the global
	// default (auto_summarize), unlike the subagent-completion reactor's
	// default of render_and_wait. This is the core CW-20260816-0065
	// behavior: message_send wakes the recipient by default.
	svc, cs, sw := newMessagingReactorTestService(t, "")
	r := &messagingWakeReactor{chat: svc}

	r.ReactToMessage(context.Background(), &messaging.Message{
		ID: "msg-1", ToSessionID: "sess-1", FromSessionID: "sess-peer",
		FromAgentID: "peer-agent", Body: "hello from peer",
	})

	if cs.callCount != 1 {
		t.Fatalf("expected exactly 1 wake-triggered message, got %d CreateMessage calls", cs.callCount)
	}
	if cs.lastMsg.SessionID != "sess-1" || cs.lastMsg.Role != "user" {
		t.Errorf("unexpected message: session=%q role=%q", cs.lastMsg.SessionID, cs.lastMsg.Role)
	}
	if !strings.Contains(cs.lastMsg.Content, "hello from peer") {
		t.Errorf("expected the actual message body in the turn content (no turn-start injection exists for generic messages), got %q", cs.lastMsg.Content)
	}
	if !strings.Contains(cs.lastMsg.Metadata, `"source":"agent_message"`) || !strings.Contains(cs.lastMsg.Metadata, `"message_id":"msg-1"`) {
		t.Errorf("expected agent_message/message_id provenance in message metadata, got %q", cs.lastMsg.Metadata)
	}

	if sw.count() != 1 {
		t.Fatalf("expected exactly 1 session_events row, got %d", sw.count())
	}
	if sw.events[0].eventType != "harness_triggered_turn" {
		t.Errorf("eventType = %q, want harness_triggered_turn", sw.events[0].eventType)
	}
	if !strings.Contains(sw.events[0].payloadJSON, "a2a_message") || !strings.Contains(sw.events[0].payloadJSON, "msg-1") {
		t.Errorf("expected triggered_by+message_id in payload, got %q", sw.events[0].payloadJSON)
	}
}

func TestReactToMessage_AutoSummarize_SkipsWhenSessionBusy(t *testing.T) {
	svc, cs, _ := newMessagingReactorTestService(t, `{"message_wake_policy":"auto_summarize"}`)
	svc.activeGen["sess-1"] = &inFlightGen{msgID: "in-flight", cancel: func() {}}
	r := &messagingWakeReactor{chat: svc}

	r.ReactToMessage(context.Background(), &messaging.Message{ID: "msg-1", ToSessionID: "sess-1", FromAgentID: "peer", Body: "hi"})

	if cs.callCount != 0 {
		t.Errorf("expected no wake trigger while session busy, got %d CreateMessage calls", cs.callCount)
	}
}

func TestReactToMessage_NilMsgOrEmptyToSession_NoOp(t *testing.T) {
	svc, cs, _ := newMessagingReactorTestService(t, `{"message_wake_policy":"auto_summarize"}`)
	r := &messagingWakeReactor{chat: svc}

	r.ReactToMessage(context.Background(), nil)
	r.ReactToMessage(context.Background(), &messaging.Message{ID: "msg-1"}) // empty ToSessionID

	if cs.callCount != 0 {
		t.Errorf("expected no wake trigger for nil msg / empty to_session_id, got %d CreateMessage calls", cs.callCount)
	}
}
