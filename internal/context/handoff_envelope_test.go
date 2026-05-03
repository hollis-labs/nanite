package context

import (
	"errors"
	"strings"
	"testing"
)

func TestMarshalHandoffEnvelope_RoundTrip(t *testing.T) {
	in := HandoffPayload{
		SessionIntent:  "Glass-4 keystone implementation",
		NextStepAnchor: "run E2E test next",
		RecentDecisions: []string{
			"Use deterministic fallback for at-compaction stash",
			"Skip P7 when long-running",
		},
		ActivePointers: []HandoffPointer{
			{CacheKey: "abc123", Label: "implementer-report", Purpose: "Latest progress notes"},
		},
	}

	bytes, err := MarshalHandoffEnvelope(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(bytes), `"schema":"glass-4"`) {
		t.Errorf("envelope missing glass-4 schema discriminator: %s", string(bytes))
	}

	out, err := ParseHandoffEnvelope(bytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if out.SessionIntent != in.SessionIntent {
		t.Errorf("round-trip session_intent mismatch: got %q want %q", out.SessionIntent, in.SessionIntent)
	}
	if out.NextStepAnchor != in.NextStepAnchor {
		t.Errorf("round-trip next_step_anchor mismatch")
	}
	if len(out.RecentDecisions) != 2 {
		t.Errorf("round-trip recent_decisions length mismatch")
	}
	if len(out.ActivePointers) != 1 || out.ActivePointers[0].CacheKey != "abc123" {
		t.Errorf("round-trip active_pointers mismatch")
	}
}

func TestParseHandoffEnvelope_RejectsLegacyP7(t *testing.T) {
	// P7 payload shape — no schema field.
	legacy := []byte(`{"decisions_locked":["a"],"open_questions":["b"]}`)
	_, err := ParseHandoffEnvelope(legacy)
	if !errors.Is(err, ErrHandoffEnvelopeWrongSchema) {
		t.Errorf("expected ErrHandoffEnvelopeWrongSchema for legacy P7 payload, got %v", err)
	}
}

func TestParseHandoffEnvelope_RejectsUnknownSchema(t *testing.T) {
	future := []byte(`{"schema":"glass-99","payload":{}}`)
	_, err := ParseHandoffEnvelope(future)
	if !errors.Is(err, ErrHandoffEnvelopeWrongSchema) {
		t.Errorf("expected ErrHandoffEnvelopeWrongSchema for future schema, got %v", err)
	}
}

func TestMarshalHandoffEnvelope_RejectsInvalidPayload(t *testing.T) {
	// Missing required field next_step_anchor.
	bad := HandoffPayload{SessionIntent: "x"}
	_, err := MarshalHandoffEnvelope(bad)
	if !errors.Is(err, ErrHandoffMissingField) {
		t.Errorf("expected ErrHandoffMissingField, got %v", err)
	}
}

func TestRenderHandoffForSlot_HasAllExpectedSections(t *testing.T) {
	payload := HandoffPayload{
		SessionIntent:   "long-running",
		NextStepAnchor:  "verify the compaction transcript",
		RecentDecisions: []string{"locked schema discriminator", "skip P7 stash on long-running"},
		ActivePointers: []HandoffPointer{
			{CacheKey: "k1", Label: "report", Purpose: "implementer notes"},
		},
	}
	out := RenderHandoffForSlot(payload)

	wants := []string{
		"handoff from before compaction is loaded",
		"long-running",
		"Recent decisions:",
		"locked schema discriminator",
		"Active pointers",
		"handoff_pointers_expand",
		"k1",
		"Next step anchor:",
		"verify the compaction transcript",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("rendered slot missing %q\nrendered:\n%s", w, out)
		}
	}
}
