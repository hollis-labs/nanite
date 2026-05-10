package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/runtime/agent/recovery"
)

// TestProjectRecoveryEnvelope_KindMapping pins the wire shape per
// recovery envelope Kind. This is the contract the FE EnvelopeRenderer
// reads (no FE schema landing required) — drift here is a wire-break.
func TestProjectRecoveryEnvelope_KindMapping(t *testing.T) {
	cases := []struct {
		name             string
		env              recovery.Envelope
		wantType         string
		wantDataContains map[string]any
	}{
		{
			name: "info-card transient retry",
			env: recovery.Envelope{
				Kind:        "info-card",
				Title:       "Reconnecting agent",
				Content:     "Agent ran into a temporary error. Retrying now…",
				Severity:    "info",
				CancelToken: "ignored-here", // wrap-level, not in data
			},
			wantType: "info-card",
			wantDataContains: map[string]any{
				"title":   "Reconnecting agent",
				"body":    "Agent ran into a temporary error. Retrying now…",
				"variant": "info",
			},
		},
		{
			name: "info-card warning severity",
			env: recovery.Envelope{
				Kind:     "info-card",
				Title:    "Slow recovery",
				Content:  "Agent is slow to respond.",
				Severity: "warning",
			},
			wantType: "info-card",
			wantDataContains: map[string]any{
				"title":   "Slow recovery",
				"body":    "Agent is slow to respond.",
				"variant": "warning",
			},
		},
		{
			name: "info-card error severity maps to danger",
			env: recovery.Envelope{
				Kind:     "info-card",
				Title:    "Stuck",
				Content:  "Agent appears stuck.",
				Severity: "error",
			},
			wantType: "info-card",
			wantDataContains: map[string]any{
				"variant": "danger",
			},
		},
		{
			name: "error-report permanent failure",
			env: recovery.Envelope{
				Kind:     "error-report",
				Title:    "Agent unavailable",
				Content:  "Exit code 127: agent binary not found on PATH. Try a different agent.",
				Severity: "error",
			},
			wantType: "error-report",
			wantDataContains: map[string]any{
				"code": "agent_recovery_failed",
			},
		},
		{
			name: "chat-loop-terminated runaway",
			env: recovery.Envelope{
				Kind:     "chat-loop-terminated",
				Title:    "Agent stopped",
				Content:  "The agent kept failing after multiple restart attempts.",
				Severity: "error",
			},
			wantType: "chat-loop-terminated",
			wantDataContains: map[string]any{
				"code": "retry_budget_exhausted",
			},
		},
		{
			name:     "missing kind degrades to error-report",
			env:      recovery.Envelope{Title: "X"},
			wantType: "error-report",
			wantDataContains: map[string]any{
				"code": "agent_recovery_internal_error",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotType, gotPayload, err := projectRecoveryEnvelope(tc.env)
			if err != nil {
				t.Fatalf("projectRecoveryEnvelope: %v", err)
			}
			if gotType != tc.wantType {
				t.Errorf("type = %q, want %q", gotType, tc.wantType)
			}
			var data map[string]any
			if err := json.Unmarshal(gotPayload, &data); err != nil {
				t.Fatalf("payload unmarshal: %v", err)
			}
			for k, v := range tc.wantDataContains {
				if data[k] != v {
					t.Errorf("data[%q] = %v, want %v", k, data[k], v)
				}
			}
		})
	}
}

// TestProjectRecoveryEnvelope_UnknownKindError ensures unrecognised
// kinds surface as an error rather than silently producing an empty
// envelope (defends against typos drifting into the wire).
func TestProjectRecoveryEnvelope_UnknownKindError(t *testing.T) {
	_, _, err := projectRecoveryEnvelope(recovery.Envelope{Kind: "made-up-kind"})
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
}

// TestBuildRecoveryEnvelopeWrap_CancelTokenPresenceDrivesWireField
// covers both "cancel_token threads through to wire when non-empty" and
// "absent when empty". These are the two cases the FE relies on for
// [Cancel retry] visibility.
func TestBuildRecoveryEnvelopeWrap_CancelTokenPresenceDrivesWireField(t *testing.T) {
	payload := []byte(`{"title":"x","body":"y","variant":"info"}`)

	t.Run("token present", func(t *testing.T) {
		wrap, err := buildRecoveryEnvelopeWrap("info-card", payload, "tok-abc")
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(wrap, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got["cancel_token"] != "tok-abc" {
			t.Errorf("cancel_token = %v, want tok-abc", got["cancel_token"])
		}
		if got["type"] != "info-card" {
			t.Errorf("type = %v, want info-card", got["type"])
		}
		if got["id"] != "" {
			t.Errorf("id = %v, want \"\" (stream-only)", got["id"])
		}
	})

	t.Run("token absent omitted", func(t *testing.T) {
		wrap, err := buildRecoveryEnvelopeWrap("error-report", payload, "")
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(wrap, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if _, ok := got["cancel_token"]; ok {
			t.Errorf("cancel_token should be omitted when empty, got %v", got["cancel_token"])
		}
	})
}

// TestRecoveryEnvelopeSink_EmitsPluginEnvelopeOnSSE is the integration
// pass: a recovery.Envelope through the production sink lands as a
// plugin_envelope SSE event with the projected payload + cancel_token
// at wrap level. Validates end-to-end broker → sink → SSE channel.
func TestRecoveryEnvelopeSink_EmitsPluginEnvelopeOnSSE(t *testing.T) {
	sm := NewStreamManager()
	sessionID := "sess-recov-1"
	produce := sm.CreateStream("msg-1", sessionID)
	defer close(produce)
	ch, _, ok := sm.Subscribe("msg-1", 0)
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	sink := &recoveryEnvelopeSink{streams: sm}
	if err := sink.Emit(sessionID, recovery.Envelope{
		Kind:        "info-card",
		Title:       "Reconnecting agent",
		Content:     "Retrying now…",
		Severity:    "info",
		CancelToken: "tok-deadbeef",
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	select {
	case evt := <-ch:
		if evt.Type != "plugin_envelope" {
			t.Fatalf("event type = %q, want plugin_envelope", evt.Type)
		}
		var wrap struct {
			ID          string          `json:"id"`
			Type        string          `json:"type"`
			Data        json.RawMessage `json:"data"`
			CancelToken string          `json:"cancel_token,omitempty"`
		}
		if err := json.Unmarshal([]byte(evt.Envelope), &wrap); err != nil {
			t.Fatalf("unmarshal wrap: %v", err)
		}
		if wrap.Type != "info-card" {
			t.Errorf("wrap.type = %q, want info-card", wrap.Type)
		}
		if wrap.CancelToken != "tok-deadbeef" {
			t.Errorf("wrap.cancel_token = %q, want tok-deadbeef", wrap.CancelToken)
		}
		if wrap.ID != "" {
			t.Errorf("wrap.id = %q, want \"\" (stream-only)", wrap.ID)
		}
		var data map[string]any
		if err := json.Unmarshal(wrap.Data, &data); err != nil {
			t.Fatalf("unmarshal data: %v", err)
		}
		if data["variant"] != "info" {
			t.Errorf("data.variant = %v, want info", data["variant"])
		}
		if !strings.Contains(data["body"].(string), "Retrying") {
			t.Errorf("data.body = %v, missing 'Retrying'", data["body"])
		}
	case <-time.After(time.Second):
		t.Fatal("no SSE event delivered within 1s")
	}
}

// TestRecoveryEnvelopeSink_NilStreamsIsBenign covers the boot-degraded
// path: a sink with no StreamManager (composition root failed to wire
// streams or the field is nil in a test) returns nil without panicking.
// The broker treats Emit errors as advisory anyway, but a panic would
// take down the recovery flow entirely.
func TestRecoveryEnvelopeSink_NilStreamsIsBenign(t *testing.T) {
	sink := &recoveryEnvelopeSink{streams: nil}
	if err := sink.Emit("s", recovery.Envelope{Kind: "info-card", Title: "x", Content: "y"}); err != nil {
		t.Errorf("Emit with nil streams: %v", err)
	}
}

// TestVariantFromSeverity pins the Severity → variant map. Every
// supported severity must map onto a value the info-card schema's
// variant enum (info/success/warning/danger) accepts.
func TestVariantFromSeverity(t *testing.T) {
	cases := map[string]string{
		"info":       "info",
		"warning":    "warning",
		"error":      "danger",
		"":           "info", // default fallback
		"unexpected": "info", // unknown → safe default
	}
	for in, want := range cases {
		if got := variantFromSeverity(in); got != want {
			t.Errorf("variantFromSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}
