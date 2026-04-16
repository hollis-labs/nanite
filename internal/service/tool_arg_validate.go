package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/message"
)

// argValidator caches compiled JSON Schemas per tool to avoid re-compilation
// on every call within a turn.
type argValidator struct {
	mu      sync.RWMutex
	schemas map[string]*jsonschema.Schema // toolName → compiled schema
}

func newArgValidator() *argValidator {
	return &argValidator{schemas: make(map[string]*jsonschema.Schema)}
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
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
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
