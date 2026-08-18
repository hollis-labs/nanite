package adapteropencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDiscover_Noop guards Phase 0 item 16 (external-format agent import cut):
// even when a legitimate OPENCODE.md file is present, Discover must no longer
// import it as a Nanite agent.
func TestDiscover_Noop(t *testing.T) {
	dir := t.TempDir()
	content := "# Opencode Agent\n\nSome system prompt.\n"
	if err := os.WriteFile(filepath.Join(dir, "OPENCODE.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	a := New().Adapter()
	defs, err := a.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if defs != nil {
		t.Errorf("Discover should be a no-op (external-format import cut), got %v", defs)
	}
}

func TestSyncProjectRoot_EmptyAgentsWritesPlaceholder(t *testing.T) {
	dir := t.TempDir()
	a := New().Adapter()
	if err := a.SyncProjectRoot(dir, nil); err != nil {
		t.Fatalf("SyncProjectRoot: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "OPENCODE.md"))
	if err != nil {
		t.Fatalf("OPENCODE.md should exist: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "<!-- nanite:start -->") {
		t.Errorf("missing start marker: %q", got)
	}
	if !strings.Contains(got, "<!-- nanite:end -->") {
		t.Errorf("missing end marker: %q", got)
	}
	if !strings.Contains(got, "No agents configured") {
		t.Errorf("missing placeholder text: %q", got)
	}
	if !strings.Contains(got, "NANITE.md") {
		t.Errorf("missing NANITE.md pointer: %q", got)
	}
}
