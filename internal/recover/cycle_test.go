package recover

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/third_party/jsonschema"
)

// A host tool whose schema refers back to itself fails each call with a clear
// error instead of overflowing the stack inside validation.
func TestCyclicToolSchemaFailsTheCallNotTheProcess(t *testing.T) {
	v := NewValidator(json.RawMessage(`{"type":"object","allOf":[{"$ref":"#"}]}`))
	err := v.Validate(t.Context(), map[string]any{"x": 1})
	if err == nil || !strings.Contains(err.Error(), "invalid tool schema") || !strings.Contains(err.Error(), "refers back to itself") {
		t.Fatalf("Validate = %v", err)
	}
}

// Tool input nested past the limit is refused before validation recurses on it.
func TestDeepToolInputIsRefused(t *testing.T) {
	v := NewValidator(json.RawMessage(`{"type":"object","properties":{"x":{"$ref":"#/$defs/a"}},"$defs":{"a":{"type":"array","items":{"$ref":"#/$defs/a"}}}}`))
	var deep any = []any{}
	for range 5000 {
		deep = []any{deep}
	}
	err := v.Validate(t.Context(), map[string]any{"x": deep})
	if err == nil || !strings.Contains(err.Error(), "nests too deeply") {
		t.Fatalf("Validate = %v", err)
	}
	if v.Classify(err) {
		t.Fatal("deep input must not go to argument repair")
	}
}

// Validation that ran out of budget or time is not sent to argument repair.
func TestBudgetAndContextErrorsAreNotRepairable(t *testing.T) {
	v := NewValidator(json.RawMessage(`{"type":"object","properties":{"x":{"$ref":"#/$defs/a"}},"$defs":{"a":{"allOf":[{"type":"array","items":{"$ref":"#/$defs/a"}},{"type":"array","items":{"$ref":"#/$defs/a"}}]}}}`))
	var deep any = []any{}
	for range 40 {
		deep = []any{deep}
	}
	err := v.Validate(t.Context(), map[string]any{"x": deep})
	if !errors.Is(err, jsonschema.ErrBudget) {
		t.Fatalf("Validate = %v", err)
	}
	if v.Classify(err) {
		t.Fatal("a budget error must not go to argument repair")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = v.Validate(ctx, map[string]any{"x": []any{[]any{}, []any{}}})
	if !errors.Is(err, context.Canceled) || v.Classify(err) {
		t.Fatalf("cancelled: %v, repairable %v", err, v.Classify(err))
	}
	if !v.Classify(v.Validate(t.Context(), map[string]any{"x": "not an array"})) {
		t.Fatal("an ordinary invalid argument should still be repairable")
	}
}
