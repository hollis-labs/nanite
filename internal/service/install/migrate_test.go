package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupFakeHome extracts the embedded framework to a tempdir-based HOME so
// that InstallProject has something to symlink against.
func setupFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	svc := New()
	if _, err := svc.InstallHome(InstallHomeOptions{Target: filepath.Join(home, ".nanite")}); err != nil {
		t.Fatalf("setup InstallHome: %v", err)
	}
	return home
}

func TestInstallProject_Fresh(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	})
	if err != nil {
		t.Fatalf("InstallProject: %v", err)
	}
	if !report.FreshScaffold {
		t.Error("expected FreshScaffold=true")
	}

	// .nanite/config.yaml exists
	if _, err := os.Stat(filepath.Join(project, ".nanite", "config.yaml")); err != nil {
		t.Errorf(".nanite/config.yaml missing: %v", err)
	}
	// NANITE.md exists
	if _, err := os.Stat(filepath.Join(project, "NANITE.md")); err != nil {
		t.Errorf("NANITE.md missing: %v", err)
	}
	// With no --adapters flag and no detection evidence, non-interactive fresh
	// scaffold should NOT write any CLI integration files (CLAUDE.md, etc.).
	if _, err := os.Stat(filepath.Join(project, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.md should NOT exist on non-interactive fresh install with no adapters flag: %v", err)
	}
}

func TestInstallProject_RefusesWhenAgentrcPresent(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	os.MkdirAll(filepath.Join(project, ".agentrc"), 0o755)
	os.WriteFile(filepath.Join(project, ".agentrc", "config.yaml"), []byte("x\n"), 0o644)

	svc := New()
	_, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	})
	if err == nil {
		t.Error("expected error when .agentrc/ present without migration flag")
	}
}

func TestInstallProject_RefusesWhenBothAgentrcAndNanitePresent(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	os.MkdirAll(filepath.Join(project, ".agentrc"), 0o755)
	os.MkdirAll(filepath.Join(project, ".nanite"), 0o755)

	svc := New()
	_, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	})
	if err == nil {
		t.Error("expected error when both .agentrc/ and .nanite/ present")
	}
}

func TestInstallProject_MigrateFromAgentrc(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()

	mkFile := func(p, c string) {
		t.Helper()
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(c), 0o644)
	}
	mkFile(filepath.Join(project, ".agentrc", "config.yaml"), "agentrc_version: 2.2.0\nagents: {}\n")
	mkFile(filepath.Join(project, ".agentrc", "agents", "backend.md"), "# Backend context\n")
	mkFile(filepath.Join(project, ".agentrc", "boot-prompt.md"), "# Boot\n")
	mkFile(filepath.Join(project, ".agentrc-legacy", "old.md"), "legacy\n")
	mkFile(filepath.Join(project, "CLAUDE.md"), "# Project\n\n## agentrc\n\nold stuff\n")

	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir:         project,
		GlobalHome:         filepath.Join(home, ".nanite"),
		MigrateFromAgentrc: true,
	})
	if err != nil {
		t.Fatalf("InstallProject: %v", err)
	}
	if !report.Migrated {
		t.Error("expected Migrated=true")
	}

	// .agentrc/ archived
	if _, err := os.Stat(filepath.Join(project, ".agentrc")); !errors.Is(err, os.ErrNotExist) {
		t.Error(".agentrc not archived")
	}
	// .nanite/ scaffolded with carried-over content
	if _, err := os.Stat(filepath.Join(project, ".nanite", "config.yaml")); err != nil {
		t.Errorf(".nanite/config.yaml missing: %v", err)
	}
	cfgData, _ := os.ReadFile(filepath.Join(project, ".nanite", "config.yaml"))
	if !strings.Contains(string(cfgData), "nanite_version:") {
		t.Errorf("config.yaml missing renamed version field: %q", cfgData)
	}
	if strings.Contains(string(cfgData), "agentrc_version:") {
		t.Errorf("config.yaml still has agentrc_version: %q", cfgData)
	}
	// agents/ content carried over
	if _, err := os.Stat(filepath.Join(project, ".nanite", "agents", "backend.md")); err != nil {
		t.Errorf(".nanite/agents/backend.md missing: %v", err)
	}
	// boot-prompt.md carried over
	if _, err := os.Stat(filepath.Join(project, ".nanite", "boot-prompt.md")); err != nil {
		t.Errorf(".nanite/boot-prompt.md missing: %v", err)
	}
	// CLAUDE.md agentrc section removed, nanite markers added
	claude, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if strings.Contains(string(claude), "## agentrc") {
		t.Errorf("CLAUDE.md still has agentrc section: %q", claude)
	}
	if !strings.Contains(string(claude), "<!-- nanite:start -->") {
		t.Errorf("CLAUDE.md missing managed section: %q", claude)
	}
	// Archive dir exists and has state marker at PhaseComplete
	if report.ArchivePath == "" {
		t.Fatal("empty ArchivePath")
	}
	state, err := ReadState(filepath.Join(report.ArchivePath, StateFileName))
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if state.Phase != PhaseComplete {
		t.Errorf("phase = %q, want %q", state.Phase, PhaseComplete)
	}
	// CLAUDE.md.pre-edit snapshot exists
	if _, err := os.Stat(filepath.Join(report.ArchivePath, "CLAUDE.md.pre-edit")); err != nil {
		t.Errorf("CLAUDE.md.pre-edit snapshot missing: %v", err)
	}
}

func TestInstallProject_ArchiveOnly(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	os.MkdirAll(filepath.Join(project, ".agentrc"), 0o755)
	os.WriteFile(filepath.Join(project, ".agentrc", "config.yaml"), []byte("x\n"), 0o644)

	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir:  project,
		GlobalHome:  filepath.Join(home, ".nanite"),
		ArchiveOnly: true,
	})
	if err != nil {
		t.Fatalf("InstallProject: %v", err)
	}
	if !report.ArchiveOnly {
		t.Error("expected ArchiveOnly=true")
	}
	// .agentrc/ gone
	if _, err := os.Stat(filepath.Join(project, ".agentrc")); !errors.Is(err, os.ErrNotExist) {
		t.Error(".agentrc not archived")
	}
	// .nanite/ NOT created
	if _, err := os.Stat(filepath.Join(project, ".nanite")); !errors.Is(err, os.ErrNotExist) {
		t.Error(".nanite/ should not be scaffolded in archive-only mode")
	}
	// NANITE.md NOT created
	if _, err := os.Stat(filepath.Join(project, "NANITE.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("NANITE.md should not be scaffolded in archive-only mode")
	}
}

func TestInstallProject_AdoptExisting(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()

	// Pre-create .nanite/ as if PR #11 had done a manual rename: config.yaml +
	// agents/ exist, but no symlinks, no NANITE.md, no CLAUDE.md managed section.
	mkFile := func(p, c string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkFile(filepath.Join(project, ".nanite", "config.yaml"), "nanite_version: 2.3.0\nagents: {}\n")
	mkFile(filepath.Join(project, ".nanite", "agents", "backend.md"), "# Backend\n")
	mkFile(filepath.Join(project, "CLAUDE.md"), "# Project\n\nUser content.\n")

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	})
	if err != nil {
		t.Fatalf("InstallProject: %v", err)
	}
	if !report.Adopted {
		t.Error("expected Adopted=true")
	}

	// Symlinks should now exist.
	for _, sub := range []string{"roles", "skills", "commands"} {
		link := filepath.Join(project, ".nanite", sub)
		info, err := os.Lstat(link)
		if err != nil {
			t.Errorf("%s symlink missing: %v", sub, err)
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is not a symlink", sub)
		}
	}
	// NANITE.md scaffolded.
	if _, err := os.Stat(filepath.Join(project, "NANITE.md")); err != nil {
		t.Errorf("NANITE.md missing: %v", err)
	}
	// CLAUDE.md has managed section and preserves user content.
	claude, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	claudeStr := string(claude)
	if !strings.Contains(claudeStr, "<!-- nanite:start -->") {
		t.Errorf("CLAUDE.md missing managed section: %q", claude)
	}
	if !strings.Contains(claudeStr, "User content.") {
		t.Errorf("user content lost: %q", claude)
	}
	// Existing config.yaml and agents/backend.md preserved verbatim.
	cfg, _ := os.ReadFile(filepath.Join(project, ".nanite", "config.yaml"))
	if string(cfg) != "nanite_version: 2.3.0\nagents: {}\n" {
		t.Errorf("config.yaml modified: %q", cfg)
	}
	data, _ := os.ReadFile(filepath.Join(project, ".nanite", "agents", "backend.md"))
	if string(data) != "# Backend\n" {
		t.Errorf("existing agents content modified: %q", data)
	}
}

func TestInstallProject_DetectsPartialInstall(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	// Set up a dangling archive: state marker at PhaseArchived, no .agentrc
	// or .nanite in the project, but a matching archive dir with the marker.
	basename := filepath.Base(project)
	archiveDir := filepath.Join(archBase, basename+"-2026-04-09")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(archiveDir, ".agentrc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, ".agentrc", "config.yaml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := NewState(basename, project, archiveDir, "2.3.0")
	state.MarkPhaseComplete(PhaseArchived)
	if err := WriteState(filepath.Join(archiveDir, StateFileName), state); err != nil {
		t.Fatal(err)
	}

	svc := New()
	_, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	})
	if err == nil {
		t.Fatal("expected error for partial install without --resume or --restart")
	}
	if !errors.Is(err, ErrPartialInstall) {
		t.Errorf("expected ErrPartialInstall, got %v", err)
	}
	// Error message should contain the phase and the archive path for the CLI prompt.
	if !strings.Contains(err.Error(), "archived") {
		t.Errorf("error message missing phase: %v", err)
	}
	if !strings.Contains(err.Error(), "resume") {
		t.Errorf("error message missing --resume hint: %v", err)
	}
}

func TestInstallProject_IgnoresCompletePartialState(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	// Set up an archive with a marker at PhaseComplete (e.g., from a prior
	// archive-only install that finished cleanly). Fresh scaffold on this
	// project should proceed without error — a completed archive doesn't
	// count as "partial".
	basename := filepath.Base(project)
	archiveDir := filepath.Join(archBase, basename+"-2026-04-09")
	os.MkdirAll(archiveDir, 0o755)
	state := NewState(basename, project, archiveDir, "2.3.0")
	state.MarkPhaseComplete(PhaseArchived)
	state.MarkPhaseComplete(PhaseComplete)
	WriteState(filepath.Join(archiveDir, StateFileName), state)

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.FreshScaffold {
		t.Error("expected fresh scaffold to proceed despite completed archive")
	}
}

func TestInstallProject_AdoptExisting_Idempotent(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()

	os.MkdirAll(filepath.Join(project, ".nanite"), 0o755)
	os.WriteFile(filepath.Join(project, ".nanite", "config.yaml"), []byte("nanite_version: 2.3.0\nagents: {}\n"), 0o644)

	svc := New()
	// First adopt.
	if _, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	}); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	firstNanite, _ := os.ReadFile(filepath.Join(project, "NANITE.md"))

	// Second adopt.
	if _, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	}); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	secondNanite, _ := os.ReadFile(filepath.Join(project, "NANITE.md"))

	if string(first) != string(second) {
		t.Errorf("CLAUDE.md not idempotent:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if string(firstNanite) != string(secondNanite) {
		t.Errorf("NANITE.md not idempotent:\nfirst:\n%s\nsecond:\n%s", firstNanite, secondNanite)
	}
}
