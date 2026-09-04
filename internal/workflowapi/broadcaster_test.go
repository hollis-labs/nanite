package workflowapi

import (
	"testing"
	"time"
)

func TestBroadcasterFansOutAndUnsubscribes(t *testing.T) {
	broadcaster := NewBroadcaster()
	first := broadcaster.Subscribe()
	second := broadcaster.Subscribe()
	event := Event{Type: "pipeline.completed", RunID: "run-1", Timestamp: time.Now()}

	broadcaster.Broadcast(event)
	for index, subscriber := range []chan Event{first, second} {
		select {
		case got := <-subscriber:
			if got.Type != event.Type || got.RunID != event.RunID {
				t.Fatalf("subscriber %d received %+v", index, got)
			}
		default:
			t.Fatalf("subscriber %d received no event", index)
		}
	}

	broadcaster.Unsubscribe(first)
	if _, open := <-first; open {
		t.Fatal("unsubscribed channel remains open")
	}
	broadcaster.Broadcast(event)
	select {
	case <-second:
	default:
		t.Fatal("remaining subscriber received no event")
	}
}

func TestBroadcasterDoesNotBlockOnFullSubscriber(t *testing.T) {
	broadcaster := NewBroadcaster()
	subscriber := broadcaster.Subscribe()
	for i := 0; i < cap(subscriber); i++ {
		broadcaster.Broadcast(Event{Type: "step.started"})
	}

	done := make(chan struct{})
	go func() {
		broadcaster.Broadcast(Event{Type: "step.completed"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Broadcast blocked on a full subscriber")
	}
}
