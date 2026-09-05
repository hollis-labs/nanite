package service

// Tests for CW-20260816-0065: TriggerMessageWake mirrors
// TriggerHarnessTurn's reject-if-busy semantics (see
// chat_trigger_harness_turn_test.go) but additionally must carry the
// arriving message's own body into the turn content, since generic A2A
// messages have no kind=subagent_result-style turn-start injection to
// fall back on.

import (
	"context"
	"errors"
	"strings"
	"testing"

	messaging "github.com/hollis-labs/go-messaging/mailbox"
	"github.com/hollis-labs/nanite/internal/lifecycle"
)

func newTriggerMessageWakeTestService(t *testing.T) (*chatServiceImpl, *capturingStore) {
	t.Helper()
	cs := &capturingStore{}
	return &chatServiceImpl{
		store:     cs,
		streams:   NewStreamManager(),
		lifecycle: lifecycle.NewManager("test.trigger-message-wake"),
		activeGen: make(map[string]*inFlightGen),
	}, cs
}

func TestTriggerMessageWake_BusySession_RejectsWithoutInterrupting(t *testing.T) {
	svc, cs := newTriggerMessageWakeTestService(t)

	canceled := false
	svc.activeGen["sess-1"] = &inFlightGen{msgID: "user-turn-in-flight", cancel: func() { canceled = true }}

	msg := &messaging.Message{ID: "msg-1", ToSessionID: "sess-1", FromAgentID: "peer", Body: "hi"}
	_, err := svc.TriggerMessageWake(context.Background(), "sess-1", msg)

	if !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("expected ErrSessionBusy, got %v", err)
	}
	if canceled {
		t.Error("TriggerMessageWake must not cancel the existing in-flight user generation (takeover semantics would do this — reject-if-busy must not)")
	}
	if cs.callCount != 0 {
		t.Errorf("expected no message created when session is busy, got %d CreateMessage calls", cs.callCount)
	}
	if got := svc.activeGen["sess-1"]; got == nil || got.msgID != "user-turn-in-flight" {
		t.Errorf("expected the original in-flight generation slot to remain untouched, got %+v", got)
	}
}

func TestTriggerMessageWake_IdleSession_Succeeds(t *testing.T) {
	svc, cs := newTriggerMessageWakeTestService(t)

	msg := &messaging.Message{
		ID: "msg-1", ToSessionID: "sess-1", FromSessionID: "sess-peer",
		FromAgentID: "peer-agent", Body: "please review the attached plan",
	}
	msgID, err := svc.TriggerMessageWake(context.Background(), "sess-1", msg)
	if err != nil {
		t.Fatalf("TriggerMessageWake: %v", err)
	}
	if msgID == "" {
		t.Error("expected non-empty assistant message id")
	}
	if cs.callCount != 1 {
		t.Fatalf("expected 1 message created, got %d", cs.callCount)
	}

	// Unlike TriggerHarnessTurn's generic "review the subagent result"
	// prompt (delivered via the kind=subagent_result turn-start
	// injection), a generic A2A message has no such fallback — the real
	// body must ride directly in the synthetic turn's content or the
	// recipient wakes up with nothing to react to.
	if !strings.Contains(cs.lastMsg.Content, "please review the attached plan") {
		t.Errorf("expected message body in turn content, got %q", cs.lastMsg.Content)
	}
	if !strings.Contains(cs.lastMsg.Content, "peer-agent") {
		t.Errorf("expected sender attribution in turn content, got %q", cs.lastMsg.Content)
	}
	if cs.lastMsg.SessionID != "sess-1" || cs.lastMsg.Role != "user" {
		t.Errorf("unexpected message: session=%q role=%q", cs.lastMsg.SessionID, cs.lastMsg.Role)
	}
}
