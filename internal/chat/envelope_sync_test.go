package chat

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEnvelopeRegistrySync verifies that every backend envelope type in
// registeredTypes has a matching entry in the frontend registry file
// (ui/src/generated/plugin-envelopes.ts). This catches the silent-drop
// bug where a backend type is added without a frontend component.
func TestEnvelopeRegistrySync(t *testing.T) {
	// Locate project root relative to this test file.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine test file path")
	}
	projectRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	registryPath := filepath.Join(projectRoot, "ui", "src", "generated", "plugin-envelopes.ts")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Skipf("frontend registry not found (skipping in CI-only builds): %v", err)
	}
	registryContent := string(data)

	var missing []string
	for envelopeType := range registeredTypes {
		// The frontend file uses the type string as a quoted key, e.g. "kb-result":
		needle := `"` + envelopeType + `"`
		if !strings.Contains(registryContent, needle) {
			missing = append(missing, envelopeType)
		}
	}

	if len(missing) > 0 {
		t.Errorf("backend envelope types missing from frontend registry (%s):\n  %s\n"+
			"Add a component entry for each in ui/src/generated/plugin-envelopes.ts",
			registryPath, strings.Join(missing, "\n  "))
	}
}
