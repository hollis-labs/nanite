package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestPlantInjectionSpec_NativeFilesAndOverlay verifies the shared
// planting routine writes NativeFiles and BootDirOverlay entries,
// creates intermediate directories, and applies the overlay-wins-last
// ordering (an overlay entry overrides a native file at the same path).
func TestPlantInjectionSpec_NativeFilesAndOverlay(t *testing.T) {
	bootDir := t.TempDir()

	spec := agentlaunch.InjectionSpec{
		NativeFiles: []agentlaunch.NativeFile{
			nativeFileRaw("top.md", "native-top", 0o644),
			nativeFileRaw("nested/dir/file.md", "native-nested", 0o644),
			nativeFileRaw("collide.txt", "native-loser", 0o644),
		},
		BootDirOverlay: map[string]string{
			"collide.txt":  "overlay-winner",
			"overlay-only": "overlay-content",
		},
	}
	if err := plantInjectionSpec(bootDir, spec); err != nil {
		t.Fatalf("plantInjectionSpec: %v", err)
	}

	cases := []struct {
		path string
		want string
	}{
		{"top.md", "native-top"},
		{"nested/dir/file.md", "native-nested"},
		{"collide.txt", "overlay-winner"}, // overlay wins last
		{"overlay-only", "overlay-content"},
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
}

// TestPlantInjectionSpec_RejectsUnsafePath verifies the shared bootdir
// path-safety gate (agentlaunch.ValidateBootDirRelPath) rejects a
// traversal path before any write happens.
func TestPlantInjectionSpec_RejectsUnsafePath(t *testing.T) {
	bootDir := t.TempDir()
	err := plantInjectionSpec(bootDir, agentlaunch.InjectionSpec{
		BootDirOverlay: map[string]string{"../escape.txt": "nope"},
	})
	if err == nil {
		t.Fatal("expected path-safety rejection, got nil")
	}
	if !strings.Contains(err.Error(), "escape.txt") {
		t.Errorf("error should name the offending path, got %v", err)
	}
}

// TestPlantInjectionSpec_RejectsNonRawNativeFile verifies a NativeFile
// of a kind other than NativeFileRaw is rejected loudly — Nanite plants
// no provider-native skill files into the bootdir.
func TestPlantInjectionSpec_RejectsNonRawNativeFile(t *testing.T) {
	bootDir := t.TempDir()
	err := plantInjectionSpec(bootDir, agentlaunch.InjectionSpec{
		NativeFiles: []agentlaunch.NativeFile{
			{Kind: agentlaunch.NativeFileSkill, ID: "some-skill", Content: "x"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("expected unsupported-kind error, got %v", err)
	}
}

// TestClaudeInjectionSpec_RidesSharedPath verifies the claude layout's
// app-extra files (.sandbox/* docs) are represented as NativeFile
// entries and the MCP descriptor as a BootDirOverlay entry — i.e. the
// Nanite app extras ride the shared InjectionSpec planting path.
func TestClaudeInjectionSpec_RidesSharedPath(t *testing.T) {
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
	spec, err := claudeInjectionSpec(params)
	if err != nil {
		t.Fatalf("claudeInjectionSpec: %v", err)
	}

	var sawSandboxCtx, sawEnvelope bool
	for _, nf := range spec.NativeFiles {
		if nf.Kind != agentlaunch.NativeFileRaw {
			t.Errorf("native file %q has non-raw kind %q", nf.RelPath, nf.Kind)
		}
		switch nf.RelPath {
		case ".sandbox/agent-context.md":
			sawSandboxCtx = true
		case ".sandbox/envelope-schema.md":
			sawEnvelope = true
		}
	}
	if !sawSandboxCtx || !sawEnvelope {
		t.Errorf("sandbox app-extras not represented as NativeFiles: ctx=%v envelope=%v", sawSandboxCtx, sawEnvelope)
	}
	if _, ok := spec.BootDirOverlay[".mcp.json"]; !ok {
		t.Errorf(".mcp.json should ride as a BootDirOverlay entry, overlay=%v", spec.BootDirOverlay)
	}
}

// TestMCPOverlay_DisabledWhenNoDBPath verifies MCP planting is skipped
// (empty overlay) when MCPConfig carries no DBPath — matching the
// pre-refactor writeMCPJSON behavior.
func TestMCPOverlay_DisabledWhenNoDBPath(t *testing.T) {
	overlay, err := mcpOverlay(SetupParams{SessionID: "s1"})
	if err != nil {
		t.Fatalf("mcpOverlay: %v", err)
	}
	if len(overlay) != 0 {
		t.Errorf("expected no MCP overlay when DBPath empty, got %v", overlay)
	}
}
