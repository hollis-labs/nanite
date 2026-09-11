package selftools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agent/reflexes"
	"github.com/hollis-labs/nanite/internal/store/mailboxadapter"
)

// TestWakeOnMail_PrescribedSequenceDischargesTheTrigger closes the gap
// CW-20260911-0075 turned on: nothing proved that anything an agent can do
// makes the wake_on_mail trigger go quiet.
//
// TestEvaluator_EventTrigger_MailReceived (reflexes/evaluator_test.go) is
// correct and unchanged — it proves that *if* MailUnreadCount reaches zero the
// trigger stops. It is a pure-predicate test over a hand-built State, so it
// stays green through every variant of this defect: the original
// `mux_message_inbox` body, a rename-only fix, and a correct fix alike. The
// missing assertion sits between the predicate and the seeded body, which is
// why this test lives here, where both the mailbox tools and the trigger are
// reachable.
//
// It deliberately drives the tools *named in the live seed body* rather than a
// sequence chosen by the test, so a future edit that drops `message_ack` from
// the body fails here instead of shipping.
func TestWakeOnMail_PrescribedSequenceDischargesTheTrigger(t *testing.T) {
	ctx := context.Background()

	// One store for both sides, deliberately not newSelfTools + newTestMessaging.
	// Those helpers call newTestStore independently, so the transport and the
	// messaging service end up on two different databases — mail sent through
	// one is invisible to a trigger reading the other, and this test would
	// measure nothing. The guard below would catch it, but the wiring is the
	// thing worth stating.
	s := newTestStore(t)
	st := NewSelfToolsTransport(s)
	st.MessagingTools.Service = mailboxadapter.New(s).Service

	const (
		fromSession = "sess-wake-from"
		toSession   = "sess-wake-to"
	)
	agentID := a2a.UserSentinel
	collector := &reflexes.StateCollector{Store: s}

	// The body under test must name both halves. Assert that first: the rest
	// of this test drives what the body says, so a body that lost its
	// discharge would otherwise make the test vacuous rather than red.
	body := seededWakeOnMailBody(t, "process")
	for _, want := range []string{"message_inbox", "message_ack"} {
		if !strings.Contains(body, want) {
			t.Fatalf("seeded process wake_on_mail body does not name %q — a body that "+
				"names only the read cannot discharge its own trigger (CW-20260911-0075).\nbody: %s", want, body)
		}
	}
	if strings.Contains(body, "mux_") {
		t.Errorf("seeded wake_on_mail body names a mux_* tool; the trigger reads Nanite's "+
			"agent_messages, so the prescribed tool must be Nanite's own.\nbody: %s", body)
	}

	// ── Arrange: unread mail for the agent the trigger reads for.
	sendText := messagingToolText(t, st, ctx, "message_send", map[string]any{
		"from_session_id": fromSession,
		"from_agent_id":   agentID,
		"to_session_id":   toSession,
		"to_agent_id":     agentID,
		"body":            "wake up",
	})
	messageID := strings.TrimPrefix(sendText, "sent: ")
	if messageID == sendText || messageID == "" {
		t.Fatalf("message_send result = %q, want \"sent: <id>\"", sendText)
	}

	// ── The trigger fires. This is the half a fires-only test would stop at.
	state, err := collector.Collect(ctx, toSession, agentID, "process")
	if err != nil {
		t.Fatalf("Collect (pre-ack): %v", err)
	}
	if state.MailUnreadCount == 0 {
		t.Fatalf("MailUnreadCount = 0 after message_send; the test never established the "+
			"condition it is about to discharge (session=%s agent=%s)", toSession, agentID)
	}
	fired, err := reflexes.EvaluateTrigger("event", `{"name":"mail_received"}`, state)
	if err != nil {
		t.Fatalf("EvaluateTrigger (pre-ack): %v", err)
	}
	if !fired {
		t.Fatalf("mail_received did not fire with MailUnreadCount=%d", state.MailUnreadCount)
	}

	// ── Act: run the sequence the body prescribes, in order.
	inbox := messagingToolText(t, st, ctx, "message_inbox", map[string]any{
		"session_id": toSession,
		"agent_id":   agentID,
		"status":     "unread",
	})
	if !strings.Contains(inbox, messageID) {
		t.Fatalf("message_inbox did not return the seeded message %s: %s", messageID, inbox)
	}

	// Reading must NOT be what clears it. This is the property that made the
	// original fix insufficient, so it is asserted rather than assumed.
	afterRead, err := collector.Collect(ctx, toSession, agentID, "process")
	if err != nil {
		t.Fatalf("Collect (post-inbox): %v", err)
	}
	if afterRead.MailUnreadCount != state.MailUnreadCount {
		t.Errorf("message_inbox changed MailUnreadCount %d → %d; it is documented as "+
			"non-marking, and the reflex body's guidance depends on that",
			state.MailUnreadCount, afterRead.MailUnreadCount)
	}

	messagingToolText(t, st, ctx, "message_ack", map[string]any{
		"session_id": toSession,
		"agent_id":   agentID,
		"message_id": messageID,
	})

	// ── Assert: the count reaches zero and the trigger goes quiet.
	final, err := collector.Collect(ctx, toSession, agentID, "process")
	if err != nil {
		t.Fatalf("Collect (post-ack): %v", err)
	}
	if final.MailUnreadCount != 0 {
		t.Errorf("MailUnreadCount = %d after the prescribed sequence, want 0 — the reflex "+
			"cannot be discharged by the action it prescribes", final.MailUnreadCount)
	}
	stillFiring, err := reflexes.EvaluateTrigger("event", `{"name":"mail_received"}`, final)
	if err != nil {
		t.Fatalf("EvaluateTrigger (post-ack): %v", err)
	}
	if stillFiring {
		t.Errorf("mail_received still fires after the prescribed sequence; the reminder " +
			"would re-fire every tick forever")
	}
}

// TestWakeOnMail_BothSeededBodiesAreDischargeable covers the advisor class on
// the same ground. Both classes seed a wake_on_mail and the original defect was
// present in both; fixing one is the likely partial regression.
func TestWakeOnMail_BothSeededBodiesAreDischargeable(t *testing.T) {
	for _, class := range []string{"process", "advisor"} {
		body := seededWakeOnMailBody(t, class)
		if strings.Contains(body, "mux_") {
			t.Errorf("class=%s: body names a mux_* tool: %s", class, body)
		}
		if !strings.Contains(body, "message_inbox") {
			t.Errorf("class=%s: body does not name the read (message_inbox): %s", class, body)
		}
		if !strings.Contains(body, "message_ack") {
			t.Errorf("class=%s: body does not name the discharge (message_ack), so following "+
				"it leaves the trigger firing: %s", class, body)
		}
	}
}

// seededWakeOnMailBody reads the reminder body out of the live seed set rather
// than restating it, so these tests track the shipped guidance instead of a
// copy that can drift from it.
func seededWakeOnMailBody(t *testing.T, classTag string) string {
	t.Helper()
	for _, s := range reflexes.BaseSeeds() {
		if s.Name != "wake_on_mail" || s.ClassTag != classTag {
			continue
		}
		raw, err := json.Marshal(s.ActionSpec)
		if err != nil {
			t.Fatalf("marshal action spec for class=%s: %v", classTag, err)
		}
		var spec struct {
			Body string `json:"body"`
		}
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatalf("unmarshal action spec for class=%s: %v", classTag, err)
		}
		if spec.Body == "" {
			t.Fatalf("class=%s wake_on_mail seed has an empty body", classTag)
		}
		return spec.Body
	}
	t.Fatalf("no wake_on_mail seed found for class=%s — BaseSeeds shape changed and these "+
		"tests would otherwise pass vacuously", classTag)
	return ""
}
