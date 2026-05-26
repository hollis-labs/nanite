//go:build devmode

package muxproxy

import (
	"testing"

	"github.com/hollis-labs/tether/pkg/claudestream"
)

func TestParseOne_Delta(t *testing.T) {
	payload := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`
	ev, ok := parseOne(payload)
	if !ok {
		t.Fatal("parseOne returned ok=false")
	}
	if ev.Kind != claudestream.KindDelta {
		t.Fatalf("want KindDelta, got %q", ev.Kind)
	}
	if ev.Text != "hello" {
		t.Fatalf("want text=hello, got %q", ev.Text)
	}
}

func TestParseOne_SessionID(t *testing.T) {
	payload := `{"type":"system","subtype":"init","session_id":"sess-123"}`
	ev, ok := parseOne(payload)
	if !ok {
		t.Fatal("parseOne returned ok=false")
	}
	if ev.Kind != claudestream.KindSessionID {
		t.Fatalf("want KindSessionID, got %q", ev.Kind)
	}
	if ev.SessionID != "sess-123" {
		t.Fatalf("want sess-123, got %q", ev.SessionID)
	}
}

func TestParseOne_Empty(t *testing.T) {
	_, ok := parseOne("")
	if ok {
		t.Fatal("parseOne should reject empty payload")
	}
}

func TestParseOne_Noise(t *testing.T) {
	payload := `{"type":"rate_limit_event"}`
	_, ok := parseOne(payload)
	if ok {
		t.Fatal("parseOne should return ok=false for filtered events")
	}
}
