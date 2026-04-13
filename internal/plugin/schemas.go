package plugin

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// PluginSchemaV1 is the embedded JSON Schema (draft 2020-12) describing the
// plugin.yaml v1 format. It is the authoritative validation source for
// install-time manifest checks.
//
//go:embed schemas/plugin.schema.v1.json
var PluginSchemaV1 []byte

// PluginSchemaV1ID is the URI id declared inside PluginSchemaV1.
const PluginSchemaV1ID = "https://nanite.hollis-labs.dev/schemas/plugin.schema.v1.json"

var (
	schemaV1Once sync.Once
	schemaV1     *jsonschema.Schema
	schemaV1Err  error
)

// SchemaV1 returns the compiled plugin.yaml v1 JSON Schema. Compilation
// happens at most once per process; subsequent calls return the cached
// schema. Callers that want to validate a document without re-decoding the
// embedded bytes on every call should prefer this helper.
//
// A nil schema and non-nil error means the embedded source is malformed —
// that is a build bug (the schema ships compiled-in) and callers should
// treat it as fatal.
func SchemaV1() (*jsonschema.Schema, error) {
	schemaV1Once.Do(func() {
		var raw any
		if err := json.NewDecoder(bytes.NewReader(PluginSchemaV1)).Decode(&raw); err != nil {
			schemaV1Err = fmt.Errorf("decode embedded plugin schema: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(PluginSchemaV1ID, raw); err != nil {
			schemaV1Err = fmt.Errorf("register embedded plugin schema: %w", err)
			return
		}
		s, err := c.Compile(PluginSchemaV1ID)
		if err != nil {
			schemaV1Err = fmt.Errorf("compile embedded plugin schema: %w", err)
			return
		}
		schemaV1 = s
	})
	return schemaV1, schemaV1Err
}
