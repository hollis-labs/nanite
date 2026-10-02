package plugin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
	"github.com/hollis-labs/plugin-sdk/manifest"
)

func sharedManifestBytes(t *testing.T) string {
	t.Helper()
	block, err := pluginapi.EncodeBlock(pluginapi.Block{Registers: pluginapi.Registrations{Commands: []pluginapi.Command{{Name: "hello", Description: "Say hello"}}}})
	if err != nil {
		t.Fatal(err)
	}
	common := manifest.Manifest{SchemaVersion: 2, ID: "example.plugin", Name: "Example", Version: "1.0.0", Protocol: 1, Runtime: "subprocess", Entrypoint: manifest.Entrypoint{Command: "bin/plugin", Args: []string{"literal argument"}}, Hosts: map[string]manifest.HostRange{"nanite": {Min: "0.1.0", Max: "0.1.0"}}, Nanite: block, Config: manifest.Config{Fields: map[string]manifest.Field{"enabled": {Type: "boolean", Default: "false"}}, Secrets: map[string]manifest.Secret{"token": {Required: true, Env: "EXAMPLE_TOKEN"}}}}
	var out strings.Builder
	if encodeErr := manifest.Encode(&out, common); encodeErr != nil {
		t.Fatal(encodeErr)
	}
	return out.String()
}

func TestDecodeSharedManifestPreservesExecutionAndSecretDeclarations(t *testing.T) {
	parsed, err := DecodeManifest(strings.NewReader(sharedManifestBytes(t)))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Shared == nil || parsed.Identifier() != "example.plugin" || len(parsed.EntrypointArgs) != 1 || parsed.EntrypointArgs[0] != "literal argument" {
		t.Fatalf("execution metadata = %+v", parsed)
	}
	if parsed.Config["enabled"].Default != "false" || !parsed.Config["token"].Secret || !parsed.Config["token"].Required {
		t.Fatalf("config declarations = %+v", parsed.Config)
	}
	if len(parsed.Registers.Commands) != 1 || parsed.Registers.Commands[0].Name != "hello" {
		t.Fatalf("host declarations = %+v", parsed.Registers)
	}
}

func TestDecodeSharedManifestRejectsLegacyAndAmbiguousDeclarations(t *testing.T) {
	valid := sharedManifestBytes(t)
	cases := map[string]string{
		"legacy YAML":        "schema_version: 1\nid: example\nname: Example\nversion: 1.0.0\nruntime: subprocess\nentrypoint: ./plugin\n",
		"legacy schema":      strings.Replace(valid, `"schema_version": 2`, `"schema_version": 1`, 1),
		"duplicate identity": strings.Replace(valid, `"id": "example.plugin"`, `"id": "example.plugin", "id": "different"`, 1),
		"case variant":       strings.Replace(valid, `"id": "example.plugin"`, `"ID": "example.plugin"`, 1),
		"unknown common":     strings.Replace(valid, `"name": "Example"`, `"name": "Example", "builtin": true`, 1),
		"trailing document":  valid + `{}`,
		"oversized":          strings.Repeat(" ", manifest.MaxBytes+1) + valid,
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(valid), &object); err != nil {
		t.Fatal(err)
	}
	object["nanite"] = json.RawMessage(`{"unknown":true}`)
	unknown, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	cases["unknown extension"] = string(unknown)
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, decodeErr := DecodeManifest(strings.NewReader(raw)); decodeErr == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestPublicHostRangeIsIndependentOfApplicationVersion(t *testing.T) {
	for _, required := range []manifest.HostRange{{Min: "0.1.0", Max: "0.1.0"}, {Min: "0.0.9"}, {Max: "0.1.1"}} {
		if err := CheckHostRange(required); err != nil {
			t.Fatal(err)
		}
	}
	for _, required := range []manifest.HostRange{{Min: "0.3.0"}, {Max: "0.0.9"}, {Min: "0.2.0", Max: "0.1.0"}, {Min: "v0.1.0"}} {
		if err := CheckHostRange(required); err == nil {
			t.Fatalf("incompatible range accepted: %+v", required)
		}
	}
}
