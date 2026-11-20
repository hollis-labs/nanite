package elicitation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveLimitedRefusesWhatCouldRecurse(t *testing.T) {
	for name, schema := range map[string]string{
		// The reviewer's crash shapes: a property named like a data keyword,
		// and a #/ pointer read against a nested $id.
		"property named enum":  `{"properties":{"enum":{"$ref":"#/properties/enum"}}}`,
		"nested id":            `{"$defs":{"a":{"$id":"https://example.com/a","properties":{"x":{"$ref":"#"}}}},"$ref":"#/$defs/a"}`,
		"self":                 `{"$ref":"#"}`,
		"defs cycle":           `{"$ref":"#/$defs/a","$defs":{"a":{"$ref":"#/$defs/b"},"b":{"allOf":[{"$ref":"#/$defs/a"}]}}}`,
		"definitions cycle":    `{"$ref":"#/definitions/a","definitions":{"a":{"not":{"$ref":"#/definitions/a"}}}}`,
		"if then":              `{"$defs":{"a":{"if":{"$ref":"#/$defs/a"}}},"$ref":"#/$defs/a"}`,
		"dependentSchemas":     `{"$defs":{"a":{"dependentSchemas":{"x":{"$ref":"#/$defs/a"}}}},"$ref":"#/$defs/a"}`,
		"dependencies":         `{"$defs":{"a":{"dependencies":{"x":{"$ref":"#/$defs/a"}}}},"$ref":"#/$defs/a"}`,
		"percent cycle":        `{"$defs":{"a b":{"anyOf":[{"$ref":"#/$defs/a%20b"}]}},"$ref":"#/$defs/a%20b"}`,
		"id":                   `{"$id":"https://example.com/s","type":"object"}`,
		"anchor":               `{"$anchor":"top","type":"object"}`,
		"dynamic ref":          `{"$dynamicRef":"#meta"}`,
		"recursive ref":        `{"$recursiveRef":"#"}`,
		"ref to property":      `{"properties":{"a":{"type":"string"},"b":{"$ref":"#/properties/a"}}}`,
		"remote":               `{"$ref":"https://example.com/schema.json"}`,
		"anchor ref":           `{"$ref":"#thing"}`,
		"dangling":             `{"$ref":"#/$defs/missing"}`,
		"deeper pointer":       `{"$defs":{"a":{"$defs":{"b":{}}}},"$ref":"#/$defs/a/$defs/b"}`,
		"forbidden in items":   `{"type":"array","items":{"$anchor":"x"}}`,
		"forbidden nested def": `{"allOf":[{"$defs":{"q":{"$id":"x"}}}]}`,
		"oversized":            `{"description":"` + strings.Repeat("x", MaxSchemaBytes) + `"}`,
	} {
		if _, err := ResolveLimited(json.RawMessage(schema)); err == nil {
			t.Errorf("%s: accepted %s", name, schema)
		}
	}
}

func TestResolveLimitedAllowsRecursionThroughValues(t *testing.T) {
	tree := `{"$defs":{"node":{"type":"object","properties":{"name":{"type":"string"},"enum":{"type":"string"},"children":{"type":"array","items":{"$ref":"#/$defs/node"}}}}},"$ref":"#/$defs/node"}`
	resolved, err := ResolveLimited(json.RawMessage(tree))
	if err != nil {
		t.Fatal(err)
	}
	var value any
	_ = json.Unmarshal([]byte(`{"name":"a","children":[{"name":"b","children":[]}]}`), &value)
	if err := resolved.Validate(value); err != nil {
		t.Fatal(err)
	}
	for name, schema := range map[string]string{
		"plain":          `{"type":"object","required":["files"],"properties":{"files":{"type":"array","items":{"type":"string"}}}}`,
		"shared def":     `{"$defs":{"s":{"type":"string"}},"properties":{"a":{"$ref":"#/$defs/s"},"b":{"$ref":"#/$defs/s"}}}`,
		"chained defs":   `{"$defs":{"a":{"$ref":"#/$defs/b"},"b":{"type":"string"}},"$ref":"#/$defs/a"}`,
		"definitions":    `{"definitions":{"s":{"type":"string"}},"items":{"$ref":"#/definitions/s"}}`,
		"default object": `{"type":"object","default":{"$ref":"#"},"enum":[{"$id":"x"}]}`,
	} {
		if _, err := ResolveLimited(json.RawMessage(schema)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestResolveKeepsToolSchemasAsTheyWere(t *testing.T) {
	// Tool input schemas from hosts and MCP servers: no new refusals.
	for name, schema := range map[string]string{
		"anchor":            `{"$defs":{"a":{"$anchor":"thing","type":"string"}},"properties":{"x":{"$ref":"#thing"}}}`,
		"percent pointer":   `{"$defs":{"a b":{"type":"string"}},"properties":{"x":{"$ref":"#/$defs/a%20b"}}}`,
		"property default":  `{"properties":{"default":{"type":"string"},"x":{"$ref":"#/properties/default"}}}`,
		"nested id pointer": `{"$defs":{"a":{"$id":"https://example.com/a","$defs":{"b":{"type":"string"}},"properties":{"x":{"$ref":"#/$defs/b"}}}},"properties":{"y":{"$ref":"#/$defs/a"}}}`,
	} {
		if _, err := Resolve(json.RawMessage(schema)); err != nil {
			t.Errorf("%s: a tool schema that resolves today was refused: %v", name, err)
		}
	}
}
