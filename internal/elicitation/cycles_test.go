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
			if _, err := Resolve(json.RawMessage(schema)); !errors.Is(err, ErrCyclicSchema) {
				t.Fatalf("Resolve(%s) = %v, want ErrCyclicSchema", schema, err)
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
// over-refusing every schema with a $ref.
func TestResolvedLayout(t *testing.T) {
	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(`{"$defs":{"s":{"type":"string"}},"properties":{"x":{"$ref":"#/$defs/s"}}}`), &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := resolvedTargets(resolved)
	if err != nil {
		t.Fatal(err)
	}
	ref := schema.Properties["x"]
	got := targets[reflect.ValueOf(ref).Pointer()].targets
	if len(got) != 1 || got[0] != reflect.ValueOf(schema.Defs["s"]).Pointer() {
		t.Fatalf("targets of x = %v", got)
	}
}

func TestCycleErrorNamesThePath(t *testing.T) {
	_, err := Resolve(json.RawMessage(`{"$defs":{"a":{"allOf":[{"$ref":"#/$defs/a"}]}}}`))
	if err == nil || !errors.Is(err, ErrCyclicSchema) || !strings.Contains(err.Error(), "#/$defs/a") {
		t.Fatalf("err = %v", err)
	}
}
