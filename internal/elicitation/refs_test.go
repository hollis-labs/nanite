package elicitation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveRefusesSchemasThatRecurseInPlace(t *testing.T) {
	for name, schema := range map[string]string{
		"self":           `{"$ref":"#"}`,
		"defs cycle":     `{"$ref":"#/$defs/a","$defs":{"a":{"$ref":"#/$defs/b"},"b":{"allOf":[{"$ref":"#/$defs/a"}]}}}`,
		"allOf root":     `{"type":"object","allOf":[{"$ref":"#"}]}`,
		"not":            `{"not":{"$ref":"#"}}`,
		"dynamic":        `{"$dynamicRef":"#meta"}`,
		"recursive":      `{"$recursiveRef":"#"}`,
		"remote":         `{"$ref":"https://example.com/schema.json"}`,
		"anchor":         `{"$ref":"#thing"}`,
		"dangling":       `{"$ref":"#/$defs/missing"}`,
		"if then cycle":  `{"$defs":{"a":{"if":{"$ref":"#/$defs/a"}}},"$ref":"#/$defs/a"}`,
		"dependent loop": `{"$defs":{"a":{"dependentSchemas":{"x":{"$ref":"#/$defs/a"}}}},"$ref":"#/$defs/a"}`,
	} {
		if _, err := Resolve(json.RawMessage(schema)); err == nil {
			t.Errorf("%s: accepted %s", name, schema)
		}
	}
}

func TestResolveAllowsRecursionThroughValues(t *testing.T) {
	tree := `{"$defs":{"node":{"type":"object","properties":{"name":{"type":"string"},"children":{"type":"array","items":{"$ref":"#/$defs/node"}}}}},"$ref":"#/$defs/node"}`
	resolved, err := Resolve(json.RawMessage(tree))
	if err != nil {
		t.Fatal(err)
	}
	var value any
	_ = json.Unmarshal([]byte(`{"name":"a","children":[{"name":"b","children":[]}]}`), &value)
	if err := resolved.Validate(value); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(json.RawMessage(`{"$ref":"#/$defs/a","$defs":{"a":{"type":"string"}},"default":{"$ref":"#"}}`)); err != nil {
		t.Fatalf("a data value shaped like a $ref was treated as a schema: %v", err)
	}
}

func TestResolveLimitedCapsSize(t *testing.T) {
	big := `{"description":"` + strings.Repeat("x", MaxSchemaBytes) + `"}`
	if _, err := ResolveLimited(json.RawMessage(big)); err == nil {
		t.Fatal("an oversized schema was accepted")
	}
	if _, err := Resolve(json.RawMessage(big)); err != nil {
		t.Fatalf("Resolve itself has no size cap: %v", err)
	}
}
