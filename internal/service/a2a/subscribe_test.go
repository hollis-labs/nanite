// Package a2a — subscribe_test.go
//
// Tests for the in-process pubsub that backs MCP streaming subscribers.
// The pubsub has no replay buffer: only messages published AFTER a
// subscription takes effect are delivered to that subscriber, and slow
// subscribers are dropped on a full channel (buffer = 16).
package a2a

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestSubscribe_ReceivesNewMessage verifies that SubscribeSessionAgent
// returns a channel which receives a matching message published after the
// subscription is established.
func TestSubscribe_ReceivesNewMessage(t *testing.T) {
	svc, _ := newTestService(t, "file-a")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := svc.SubscribeSessionAgent(ctx, "sess-1", "file-a")
	if err != nil {
		t.Fatalf("SubscribeSessionAgent: %v", err)
	}

	msg := &store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   UserSentinel,
		ToSessionID:   "sess-1",
		ToAgentID:     "file-a",
		Body:          "hello",
	}

	// Publish in a goroutine after a small delay so the receive below is
	// already blocking on the channel when the send fires. The delay is
	// deliberate: this test proves live fan-out, not buffered delivery.
	go func() {
		time.Sleep(10 * time.Millisecond)
		if _, err := svc.SendMessage(context.Background(), msg); err != nil {
			t.Errorf("SendMessage: %v", err)
		}
	}()

	select {
	case received := <-ch:
		if received == nil {
			t.Fatal("received nil message on subscriber channel")
		}
		if received.Body != "hello" {
			t.Errorf("Body = %q, want %q", received.Body, "hello")
		}
		if received.ToAgentID != "file-a" {
			t.Errorf("ToAgentID = %q, want file-a", received.ToAgentID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for pushed message")
	}
}

// TestSubscribe_IgnoresNonMatching verifies that a subscription for
// (sessionID, agentID) does not receive messages addressed to a different
// (sessionID, agentID) pair.
func TestSubscribe_IgnoresNonMatching(t *testing.T) {
	svc, _ := newTestService(t, "file-a", "file-b")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := svc.SubscribeSessionAgent(ctx, "sess-1", "file-a")
	if err != nil {
		t.Fatalf("SubscribeSessionAgent: %v", err)
	}

	if _, err := svc.SendMessage(context.Background(), &store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   UserSentinel,
		ToSessionID:   "sess-1",
		ToAgentID:     "file-b",
		Body:          "not for us",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	select {
	case received := <-ch:
		t.Errorf("unexpected push to file-a subscriber: %+v", received)
	case <-time.After(100 * time.Millisecond):
		// good — nothing arrived.
	}
}

// TestSubscribe_NoReplayOnResubscribe verifies there is no replay buffer:
// a message published before any subscription exists is dropped, and a
// subsequent subscriber does not receive it.
func TestSubscribe_NoReplayOnResubscribe(t *testing.T) {
	svc, _ := newTestService(t, "file-a")

	// Publish BEFORE subscribing. There are zero subscribers, so this must
	// be dropped by the pubsub.
	if _, err := svc.SendMessage(context.Background(), &store.A2AMessage{
		FromSessionID: "sess-1",
		FromAgentID:   UserSentinel,
		ToSessionID:   "sess-1",
		ToAgentID:     "file-a",
		Body:          "old",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := svc.SubscribeSessionAgent(ctx, "sess-1", "file-a")
	if err != nil {
		t.Fatalf("SubscribeSessionAgent: %v", err)
	}

	select {
	case received := <-ch:
		t.Errorf("expected no replay of pre-subscription messages, got: %+v", received)
	case <-time.After(100 * time.Millisecond):
		// good — no replay.
	}
}

// TestSubscribe_PublishUnsubscribeRace stresses the close-during-publish window.
// Before the fix this reliably panicked with "send on closed channel" under -race
// within a few iterations.
func TestSubscribe_PublishUnsubscribeRace(t *testing.T) {
	svc, _ := newTestService(t, "file-a")

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(2)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer wg.Done()
			_, _ = svc.SubscribeSessionAgent(ctx, "sess-1", "file-a")
			cancel()
		}()
		go func() {
			defer wg.Done()
			_, _ = svc.SendMessage(context.Background(), &store.A2AMessage{
				FromSessionID: "sess-1",
				FromAgentID:   UserSentinel,
				ToSessionID:   "sess-1",
				ToAgentID:     "file-a",
				Body:          "race",
			})
		}()
	}
	wg.Wait()
}
