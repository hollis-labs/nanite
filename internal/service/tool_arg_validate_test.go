package service

import (
	"strings"
	"testing"
)

func TestArgValidator_ValidInput(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	}
	args := map[string]any{"path": "/foo/bar.go"}
	if msg := v.validate("dev_read", schema, args); msg != "" {
		t.Errorf("expected valid, got: %s", msg)
	}
}

func TestArgValidator_MissingRequired(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
	}
	args := map[string]any{"other": "value"}
	msg := v.validate("dev_read", schema, args)
	if msg == "" {
		t.Fatal("expected validation error for missing required field")
	}
	if !strings.Contains(msg, "ARG_VALIDATION_FAILED") {
		t.Errorf("expected ARG_VALIDATION_FAILED prefix, got: %s", msg)
	}
	if !strings.Contains(msg, "dev_read") {
		t.Errorf("expected tool name in error, got: %s", msg)
	}
}

func TestArgValidator_WrongType(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
	}
	args := map[string]any{"count": "not_a_number"}
	msg := v.validate("test_tool", schema, args)
	if msg == "" {
		t.Fatal("expected validation error for wrong type")
	}
	if !strings.Contains(msg, "ARG_VALIDATION_FAILED") {
		t.Errorf("expected ARG_VALIDATION_FAILED, got: %s", msg)
	}
}

func TestArgValidator_NoSchema(t *testing.T) {
	v := newArgValidator()
	// nil schema should pass (back-compat).
	if msg := v.validate("no_schema_tool", nil, map[string]any{"any": "value"}); msg != "" {
		t.Errorf("nil schema should pass, got: %s", msg)
	}
	// Empty schema should pass.
	if msg := v.validate("empty_schema_tool", map[string]any{}, map[string]any{"any": "value"}); msg != "" {
		t.Errorf("empty schema should pass, got: %s", msg)
	}
}

func TestArgValidator_NilArgs(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
	}
	// nil args should be treated as empty object.
	if msg := v.validate("test_tool", schema, nil); msg != "" {
		t.Errorf("nil args with type:object schema should pass, got: %s", msg)
	}
}

func TestArgValidator_CachesSchema(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
	// First call compiles.
	v.validate("cached_tool", schema, map[string]any{"name": "test"})
	// Second call uses cache.
	v.validate("cached_tool", schema, map[string]any{"name": "test2"})

	v.mu.RLock()
	_, cached := v.schemas["cached_tool"]
	v.mu.RUnlock()
	if !cached {
		t.Error("schema should be cached after first validation")
	}
}

func TestArgValidator_MultipleViolations(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"count": map[string]any{"type": "integer"},
		},
		"required": []any{"name", "count"},
	}
	args := map[string]any{} // missing both required fields
	msg := v.validate("multi_tool", schema, args)
	if msg == "" {
		t.Fatal("expected validation errors")
	}
	if !strings.Contains(msg, "ARG_VALIDATION_FAILED") {
		t.Errorf("expected prefix, got: %s", msg)
	}
}
