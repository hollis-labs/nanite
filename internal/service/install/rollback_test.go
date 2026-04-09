package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollback_RoundTrip(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	mkFile := func(p, c string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkFile(filepath.Join(project, ".agentrc", "config.yaml"), "agentrc_version: 2.2.0\nagents: {}\n")
	mkFile(filepath.Join(project, ".agentrc", "agents", "backend.md"), "# Backend\n")
	originalCLAUDE := "# Project\n\n## agentrc\n\nold loader\n"
	mkFile(filepath.Join(project, "CLAUDE.md"), originalCLAUDE)

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir:         project,
		GlobalHome:         filepath.Join(home, ".nanite"),
		MigrateFromAgentrc: true,
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Sanity: .agentrc/ is gone, .nanite/ is present.
	if _, err := os.Stat(filepath.Join(project, ".agentrc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(".agentrc not archived during setup")
	}

	// Roll back.
	if err := svc.Rollback(RollbackOptions{ProjectDir: project, ArchivePath: report.ArchivePath}); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// .agentrc/ restored.
	if _, err := os.Stat(filepath.Join(project, ".agentrc", "config.yaml")); err != nil {
		t.Errorf(".agentrc not restored: %v", err)
	}
	// .nanite/ gone.
	if _, err := os.Stat(filepath.Join(project, ".nanite")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".nanite not removed after rollback")
	}
	// NANITE.md gone.
	if _, err := os.Stat(filepath.Join(project, "NANITE.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("NANITE.md not removed after rollback")
	}
	// CLAUDE.md restored with the old agentrc section.
	data, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if !strings.Contains(string(data), "## agentrc") {
		t.Errorf("CLAUDE.md agentrc section not restored: %q", data)
	}
}

func TestRollback_NeverMigrated(t *testing.T) {
	project := t.TempDir()
	svc := New()
	if err := svc.Rollback(RollbackOptions{ProjectDir: project}); err == nil {
		t.Error("expected error for never-migrated project")
	}
}

func TestRollback_FindsLatestWhenArchivePathEmpty(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	// Set up a fake project with .agentrc/ and migrate it.
	os.MkdirAll(filepath.Join(project, ".agentrc"), 0o755)
	os.WriteFile(filepath.Join(project, ".agentrc", "config.yaml"), []byte("agentrc_version: 2.2.0\nagents: {}\n"), 0o644)
	os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("# P\n\n## agentrc\n\nstuff\n"), 0o644)

	svc := New()
	if _, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir:         project,
		GlobalHome:         filepath.Join(home, ".nanite"),
		MigrateFromAgentrc: true,
	}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Roll back without specifying ArchivePath — should find the latest.
	if err := svc.Rollback(RollbackOptions{ProjectDir: project}); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agentrc", "config.yaml")); err != nil {
		t.Errorf(".agentrc not restored: %v", err)
	}
}

func TestRollback_ArchiveWithoutStateMarkerRefuses(t *testing.T) {
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)
	// Create a matching archive dir but NO state marker.
	basename := filepath.Base(project)
	archiveDir := filepath.Join(archBase, basename+"-2026-04-09")
	os.MkdirAll(archiveDir, 0o755)
	os.MkdirAll(filepath.Join(archiveDir, ".agentrc"), 0o755)

	svc := New()
	err := svc.Rollback(RollbackOptions{ProjectDir: project, ArchivePath: archiveDir})
	if err == nil {
		t.Error("expected error when archive lacks state marker")
	}
}
