package chat

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	envelopes "github.com/hollis-labs/go-envelopes"
)

// TestEnvelopeRegistrySync verifies that every core component declared in the
// host bindings has a matching generated frontend entry for a live core type.
func TestEnvelopeRegistrySync(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	registry, err := envelopes.LoadCore(context.Background())
	if err != nil {
		t.Fatalf("failed to load core envelope registry: %v", err)
	}

	// Load the generated frontend registry.
	registryPath := filepath.Join(projectRoot, "ui", "src", "generated", "plugin-envelopes.ts")
	registryData, err := os.ReadFile(registryPath)
	if err != nil {
		t.Skipf("frontend registry not found (skipping — run npm run generate:plugins first): %v", err)
	}
	registryContent := string(registryData)

	bindingsData, err := os.ReadFile(filepath.Join(projectRoot, "scripts", "lib", "core-envelope-bindings.json")) //nolint:gosec // Trusted repository path derived from this test's source location.
	if err != nil {
		t.Fatal(err)
	}
	var bindings map[string]struct {
		Component   string `json:"component"`
		BackendOnly bool   `json:"backendOnly"`
	}
	if err := json.Unmarshal(bindingsData, &bindings); err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, spec := range registry.All() {
		binding, ok := bindings[spec.Name]
		if !ok || (binding.Component == "" && !binding.BackendOnly) {
			t.Errorf("core type %s has no explicit host renderer disposition", spec.Name)
			continue
		}
		if binding.BackendOnly {
			continue
		}
		needle := `"` + spec.Name + `"`
		if !strings.Contains(registryContent, needle) {
			missing = append(missing, spec.Name)
		}
	}

	if len(missing) > 0 {
		t.Errorf("envelope types in manifest but missing from generated frontend registry (%s):\n  %s\n"+
			"Run: npm run generate:plugins",
			registryPath, strings.Join(missing, "\n  "))
	}
}
