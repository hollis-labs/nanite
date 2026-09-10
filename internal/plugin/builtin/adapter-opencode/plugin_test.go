package adapteropencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImport_NotThisFormat guards the scope fence CW-20260910-0012 kept:
// re-arming the adapter seam as Import(path) shipped exactly ONE format
// (adapter-claude), so this adapter still declines every path. (nil, nil) is
// the interface's "not my format" answer, which lets a registry try the next
// adapter -- it is not an error and not a half-built stub.
func TestImport_NotThisFormat(t *testing.T) {
	dir := t.TempDir()
	content := "# Opencode Agent\n\nSome system prompt.\n"
	if err := os.WriteFile(filepath.Join(dir, "OPENCODE.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	a := New().Adapter()
	defs, err := a.Import(dir)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if defs != nil {
		t.Errorf("Import should decline this path (only adapter-claude ships a format), got %v", defs)
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
