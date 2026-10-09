package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
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

// TestCaptureSessionEventWriter_RecordsWrites is a sanity check for the test
// double itself. Validates that captureSessionEventWriter records calls
// thread-safely.
func TestCaptureSessionEventWriter_RecordsWrites(t *testing.T) {
	w := &captureSessionEventWriter{}

	w.WriteSessionEvent(context.Background(), "sess-1", EventCLITurnStart, "cli", `{"x":1}`)
	w.WriteSessionEvent(context.Background(), "sess-1", EventCLITurnComplete, "cli", `{"x":2}`)

	evts := w.events()
	if len(evts) != 2 {
		t.Fatalf("len=%d, want 2", len(evts))
	}
	if evts[0].EventType != EventCLITurnStart {
		t.Errorf("evts[0].EventType = %q, want %q", evts[0].EventType, EventCLITurnStart)
	}
	if evts[1].EventType != EventCLITurnComplete {
		t.Errorf("evts[1].EventType = %q, want %q", evts[1].EventType, EventCLITurnComplete)
	}
}

func TestNativeCLITurnUsesPipesAndReportsCLIObservability(t *testing.T) {
	for _, tc := range []nativeCLICase{claudeStreamingStdioCase, codexSubprocessCase} {
		t.Run(tc.name, func(t *testing.T) {
			tc.script = strings.Replace(tc.script, "#!/bin/sh\n", "#!/bin/sh\nif [ -t 0 ] || [ -t 1 ] || [ -t 2 ]; then exit 33; fi\n", 1)
			writer := &captureSessionEventWriter{}
			f, events := runNativeCLITurn(t, tc, func(f *characterizationFixture) { f.svc.sessionEventWriter = writer })
			if findEvent(events, "stream_end") == nil || findEvent(events, "error") != nil {
				t.Fatalf("headless turn events = %+v", events)
			}
			logged := writer.events()
			if len(logged) != 2 || logged[0].EventType != EventCLITurnStart || logged[1].EventType != EventCLITurnComplete {
				t.Fatalf("CLI lifecycle events = %+v", logged)
			}
			for _, ev := range logged {
				var payload struct {
					Provider string `json:"provider"`
				}
				if err := json.Unmarshal([]byte(ev.PayloadJSON), &payload); err != nil {
					t.Fatal(err)
				}
				if ev.Channel != "cli" || payload.Provider != tc.provider {
					t.Fatalf("CLI channel/provider provenance = %+v", ev)
				}
			}
			var adapter string
			if err := f.st.DB.QueryRowContext(context.Background(), `SELECT adapter FROM execution_metrics WHERE message_id = ?`, nativeCLIMessageID).Scan(&adapter); err != nil {
				t.Fatal(err)
			}
			if adapter != "cli" {
				t.Fatalf("headless execution adapter = %q", adapter)
			}
		})
	}
}
