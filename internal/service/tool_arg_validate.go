package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/message"
)

// argValidator caches compiled JSON Schemas per tool to avoid re-compilation
// on every call. Process-scoped; schemas are invalidated on tool rediscovery
// by clearing the cache (see invalidate).
type argValidator struct {
	mu      sync.RWMutex
	schemas map[string]*jsonschema.Schema // toolName → compiled schema
}

func newArgValidator() *argValidator {
	return &argValidator{schemas: make(map[string]*jsonschema.Schema)}
}

// invalidate clears cached schemas for the given tool names (e.g., after tool
// rediscovery). Pass nil to clear all cached schemas.
func (v *argValidator) invalidate(toolNames []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if toolNames == nil {
		v.schemas = make(map[string]*jsonschema.Schema)
		return
	}
	for _, name := range toolNames {
		delete(v.schemas, name)
	}
}

// validate checks args against the tool's InputSchema. Returns a human-readable
// error string if validation fails, or "" if valid. If the schema is nil or
// cannot be compiled, the call is allowed (with a warning logged).
func (v *argValidator) validate(toolName string, schema map[string]any, args map[string]any) string {
	if len(schema) == 0 {
		return ""
	}

	compiled, err := v.getOrCompile(toolName, schema)
	if err != nil {
		slog.Warn("tool-arg-validate: schema compile error — allowing call",
			"tool", toolName, "err", err)
		return ""
	}

	// CW-20260417-0486: coerce primitive types at the validator boundary before
	// strict schema validation. LLM providers routinely send "5" where the
	// schema declares number/integer, and "true" where boolean is declared.
	// Mutations land in the caller's map so downstream code (permissions,
	// tool execution, telemetry, retry prompts) sees the coerced form.
	if args != nil {
		coercePrimitives(schema, args)
	}

	// Convert args to any for the validator (it expects the value to match
	// the schema root, which is typically type:object).
	var val any = args
	if args == nil {
		val = map[string]any{}
	}

	if err := compiled.Validate(val); err != nil {
		return formatValidationError(toolName, err)
	}
	return ""
}

// coercePrimitives walks the schema's top-level properties and rewrites args
// in place when a primitive-type mismatch has a safe, well-defined conversion:
//
//   - number: numeric-looking string → float64
//   - integer: numeric-looking string with no fractional part → float64; or
//     an integral float64 → unchanged (already valid)
//   - boolean: "true"/"false"/"1"/"0" (case-insensitive) → bool
//
// Lossy or undefined conversions (e.g. "5.5" → integer, "yes" → boolean,
// "abc" → number) are left untouched so the existing validator surfaces
// ARG_VALIDATION_FAILED with the original value preserved for the retry
// message. We deliberately do NOT coerce strings, objects, or arrays.
func coercePrimitives(schema map[string]any, args map[string]any) {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return
	}
	for name, raw := range args {
		propSchema, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		types := extractSchemaTypes(propSchema["type"])
		if len(types) == 0 {
			continue
		}
		if coerced, changed := coerceValue(raw, types); changed {
			args[name] = coerced
		}
	}
}

// extractSchemaTypes normalizes JSON Schema's type field, which may be a
// single string (e.g. "integer") or an array of strings (e.g. ["integer",
// "null"]). Returns the set of candidate types for coercion decisions.
func extractSchemaTypes(raw any) []string {
	switch t := raw.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// coerceValue returns the coerced value and true when a safe conversion
// applies for any of the allowed schema types. Integer coercion is tried
// before number so "5" under ["integer","number"] lands as an integral
// float64 suitable for either.
func coerceValue(raw any, types []string) (any, bool) {
	hasType := func(t string) bool {
		for _, s := range types {
			if s == t {
				return true
			}
		}
		return false
	}

	// Integer first (stricter than number).
	if hasType("integer") {
		if f, ok := toInteger(raw); ok {
			return f, !sameValue(raw, f)
		}
	}
	if hasType("number") {
		if f, ok := toNumber(raw); ok {
			return f, !sameValue(raw, f)
		}
	}
	if hasType("boolean") {
		if b, ok := toBoolean(raw); ok {
			return b, !sameValue(raw, b)
		}
	}
	return raw, false
}

// sameValue reports whether raw already equals the coerced form — used to
// avoid spurious mutations when the input already matched the schema.
func sameValue(raw, coerced any) bool {
	return raw == coerced
}

// toInteger attempts to produce a float64 carrying an integral value suitable
// for an integer-typed JSON Schema slot. Returns (value, ok).
func toInteger(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		if math.Trunc(v) == v && !math.IsInf(v, 0) && !math.IsNaN(v) {
			return v, true
		}
		return 0, false
	case float32:
		f := float64(v)
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return 0, false
		}
		if math.Trunc(f) == f {
			return f, true
		}
		return 0, false
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return float64(i), true
		}
		if f, err := v.Float64(); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) && math.Trunc(f) == f {
			return f, true
		}
		return 0, false
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, false
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return float64(i), true
		}
		// Accept strings like "5.0" only if integral; reject "5.5".
		// strconv.ParseFloat accepts "Inf"/"NaN" — explicitly reject them so
		// non-finite inputs don't slip past integer validation downstream.
		if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) && math.Trunc(f) == f {
			return f, true
		}
		return 0, false
	}
	return 0, false
}

// toNumber attempts to produce a float64 for a number-typed JSON Schema slot.
// Non-finite values (NaN, +Inf, -Inf) are rejected so coercion never widens
// the accepted input set beyond what the schema author intended.
func toNumber(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return 0, false
		}
		return v, true
	case float32:
		f := float64(v)
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return 0, false
		}
		return f, true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		if f, err := v.Float64(); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return f, true
		}
		return 0, false
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, false
		}
		// strconv.ParseFloat accepts "NaN"/"Inf" — explicitly reject them.
		if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return f, true
		}
		return 0, false
	}
	return 0, false
}

// toBoolean accepts "true"/"false"/"1"/"0" (case-insensitive) and existing
// bool values. Fuzzy truth words (yes/no/on/off) are intentionally rejected
// to avoid hiding provider bugs behind lenient conversions.
func toBoolean(raw any) (bool, bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			return true, true
		case "false", "0":
			return false, true
		}
	}
	return false, false
}

// getOrCompile returns a cached compiled schema or compiles and caches it.
func (v *argValidator) getOrCompile(toolName string, schema map[string]any) (*jsonschema.Schema, error) {
	v.mu.RLock()
	if s, ok := v.schemas[toolName]; ok {
		v.mu.RUnlock()
		return s, nil
	}
	v.mu.RUnlock()

	// Compile the schema.
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}

	var raw any
	if err := json.Unmarshal(schemaJSON, &raw); err != nil {
		return nil, fmt.Errorf("decode schema: %w", err)
	}

	uri := fmt.Sprintf("urn:nanite:tool:%s", toolName)
	c := jsonschema.NewCompiler()
	if err := c.AddResource(uri, raw); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	compiled, err := c.Compile(uri)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}

	v.mu.Lock()
	v.schemas[toolName] = compiled
	v.mu.Unlock()
	return compiled, nil
}

// formatValidationError converts a jsonschema validation error into a concise,
// LLM-friendly error message. Does not expose raw library internals.
func formatValidationError(toolName string, err error) string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return fmt.Sprintf("ARG_VALIDATION_FAILED: %s — %s", toolName, err.Error())
	}

	var parts []string
	collectErrors(ve, &parts, 0)
	if len(parts) == 0 {
		return fmt.Sprintf("ARG_VALIDATION_FAILED: %s — invalid arguments", toolName)
	}

	// Cap the number of reported violations to keep the error concise.
	if len(parts) > 5 {
		parts = append(parts[:5], fmt.Sprintf("... and %d more violations", len(parts)-5))
	}

	return fmt.Sprintf("ARG_VALIDATION_FAILED: %s — %s", toolName, strings.Join(parts, "; "))
}

// defaultPrinter is used to format jsonschema validation error messages.
var defaultPrinter = message.NewPrinter(message.MatchLanguage("en"))

// collectErrors recursively extracts leaf validation messages from the error tree.
func collectErrors(ve *jsonschema.ValidationError, parts *[]string, depth int) {
	if depth > 10 {
		return // safety limit
	}
	if len(ve.Causes) == 0 && ve.ErrorKind != nil {
		path := "/" + strings.Join(ve.InstanceLocation, "/")
		msg := ve.ErrorKind.LocalizedString(defaultPrinter)
		*parts = append(*parts, fmt.Sprintf("%s: %s", path, msg))
		return
	}
	for _, child := range ve.Causes {
		collectErrors(child, parts, depth+1)
	}
}
