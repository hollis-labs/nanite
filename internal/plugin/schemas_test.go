package plugin

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// compileV1Schema compiles the embedded v1 schema for test use.
func compileV1Schema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	var raw any
	if err := json.NewDecoder(bytes.NewReader(PluginSchemaV1)).Decode(&raw); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(PluginSchemaV1ID, raw); err != nil {
		t.Fatalf("AddResource: %v", err)
	}
	s, err := c.Compile(PluginSchemaV1ID)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return s
}

// yamlToJSONValue loads a yaml doc as a generic any with json-compatible types.
func yamlToJSONValue(t *testing.T, src string) any {
	t.Helper()
	var raw any
	if err := yaml.Unmarshal([]byte(src), &raw); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	// Marshal + unmarshal through json to coerce types (map keys → strings, etc.)
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	return out
}

const validV1Fixture = `schema_version: 1
id: giphy
name: Giphy
version: 1.2.3
description: gif search
author: Acme
license: MIT
runtime: subprocess
protocol: 1
entrypoint: ./giphy
nanite_compat:
  min: "0.9.0"
registers:
  envelopes:
    - type: giphy-modal
      component: GiphyModalCard
      version: 1
      schema: envelopes/giphy-modal.schema.json
  commands:
    - name: giphy
      description: search
      args:
        - name: q
          type: string
          required: true
`

func TestPluginSchemaV1_SelfValidates(t *testing.T) {
	// Minimal smoke test: schema itself must be valid JSON.
	var raw any
	if err := json.Unmarshal(PluginSchemaV1, &raw); err != nil {
		t.Fatalf("schema not valid JSON: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(PluginSchemaV1ID, raw); err != nil {
		t.Fatalf("schema fails to register: %v", err)
	}
	if _, err := c.Compile(PluginSchemaV1ID); err != nil {
		t.Fatalf("schema fails to compile: %v", err)
	}
}

func TestPluginSchemaV1_AcceptsValidManifest(t *testing.T) {
	s := compileV1Schema(t)
	doc := yamlToJSONValue(t, validV1Fixture)
	if err := s.Validate(doc); err != nil {
		t.Fatalf("valid fixture rejected: %v", err)
	}
}

func TestPluginSchemaV1_RejectsMissingRequired(t *testing.T) {
	s := compileV1Schema(t)
	doc := yamlToJSONValue(t, `schema_version: 1
name: Foo
version: 1.0.0
description: x
author: a
license: MIT
runtime: builtin
protocol: 1
nanite_compat:
  min: "0.9.0"
`)
	err := s.Validate(doc)
	if err == nil {
		t.Fatal("expected error for missing id")
	}
	if !strings.Contains(err.Error(), "id") {
		t.Fatalf("expected id error, got: %v", err)
	}
}

func TestPluginSchemaV1_RejectsBadID(t *testing.T) {
	s := compileV1Schema(t)
	doc := yamlToJSONValue(t, `schema_version: 1
id: Bad-ID-With-Caps
name: x
version: 1.0.0
description: x
author: a
license: MIT
runtime: builtin
protocol: 1
nanite_compat:
  min: "0.9.0"
`)
	if err := s.Validate(doc); err == nil {
		t.Fatal("expected bad id to fail")
	}
}

func TestPluginSchemaV1_RejectsBadEnum(t *testing.T) {
	s := compileV1Schema(t)
	doc := yamlToJSONValue(t, `schema_version: 1
id: foo
name: Foo
version: 1.0.0
description: x
author: a
license: MIT
runtime: wasm
protocol: 1
nanite_compat:
  min: "0.9.0"
`)
	if err := s.Validate(doc); err == nil {
		t.Fatal("expected bad runtime enum to fail")
	}
}

func TestPluginSchemaV1_SubprocessRequiresEntrypoint(t *testing.T) {
	s := compileV1Schema(t)
	doc := yamlToJSONValue(t, `schema_version: 1
id: foo
name: Foo
version: 1.0.0
description: x
author: a
license: MIT
runtime: subprocess
protocol: 1
nanite_compat:
  min: "0.9.0"
`)
	if err := s.Validate(doc); err == nil {
		t.Fatal("expected subprocess without entrypoint to fail")
	}
}

func TestPluginSchemaV1_RejectsBadSemver(t *testing.T) {
	s := compileV1Schema(t)
	doc := yamlToJSONValue(t, `schema_version: 1
id: foo
name: Foo
version: notsemver
description: x
author: a
license: MIT
runtime: builtin
protocol: 1
nanite_compat:
  min: "0.9.0"
`)
	if err := s.Validate(doc); err == nil {
		t.Fatal("expected bad semver to fail")
	}
}

func TestPluginSchemaV1_RejectsBadConfigType(t *testing.T) {
	s := compileV1Schema(t)
	doc := yamlToJSONValue(t, `schema_version: 1
id: foo
name: Foo
version: 1.0.0
description: x
author: a
license: MIT
runtime: builtin
protocol: 1
nanite_compat:
  min: "0.9.0"
config:
  api_key:
    type: password
`)
	if err := s.Validate(doc); err == nil {
		t.Fatal("expected bad config.*.type to fail")
	}
}
