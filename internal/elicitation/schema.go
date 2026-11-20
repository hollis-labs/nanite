package elicitation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// Resolve compiles a local-only JSON schema. A schema that refers back to
// itself in place is refused with ErrCyclicSchema: validating against it
// would recurse until the stack overflows and the process dies.
func Resolve(raw json.RawMessage) (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil, err
	}
	if err := checkCycles(resolved); err != nil {
		return nil, err
	}
	return resolved, nil
}

// ValidateResponse adapts legacy string/boolean question validation to JSON
// answers. Null represents a declined or cancelled prompt.
func ValidateResponse(schema *jsonschema.Resolved, value json.RawMessage) error {
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
	return schema.Validate(answer)
}
