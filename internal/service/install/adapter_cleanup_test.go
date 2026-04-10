package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agent"
)

func TestCleanupRemovedAdapters_FileNotPresent(t *testing.T) {
	dir := t.TempDir()
	reports, err := cleanupRemovedAdapters(dir, []string{"claude"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(reports) != 0 {
		t.Errorf("expected 0 reports for missing file, got %d", len(reports))
	}
}

func TestCleanupRemovedAdapters_StripsAndPreservesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := agent.WriteManagedSection(path, "managed body"); err != nil {
		t.Fatal(err)
	}
	// Add user content outside the markers
	existing, _ := os.ReadFile(path)
	combined := "# user content\n\n" + string(existing)
	if err := os.WriteFile(path, []byte(combined), 0o644); err != nil {
		t.Fatal(err)
	}

	reports, err := cleanupRemovedAdapters(dir, []string{"codex"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].Adapter != "codex" {
		t.Errorf("Adapter: got %q, want codex", reports[0].Adapter)
	}
	if reports[0].Action != "stripped" {
		t.Errorf("Action: got %q, want stripped", reports[0].Action)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("AGENTS.md should still exist: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "user content") {
		t.Errorf("user content missing from %q", string(got))
	}
}

func TestCleanupRemovedAdapters_DeletesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "GEMINI.md")
	if err := agent.WriteManagedSection(path, "managed body"); err != nil {
		t.Fatal(err)
	}

	reports, err := cleanupRemovedAdapters(dir, []string{"gemini"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].Action != "deleted" {
		t.Errorf("Action: got %q, want deleted", reports[0].Action)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected GEMINI.md to be deleted, stat err: %v", err)
	}
}

func TestCleanupRemovedAdapters_MultipleAdapters(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"CLAUDE.md", "AGENTS.md", "OPENCODE.md"} {
		if err := agent.WriteManagedSection(filepath.Join(dir, f), "body"); err != nil {
			t.Fatal(err)
		}
	}

	reports, err := cleanupRemovedAdapters(dir, []string{"claude", "codex", "opencode"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(reports) != 3 {
		t.Fatalf("expected 3 reports, got %d", len(reports))
	}
	for _, r := range reports {
		if r.Action != "deleted" {
			t.Errorf("expected deleted, got %q for %q", r.Action, r.Adapter)
		}
	}
}

func TestCleanupRemovedAdapters_UnknownAdapterIgnored(t *testing.T) {
	dir := t.TempDir()
	reports, err := cleanupRemovedAdapters(dir, []string{"frobnicate"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(reports) != 0 {
		t.Errorf("expected 0 reports for unknown adapter, got %d", len(reports))
	}
}
