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

// validManifest returns a full, consistent plugin.yaml for positive tests.
func validManifest() string {
	return `schema_version: 1
id: giphy
name: Giphy
version: 1.0.0
description: gif search
author: Acme
license: MIT
runtime: subprocess
protocol: 1
entrypoint: ./bin/giphy
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
`
}

func setupPlugin(t *testing.T, manifest string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugin.yaml"), manifest)
	for rel, content := range files {
		writeFile(t, filepath.Join(dir, rel), content)
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

func TestValidateManifest_MissingEnvelopeSchema_DeveloperWarn(t *testing.T) {
	dir := setupPlugin(t, validManifest(), map[string]string{
		"bin/giphy": "#!/bin/sh\n",
	})
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{DeveloperMode: true})
	if err == nil {
		t.Fatal("expected warnings")
	}
	if err.HasRefusals() {
		t.Fatalf("developer mode should downgrade to warning, got refuse: %s", err)
	}
	if len(err.Warnings()) == 0 {
		t.Fatal("expected at least one warning")
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
	src := `schema_version: 1
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
registers:
  envelopes:
    - type: same
      component: A
    - type: same
      component: B
`
	dir := setupPlugin(t, src, nil)
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{})
	if err == nil || !err.HasRefusals() {
		t.Fatal("expected refuse for duplicate envelope type")
	}
	if !strings.Contains(err.Error(), "duplicate envelope") {
		t.Fatalf("expected duplicate envelope message, got: %s", err)
	}
}

func TestValidateManifest_DuplicateCommand(t *testing.T) {
	src := `schema_version: 1
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
registers:
  commands:
    - name: do
    - name: do
`
	dir := setupPlugin(t, src, nil)
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{})
	if err == nil || !err.HasRefusals() {
		t.Fatal("expected refuse for duplicate command")
	}
}

func TestValidateManifest_UIBundleMissing(t *testing.T) {
	src := `schema_version: 1
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
ui:
  bundle_dir: ui/dist
  entry: index.js
  stylesheet: style.css
`
	dir := setupPlugin(t, src, nil)
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{})
	if err == nil || !err.HasRefusals() {
		t.Fatal("expected refuse when ui entry missing")
	}
}

func TestValidateManifest_SubprocessEntrypointMissingFromDir_Warn(t *testing.T) {
	// Only dot-prefixed entrypoints are checked for existence.
	src := `schema_version: 1
id: foo
name: Foo
version: 1.0.0
description: x
author: a
license: MIT
runtime: subprocess
protocol: 1
entrypoint: ./bin/does-not-exist
nanite_compat:
  min: "0.9.0"
`
	dir := setupPlugin(t, src, nil)
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{})
	if err == nil || !err.HasRefusals() {
		t.Fatal("expected refuse on missing entrypoint")
	}
	// developer mode should warn
	err2 := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{DeveloperMode: true})
	if err2 != nil && err2.HasRefusals() {
		t.Fatalf("dev mode should warn, got refuse: %s", err2)
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

func TestValidateManifest_AllowedPlatformsWarn(t *testing.T) {
	src := `schema_version: 1
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
release:
  platforms: [darwin-arm64, solaris-sparc]
`
	dir := setupPlugin(t, src, nil)
	err := ValidateManifest(filepath.Join(dir, "plugin.yaml"), dir, ValidationOptions{
		AllowedPlatforms: []string{"darwin-arm64", "linux-amd64"},
	})
	if err == nil {
		t.Fatal("expected warning for unknown platform")
	}
	if err.HasRefusals() {
		t.Fatalf("unknown platform should warn, not refuse: %s", err)
	}
}
