package elicitation

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// Each of these makes jsonschema-go v0.4.3 recurse without end on Validate
// (a fatal stack overflow). Resolve must refuse them, so Validate never runs.
func TestResolveRefusesSchemasThatReferBackInPlace(t *testing.T) {
	for name, schema := range map[string]string{
		"root self ref":                `{"$ref":"#"}`,
		"allOf root":                   `{"type":"object","allOf":[{"$ref":"#"}]}`,
		"defs allOf/anyOf loop":        `{"$defs":{"a":{"allOf":[{"$ref":"#/$defs/b"}]},"b":{"anyOf":[{"$ref":"#/$defs/a"}]}},"$ref":"#/$defs/a"}`,
		"definitions loop":             `{"definitions":{"a":{"oneOf":[{"$ref":"#/definitions/a"}]}}}`,
		"not":                          `{"$defs":{"a":{"not":{"$ref":"#/$defs/a"}}}}`,
		"if":                           `{"$defs":{"a":{"if":{"$ref":"#/$defs/a"}}}}`,
		"then":                         `{"$defs":{"a":{"if":true,"then":{"$ref":"#/$defs/a"}}}}`,
		"else":                         `{"$defs":{"a":{"if":false,"else":{"$ref":"#/$defs/a"}}}}`,
		"dependentSchemas":             `{"$defs":{"a":{"dependentSchemas":{"x":{"$ref":"#/$defs/a"}}}}}`,
		"draft-07 dependencies":        `{"$schema":"http://json-schema.org/draft-07/schema#","definitions":{"a":{"dependencies":{"x":{"$ref":"#/definitions/a"}}}}}`,
		"keyword-named property":       `{"properties":{"enum":{"$ref":"#/properties/enum"}}}`,
		"property named default":       `{"properties":{"default":{"allOf":[{"$ref":"#/properties/default"}]}}}`,
		"nested $id":                   `{"properties":{"x":{"$id":"http://example.com/a","allOf":[{"$ref":"#"}]}}}`,
		"nested $id with its own defs": `{"properties":{"x":{"$id":"http://example.com/a","$defs":{"b":{"allOf":[{"$ref":"#/$defs/b"}]}}}}}`,
		"$anchor":                      `{"$defs":{"a":{"$anchor":"x","allOf":[{"$ref":"#x"}]}}}`,
		"percent-encoded name":         `{"$defs":{"my type":{"anyOf":[{"$ref":"#/$defs/my%20type"}]}}}`,
		"pointer escapes":              `{"$defs":{"a/b~c":{"allOf":[{"$ref":"#/$defs/a~1b~0c"}]}}}`,
		"$dynamicRef":                  `{"$dynamicAnchor":"n","allOf":[{"$dynamicRef":"#n"}]}`,
		"upper-case $REF":              `{"allOf":[{"$REF":"#"}]}`,
		"upper-case ALLOF":             `{"$defs":{"a":{"ALLOF":[{"$ref":"#/$defs/a"}]}},"$ref":"#/$defs/a"}`,
		"mixed-case $Ref and NOT":      `{"$defs":{"a":{"NOT":{"$Ref":"#/$defs/a"}}}}`,
		"case-variant duplicate":       `{"allOf":[],"AllOf":[{"$ref":"#"}]}`,
		"upper-case $ID":               `{"properties":{"x":{"$ID":"http://example.com/a","allOf":[{"$ref":"#"}]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Resolve(json.RawMessage(schema))
			if !errors.Is(err, ErrUnsafeSchema) {
				t.Fatalf("Resolve(%s) = %v, want ErrUnsafeSchema", schema, err)
			}
			if !strings.Contains(err.Error(), "refers back to itself") {
				t.Fatalf("Resolve(%s) = %v, want a cycle", schema, err)
			}
		})
	}
}

// Shapes MCP servers and hosts use, including recursion that terminates, still
// resolve and validate.
func TestResolveKeepsSchemasThatTerminate(t *testing.T) {
	for name, tc := range map[string]struct{ schema, value string }{
		"$anchor":                       {`{"$defs":{"a":{"$anchor":"foo","type":"string"}},"properties":{"x":{"$ref":"#foo"}}}`, `{"x":"y"}`},
		"percent-encoded pointer":       {`{"$defs":{"my type":{"type":"string"}},"properties":{"x":{"$ref":"#/$defs/my%20type"}}}`, `{"x":"y"}`},
		"ref to property named default": {`{"properties":{"default":{"type":"string"},"x":{"$ref":"#/properties/default"}}}`, `{"x":"y","default":"z"}`},
		"ref to property named enum":    {`{"properties":{"enum":{"type":"integer"},"x":{"$ref":"#/properties/enum"}}}`, `{"x":1}`},
		"$id-scoped pointer":            {`{"properties":{"x":{"$id":"http://example.com/a","$defs":{"s":{"type":"string"}},"properties":{"y":{"$ref":"#/$defs/s"}}}}}`, `{"x":{"y":"z"}}`},
		"tree through properties and items": {
			`{"$defs":{"node":{"type":"object","properties":{"name":{"type":"string"},"children":{"type":"array","items":{"$ref":"#/$defs/node"}}}}},"$ref":"#/$defs/node"}`,
			`{"name":"a","children":[{"name":"b","children":[]}]}`,
		},
		"anyOf recursion through items": {`{"$defs":{"v":{"anyOf":[{"type":"string"},{"type":"array","items":{"$ref":"#/$defs/v"}}]}},"$ref":"#/$defs/v"}`, `["a",["b"]]`},
		"self ref through properties":   {`{"type":"object","properties":{"next":{"$ref":"#"}}}`, `{"next":{"next":{}}}`},
		"dynamic tree":                  {`{"$dynamicAnchor":"node","type":"object","properties":{"kids":{"type":"array","items":{"$dynamicRef":"#node"}}}}`, `{"kids":[{"kids":[]}]}`},
		"shared definition, no loop":    {`{"$defs":{"s":{"type":"string"}},"allOf":[{"$ref":"#/$defs/s"}],"anyOf":[{"$ref":"#/$defs/s"}]}`, `"a"`},
	} {
		t.Run(name, func(t *testing.T) {
			resolved, err := Resolve(json.RawMessage(tc.schema))
			if err != nil {
				t.Fatalf("Resolve(%s): %v", tc.schema, err)
			}
			var value any
			if err := json.Unmarshal([]byte(tc.value), &value); err != nil {
				t.Fatal(err)
			}
			if err := resolved.Validate(value); err != nil {
				t.Fatalf("Validate(%s): %v", tc.value, err)
			}
		})
	}
}

// The check reads reference targets the library keeps unexported. If an
// upgrade changes that layout, this fails rather than the check quietly
// refusing every schema with a $ref.
func TestResolvedLayout(t *testing.T) {
	for name, tc := range map[string]struct {
		schema string
		from   func(*jsonschema.Schema) *jsonschema.Schema
		to     func(*jsonschema.Schema) *jsonschema.Schema
		anchor string
	}{
		"$ref": {`{"$defs":{"s":{"type":"string"}},"properties":{"x":{"$ref":"#/$defs/s"}}}`,
			func(r *jsonschema.Schema) *jsonschema.Schema { return r.Properties["x"] },
			func(r *jsonschema.Schema) *jsonschema.Schema { return r.Defs["s"] }, ""},
		// A $dynamicRef to a plain anchor resolves lexically, like $ref.
		"lexical $dynamicRef": {`{"$defs":{"s":{"$anchor":"a","type":"string"}},"properties":{"x":{"$dynamicRef":"#a"}}}`,
			func(r *jsonschema.Schema) *jsonschema.Schema { return r.Properties["x"] },
			func(r *jsonschema.Schema) *jsonschema.Schema { return r.Defs["s"] }, ""},
		// To a dynamic anchor it resolves at validation time, by anchor name.
		"dynamic $dynamicRef": {`{"$dynamicAnchor":"n","properties":{"x":{"$dynamicRef":"#n"}}}`,
			func(r *jsonschema.Schema) *jsonschema.Schema { return r.Properties["x"] },
			nil, "n"},
	} {
		t.Run(name, func(t *testing.T) {
			var schema jsonschema.Schema
			if err := json.Unmarshal([]byte(tc.schema), &schema); err != nil {
				t.Fatal(err)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			order, _ := index(&schema)
			nodes := map[uintptr]*jsonschema.Schema{}
			for _, s := range order {
				nodes[reflect.ValueOf(s).Pointer()] = s
			}
			targets, err := resolvedTargets(resolved, nodes)
			if err != nil {
				t.Fatal(err)
			}
			got := targets[tc.from(&schema)]
			if got.dynamicAnchor != tc.anchor {
				t.Fatalf("dynamic anchor %q, want %q", got.dynamicAnchor, tc.anchor)
			}
			if tc.to == nil {
				if len(got.targets) != 0 {
					t.Fatalf("lexical targets %v for a dynamic reference", got.targets)
				}
				return
			}
			if len(got.targets) != 1 || got.targets[0] != tc.to(&schema) {
				t.Fatalf("targets %v", got.targets)
			}
		})
	}
}

func TestErrorsNameJSONPaths(t *testing.T) {
	_, err := Resolve(json.RawMessage(`{"$defs":{"a":{"allOf":[{"$ref":"#/$defs/a"}]}}}`))
	if err == nil || !errors.Is(err, ErrUnsafeSchema) || !strings.Contains(err.Error(), "#/$defs/a") {
		t.Fatalf("err = %v", err)
	}
	// draft-07 dependencies and items are split into Go-only fields; the path
	// still uses the JSON keyword.
	_, err = Resolve(json.RawMessage(`{"$schema":"http://json-schema.org/draft-07/schema#","items":{"dependencies":{"x":{"$ref":"#/items"}}}}`))
	if err == nil || !strings.Contains(err.Error(), "#/items/dependencies/x") || strings.Contains(err.Error(), "DependencySchemas") {
		t.Fatalf("err = %v", err)
	}
}
