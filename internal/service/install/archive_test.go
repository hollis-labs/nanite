package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExpandArchiveBase(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no tilde", "/tmp/foo", "/tmp/foo"},
		{"tilde root", "~", home},
		{"tilde slash", "~/Projects-apps/.archived", filepath.Join(home, "Projects-apps/.archived")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExpandArchiveBase(tc.in)
			if err != nil {
				t.Fatalf("ExpandArchiveBase(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestResolveArchiveDir_EmptyBasename(t *testing.T) {
	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	if _, err := ResolveArchiveDir(t.TempDir(), "", ts); err == nil {
		t.Error("expected error for empty basename")
	}
}

func TestResolveArchiveDir_NoCollision(t *testing.T) {
	base := t.TempDir()
	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	got, err := ResolveArchiveDir(base, "hadron", ts)
	if err != nil {
		t.Fatalf("ResolveArchiveDir: %v", err)
	}
	want := filepath.Join(base, "hadron-2026-04-09")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestResolveArchiveDir_Collision(t *testing.T) {
	base := t.TempDir()
	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	if err := os.MkdirAll(filepath.Join(base, "hadron-2026-04-09"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveArchiveDir(base, "hadron", ts)
	if err != nil {
		t.Fatalf("ResolveArchiveDir: %v", err)
	}
	want := filepath.Join(base, "hadron-2026-04-09-143022")
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestResolveArchiveDir_DoubleCollision(t *testing.T) {
	base := t.TempDir()
	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	os.MkdirAll(filepath.Join(base, "hadron-2026-04-09"), 0o755)
	os.MkdirAll(filepath.Join(base, "hadron-2026-04-09-143022"), 0o755)
	got, err := ResolveArchiveDir(base, "hadron", ts)
	if err != nil {
		t.Fatalf("ResolveArchiveDir: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(got), "hadron-2026-04-09-143022-") {
		t.Errorf("expected sequence suffix, got %q", got)
	}
}

func TestArchiveProjectAgentrc(t *testing.T) {
	project := t.TempDir()
	archiveBase := t.TempDir()

	mkFile := func(p, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkFile(filepath.Join(project, ".agentrc", "config.yaml"), "version: 2.2.0\n")
	mkFile(filepath.Join(project, ".agentrc", "agents", "backend.md"), "# Backend\n")
	mkFile(filepath.Join(project, ".agentrc-legacy", "old.md"), "legacy content\n")

	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	archiveDir, err := ArchiveProjectAgentrc(project, archiveBase, filepath.Base(project), ts)
	if err != nil {
		t.Fatalf("ArchiveProjectAgentrc: %v", err)
	}

	// .agentrc/ and .agentrc-legacy/ should no longer exist in project.
	if _, err := os.Stat(filepath.Join(project, ".agentrc")); !os.IsNotExist(err) {
		t.Error(".agentrc not removed from project")
	}
	if _, err := os.Stat(filepath.Join(project, ".agentrc-legacy")); !os.IsNotExist(err) {
		t.Error(".agentrc-legacy not removed from project")
	}

	// Archive dir should contain both.
	if _, err := os.Stat(filepath.Join(archiveDir, ".agentrc", "config.yaml")); err != nil {
		t.Errorf("archived .agentrc/config.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, ".agentrc", "agents", "backend.md")); err != nil {
		t.Errorf("archived .agentrc/agents/backend.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(archiveDir, ".agentrc-legacy", "old.md")); err != nil {
		t.Errorf("archived .agentrc-legacy/old.md missing: %v", err)
	}
}

func TestArchiveProjectAgentrc_NoLegacy(t *testing.T) {
	project := t.TempDir()
	archiveBase := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".agentrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".agentrc", "config.yaml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts := time.Date(2026, 4, 9, 14, 30, 22, 0, time.UTC)
	if _, err := ArchiveProjectAgentrc(project, archiveBase, filepath.Base(project), ts); err != nil {
		t.Fatalf("should succeed without .agentrc-legacy: %v", err)
	}
}
