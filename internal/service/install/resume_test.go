package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResume_FromArchivedPhase(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	// Simulate a partial migration that crashed after archive but before scaffold:
	// set up an archive dir with the project's .agentrc content and a state
	// marker stuck at PhaseArchived.
	basename := filepath.Base(project)
	archiveDir := filepath.Join(archBase, basename+"-2026-04-09")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mkFile := func(p, c string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkFile(filepath.Join(archiveDir, ".agentrc", "config.yaml"), "agentrc_version: 2.2.0\nagents: {}\n")
	mkFile(filepath.Join(archiveDir, ".agentrc", "agents", "backend.md"), "# Backend\n")

	state := NewState(basename, project, archiveDir, "2.3.0")
	state.MarkPhaseComplete(PhaseArchived)
	if err := WriteState(filepath.Join(archiveDir, StateFileName), state); err != nil {
		t.Fatal(err)
	}

	svc := New()
	if err := svc.Resume(ResumeOptions{
		ProjectDir:  project,
		ArchivePath: archiveDir,
		GlobalHome:  filepath.Join(home, ".nanite"),
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	// .nanite/ should now exist with carried-over content.
	if _, err := os.Stat(filepath.Join(project, ".nanite", "config.yaml")); err != nil {
		t.Errorf(".nanite/config.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".nanite", "agents", "backend.md")); err != nil {
		t.Errorf(".nanite/agents/backend.md missing: %v", err)
	}
	// NANITE.md scaffolded.
	if _, err := os.Stat(filepath.Join(project, "NANITE.md")); err != nil {
		t.Errorf("NANITE.md missing: %v", err)
	}
	// State marker should be at PhaseComplete.
	newState, err := ReadState(filepath.Join(archiveDir, StateFileName))
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if newState.Phase != PhaseComplete {
		t.Errorf("phase = %q, want %q", newState.Phase, PhaseComplete)
	}
}

func TestResume_AlreadyComplete(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	basename := filepath.Base(project)
	archiveDir := filepath.Join(archBase, basename+"-2026-04-09")
	os.MkdirAll(archiveDir, 0o755)

	state := NewState(basename, project, archiveDir, "2.3.0")
	state.MarkPhaseComplete(PhaseComplete)
	if err := WriteState(filepath.Join(archiveDir, StateFileName), state); err != nil {
		t.Fatal(err)
	}

	svc := New()
	// Should succeed without doing any work.
	if err := svc.Resume(ResumeOptions{
		ProjectDir:  project,
		ArchivePath: archiveDir,
		GlobalHome:  filepath.Join(home, ".nanite"),
	}); err != nil {
		t.Errorf("Resume on completed install should be no-op, got: %v", err)
	}
}

func TestRestart_ReversesArchive(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	// Simulate same partial state as Resume test. Use a historical date so
	// the fresh migration's archive dir (resolved from today's date) lands
	// at a different path and we can assert the old one is gone.
	basename := filepath.Base(project)
	archiveDir := filepath.Join(archBase, basename+"-2020-01-01")
	os.MkdirAll(archiveDir, 0o755)
	os.MkdirAll(filepath.Join(archiveDir, ".agentrc"), 0o755)
	os.WriteFile(filepath.Join(archiveDir, ".agentrc", "config.yaml"), []byte("agentrc_version: 2.2.0\nagents: {}\n"), 0o644)
	state := NewState(basename, project, archiveDir, "2.3.0")
	state.MarkPhaseComplete(PhaseArchived)
	WriteState(filepath.Join(archiveDir, StateFileName), state)

	svc := New()
	if err := svc.Restart(RestartOptions{
		ProjectDir:  project,
		ArchivePath: archiveDir,
		GlobalHome:  filepath.Join(home, ".nanite"),
	}); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	// After restart: a fresh migration has run. .nanite/ should exist, .agentrc/
	// should NOT be in the project (it was moved to a NEW archive dir).
	if _, err := os.Stat(filepath.Join(project, ".nanite", "config.yaml")); err != nil {
		t.Errorf(".nanite/config.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".agentrc")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".agentrc still in project after restart")
	}
	// Old archive dir should be gone (Restart removed it before the fresh migration).
	if _, err := os.Stat(archiveDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old archive still exists after restart")
	}
}
