package service

import (
	"context"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/messaging"
)

// captureSessionEventWriter is a test double for SessionEventWriter that records
// every WriteSessionEvent call for assertion.
type captureSessionEventWriter struct {
	mu     sync.Mutex
	writes []capturedSessionEvent
}

type capturedSessionEvent struct {
	SessionID   string
	EventType   string
	Channel     string
	PayloadJSON string
}

func (c *captureSessionEventWriter) WriteSessionEvent(_ context.Context, sessionID, eventType, channel, payloadJSON string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, capturedSessionEvent{
		SessionID:   sessionID,
		EventType:   eventType,
		Channel:     channel,
		PayloadJSON: payloadJSON,
	})
}

func (c *captureSessionEventWriter) events() []capturedSessionEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedSessionEvent, len(c.writes))
	copy(out, c.writes)
	return out
}

// Compile-time assertion: *messaging.Service must satisfy SessionEventWriter.
var _ SessionEventWriter = (*messaging.Service)(nil)

// TestCaptureSessionEventWriter_RecordsWrites is a sanity check for the test
// double itself. Validates that captureSessionEventWriter records calls
// thread-safely.
func TestCaptureSessionEventWriter_RecordsWrites(t *testing.T) {
	w := &captureSessionEventWriter{}

	w.WriteSessionEvent(context.Background(), "sess-1", messaging.EventPTYTurnStart, "pty", `{"x":1}`)
	w.WriteSessionEvent(context.Background(), "sess-1", messaging.EventPTYTurnComplete, "pty", `{"x":2}`)

	evts := w.events()
	if len(evts) != 2 {
		t.Fatalf("len=%d, want 2", len(evts))
	}
	if evts[0].EventType != messaging.EventPTYTurnStart {
		t.Errorf("evts[0].EventType = %q, want %q", evts[0].EventType, messaging.EventPTYTurnStart)
	}
	if evts[1].EventType != messaging.EventPTYTurnComplete {
		t.Errorf("evts[1].EventType = %q, want %q", evts[1].EventType, messaging.EventPTYTurnComplete)
	}
}
