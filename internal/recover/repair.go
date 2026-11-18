package recover

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hollis-labs/nanite/internal/elicitation"
)

type Validator struct {
	schema *jsonschema.Resolved
	err    error
}

func NewValidator(schema json.RawMessage) Validator {
	resolved, err := elicitation.Resolve(schema)
	return Validator{schema: resolved, err: err}
}

func (v Validator) Validate(input map[string]any) error {
	if v.err != nil {
		return fmt.Errorf("invalid tool schema: %w", v.err)
	}
	if input == nil {
		input = map[string]any{}
	}
	return v.schema.Validate(input)
}

// Classify identifies invalid arguments to a valid schema.
func (v Validator) Classify(err error) bool { return v.schema != nil && err != nil }

type outcome struct {
	RepairedArgs    map[string]any `json:"repaired_args"`
	MissingRequired []string       `json:"missing_required"`
}

// Repair adapts the original structural repair contract. The model may move
// existing values or use an authored schema default. Every proposed repair
// must pass both the value check and the complete schema before execution.
func ParseRepair(raw string, original map[string]any, schema json.RawMessage) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") && strings.HasSuffix(raw, "```") {
		_, body, _ := strings.Cut(raw, "\n")
		raw = strings.TrimSpace(strings.TrimSuffix(body, "```"))
	}
	var reply outcome
	if err := json.Unmarshal([]byte(raw), &reply); err != nil {
		return nil, err
	}
	if reply.RepairedArgs == nil || len(reply.MissingRequired) > 0 {
		return nil, fmt.Errorf("repair requires missing values")
	}
	allowed := make(map[string]bool)
	collectValues(original, allowed)
	var doc map[string]any
	if err := json.Unmarshal(schema, &doc); err != nil {
		return nil, err
	}
	collectDefaults(doc, allowed)
	repaired := make(map[string]bool)
	collectValues(reply.RepairedArgs, repaired)
	for value := range maps.Keys(repaired) {
		if !allowed[value] {
			return nil, fmt.Errorf("repair would introduce a new argument value")
		}
	}
	return reply.RepairedArgs, nil
}

func collectValues(value any, out map[string]bool) {
	switch v := value.(type) {
	case map[string]any:
		for _, item := range v {
			collectValues(item, out)
		}
	case []any:
		for _, item := range v {
			collectValues(item, out)
		}
	default:
		raw, err := json.Marshal(v)
		if err == nil {
			out[string(raw)] = true
		}
	}
}

func collectDefaults(value any, out map[string]bool) {
	switch v := value.(type) {
	case map[string]any:
		for k, item := range v {
			if k == "default" {
				collectValues(item, out)
			} else {
				collectDefaults(item, out)
			}
		}
	case []any:
		for _, item := range v {
			collectDefaults(item, out)
		}
	}
}
