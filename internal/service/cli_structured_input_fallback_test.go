package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestIsCLIStructuredInputUnsupported pins the tool-name match table so a
// future agent surface change (case shift, whitespace, additional tool
// names) lands deliberately. CW-20260519-0051.
func TestIsCLIStructuredInputUnsupported(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"AskUserQuestion", true},
		{"askuserquestion", true},
		{"  AskUserQuestion  ", true},
		{"Bash", false},
		{"", false},
		{"ask_user_question", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCLIStructuredInputUnsupported(tc.name); got != tc.want {
				t.Fatalf("isCLIStructuredInputUnsupported(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestEmitCLIStructuredInputFallback_BroadcastsInfoCard asserts the helper
// publishes a `plugin_envelope` stream event carrying an info-card payload
// with the warning variant, the tool name embedded in the body, and the
// alert display_class so the FE surfaces it as a standalone card.
// CW-20260519-0051.
func TestEmitCLIStructuredInputFallback_BroadcastsInfoCard(t *testing.T) {
	sm := NewStreamManager()
	bridge := &agentEventBridge{streams: sm}

	const sessionID = "sess-fallback"
	const messageID = "msg-fallback"
	_ = sm.CreateStream(messageID, sessionID)
	// Subscribe BEFORE emitting so the pump fans the event out to the
	// subscriber chan. Subscribing to the producer chan directly would
	// race the messageStream pump goroutine (which drains produce to
	// assign event IDs + populate the ring buffer + forward to the
	// current subscriber).
	sub, closed, ok := sm.Subscribe(messageID, 0)
	if !ok || closed {
		t.Fatalf("Subscribe: ok=%v closed=%v", ok, closed)
	}

	bridge.emitCLIStructuredInputFallback(sessionID, "AskUserQuestion", "tool-use-123")

	var evt struct {
		Type     string `json:"type"`
		Envelope string `json:"envelope"`
	}
	select {
	case got := <-sub:
		evt.Type = got.Type
		evt.Envelope = got.Envelope
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for plugin_envelope stream event")
	}

	if evt.Type != "plugin_envelope" {
		t.Fatalf("stream event type = %q, want plugin_envelope", evt.Type)
	}
	if evt.Envelope == "" {
		t.Fatal("envelope payload is empty")
	}

	var wrap struct {
		ID           string          `json:"id"`
		Type         string          `json:"type"`
		Data         json.RawMessage `json:"data"`
		DisplayClass string          `json:"display_class"`
	}
	if err := json.Unmarshal([]byte(evt.Envelope), &wrap); err != nil {
		t.Fatalf("unmarshal envelope wrap: %v", err)
	}
	if wrap.Type != cliStructuredInputFallbackEnvelopeType {
		t.Errorf("envelope type = %q, want %q", wrap.Type, cliStructuredInputFallbackEnvelopeType)
	}
	if wrap.DisplayClass != "alert" {
		t.Errorf("envelope display_class = %q, want alert", wrap.DisplayClass)
	}

	var payload cliStructuredInputFallbackPayload
	if err := json.Unmarshal(wrap.Data, &payload); err != nil {
		t.Fatalf("unmarshal info-card payload: %v", err)
	}
	if payload.Variant != "warning" {
		t.Errorf("payload.Variant = %q, want warning", payload.Variant)
	}
	if payload.Title == "" {
		t.Error("payload.Title is empty")
	}
	if !strings.Contains(payload.Body, "AskUserQuestion") {
		t.Errorf("payload.Body should embed the triggering tool name, got %q", payload.Body)
	}
	if !strings.Contains(payload.Body, "CW-20260519-0051") {
		t.Errorf("payload.Body should reference the tracking ID for grep-discoverability, got %q", payload.Body)
	}
}

// TestEmitCLIStructuredInputFallback_NoSessionStreamIsNoOp verifies the
// helper silently drops the broadcast when no message stream is registered
// for the session (matches the BroadcastSessionStreamEvent contract). The
// typed callback fires per-event with no liveness check; we must not panic
// or block when no FE is subscribed.
func TestEmitCLIStructuredInputFallback_NoSessionStreamIsNoOp(t *testing.T) {
	sm := NewStreamManager()
	bridge := &agentEventBridge{streams: sm}

	// No CreateStream — the session has no registered consumer.
	bridge.emitCLIStructuredInputFallback("sess-nobody-home", "AskUserQuestion", "tool-x")
	// Reaching here without panicking is the assertion.
}

// TestEmitCLIStructuredInputFallback_NilStreamsIsNoOp pins the defensive
// guard against a teardown-ordering race where typedCallback fires after
// the bridge's streams reference has been zeroed.
func TestEmitCLIStructuredInputFallback_NilStreamsIsNoOp(t *testing.T) {
	bridge := &agentEventBridge{}
	bridge.emitCLIStructuredInputFallback("sess-x", "AskUserQuestion", "tool-x")
}
