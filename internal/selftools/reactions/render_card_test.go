package reactions

import (
	"encoding/json"
	"testing"
)

func TestResolveRenderCard_SubstitutesTemplateAndMarshalsEnvelope(t *testing.T) {
	configJSON := `{"envelope_type":"info-card","template":{"title":"Task update","body":"{{msg}}","meta":{"task_id":"{{id}}"}}}`
	payload := map[string]any{"id": "task-1", "msg": "hello"}

	out, err := ResolveRenderCard(configJSON, payload)
	if err != nil {
		t.Fatalf("ResolveRenderCard: %v", err)
	}

	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("output is not valid JSON: %v (%s)", err, out)
	}

	if env["kind"] != "envelope" {
		t.Errorf("kind = %v, want %q", env["kind"], "envelope")
	}
	if v, ok := env["version"].(float64); !ok || v != 1 {
		t.Errorf("version = %v, want 1", env["version"])
	}
	if env["type"] != "info-card" {
		t.Errorf("type = %v, want %q", env["type"], "info-card")
	}

	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not an object: %+v", env)
	}
	if data["title"] != "Task update" {
		t.Errorf("data.title = %v, want %q (untouched literal mangled)", data["title"], "Task update")
	}
	if data["body"] != "hello" {
		t.Errorf("data.body = %v, want %q", data["body"], "hello")
	}
	meta, ok := data["meta"].(map[string]any)
	if !ok {
		t.Fatalf("data.meta is not an object: %+v", data)
	}
	if meta["task_id"] != "task-1" {
		t.Errorf("data.meta.task_id = %v, want %q (nested substitution failed)", meta["task_id"], "task-1")
	}
}

func TestResolveRenderCard_MissingPayloadKey_LeavesPlaceholderLiteral(t *testing.T) {
	out, err := ResolveRenderCard(`{"envelope_type":"info-card","template":{"body":"{{missing}}"}}`, map[string]any{})
	if err != nil {
		t.Fatalf("ResolveRenderCard: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	data := env["data"].(map[string]any)
	if data["body"] != "{{missing}}" {
		t.Errorf("data.body = %v, want literal %q for a payload key that was never supplied", data["body"], "{{missing}}")
	}
}

func TestResolveRenderCard_InvalidConfigJSON(t *testing.T) {
	if _, err := ResolveRenderCard(`not json`, map[string]any{}); err == nil {
		t.Fatal("ResolveRenderCard with invalid JSON config returned nil error, want an error")
	}
}

func TestResolveRenderCard_MissingEnvelopeType(t *testing.T) {
	if _, err := ResolveRenderCard(`{"template":{"body":"x"}}`, map[string]any{}); err == nil {
		t.Fatal("ResolveRenderCard with missing envelope_type returned nil error, want an error")
	}
}
