package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverPlugins_FaultTolerantAndPureRead(t *testing.T) {
	pluginsDir := t.TempDir()

	// 1. Valid subprocess plugin
	validDir := filepath.Join(pluginsDir, "valid-subproc")
	if err := os.MkdirAll(validDir, 0o750); err != nil {
		t.Fatal(err)
	}
	validManifest := `schema_version: 1
name: valid-subproc
id: valid-subproc
version: 0.1.0
runtime: subprocess
entrypoint: ./bin/run
`
	if err := os.WriteFile(filepath.Join(validDir, "plugin.yaml"), []byte(validManifest), 0o600); err != nil {
		t.Fatal(err)
	}

	// 2. Directory with malformed YAML (should be skipped with warning, not abort scan)
	malformedDir := filepath.Join(pluginsDir, "malformed-plugin")
	if err := os.MkdirAll(malformedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(malformedDir, "plugin.yaml"), []byte("invalid: yaml: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 3. Directory with subprocess runtime but missing entrypoint (should be skipped with warning)
	missingEntrypointDir := filepath.Join(pluginsDir, "no-entrypoint")
	if err := os.MkdirAll(missingEntrypointDir, 0o750); err != nil {
		t.Fatal(err)
	}
	noEntrypointManifest := `schema_version: 1
name: no-entrypoint
id: no-entrypoint
version: 0.1.0
runtime: subprocess
`
	if err := os.WriteFile(filepath.Join(missingEntrypointDir, "plugin.yaml"), []byte(noEntrypointManifest), 0o600); err != nil {
		t.Fatal(err)
	}

	// 4. Legacy disabled manifest (plugin.yaml.disabled)
	// Pure-read: must discover with MigratedFromDisabled=true and NOT rename file.
	legacyDir := filepath.Join(pluginsDir, "legacy-disabled")
	if err := os.MkdirAll(legacyDir, 0o750); err != nil {
		t.Fatal(err)
	}
	legacyManifest := `schema_version: 1
name: legacy-disabled
id: legacy-disabled
version: 0.1.0
runtime: subprocess
entrypoint: ./bin/legacy
`
	legacyPath := filepath.Join(legacyDir, "plugin.yaml.disabled")
	if err := os.WriteFile(legacyPath, []byte(legacyManifest), 0o600); err != nil {
		t.Fatal(err)
	}

	// 5. Stray file in pluginsDir (not a directory)
	if err := os.WriteFile(filepath.Join(pluginsDir, "stray-file.txt"), []byte("ignore me"), 0o600); err != nil {
		t.Fatal(err)
	}

	discovered, err := DiscoverPlugins(pluginsDir)
	if err != nil {
		t.Fatalf("DiscoverPlugins failed unexpectedly: %v", err)
	}

	if len(discovered) != 2 {
		t.Fatalf("expected 2 discovered plugins (valid-subproc and legacy-disabled), got %d: %+v", len(discovered), discovered)
	}

	// Verify valid-subproc was discovered
	var validPlugin, legacyPlugin *DiscoveredPlugin
	for i := range discovered {
		if discovered[i].Manifest.ID == "valid-subproc" {
			validPlugin = &discovered[i]
		}
		if discovered[i].Manifest.ID == "legacy-disabled" {
			legacyPlugin = &discovered[i]
		}
	}

	if validPlugin == nil {
		t.Fatal("valid-subproc was not discovered")
	}
	if validPlugin.MigratedFromDisabled {
		t.Errorf("valid-subproc should have MigratedFromDisabled=false")
	}

	if legacyPlugin == nil {
		t.Fatal("legacy-disabled was not discovered")
	}
	if !legacyPlugin.MigratedFromDisabled {
		t.Errorf("legacy-disabled should have MigratedFromDisabled=true")
	}

	// Verify pure-read: plugin.yaml.disabled must STILL exist and plugin.yaml must NOT have been created
	if _, err := os.Stat(legacyPath); err != nil {
		t.Errorf("plugin.yaml.disabled should still exist: %v", err)
	}
	renamedPath := filepath.Join(legacyDir, "plugin.yaml")
	if _, err := os.Stat(renamedPath); err == nil {
		t.Errorf("plugin.yaml was unexpectedly created/renamed on disk during discovery scan")
	}
}
