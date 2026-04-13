package plugin

import (
	_ "embed"
)

// PluginSchemaV1 is the embedded JSON Schema (draft 2020-12) describing the
// plugin.yaml v1 format. It is the authoritative validation source for
// install-time manifest checks.
//
//go:embed schemas/plugin.schema.v1.json
var PluginSchemaV1 []byte

// PluginSchemaV1ID is the URI id declared inside PluginSchemaV1.
const PluginSchemaV1ID = "https://nanite.hollis-labs.dev/schemas/plugin.schema.v1.json"
