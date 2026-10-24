package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20261024-0011: settings and opencode.json can carry credentials or env, so
// they are owner-only even when no credential is present (the shared planner's
// canonical table mandates the same); the credential-free files keep 0644.
func TestHardening_ClaudeSettingsAndOpencodeJSONAreOwnerOnlyWithoutCredential(t *testing.T) {
	params := func(id string) SetupParams {
		return SetupParams{
			SessionID:    "harden-" + id,
			AgentProfile: &store.AgentProfile{ID: id, Name: "H", Slug: "h-" + id, Description: "d"},
			BootContent:  "# Boot\n",
			ProjectDir:   t.TempDir(),
		}
	}
	claudeBoot, err := claudeLayout{}.Setup(params("c"))
	if err != nil {
		t.Fatalf("claude Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(claudeBoot) })
	if info, err := os.Stat(filepath.Join(claudeBoot, ".claude", "settings.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("claude settings.json mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	ocBoot, err := opencodeLayout{}.Setup(params("o"))
	if err != nil {
		t.Fatalf("opencode Setup: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(ocBoot) })
	if info, err := os.Stat(filepath.Join(ocBoot, "opencode.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("opencode.json mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(filepath.Join(ocBoot, "boot.md")); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("boot.md mode = %v, %v; want 0644 (credential-free files keep the default)", info.Mode().Perm(), err)
	}
	for _, b := range []string{ocBoot} {
		if _, err := os.Stat(filepath.Join(b, ".mcp.json")); !os.IsNotExist(err) {
			t.Errorf(".mcp.json mirror planted in %s: %v", b, err)
		}
	}
}
