package chat

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
)

func TestWrapResponseProducesValidJSON(t *testing.T) {
	msg := WrapResponse("Hello world", "default", nil, nil, false, false)
	data := msg.MarshalContent()
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("MarshalContent produced invalid JSON: %v", err)
	}
	v, ok := parsed["v"]
	if !ok {
		t.Fatal("missing 'v' field")
	}
	if int(v.(float64)) != 1 {
		t.Fatalf("expected v=1, got %v", v)
	}
}

func TestHashConsistency(t *testing.T) {
	text := "deterministic hash test"
	msg1 := WrapResponse(text, "default", nil, nil, false, false)
	msg2 := WrapResponse(text, "default", nil, nil, false, false)
	if msg1.Hash != msg2.Hash {
		t.Fatalf("hash mismatch: %s != %s", msg1.Hash, msg2.Hash)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
	if msg1.Hash != expected {
		t.Fatalf("hash does not match sha256: got %s, want %s", msg1.Hash, expected)
	}
}

func TestNilSlicesOmitted(t *testing.T) {
	msg := WrapResponse("text", "default", nil, nil, false, false)
	data := msg.MarshalContent()
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := parsed["envelopes"]; ok {
		t.Error("nil envelopes should be omitted, but was present")
	}
	if _, ok := parsed["tool_calls"]; ok {
		t.Error("nil tool_calls should be omitted, but was present")
	}
}

func TestTier0Minimal(t *testing.T) {
	msg := WrapResponse("simple response", "default", nil, nil, false, false)
	data := msg.MarshalContent()
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["tier"] != "default" {
		t.Errorf("expected tier=default, got %v", parsed["tier"])
	}
	if parsed["text"] != "simple response" {
		t.Errorf("expected text='simple response', got %v", parsed["text"])
	}
	// Should have: v, text, tier, hash, flags — no envelopes or tool_calls.
	if _, ok := parsed["envelopes"]; ok {
		t.Error("tier 0 should not have envelopes")
	}
	if _, ok := parsed["tool_calls"]; ok {
		t.Error("tier 0 should not have tool_calls")
	}
}

func TestTier1WithToolCallsAndEnvelopes(t *testing.T) {
	toolCalls := []ToolCallRef{
		{ID: "tc1", Name: "card_show", Status: "success", HasEnvelope: true},
		{ID: "tc2", Name: "create_task", Status: "error"},
	}
	envData, _ := json.Marshal(map[string]interface{}{"kind": "metric-card", "data": "test"})
	envelopes := []EnvelopeRef{
		{Type: "metric-card", Data: json.RawMessage(envData)},
	}
	msg := WrapResponse("response with tools", "tool", toolCalls, envelopes, true, true)
	data := msg.MarshalContent()

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["tier"] != "tool" {
		t.Errorf("expected tier=tool, got %v", parsed["tier"])
	}

	tc, ok := parsed["tool_calls"].([]interface{})
	if !ok || len(tc) != 2 {
		t.Fatalf("expected 2 tool_calls, got %v", parsed["tool_calls"])
	}
	first := tc[0].(map[string]interface{})
	if first["name"] != "card_show" {
		t.Errorf("expected first tool name=card_show, got %v", first["name"])
	}
	if first["has_envelope"] != true {
		t.Errorf("expected has_envelope=true, got %v", first["has_envelope"])
	}

	env, ok := parsed["envelopes"].([]interface{})
	if !ok || len(env) != 1 {
		t.Fatalf("expected 1 envelope, got %v", parsed["envelopes"])
	}

	flags := parsed["flags"].(map[string]interface{})
	if flags["truncated"] != true {
		t.Errorf("expected truncated=true, got %v", flags["truncated"])
	}
	if flags["has_error"] != true {
		t.Errorf("expected has_error=true, got %v", flags["has_error"])
	}
}

func TestValidateStructured(t *testing.T) {
	// Valid message — no warnings.
	msg := WrapResponse("hello", "default", nil, nil, false, false)
	if warnings := ValidateStructured(msg); len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}

	// Unknown version.
	msg.Version = 99
	warnings := ValidateStructured(msg)
	if len(warnings) == 0 {
		t.Error("expected warning for unknown version")
	}

	// Unknown tier.
	msg.Version = 1
	msg.Tier = "unknown"
	warnings = ValidateStructured(msg)
	if len(warnings) == 0 {
		t.Error("expected warning for unknown tier")
	}

	// Hash mismatch.
	msg.Tier = "default"
	msg.Hash = "badhash"
	warnings = ValidateStructured(msg)
	found := false
	for _, w := range warnings {
		if w == "hash mismatch — content may be corrupted" {
			found = true
		}
	}
	if !found {
		t.Error("expected hash mismatch warning")
	}
}

func TestEmptyTextNoHash(t *testing.T) {
	msg := WrapResponse("", "default", nil, nil, false, false)
	if msg.Hash != "" {
		t.Errorf("expected no hash for empty text, got %s", msg.Hash)
	}
}

// TestEnvelopeRefRoundTripsRoutingFields covers CW-20260429-0019: the persisted
// EnvelopeRef shape must carry render_target, target, mode,
// render_target_blocked, id, title, subtitle so that on page reload the FE can
// route a card to the correct drawer/panel. Prior to this fix the projection
// dropped every routing hint and report-card etc. always rendered inline.
func TestEnvelopeRefRoundTripsRoutingFields(t *testing.T) {
	innerData, err := json.Marshal(map[string]any{
		"title":   "Status report",
		"metrics": []any{map[string]any{"label": "Tasks", "value": 7}},
	})
	if err != nil {
		t.Fatalf("marshal inner data: %v", err)
	}

	envelopes := []EnvelopeRef{
		{
			Type:                "report-card",
			Data:                json.RawMessage(innerData),
			ID:                  "env-123",
			Title:               "April status",
			Subtitle:            "Sprint phase F",
			Target:              "bottom_chat_drawer",
			Mode:                "planning",
			RenderTarget:        "bottom_chat_drawer",
			RenderTargetBlocked: "untrusted_plugin_panel",
		},
	}
	msg := WrapResponse("here is the report", "tool", nil, envelopes, false, false)

	wireJSON := msg.MarshalContent()

	// Spot-check the on-wire form retains the routing keys verbatim.
	var raw map[string]any
	if err := json.Unmarshal([]byte(wireJSON), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	envs, ok := raw["envelopes"].([]any)
	if !ok || len(envs) != 1 {
		t.Fatalf("expected 1 envelope in wire form, got %v", raw["envelopes"])
	}
	first, ok := envs[0].(map[string]any)
	if !ok {
		t.Fatalf("envelope[0] is not an object: %T", envs[0])
	}
	wantKeys := map[string]string{
		"render_target":         "bottom_chat_drawer",
		"render_target_blocked": "untrusted_plugin_panel",
		"target":                "bottom_chat_drawer",
		"mode":                  "planning",
		"id":                    "env-123",
		"title":                 "April status",
		"subtitle":              "Sprint phase F",
	}
	for key, want := range wantKeys {
		got, present := first[key]
		if !present {
			t.Errorf("wire envelope missing key %q (full envelope: %v)", key, first)
			continue
		}
		if got != want {
			t.Errorf("wire envelope[%q] = %v, want %v", key, got, want)
		}
	}

	// Round-trip: unmarshal back into StructuredMessage and verify the typed
	// fields are recovered.
	var decoded StructuredMessage
	if err := json.Unmarshal([]byte(wireJSON), &decoded); err != nil {
		t.Fatalf("unmarshal StructuredMessage: %v", err)
	}
	if len(decoded.Envelopes) != 1 {
		t.Fatalf("expected 1 envelope after unmarshal, got %d", len(decoded.Envelopes))
	}
	got := decoded.Envelopes[0]
	if got.RenderTarget != "bottom_chat_drawer" {
		t.Errorf("RenderTarget = %q, want bottom_chat_drawer", got.RenderTarget)
	}
	if got.RenderTargetBlocked != "untrusted_plugin_panel" {
		t.Errorf("RenderTargetBlocked = %q, want untrusted_plugin_panel", got.RenderTargetBlocked)
	}
	if got.Target != "bottom_chat_drawer" {
		t.Errorf("Target = %q, want bottom_chat_drawer", got.Target)
	}
	if got.Mode != "planning" {
		t.Errorf("Mode = %q, want planning", got.Mode)
	}
	if got.ID != "env-123" {
		t.Errorf("ID = %q, want env-123", got.ID)
	}
	if got.Title != "April status" {
		t.Errorf("Title = %q, want April status", got.Title)
	}
	if got.Subtitle != "Sprint phase F" {
		t.Errorf("Subtitle = %q, want Sprint phase F", got.Subtitle)
	}
}

// TestEnvelopeRefOmitsEmptyRoutingFields verifies the omitempty contract:
// an EnvelopeRef with no routing hints set should serialize to just {type, data}
// — no empty render_target / target / mode keys should appear. This matters
// because we don't want to bloat every persisted message with empty strings.
func TestEnvelopeRefOmitsEmptyRoutingFields(t *testing.T) {
	innerData, _ := json.Marshal(map[string]any{"text": "hi"})
	envelopes := []EnvelopeRef{
		{Type: "info-card", Data: json.RawMessage(innerData)},
	}
	msg := WrapResponse("plain", "tool", nil, envelopes, false, false)
	wireJSON := msg.MarshalContent()

	var raw map[string]any
	if err := json.Unmarshal([]byte(wireJSON), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	envs := raw["envelopes"].([]any)
	first := envs[0].(map[string]any)
	for _, key := range []string{"render_target", "render_target_blocked", "target", "mode", "id", "title", "subtitle"} {
		if _, present := first[key]; present {
			t.Errorf("expected key %q to be omitted when empty, but it was present (full envelope: %v)", key, first)
		}
	}
}
