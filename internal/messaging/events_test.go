package messaging

import (
	"context"
	"encoding/json"
	"testing"
)

// TestService_SendWritesSessionEvents covers T8 happy path: a send
// produces a message_sent row in the sender's session plus a
// message_received row in the recipient's session.
func TestService_SendWritesSessionEvents(t *testing.T) {
	svc, _, _ := newTestService(t, "file-a", "file-b")
	ctx := context.Background()

	sent, err := svc.SendMessage(ctx, SendInput{
		FromSessionID: "sess-1", FromAgentID: UserSentinel,
		ToSessionID: "sess-2", ToAgentID: "file-a",
		Body: "hi",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	fromEvents, err := svc.SessionEvents(ctx, "sess-1", 0)
	if err != nil {
		t.Fatalf("SessionEvents sess-1: %v", err)
	}
	if len(fromEvents) != 1 || fromEvents[0].EventType != EventMessageSent {
		t.Errorf("sess-1 events = %+v, want 1 message_sent", fromEvents)
	}
	toEvents, err := svc.SessionEvents(ctx, "sess-2", 0)
	if err != nil {
		t.Fatalf("SessionEvents sess-2: %v", err)
	}
	if len(toEvents) != 1 || toEvents[0].EventType != EventMessageReceived {
		t.Errorf("sess-2 events = %+v, want 1 message_received", toEvents)
	}

	// Envelope pointer should contain the message_id so the consumer
	// can rehydrate without a cache hit.
	var ptr messagePointer
	if err := json.Unmarshal([]byte(toEvents[0].EnvelopePointerJSON), &ptr); err != nil {
		t.Fatalf("unmarshal envelope pointer: %v", err)
	}
	if ptr.MessageID != sent.ID {
		t.Errorf("ptr.MessageID = %q, want %q", ptr.MessageID, sent.ID)
	}
}

// TestService_AckWritesAckedEvent covers T8 ack: a successful Ack
// writes a message_acked row in the recipient's session.
func TestService_AckWritesAckedEvent(t *testing.T) {
	svc, s, _ := newTestService(t, "file-backend")
	ctx := context.Background()

	seeded, err := s.Send(ctx, SendInput{
		FromSessionID: "sess-1", FromAgentID: UserSentinel,
		ToSessionID: "sess-1", ToAgentID: "file-backend",
		Body: "hi",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.Ack(ctx, "sess-1", "file-backend", seeded.ID); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	events, err := svc.SessionEvents(ctx, "sess-1", 0)
	if err != nil {
		t.Fatalf("SessionEvents: %v", err)
	}
	found := false
	for _, e := range events {
		if e.EventType == EventMessageAcked {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected message_acked event, got %+v", events)
	}
}

// TestService_ResolveWritesResolvedEvent covers T8 resolve.
func TestService_ResolveWritesResolvedEvent(t *testing.T) {
	svc, s, _ := newTestService(t, "file-backend")
	ctx := context.Background()

	seeded, err := s.Send(ctx, SendInput{
		FromSessionID: "sess-1", FromAgentID: UserSentinel,
		ToSessionID: "sess-1", ToAgentID: "file-backend",
		Body: "hi",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.Resolve(ctx, "sess-1", "file-backend", seeded.ID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	events, err := svc.SessionEvents(ctx, "sess-1", 0)
	if err != nil {
		t.Fatalf("SessionEvents: %v", err)
	}
	found := false
	for _, e := range events {
		if e.EventType == EventMessageResolved {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected message_resolved event, got %+v", events)
	}
}

// TestService_SessionEvents_ChronologicalOrder covers replay: events
// come back oldest first.
func TestService_SessionEvents_ChronologicalOrder(t *testing.T) {
	svc, _, _ := newTestService(t, "file-backend")
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := svc.SendMessage(ctx, SendInput{
			FromSessionID: "sess-1", FromAgentID: UserSentinel,
			ToSessionID: "sess-1", ToAgentID: "file-backend",
			Body: "msg",
		}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	events, err := svc.SessionEvents(ctx, "sess-1", 0)
	if err != nil {
		t.Fatalf("SessionEvents: %v", err)
	}
	// 5 sent + 5 received since from==to (the 2-row rule applies).
	if len(events) != 10 {
		t.Errorf("len=%d, want 10", len(events))
	}
	for i := 1; i < len(events); i++ {
		if events[i].CreatedAt < events[i-1].CreatedAt {
			t.Errorf("events not chronological: %q before %q",
				events[i-1].CreatedAt, events[i].CreatedAt)
		}
	}
}

// TestService_SessionEvents_LimitCap verifies the defensive cap.
func TestService_SessionEvents_LimitCap(t *testing.T) {
	svc, _, _ := newTestService(t, "file-backend")
	ctx := context.Background()

	// 300 sends in same session → 600 event rows. Ask for 100000 to
	// exercise the 500-cap; expect 500 back.
	for i := 0; i < 300; i++ {
		if _, err := svc.SendMessage(ctx, SendInput{
			FromSessionID: "sess-1", FromAgentID: UserSentinel,
			ToSessionID: "sess-1", ToAgentID: "file-backend",
			Body: "msg",
		}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	events, err := svc.SessionEvents(ctx, "sess-1", 100000)
	if err != nil {
		t.Fatalf("SessionEvents: %v", err)
	}
	if len(events) != 500 {
		t.Errorf("len=%d, want 500 (cap)", len(events))
	}
}
