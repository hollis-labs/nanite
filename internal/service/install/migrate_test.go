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
	// CLAUDE.md was created with the managed section
	claude, err := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if err != nil {
		t.Errorf("CLAUDE.md missing: %v", err)
	} else if !strings.Contains(string(claude), "<!-- nanite:start -->") {
		t.Errorf("CLAUDE.md missing nanite markers: %q", claude)
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

func TestInstallProject_AdoptPathNotImplementedYet(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	os.MkdirAll(filepath.Join(project, ".nanite"), 0o755)

	svc := New()
	_, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: project,
		GlobalHome: filepath.Join(home, ".nanite"),
	})
	if err == nil {
		t.Error("expected error from not-yet-implemented adopt path")
	}
	if !strings.Contains(err.Error(), "Task 9") && !strings.Contains(err.Error(), "adopt") {
		t.Errorf("error should mention adopt or Task 9: %v", err)
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
