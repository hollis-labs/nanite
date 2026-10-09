package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/plugin/plugintest"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func sharedManifestBytes(t *testing.T, custom ...pluginapi.Block) string {
	t.Helper()
	declaration := pluginapi.Block{Registers: pluginapi.Registrations{Commands: []pluginapi.Command{{Name: "hello", Description: "Say hello"}}}}
	if len(custom) != 0 {
		declaration = custom[0]
	}
	block, err := pluginapi.EncodeBlock(declaration)
	if err != nil {
		t.Fatal(err)
	}
	common := manifest.Manifest{SchemaVersion: 2, ID: "example.plugin", Name: "Example", Version: "1.0.0", Protocol: 2, Runtime: "subprocess", Server: manifest.Server{Runtime: "binary", Engines: map[string]manifest.HostRange{"binary": {Min: "0.0.0"}}, Entry: "bin/plugin"}, Hosts: map[string]manifest.HostRange{"nanite": {Min: pluginapi.Version, Max: pluginapi.Version}}, Nanite: block, Config: manifest.Config{Fields: map[string]manifest.Field{"enabled": {Type: "boolean", Default: "false"}}, Secrets: map[string]manifest.Secret{"token": {Required: true, Env: "EXAMPLE_TOKEN"}}}}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(filepath.Join(root, "bin/plugin"), []byte(sharedFixtureExecutable), 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
	// #nosec G302 -- this private fixture requires owner execution; no group/world access.
	if modeErr := os.Chmod(filepath.Join(root, "bin/plugin"), 0700); modeErr != nil {
		t.Fatal(modeErr)
	}
	if err := os.WriteFile(filepath.Join(root, "asset.txt"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if declaration.UI.Bundle != "" {
		common.UI = &manifest.UI{Bundle: declaration.UI.Bundle, Stylesheet: declaration.UI.Stylesheet, Isolation: "main-origin"}
		for _, path := range []string{declaration.UI.Bundle, declaration.UI.Stylesheet} {
			if path == "" {
				continue
			}
			file := filepath.Join(root, path)
			if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("/* fixture */"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	plugintest.Inventory(t, &common, root)
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
	if parsed.Shared == nil || parsed.Identifier() != "example.plugin" || parsed.Entrypoint != "bin/plugin" || len(parsed.EntrypointArgs) != 0 {
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
	for _, required := range []manifest.HostRange{{Min: pluginapi.Version, Max: pluginapi.Version}, {Min: "0.0.9"}, {Max: pluginapi.Version}} {
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

func TestSharedDrawerSlotPresentationReachesHost(t *testing.T) {
	block := pluginapi.Block{UI: pluginapi.UI{Bundle: "ui/index.js"}, Registers: pluginapi.Registrations{Slots: []pluginapi.Slot{{Slot: pluginapi.SlotPrimaryDrawer, ID: "docs", Title: "Documents", Icon: "file-text", Component: "Docs", Priority: 20}}}}
	parsed, err := DecodeManifest(strings.NewReader(sharedManifestBytes(t, block)))
	if err != nil {
		t.Fatal(err)
	}
	slot := parsed.Registers.Slots[0]
	if slot.Slot != pluginapi.SlotPrimaryDrawer || slot.Title != "Documents" || slot.Icon != "file-text" || slot.Priority != 20 {
		t.Fatalf("slot = %+v", slot)
	}
}

const sharedFixtureExecutable = "#!/bin/sh\nexit 1\n"

func TestSharedUIDeclarationCannotBeReplacedByHostExtension(t *testing.T) {
	base := sharedManifestBytes(t, pluginapi.Block{UI: pluginapi.UI{Bundle: "ui/index.js"}})
	for _, scenario := range []string{"missing", "substituted", "frame"} {
		t.Run(scenario, func(t *testing.T) {
			declaration, err := manifest.Decode(strings.NewReader(base))
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing":
				declaration.UI = nil
			case "substituted":
				declaration.Nanite, err = pluginapi.EncodeBlock(pluginapi.Block{UI: pluginapi.UI{Bundle: "ui/other.js"}})
			case "frame":
				declaration.UI.Isolation = "sandboxed-frame"
			}
			if err != nil {
				t.Fatal(err)
			}
			var raw strings.Builder
			if err := manifest.Encode(&raw, declaration); err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeManifest(strings.NewReader(raw.String())); err == nil {
				t.Fatal("unsupported or substituted UI activated")
			}
		})
	}
}
