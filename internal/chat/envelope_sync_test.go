package chat

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestEnvelopeRegistrySync verifies that every core envelope type declared
// in the go-envelopes manifest (../go-envelopes/manifest/envelopes.yaml,
// the source of truth post Cap-5 migration) has a matching entry in the
// generated frontend registry (ui/src/generated/plugin-envelopes.ts).
func TestEnvelopeRegistrySync(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	// Load the envelope manifest from the lib (source of truth).
	manifestPath := filepath.Join(projectRoot, "..", "go-envelopes", "manifest", "envelopes.yaml")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("failed to read envelope manifest: %v", err)
	}

	type entry struct {
		Type      string `yaml:"type"`
		Component string `yaml:"component"`
	}
	type manifest struct {
		Core []entry `yaml:"core"`
	}
	var m manifest
	if err := yaml.Unmarshal(manifestData, &m); err != nil {
		t.Fatalf("failed to parse envelope manifest: %v", err)
	}

	// Load the generated frontend registry.
	registryPath := filepath.Join(projectRoot, "ui", "src", "generated", "plugin-envelopes.ts")
	registryData, err := os.ReadFile(registryPath)
	if err != nil {
		t.Skipf("frontend registry not found (skipping — run npm run generate:plugins first): %v", err)
	}
	registryContent := string(registryData)

	// Check that every manifest entry with a component appears in the TS registry.
	var missing []string
	for _, e := range m.Core {
		if e.Component == "" {
			continue // backend-only type, no frontend component expected
		}
		needle := `"` + e.Type + `"`
		if !strings.Contains(registryContent, needle) {
			missing = append(missing, e.Type)
		}
	}

	if len(missing) > 0 {
		t.Errorf("envelope types in manifest but missing from generated frontend registry (%s):\n  %s\n"+
			"Run: npm run generate:plugins",
			registryPath, strings.Join(missing, "\n  "))
	}
}
