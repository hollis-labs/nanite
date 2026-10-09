package install

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/nanite/internal/plugin/plugintest"
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
	data := []byte("#!/bin/sh\n")
	sum := sha256.Sum256(data)
	uiSum := sha256.Sum256([]byte("export {};"))
	files := []manifest.ArtifactFile{{Path: "bin/giphy", SHA256: hex.EncodeToString(sum[:]), Executable: true}, {Path: "ui/dist/index.js", SHA256: hex.EncodeToString(uiSum[:])}}
	tree, err := manifest.TreeDigest(files)
	if err != nil {
		panic(err)
	}
	declaration := manifest.Manifest{SchemaVersion: 2, ID: "giphy", Name: "Giphy", Version: "1.0.0", Runtime: "subprocess", Protocol: 2,
		Server: manifest.Server{Runtime: "binary", Entry: "bin/giphy", Engines: map[string]manifest.HostRange{"binary": {Min: "0.0.0"}}},
		UI:     &manifest.UI{Bundle: "ui/dist/index.js", Isolation: "main-origin"}, Artifact: manifest.Artifact{Files: files, TreeSHA256: tree}, Hosts: map[string]manifest.HostRange{"nanite": {Min: "0.2.0"}},
		Nanite: json.RawMessage(`{"ui":{"bundle":"ui/dist/index.js"},"registers":{"envelopes":[{"type":"giphy-modal","component":"GiphyModalCard","version":1,"schema":"envelopes/giphy-modal.schema.json"}],"commands":[{"name":"giphy","description":"search"}]}}`)}
	raw, err := json.Marshal(declaration)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func setupPlugin(t *testing.T, manifestText string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plugin.yaml"), manifestText)
	writeFile(t, filepath.Join(dir, "ui/dist/index.js"), "export {};")
	for rel, content := range files {
		writeFile(t, filepath.Join(dir, rel), content)
		if strings.HasPrefix(rel, "bin/") {
			if err := os.Chmod(filepath.Join(dir, rel), 0700); err != nil { // #nosec G302 -- executable fixture requires the owner execute bit.
				t.Fatal(err)
			}
		}
	}
	// Inventory only structurally valid positive declarations. Malformed inputs
	// remain malformed, so negative admission tests reach the real decoder.
	if parsed, decodeErr := manifest.Decode(strings.NewReader(manifestText)); decodeErr == nil {
		// Missing-executable fixtures preserve the original valid declaration;
		// the real bundle validator must refuse the absent physical file.
		if _, present := files[parsed.Server.Entry]; !present {
			return dir
		}
		plugintest.Inventory(t, &parsed, dir)
		var encoded strings.Builder
		if encodeErr := manifest.Encode(&encoded, parsed); encodeErr != nil {
			t.Fatal(encodeErr)
		}
		writeFile(t, filepath.Join(dir, "plugin.yaml"), encoded.String())
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

func refreshInventory(t *testing.T, root string) {
	t.Helper()
	confined, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := confined.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	raw, err := confined.ReadFile("plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := manifest.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	plugintest.Inventory(t, &parsed, root)
	var encoded strings.Builder
	if err := manifest.Encode(&encoded, parsed); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "plugin.yaml"), encoded.String())
}
