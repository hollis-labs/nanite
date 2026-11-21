package registryagent

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"github.com/hollis-labs/libs/security/redact"
	"github.com/hollis-labs/nanite/internal/third_party/jsonschema"
)

//go:embed schemas/*.json
var schemas embed.FS

const maxRecordBytes = 1 << 20

var schemaFiles = map[string]string{
	"agent/1":          "agent-1-registry.json",
	"launch_profile/1": "launch-profile-1-registry.json",
	"skill/1":          "skill-1.json",
	"document/1":       "document-1-registry.json",
}

// validate admits exact Registry shapes and rejects recognizable credentials.
// It never includes rejected content in errors.
func validate(ctx context.Context, raw []byte) (map[string]any, error) {
	if len(raw) > maxRecordBytes {
		return nil, fmt.Errorf("registryagent: record exceeds size limit")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	value, err := readValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("registryagent: trailing JSON")
	}
	scrubber, err := redact.New(redact.WithBenignKeys("max_output_tokens", "compact_keep_tokens", "compact_input_tokens", "turn_token_budget", "daily_token_budget"))
	if err != nil {
		return nil, err
	}
	scrubbed, err := scrubber.JSON(raw)
	if err != nil {
		return nil, fmt.Errorf("registryagent: invalid JSON")
	}
	var safe any
	if err := json.Unmarshal(scrubbed, &safe); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(value, safe) {
		return nil, fmt.Errorf("registryagent: credential-like content refused")
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("registryagent: record payload must be an object")
	}
	kind, _ := obj["schema"].(string)
	name, ok := schemaFiles[kind]
	if !ok {
		return nil, fmt.Errorf("registryagent: unsupported schema")
	}
	data, err := schemas.ReadFile("schemas/" + name)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return nil, err
	}
	if err := resolved.Validate(ctx, obj, jsonschema.Budget{}); err != nil {
		return nil, fmt.Errorf("registryagent: payload does not match %s", kind)
	}
	return obj, nil
}

func readValue(dec *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("registryagent: JSON nesting limit")
	}
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("registryagent: invalid JSON")
	}
	if s, ok := tok.(string); ok && len(redact.Default().Scan(s)) > 0 {
		return nil, fmt.Errorf("registryagent: credential-like content refused")
	}
	switch tok {
	case json.Delim('{'):
		m := map[string]any{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("registryagent: invalid JSON key")
			}
			k, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("registryagent: invalid JSON key")
			}
			if _, dup := m[k]; dup {
				return nil, fmt.Errorf("registryagent: duplicate JSON key")
			}
			v, err := readValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			m[k] = v
		}
		if _, err := dec.Token(); err != nil {
			return nil, fmt.Errorf("registryagent: unclosed object")
		}
		return m, nil
	case json.Delim('['):
		a := []any{}
		for dec.More() {
			v, err := readValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, fmt.Errorf("registryagent: unclosed array")
		}
		return a, nil
	default:
		return tok, nil
	}
}
