package install

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestIntegration_FullMigrationRoundTrip exercises the full flow:
// 1. Build a realistic fake project with .agentrc/, .agentrc-legacy/, CLAUDE.md, and README.md
// 2. Take a snapshot of the project's full file tree
// 3. Migrate-from-agentrc
// 4. Verify all invariants: archive dir created, .nanite/ populated with carried-over content,
//    NANITE.md present, CLAUDE.md has managed section with agentrc section removed,
//    README.md untouched, state marker at PhaseComplete, CLAUDE.md.pre-edit snapshot exists,
//    renamed config field (agentrc_version → nanite_version)
// 5. Roll back
// 6. Verify the project is bit-for-bit equivalent to the original snapshot
func TestIntegration_FullMigrationRoundTrip(t *testing.T) {
	home := setupFakeHome(t)
	project := t.TempDir()
	archBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archBase)

	// Build realistic fake project mirroring a real agentrc-installed layout.
	mkFile := func(p, c string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	originalConfig := "agentrc_version: 2.2.0\nagents:\n  backend-dev:\n    name: Backend\n    roles: [backend, go]\n    context: agents/backend.md\n"
	mkFile(filepath.Join(project, ".agentrc", "config.yaml"), originalConfig)
	mkFile(filepath.Join(project, ".agentrc", "agents", "backend.md"), "# Backend Context\n\nGo service.\n")
	mkFile(filepath.Join(project, ".agentrc", "boot-prompt.md"), "# Session boot\n\nRead this first.\n")
	mkFile(filepath.Join(project, ".agentrc-legacy", "old-v1.md"), "v1 legacy\n")
	originalCLAUDE := "# Project\n\nSome user prose before agentrc.\n\n## agentrc\n\n- If `.agentrc/boot-prompt.md` exists, read it first.\n\nMore agentrc instructions.\n"
	mkFile(filepath.Join(project, "CLAUDE.md"), originalCLAUDE)
	mkFile(filepath.Join(project, "README.md"), "# Test project\n")

	// Snapshot the original state.
	originalState := snapshotDir(t, project)

	// Migrate. Pass all four adapters explicitly: the fixture has no CLI
	// indicator files (no .claude/agents/, no .codex, etc.) so detection
	// would return empty. The test asserts that all four adapter files are
	// written, so we drive adapter selection via the --adapters flag.
	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir:         project,
		GlobalHome:         filepath.Join(home, ".nanite"),
		MigrateFromAgentrc: true,
		Adapters:           "claude,codex,gemini,opencode",
	})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if !report.Migrated {
		t.Fatal("expected Migrated=true")
	}

	// --- Post-migration invariants ---

	// Project layout.
	if _, err := os.Stat(filepath.Join(project, ".agentrc")); !os.IsNotExist(err) {
		t.Error(".agentrc not archived")
	}
	if _, err := os.Stat(filepath.Join(project, ".agentrc-legacy")); !os.IsNotExist(err) {
		t.Error(".agentrc-legacy not archived")
	}

	// Archive dir contents.
	if _, err := os.Stat(filepath.Join(report.ArchivePath, ".agentrc", "config.yaml")); err != nil {
		t.Errorf("archived .agentrc/config.yaml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(report.ArchivePath, ".agentrc-legacy", "old-v1.md")); err != nil {
		t.Errorf("archived .agentrc-legacy missing: %v", err)
	}

	// New .nanite/ has copied content with field renames.
	newCfg, _ := os.ReadFile(filepath.Join(project, ".nanite", "config.yaml"))
	if !strings.Contains(string(newCfg), "nanite_version:") {
		t.Errorf(".nanite/config.yaml missing nanite_version: %q", newCfg)
	}
	if strings.Contains(string(newCfg), "agentrc_version:") {
		t.Errorf(".nanite/config.yaml still has agentrc_version: %q", newCfg)
	}
	// Agents carried over.
	if _, err := os.Stat(filepath.Join(project, ".nanite", "agents", "backend.md")); err != nil {
		t.Errorf(".nanite/agents/backend.md missing: %v", err)
	}
	// boot-prompt carried over.
	if _, err := os.Stat(filepath.Join(project, ".nanite", "boot-prompt.md")); err != nil {
		t.Errorf(".nanite/boot-prompt.md missing: %v", err)
	}
	// NANITE.md scaffolded.
	if _, err := os.Stat(filepath.Join(project, "NANITE.md")); err != nil {
		t.Errorf("NANITE.md missing: %v", err)
	}
	// CLAUDE.md: agentrc section gone, user prose preserved, managed section present.
	claude, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	if strings.Contains(string(claude), "## agentrc") {
		t.Errorf("CLAUDE.md still has agentrc section: %q", claude)
	}
	if !strings.Contains(string(claude), "Some user prose before agentrc") {
		t.Errorf("CLAUDE.md user content lost: %q", claude)
	}
	if !strings.Contains(string(claude), "<!-- nanite:start -->") {
		t.Errorf("CLAUDE.md missing managed section markers: %q", claude)
	}
	// README.md untouched.
	readme, _ := os.ReadFile(filepath.Join(project, "README.md"))
	if string(readme) != "# Test project\n" {
		t.Errorf("README.md modified: %q", readme)
	}
	// State marker is PhaseComplete.
	state, err := ReadState(filepath.Join(report.ArchivePath, StateFileName))
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if state.Phase != PhaseComplete {
		t.Errorf("phase = %q, want %q", state.Phase, PhaseComplete)
	}
	// CLAUDE.md.pre-edit snapshot exists.
	if _, err := os.Stat(filepath.Join(report.ArchivePath, "CLAUDE.md.pre-edit")); err != nil {
		t.Errorf("CLAUDE.md.pre-edit snapshot missing: %v", err)
	}

	// Adapter sync should have created managed sections in the other CLI
	// target files. The migration carried over backend-dev from the
	// archived .agentrc/config.yaml, so all four adapters have an agent
	// to write about.
	for _, name := range []string{"AGENTS.md", "GEMINI.md", "OPENCODE.md"} {
		data, err := os.ReadFile(filepath.Join(project, name))
		if err != nil {
			t.Errorf("%s missing after adapter sync: %v", name, err)
			continue
		}
		content := string(data)
		if !strings.Contains(content, "<!-- nanite:start -->") {
			t.Errorf("%s missing nanite:start marker:\n%s", name, content)
		}
		if !strings.Contains(content, "Backend") {
			t.Errorf("%s missing Backend agent reference:\n%s", name, content)
		}
	}
	// These files didn't exist before install, so no pre-edit snapshots
	// should have been written for them.
	for _, name := range []string{"AGENTS.md.pre-edit", "GEMINI.md.pre-edit", "OPENCODE.md.pre-edit"} {
		if _, err := os.Stat(filepath.Join(report.ArchivePath, name)); !os.IsNotExist(err) {
			t.Errorf("%s should not exist (file was created by installer, not pre-existing)", name)
		}
	}

	// --- Roll back ---
	if err := svc.Rollback(RollbackOptions{ProjectDir: project, ArchivePath: report.ArchivePath}); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Verify bit-for-bit equivalence to original.
	rolledBack := snapshotDir(t, project)
	diffs := compareSnapshots(originalState, rolledBack)
	if len(diffs) != 0 {
		t.Errorf("rollback did not restore project to original state. Differences:\n%s", strings.Join(diffs, "\n"))
	}
}

// snapshotDir returns a map of relPath → file content (bytes) for every
// file under root, excluding directories themselves.
func snapshotDir(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = data
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}

// compareSnapshots returns a slice of human-readable difference descriptions.
// An empty slice means the two snapshots are bit-for-bit identical.
func compareSnapshots(before, after map[string][]byte) []string {
	var diffs []string
	for path, beforeBytes := range before {
		afterBytes, ok := after[path]
		if !ok {
			diffs = append(diffs, "  missing after rollback: "+path)
			continue
		}
		if string(beforeBytes) != string(afterBytes) {
			diffs = append(diffs, "  content differs: "+path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			diffs = append(diffs, "  extra after rollback: "+path)
		}
	}
	return diffs
}

func TestInstallProject_Fresh_NoAdapters_NoCLIFiles(t *testing.T) {
	dir := t.TempDir()
	home := setupFakeHome(t)

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: dir,
		GlobalHome: filepath.Join(home, ".nanite"),
		NoAdapters: true,
	})
	if err != nil {
		t.Fatalf("InstallProject: %v", err)
	}
	if !report.FreshScaffold {
		t.Error("expected FreshScaffold=true")
	}
	if len(report.Adapters) != 0 {
		t.Errorf("Adapters: got %v, want []", report.Adapters)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", "OPENCODE.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s should NOT exist with --no-adapters: %v", name, err)
		}
	}

	cfg, err := loadProjectConfig(filepath.Join(dir, ".nanite", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Adapters == nil {
		t.Error("expected non-nil Adapters in persisted config")
	}
	if len(*cfg.Adapters) != 0 {
		t.Errorf("expected empty Adapters in persisted config, got %v", *cfg.Adapters)
	}
}

func TestInstallProject_Fresh_AdaptersFlag_WritesFiles(t *testing.T) {
	dir := t.TempDir()
	home := setupFakeHome(t)

	svc := New()
	report, err := svc.InstallProject(InstallProjectOptions{
		ProjectDir: dir,
		GlobalHome: filepath.Join(home, ".nanite"),
		Adapters:   "claude,codex",
	})
	if err != nil {
		t.Fatalf("InstallProject: %v", err)
	}
	if !report.FreshScaffold {
		t.Error("expected FreshScaffold=true")
	}
	if !reflect.DeepEqual(report.Adapters, []string{"claude", "codex"}) {
		t.Errorf("Adapters: got %v, want [claude codex]", report.Adapters)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "GEMINI.md")); !os.IsNotExist(err) {
		t.Errorf("GEMINI.md should NOT exist: %v", err)
	}
}

func TestInstallProject_Adopt_Reconfigure_RemovesDroppedAdapter(t *testing.T) {
	dir := t.TempDir()
	home := setupFakeHome(t)
	globalHome := filepath.Join(home, ".nanite")

	// First install with claude + codex.
	if _, err := New().InstallProject(InstallProjectOptions{
		ProjectDir: dir,
		GlobalHome: globalHome,
		Adapters:   "claude,codex",
	}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md should exist after first install: %v", err)
	}

	// Re-run with --reconfigure and only claude → AGENTS.md should be cleaned up.
	report, err := New().InstallProject(InstallProjectOptions{
		ProjectDir:  dir,
		GlobalHome:  globalHome,
		Adapters:    "claude",
		Reconfigure: true,
	})
	if err != nil {
		t.Fatalf("reconfigure: %v", err)
	}
	if !report.Adopted {
		t.Errorf("expected Adopted=true, got %+v", report)
	}
	if !reflect.DeepEqual(report.Adapters, []string{"claude"}) {
		t.Errorf("Adapters: got %v, want [claude]", report.Adapters)
	}
	if len(report.AdapterCleanups) != 1 {
		t.Errorf("expected 1 cleanup report, got %d", len(report.AdapterCleanups))
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("AGENTS.md should be deleted, stat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md should still exist: %v", err)
	}
}
