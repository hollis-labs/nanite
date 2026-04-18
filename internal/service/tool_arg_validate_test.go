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

// --- CW-20260417-0486: primitive coercion at InputSchema boundary --------

// TestArgValidator_CoerceStringToNumber covers the common LLM pattern where
// a numeric param is sent as a JSON string (e.g. "5"). The validator should
// coerce it to a float64 so downstream code sees a consistent arg shape.
func TestArgValidator_CoerceStringToNumber(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": "number"},
		},
	}
	args := map[string]any{"limit": "5"}
	if msg := v.validate("test_tool", schema, args); msg != "" {
		t.Fatalf("expected coercion to pass validation, got: %s", msg)
	}
	got, ok := args["limit"].(float64)
	if !ok {
		t.Fatalf("expected limit to be coerced to float64, got %T (%v)", args["limit"], args["limit"])
	}
	if got != 5.0 {
		t.Errorf("expected limit=5, got %v", got)
	}
}

// TestArgValidator_CoerceStringToInteger covers the "5" -> int case for
// integer-typed params. json.Number / float64 is the canonical Go carrier.
func TestArgValidator_CoerceStringToInteger(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": "integer"},
		},
	}
	args := map[string]any{"limit": "5"}
	if msg := v.validate("test_tool", schema, args); msg != "" {
		t.Fatalf("expected coercion to pass validation, got: %s", msg)
	}
	got, ok := args["limit"].(float64)
	if !ok {
		t.Fatalf("expected limit to be coerced to float64, got %T (%v)", args["limit"], args["limit"])
	}
	if got != 5.0 {
		t.Errorf("expected limit=5, got %v", got)
	}
}

// TestArgValidator_CoerceFloatToInteger covers 5.0 -> integer (integral float).
// A provider that sends number for an integer schema must be normalized.
func TestArgValidator_CoerceFloatToInteger(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": "integer"},
		},
	}
	args := map[string]any{"limit": 5.0}
	if msg := v.validate("test_tool", schema, args); msg != "" {
		t.Fatalf("expected integral float to pass, got: %s", msg)
	}
	got, ok := args["limit"].(float64)
	if !ok {
		t.Fatalf("expected limit to remain float64, got %T (%v)", args["limit"], args["limit"])
	}
	if got != 5.0 {
		t.Errorf("expected limit=5, got %v", got)
	}
}

// TestArgValidator_LossyStringToInteger rejects fractional strings for
// integer params — no silent truncation. Original error format preserved.
func TestArgValidator_LossyStringToInteger(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": "integer"},
		},
	}
	args := map[string]any{"limit": "5.5"}
	msg := v.validate("test_tool", schema, args)
	if msg == "" {
		t.Fatal("expected ARG_VALIDATION_FAILED for lossy 5.5 -> integer")
	}
	if !strings.Contains(msg, "ARG_VALIDATION_FAILED") {
		t.Errorf("expected ARG_VALIDATION_FAILED prefix, got: %s", msg)
	}
}

// TestArgValidator_InvalidStringToNumber rejects non-numeric strings for
// number params.
func TestArgValidator_InvalidStringToNumber(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": "number"},
		},
	}
	args := map[string]any{"limit": "abc"}
	msg := v.validate("test_tool", schema, args)
	if msg == "" {
		t.Fatal("expected ARG_VALIDATION_FAILED for abc -> number")
	}
	if !strings.Contains(msg, "ARG_VALIDATION_FAILED") {
		t.Errorf("expected ARG_VALIDATION_FAILED prefix, got: %s", msg)
	}
}

// TestArgValidator_RejectNonFiniteStrings covers Copilot PR #63 feedback:
// strconv.ParseFloat accepts "NaN"/"Inf"/"+Inf"/"-Inf" without error, and
// without an explicit check those values would slip past coercion and land
// in downstream tool calls. Coerce must treat them as un-coercible so the
// caller gets ARG_VALIDATION_FAILED instead of a surprise Inf/NaN.
func TestArgValidator_RejectNonFiniteStrings(t *testing.T) {
	v := newArgValidator()
	for _, tc := range []struct {
		typ string
		in  string
	}{
		{"number", "Inf"},
		{"number", "+Inf"},
		{"number", "-Inf"},
		{"number", "NaN"},
		{"integer", "Inf"},
		{"integer", "NaN"},
	} {
		tc := tc
		t.Run(tc.typ+"_"+tc.in, func(t *testing.T) {
			schema := map[string]any{
				"type": "object",
				"properties": map[string]any{
					"v": map[string]any{"type": tc.typ},
				},
			}
			args := map[string]any{"v": tc.in}
			msg := v.validate("test_tool", schema, args)
			if msg == "" {
				t.Fatalf("expected ARG_VALIDATION_FAILED for %q -> %s", tc.in, tc.typ)
			}
			if _, coercedToFloat := args["v"].(float64); coercedToFloat {
				t.Errorf("value was silently coerced to float64 despite being non-finite: %v", args["v"])
			}
		})
	}
}

// TestArgValidator_CoerceStringToBool covers "true"/"false" (case-insensitive)
// and "1"/"0" forms.
func TestArgValidator_CoerceStringToBool(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"false", false},
		{"False", false},
		{"FALSE", false},
		{"1", true},
		{"0", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			v := newArgValidator()
			schema := map[string]any{
				"type": "object",
				"properties": map[string]any{
					"flag": map[string]any{"type": "boolean"},
				},
			}
			args := map[string]any{"flag": tc.in}
			if msg := v.validate("test_tool", schema, args); msg != "" {
				t.Fatalf("expected coercion to pass validation, got: %s", msg)
			}
			got, ok := args["flag"].(bool)
			if !ok {
				t.Fatalf("expected flag to be coerced to bool, got %T (%v)", args["flag"], args["flag"])
			}
			if got != tc.want {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

// TestArgValidator_InvalidStringToBool rejects fuzzy truth words like "yes".
// Only explicit true/false/1/0 (case-insensitive) are accepted.
func TestArgValidator_InvalidStringToBool(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"flag": map[string]any{"type": "boolean"},
		},
	}
	args := map[string]any{"flag": "yes"}
	msg := v.validate("test_tool", schema, args)
	if msg == "" {
		t.Fatal("expected ARG_VALIDATION_FAILED for yes -> boolean")
	}
	if !strings.Contains(msg, "ARG_VALIDATION_FAILED") {
		t.Errorf("expected ARG_VALIDATION_FAILED prefix, got: %s", msg)
	}
}

// TestArgValidator_NumberToStringPassThrough documents the decision: we do
// NOT coerce numbers to strings — the schema violation still surfaces. This
// avoids hiding legitimate provider bugs behind a lossy stringification.
func TestArgValidator_NumberToStringPassThrough(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
	args := map[string]any{"name": 5}
	msg := v.validate("test_tool", schema, args)
	if msg == "" {
		t.Fatal("expected number -> string to fail (no coercion in this direction)")
	}
	if !strings.Contains(msg, "ARG_VALIDATION_FAILED") {
		t.Errorf("expected ARG_VALIDATION_FAILED prefix, got: %s", msg)
	}
}

// TestArgValidator_StringToObjectNoCoerce: we scope coercion strictly to
// primitives. Non-primitive targets fall through to normal validation.
func TestArgValidator_StringToObjectNoCoerce(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"obj": map[string]any{"type": "object"},
		},
	}
	args := map[string]any{"obj": "5"}
	msg := v.validate("test_tool", schema, args)
	if msg == "" {
		t.Fatal("expected string -> object to fail (no coercion)")
	}
	if !strings.Contains(msg, "ARG_VALIDATION_FAILED") {
		t.Errorf("expected ARG_VALIDATION_FAILED prefix, got: %s", msg)
	}
}

// TestArgValidator_CoerceMutatesCallerMap confirms that coerced values
// propagate to the caller's args map — downstream code (permissions, tool
// execution, telemetry) must see the coerced form, not the raw string.
func TestArgValidator_CoerceMutatesCallerMap(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": "number"},
			"flag":  map[string]any{"type": "boolean"},
		},
	}
	args := map[string]any{"limit": "42", "flag": "true"}
	// Keep a reference that mimics how the executor hangs on to tu.Input.
	caller := args
	if msg := v.validate("test_tool", schema, args); msg != "" {
		t.Fatalf("expected valid, got: %s", msg)
	}
	if got, ok := caller["limit"].(float64); !ok || got != 42.0 {
		t.Errorf("expected caller to see limit=42 as float64, got %T (%v)", caller["limit"], caller["limit"])
	}
	if got, ok := caller["flag"].(bool); !ok || !got {
		t.Errorf("expected caller to see flag=true as bool, got %T (%v)", caller["flag"], caller["flag"])
	}
}

// TestArgValidator_CoerceAcrossMultipleTypes covers mixed args — some coerced,
// some passing cleanly, some untouched (non-coerceable types).
func TestArgValidator_CoerceAcrossMultipleTypes(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit":  map[string]any{"type": "integer"},
			"ratio":  map[string]any{"type": "number"},
			"active": map[string]any{"type": "boolean"},
			"name":   map[string]any{"type": "string"},
		},
	}
	args := map[string]any{
		"limit":  "10",
		"ratio":  "3.14",
		"active": "0",
		"name":   "conduit",
	}
	if msg := v.validate("test_tool", schema, args); msg != "" {
		t.Fatalf("expected valid, got: %s", msg)
	}
	if v, ok := args["limit"].(float64); !ok || v != 10.0 {
		t.Errorf("limit: expected 10 float64, got %T %v", args["limit"], args["limit"])
	}
	if v, ok := args["ratio"].(float64); !ok || v != 3.14 {
		t.Errorf("ratio: expected 3.14 float64, got %T %v", args["ratio"], args["ratio"])
	}
	if v, ok := args["active"].(bool); !ok || v != false {
		t.Errorf("active: expected false bool, got %T %v", args["active"], args["active"])
	}
	if v, ok := args["name"].(string); !ok || v != "conduit" {
		t.Errorf("name: expected 'conduit', got %T %v", args["name"], args["name"])
	}
}

// TestArgValidator_CoerceLeavesInvalidArgsUntouched: when coercion fails and
// validation reports an error, the original value should remain in place
// (so the LLM sees what it actually sent in the subsequent retry message).
func TestArgValidator_CoerceLeavesInvalidArgsUntouched(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": "number"},
		},
	}
	args := map[string]any{"limit": "abc"}
	if msg := v.validate("test_tool", schema, args); msg == "" {
		t.Fatal("expected validation failure")
	}
	if got, ok := args["limit"].(string); !ok || got != "abc" {
		t.Errorf("expected original value preserved on failure, got %T %v", args["limit"], args["limit"])
	}
}

// TestArgValidator_UnionTypeCoercion: when type is ["integer","null"] we still
// coerce numeric-looking strings to integer.
func TestArgValidator_UnionTypeCoercion(t *testing.T) {
	v := newArgValidator()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"limit": map[string]any{"type": []any{"integer", "null"}},
		},
	}
	args := map[string]any{"limit": "7"}
	if msg := v.validate("test_tool", schema, args); msg != "" {
		t.Fatalf("expected valid, got: %s", msg)
	}
	if got, ok := args["limit"].(float64); !ok || got != 7.0 {
		t.Errorf("expected limit=7 float64, got %T %v", args["limit"], args["limit"])
	}
}
