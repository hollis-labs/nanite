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
		{ID: "tc1", Name: "search_kb", Status: "success", HasEnvelope: true},
		{ID: "tc2", Name: "create_task", Status: "error"},
	}
	envData, _ := json.Marshal(map[string]interface{}{"kind": "kb-result", "data": "test"})
	envelopes := []EnvelopeRef{
		{Type: "kb-result", Data: json.RawMessage(envData)},
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
	if first["name"] != "search_kb" {
		t.Errorf("expected first tool name=search_kb, got %v", first["name"])
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
