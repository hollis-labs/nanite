package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// validManifest is a generated shared declaration with a public Nanite block.
func validManifest() string {
	return `{"schema_version":2,"id":"giphy","name":"Giphy","version":"1.0.0","runtime":"subprocess","protocol":1,"entrypoint":{"command":"bin/giphy"},"hosts":{"nanite":{"min":"0.1.0"}},"nanite":{"ui":{"bundle":"ui/dist/index.js"},"registers":{"envelopes":[{"type":"giphy-modal","component":"GiphyModalCard","version":1,"schema":"envelopes/giphy-modal.schema.json"}],"commands":[{"name":"giphy","description":"search"}]}}}`
}

func setupPlugin(t *testing.T, manifest string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugin.yaml"), manifest)
	writeFile(t, filepath.Join(dir, "ui/dist/index.js"), "export {};")
	for rel, content := range files {
		writeFile(t, filepath.Join(dir, rel), content)
		if strings.HasPrefix(rel, "bin/") {
			if err := os.Chmod(filepath.Join(dir, rel), 0700); err != nil { // #nosec G302 -- executable fixture requires the owner execute bit.
				t.Fatal(err)
			}
		}
	}
	return dir
}

func TestValidateManifest_Valid(t *testing.T) {
	dir := setupPlugin(t, validManifest(), map[string]string{
		"bin/giphy":                         "#!/bin/sh\n",
		"envelopes/giphy-modal.schema.json": `{"type":"object"}`,
	})
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{})
	if err != nil {
		t.Fatalf("unexpected failures: %s", err)
	}
}

func TestValidateManifest_MissingEnvelopeSchema_Refuse(t *testing.T) {
	dir := setupPlugin(t, validManifest(), map[string]string{
		"bin/giphy": "#!/bin/sh\n",
	})
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{})
	if err == nil || !err.HasRefusals() {
		t.Fatalf("expected refuse-level failure for missing envelope schema, got: %v", err)
	}
}

func TestValidateManifest_MissingEnvelopeSchema_RefuseInDeveloperMode(t *testing.T) {
	dir := setupPlugin(t, validManifest(), map[string]string{"bin/giphy": "#!/bin/sh\n"})
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{DeveloperMode: true})
	if err == nil || !err.HasRefusals() {
		t.Fatalf("missing asset must refuse: %v", err)
	}
}

func TestValidateManifest_BadYAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugin.yaml"), ":bad:yaml:")
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{})
	if err == nil || !err.HasRefusals() {
		t.Fatal("expected refuse for bad yaml")
	}
}

func TestValidateManifest_SchemaViolation_RefuseEvenInDeveloperMode(t *testing.T) {
	bad := `schema_version: 1
id: Bad-ID
name: x
version: 1.0.0
description: x
author: a
license: MIT
runtime: builtin
protocol: 1
nanite_compat:
  min: "0.9.0"
`
	dir := setupPlugin(t, bad, nil)
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{DeveloperMode: true})
	if err == nil || !err.HasRefusals() {
		t.Fatalf("schema violation must refuse even in dev mode, got: %v", err)
	}
}

func TestValidateManifest_DuplicateEnvelopeType(t *testing.T) {
	src := strings.Replace(validManifest(), `"envelopes":[`, `"envelopes":[{"type":"giphy-modal","component":"Other","version":1},`, 1)
	err := ValidateBytes([]byte(src), ValidationOptions{})
	if err == nil || !err.HasRefusals() {
		t.Fatal("accepted duplicate envelope")
	}
}

func TestValidateManifest_DuplicateCommand(t *testing.T) {
	src := strings.Replace(validManifest(), `"commands":[`, `"commands":[{"name":"giphy"},`, 1)
	err := ValidateBytes([]byte(src), ValidationOptions{})
	if err == nil || !err.HasRefusals() {
		t.Fatal("accepted duplicate command")
	}
}

func TestValidateManifest_UIBundleMissing(t *testing.T) {
	src := strings.Replace(validManifest(), `"bundle":"ui/dist/index.js"`, `"bundle":"ui/dist/missing.js"`, 1)
	dir := setupPlugin(t, src, map[string]string{"bin/giphy": "#!/bin/sh\n", "envelopes/giphy-modal.schema.json": `{"type":"object"}`})
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{})
	if err == nil || !err.HasRefusals() {
		t.Fatal("accepted missing UI bundle")
	}
}

func TestValidateManifest_EntrypointMissingRefusesEveryMode(t *testing.T) {
	dir := setupPlugin(t, validManifest(), map[string]string{"envelopes/giphy-modal.schema.json": `{"type":"object"}`})
	for _, dev := range []bool{false, true} {
		err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{DeveloperMode: dev})
		if err == nil || !err.HasRefusals() {
			t.Fatalf("missing executable accepted in dev=%v", dev)
		}
	}
}

func TestValidateManifest_AssetSymlinkEscapeRefusesEveryMode(t *testing.T) {
	dir := setupPlugin(t, validManifest(), map[string]string{"bin/giphy": "#!/bin/sh\n"})
	outside := filepath.Join(t.TempDir(), "schema.json")
	writeFile(t, outside, `{"type":"object"}`)
	if err := os.MkdirAll(filepath.Join(dir, "envelopes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "envelopes/giphy-modal.schema.json")); err != nil {
		t.Fatal(err)
	}
	for _, dev := range []bool{false, true} {
		err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{DeveloperMode: dev})
		if err == nil || !err.HasRefusals() {
			t.Fatalf("escaped asset accepted in dev=%v", dev)
		}
	}
}

func TestValidateBytes_SchemaOnly(t *testing.T) {
	err := ValidateBytes([]byte(validManifest()), ValidationOptions{})
	if err != nil {
		t.Fatalf("expected pass, got: %s", err)
	}

	err = ValidateBytes([]byte("id: Bad\nname: x\n"), ValidationOptions{})
	if err == nil || !err.HasRefusals() {
		t.Fatal("expected refuse")
	}
}
