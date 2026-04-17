package service

import (
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/chat"
)

func TestDrainCapture_DeltasConcatenateIntoSummary(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "delta", Content: "Hello "}
	ch <- chat.StreamEvent{Type: "delta", Content: "world"}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, envelope, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "Hello world" {
		t.Errorf("summary = %q, want %q", summary, "Hello world")
	}
	if envelope != "{}" {
		t.Errorf("envelope = %q, want \"{}\" (no envelope events)", envelope)
	}
}

func TestDrainCapture_CapturesEnvelopeAsResultJSON(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "delta", Content: "summary text"}
	ch <- chat.StreamEvent{Type: "plugin_envelope", Envelope: `{"finding":"x"}`}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, envelope, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "summary text" {
		t.Errorf("summary = %q", summary)
	}
	if envelope != `{"finding":"x"}` {
		t.Errorf("envelope = %q", envelope)
	}
}

func TestDrainCapture_LastEnvelopeWins(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "plugin_envelope", Envelope: `{"first":1}`}
	ch <- chat.StreamEvent{Type: "delta", Content: "between"}
	ch <- chat.StreamEvent{Type: "plugin_envelope", Envelope: `{"second":2}`}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	_, envelope, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if envelope != `{"second":2}` {
		t.Errorf("envelope = %q, want last-wins", envelope)
	}
}

func TestDrainCapture_EmptySummaryFallback(t *testing.T) {
	ch := make(chan chat.StreamEvent, 4)
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, _, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "" {
		t.Errorf("summary = %q, want empty (fallback applied by caller, not drainCapture)", summary)
	}
}

func TestDrainCapture_ErrorEventTerminatesDrain(t *testing.T) {
	ch := make(chan chat.StreamEvent, 8)
	ch <- chat.StreamEvent{Type: "delta", Content: "partial"}
	ch <- chat.StreamEvent{Type: "error", Error: "provider exploded"}
	ch <- chat.StreamEvent{Type: "delta", Content: "should not appear"}
	close(ch)

	_, _, err := drainCapture(ch)
	if err == nil {
		t.Fatal("expected error from drainCapture, got nil")
	}
	if !errors.Is(err, errStreamFailure) && err.Error() == "" {
		t.Errorf("error = %v", err)
	}
}

func TestDrainCapture_IgnoresOtherEventTypes(t *testing.T) {
	ch := make(chan chat.StreamEvent, 16)
	ch <- chat.StreamEvent{Type: "stream_start"}
	ch <- chat.StreamEvent{Type: "tool_call", Tool: "x"}
	ch <- chat.StreamEvent{Type: "tool_result", Tool: "x", Summary: "ok"}
	ch <- chat.StreamEvent{Type: "status", Content: "thinking"}
	ch <- chat.StreamEvent{Type: "delta", Content: "real text"}
	ch <- chat.StreamEvent{Type: "stream_end"}
	close(ch)

	summary, envelope, err := drainCapture(ch)
	if err != nil {
		t.Fatalf("drainCapture: %v", err)
	}
	if summary != "real text" {
		t.Errorf("summary = %q, want only delta content", summary)
	}
	if envelope != "{}" {
		t.Errorf("envelope = %q", envelope)
	}
}
