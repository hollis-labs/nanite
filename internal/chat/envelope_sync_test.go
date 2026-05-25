package chat

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	envelopes "github.com/hollis-labs/go-envelopes"
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

	// Check that every manifest entry with a component appears in the TS registry.
	var missing []string
	for _, spec := range registry.All() {
		component, _ := spec.UIMetadata["component"].(string)
		if component == "" {
			continue // backend-only type, no frontend component expected
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
