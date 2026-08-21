package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-agent-wrapper/plant"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestPlantSpec_FilesMCPAndProviderSettings verifies the shared plantSpec
// write routine writes Files entries, the MCPConfig ".mcp.json" shortcut,
// and the configured ProviderSettings destination.
func TestPlantSpec_FilesMCPAndProviderSettings(t *testing.T) {
	bootDir := t.TempDir()

	spec := plant.Spec{
		Files: map[string][]byte{
			"top.md":             []byte("top-content"),
			"nested/dir/file.md": []byte("nested-content"),
		},
		MCPConfig: []byte(`{"mcp":true}`),
		ProviderSettings: map[string][]byte{
			"claude": []byte(`{"permissions":{}}`),
		},
	}
	result, err := plantSpec(bootDir, spec, plantConfig{
		provider:             "claude",
		providerSettingsPath: ".claude/settings.json",
	})
	if err != nil {
		t.Fatalf("plantSpec: %v", err)
	}

	cases := []struct {
		path string
		want string
	}{
		{"top.md", "top-content"},
		{"nested/dir/file.md", "nested-content"},
		{".mcp.json", `{"mcp":true}`},
		{".claude/settings.json", `{"permissions":{}}`},
	}
	for _, c := range cases {
		body, err := os.ReadFile(filepath.Join(bootDir, filepath.FromSlash(c.path)))
		if err != nil {
			t.Errorf("read %s: %v", c.path, err)
			continue
		}
		if string(body) != c.want {
			t.Errorf("%s = %q, want %q", c.path, string(body), c.want)
		}
	}
	if len(result.PlantedFiles) != len(cases) {
		t.Errorf("PlantedFiles = %v, want %d entries", result.PlantedFiles, len(cases))
	}
}

// TestPlantSpec_ProviderSettingsAndFileModeOverrides verifies a
// configured providerSettingsMode / fileModeOverrides entry is honored —
// codex's config.toml/auth.json need 0o600, not the 0o644 Files default.
func TestPlantSpec_ProviderSettingsAndFileModeOverrides(t *testing.T) {
	bootDir := t.TempDir()
	spec := plant.Spec{
		Files:            map[string][]byte{"auth.json": []byte("secret")},
		ProviderSettings: map[string][]byte{"codex": []byte("policy")},
	}
	_, err := plantSpec(bootDir, spec, plantConfig{
		provider:             "codex",
		providerSettingsPath: "config.toml",
		providerSettingsMode: 0o600,
		fileModeOverrides:    map[string]os.FileMode{"auth.json": 0o600},
	})
	if err != nil {
		t.Fatalf("plantSpec: %v", err)
	}
	for _, p := range []string{"config.toml", "auth.json"} {
		info, err := os.Stat(filepath.Join(bootDir, p))
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode = %o, want 0600", p, perm)
		}
	}
}

// TestPlantSpec_RejectsUnsafePath verifies the shared bootdir path-safety
// gate (agentlaunch.ValidateBootDirRelPath) rejects a traversal path
// before any write happens.
func TestPlantSpec_RejectsUnsafePath(t *testing.T) {
	bootDir := t.TempDir()
	_, err := plantSpec(bootDir, plant.Spec{
		Files: map[string][]byte{"../escape.txt": []byte("nope")},
	}, plantConfig{provider: "claude"})
	if err == nil {
		t.Fatal("expected path-safety rejection, got nil")
	}
	if !strings.Contains(err.Error(), "escape.txt") {
		t.Errorf("error should name the offending path, got %v", err)
	}
}

// TestPlantSpec_RejectsHooks verifies a non-empty Spec.Hooks is rejected
// loudly — Nanite has no hook-planting destination yet, and nothing in
// this package populates Hooks today, so a non-empty value can only mean
// a caller expected behavior that isn't implemented.
func TestPlantSpec_RejectsHooks(t *testing.T) {
	bootDir := t.TempDir()
	_, err := plantSpec(bootDir, plant.Spec{
		Hooks: []plant.Hook{{Provider: "claude", Name: "pre-tool-use"}},
	}, plantConfig{provider: "claude"})
	if err == nil || !strings.Contains(err.Error(), "hooks") {
		t.Fatalf("expected hooks-unsupported error, got %v", err)
	}
}

// TestPlantSpec_RejectsRecoveryPrompt mirrors TestPlantSpec_RejectsHooks
// for Spec.RecoveryPrompt.
func TestPlantSpec_RejectsRecoveryPrompt(t *testing.T) {
	bootDir := t.TempDir()
	_, err := plantSpec(bootDir, plant.Spec{
		RecoveryPrompt: "resume here",
	}, plantConfig{provider: "claude"})
	if err == nil || !strings.Contains(err.Error(), "RecoveryPrompt") {
		t.Fatalf("expected RecoveryPrompt-unsupported error, got %v", err)
	}
}

// TestClaudePlantSpec_Shape verifies the claude layout's app-extra files
// (.sandbox/* docs) and provider settings ride the plant.Spec vocabulary
// correctly — i.e. the Nanite app extras land in Spec.Files and the MCP
// descriptor lands in Spec.MCPConfig.
func TestClaudePlantSpec_Shape(t *testing.T) {
	params := SetupParams{
		SessionID:    "sess-x",
		AgentProfile: &store.AgentProfile{Name: "Specced", Slug: "specced"},
		SystemPrompt: "be helpful",
		BootContent:  "# kickoff\n",
		MCPConfig: MCPConfig{
			BinaryPath: "/bin/nanite",
			DBPath:     "/tmp/x.db",
		},
	}
	spec, err := claudePlantSpec(params)
	if err != nil {
		t.Fatalf("claudePlantSpec: %v", err)
	}
	for _, want := range []string{".sandbox/agent-context.md", ".sandbox/envelope-schema.md", "CLAUDE.md", "boot.md"} {
		if _, ok := spec.Files[want]; !ok {
			t.Errorf("claudePlantSpec: missing Files[%q]", want)
		}
	}
	if len(spec.MCPConfig) == 0 {
		t.Errorf("claudePlantSpec: expected non-empty MCPConfig")
	}
	if _, ok := spec.ProviderSettings["claude"]; !ok {
		t.Errorf("claudePlantSpec: missing ProviderSettings[claude]")
	}
}

// TestMCPConfigBytes_DisabledWhenNoDBPath verifies MCP planting is
// skipped (nil MCPConfig bytes) when MCPConfig carries no DBPath —
// matching mcpOverlay's existing DBPath-gating behavior.
func TestMCPConfigBytes_DisabledWhenNoDBPath(t *testing.T) {
	body, err := mcpConfigBytes(SetupParams{SessionID: "s1"})
	if err != nil {
		t.Fatalf("mcpConfigBytes: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("expected no MCP config when DBPath empty, got %q", body)
	}
}

// TestPlanters_ImplementPlanterInterface is a compile-time-adjacent
// smoke test pinning that all three provider Planters satisfy
// plant.Planter (also enforced by the `var _ plant.Planter = ...`
// assertions alongside each type, but kept here too so `go test` output
// names the invariant explicitly).
func TestPlanters_ImplementPlanterInterface(t *testing.T) {
	var planters = []plant.Planter{claudePlanter{}, codexPlanter{}, opencodePlanter{}}
	if len(planters) != 3 {
		t.Fatalf("expected 3 planters, got %d", len(planters))
	}
}
