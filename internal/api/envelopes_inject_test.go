package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestInjectEnvelopePriorResponses(t *testing.T) {
	// A message whose envelope has an id that was already responded to.
	respondedAt := time.Now().UTC()
	inst := &store.EnvelopeInstance{
		ID:           "env-abc",
		EnvelopeJSON: `{"kind":"envelope","version":1,"type":"approval-card","id":"env-abc"}`,
		RespondedAt:  &respondedAt,
		ResponseJSON: `{"v":1,"kind":"approval-card","id":"env-abc","status":"submitted"}`,
	}

	messages := []store.Message{
		{Envelope: `{"kind":"envelope","version":1,"type":"approval-card","id":"env-abc"}`},
		{Envelope: `{"kind":"envelope","version":1,"type":"approval-card","id":"env-xyz"}`}, // no response
		{Envelope: ``}, // no envelope
	}

	lookup := map[string]*store.EnvelopeInstance{
		"env-abc": inst,
	}

	result := injectEnvelopePriorResponses(messages, lookup)

	// First message: must have prior_response injected.
	var env0 map[string]any
	if err := json.Unmarshal([]byte(result[0].Envelope), &env0); err != nil {
		t.Fatalf("failed to parse enriched envelope 0: %v", err)
	}
	if env0["prior_response"] == nil {
		t.Errorf("expected prior_response in first message envelope, got nil")
	}
	pr, ok := env0["prior_response"].(map[string]any)
	if !ok {
		t.Fatalf("prior_response is not an object")
	}
	if pr["status"] != "submitted" {
		t.Errorf("prior_response.status: got %v, want submitted", pr["status"])
	}

	// Second message: no prior_response (no response stored for env-xyz).
	var env1 map[string]any
	if err := json.Unmarshal([]byte(result[1].Envelope), &env1); err != nil {
		t.Fatalf("failed to parse envelope 1: %v", err)
	}
	if env1["prior_response"] != nil {
		t.Errorf("envelope 1 should not have prior_response, got %v", env1["prior_response"])
	}

	// Third message: empty envelope stays empty.
	if result[2].Envelope != `` {
		t.Errorf("empty envelope should be unchanged, got %q", result[2].Envelope)
	}
}
