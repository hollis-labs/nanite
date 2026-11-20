package elicitation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hollis-labs/nanite/internal/third_party/jsonschema"
)

// Resolve compiles a local-only JSON schema. It refuses, with
// ErrUnsafeSchema, a schema validation could not finish on (see
// checkResolved), one over MaxToolSchemaBytes, and one whose JSON nests more
// than MaxSchemaNesting levels. Results are cached by content.
func Resolve(raw json.RawMessage) (*jsonschema.Resolved, error) {
	if len(raw) > MaxToolSchemaBytes {
		return nil, fmt.Errorf("%w: %d bytes; the limit is %d", ErrUnsafeSchema, len(raw), MaxToolSchemaBytes)
	}
	return cachedResolve(raw, func() (*jsonschema.Resolved, error) {
		if n := jsonNesting(raw); n > MaxSchemaNesting {
			return nil, fmt.Errorf("%w: it nests %d levels; the limit is %d", ErrUnsafeSchema, n, MaxSchemaNesting)
		}
		var schema jsonschema.Schema
		if err := json.Unmarshal(raw, &schema); err != nil {
			return nil, err
		}
		if err := checkPatterns(&schema); err != nil {
			return nil, err
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			return nil, err
		}
		if err := checkResolved(resolved); err != nil {
			return nil, err
		}
		return resolved, nil
	})
}

// ValidateResponse adapts legacy string/boolean question validation to JSON
// answers. Null represents a declined or cancelled prompt.
func ValidateResponse(ctx context.Context, schema *jsonschema.Resolved, value json.RawMessage) error {
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil
	}
	var answer any
	dec := json.NewDecoder(bytes.NewReader(value))
	if err := dec.Decode(&answer); err != nil {
		return fmt.Errorf("parse answer: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("answer must contain one JSON value")
	}
	if s, ok := answer.(string); ok && strings.TrimSpace(s) == "" && schema.Schema().Type == "string" {
		return fmt.Errorf("string answer requires non-empty content")
	}
	return Validate(ctx, schema, answer)
}
