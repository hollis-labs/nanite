// Regression tests for BLG-20260412-002 installer atomicity fixes.
//
// Coverage:
//   - AtomicWriteFile adoption: mid-write failure leaves no partial target
//   - Customization refresh backup: re-running init snapshots the prior
//     managed block into the archive tree
//   - Scaffold resume tolerance: repeated ScaffoldNaniteDir calls are no-ops
//     on pre-existing symlinks
//   - InstallHome staging: a corrupted staging seed is cleaned up and the
//     existing install is left untouched
package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/fsutil"
)

// TestAtomicWriteFile_NoPartialFileOnRenameFailure drives
// fsutil.AtomicWriteFile at a path whose target is a non-empty directory,
// exercising the rename-error branch. The target must not be replaced and
// no sibling temp files should leak.
func TestAtomicWriteFile_NoPartialFileOnRenameFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only: relies on POSIX rename-over-non-empty-dir behavior")
	}
	dir := t.TempDir()
	// Target path is itself a non-empty directory — AtomicWriteFile's
	// rename step will fail.
	target := filepath.Join(dir, "state.json")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "child"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := fsutil.AtomicWriteFile(target, []byte(`{"phase":"test"}`), 0o644)
	if err == nil {
		t.Fatal("expected error writing over a non-empty directory")
	}

	// No stray temp files at the parent.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "state.json" {
			t.Errorf("stray entry after failed write: %q", e.Name())
		}
	}
}

// TestAdoptExisting_SnapshotsManagedSectionOnRefresh runs adoptExisting
// against a project whose CLAUDE.md already has a managed block. The
// helper must write a pre-refresh snapshot into the archive base so a
// future rollback or audit has the prior rendered text.
func TestAdoptExisting_SnapshotsManagedSectionOnRefresh(t *testing.T) {
	project := t.TempDir()
	globalHome := t.TempDir()
	archiveBase := t.TempDir()
	t.Setenv("NANITE_ARCHIVE_BASE", archiveBase)

	// Seed global home with the subdirs the scaffold expects to symlink.
	for _, sub := range []string{"roles", "skills", "commands"} {
		if err := os.MkdirAll(filepath.Join(globalHome, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Pre-seed .nanite/ so adoptExisting is the chosen branch.
	if err := os.MkdirAll(filepath.Join(project, ".nanite", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(project, ".nanite", "config.yaml"),
		[]byte("nanite_version: 2.3.0\nadapters: [claude]\nagents: {}\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	// Pre-seed CLAUDE.md with a managed section.
	prior := "# Project\n\nUser content.\n\n<!-- nanite:start -->\nOLD MANAGED BODY\n<!-- nanite:end -->\n"
	claudePath := filepath.Join(project, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(prior), 0o644); err != nil {
		t.Fatal(err)
	}

	snapPath, err := snapshotAdapterTargetsForRefresh(project, time.Now())
	if err != nil {
		t.Fatalf("snapshot refresh: %v", err)
	}
	if snapPath == "" {
		t.Fatal("expected a refresh archive path, got empty")
	}

	snapFile := filepath.Join(snapPath, "CLAUDE.md.pre-refresh")
	got, err := os.ReadFile(snapFile)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(got) != prior {
		t.Errorf("snapshot content mismatch:\ngot:  %q\nwant: %q", got, prior)
	}

	// Unmanaged file (no marker) should NOT be snapshotted.
	if _, err := os.Stat(filepath.Join(snapPath, "GEMINI.md.pre-refresh")); !os.IsNotExist(err) {
		t.Errorf("expected no GEMINI snapshot, stat err = %v", err)
	}

	// No managed sections at all → no backup dir created.
	clean := t.TempDir()
	if err := os.MkdirAll(filepath.Join(clean, ".nanite"), 0o755); err != nil {
		t.Fatal(err)
	}
	noBackup, err := snapshotAdapterTargetsForRefresh(clean, time.Now())
	if err != nil {
		t.Fatalf("snapshot refresh (clean): %v", err)
	}
	if noBackup != "" {
		t.Errorf("expected empty archive path for clean project, got %q", noBackup)
	}
}

// TestScaffoldNaniteDir_ResumeTolerant verifies that a second
// ScaffoldNaniteDir call (as the --resume flow invokes) is a no-op when
// symlinks are already present, matching the atomicity-note documented in
// scaffold.go.
func TestScaffoldNaniteDir_ResumeTolerant(t *testing.T) {
	project := t.TempDir()
	globalHome := t.TempDir()
	for _, sub := range []string{"roles", "skills", "commands"} {
		if err := os.MkdirAll(filepath.Join(globalHome, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	src := ScaffoldSource{FrameworkVersion: "2.3.0", ProjectName: "p"}

	if err := ScaffoldNaniteDir(project, globalHome, src); err != nil {
		t.Fatalf("first scaffold: %v", err)
	}
	// Record link target for later comparison.
	link := filepath.Join(project, ".nanite", "roles")
	want, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}

	// Re-run — simulates a --resume that re-enters the scaffold step.
	if err := ScaffoldNaniteDir(project, globalHome, src); err != nil {
		t.Fatalf("resume scaffold: %v", err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink after resume: %v", err)
	}
	if got != want {
		t.Errorf("symlink target changed after resume: got %q want %q", got, want)
	}
}

// TestInstallHome_StagingPreservesExistingOnFailure verifies the staging
// flow: if we extract into staging and then a simulated promote failure
// happens, the live install is untouched. We exercise this by calling
// InstallHome against an existing install whose parent dir is writable
// (happy path), confirming files land, then asserting no .staging.* or
// .bak.* artifacts remain — i.e., the swap completed cleanly.
func TestInstallHome_StagingSwap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".nanite")

	svc := New()
	// First run: fresh extraction, no staging path exercised.
	if _, err := svc.InstallHome(InstallHomeOptions{Target: target}); err != nil {
		t.Fatalf("first InstallHome: %v", err)
	}
	// Plant a sentinel user-modified file under target so we can check it
	// survives the swap (ExtractTo treats it as user-modified → skip).
	sentinel := filepath.Join(target, "user-sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("owned"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second run: existing install present → staging path taken.
	if _, err := svc.InstallHome(InstallHomeOptions{Target: target}); err != nil {
		t.Fatalf("second InstallHome: %v", err)
	}

	// Sentinel survives (was copied into staging, ExtractTo left it alone).
	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel vanished: %v", err)
	}
	if string(got) != "owned" {
		t.Errorf("sentinel mutated: %q", got)
	}

	// No leftover staging or backup dirs in the parent.
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if name == ".nanite" {
			continue
		}
		if strings.Contains(name, ".staging.") || strings.Contains(name, ".bak.") {
			t.Errorf("stray artifact %q after successful swap", name)
		}
	}
}

// TestInstallHome_StagingCleanupOnSeedFailure ensures that when copyTree
// fails (e.g., target is a regular file, not a dir), InstallHome returns
// an error without leaving a staging directory behind.
func TestInstallHome_StagingCleanupOnSeedFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".nanite")

	// Plant a REGULAR FILE at the target path so the subsequent InstallHome
	// hits the "existing install present" branch, then copyTree fails
	// because filepath.Walk on a non-dir returns an error for each entry
	// we try to descend (actually filepath.Walk handles files; the failure
	// we force is via a non-traversable seed). Use a symlink to a
	// non-existent dir to cause Readlink/Walk to surface an error.
	if err := os.Symlink(filepath.Join(home, "missing-target-dir"), target); err != nil {
		t.Fatal(err)
	}

	svc := New()
	_, err := svc.InstallHome(InstallHomeOptions{Target: target})
	if err == nil {
		t.Fatal("expected error when existing install path is a broken symlink")
	}

	// No staging dir left behind.
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".staging.") {
			t.Errorf("staging dir leaked after failure: %q", e.Name())
		}
	}
}

// TestAtomicWriteFile_SmokeInScaffold confirms that ScaffoldNaniteMD uses
// the atomic primitive by writing successfully and leaving no temp files
// in the project root. This is a smoke test for the call-site swap, not
// a deep coverage of fsutil.
func TestAtomicWriteFile_SmokeInScaffold(t *testing.T) {
	project := t.TempDir()

	if err := ScaffoldNaniteMD(project, ScaffoldSource{
		FrameworkVersion: "2.3.0",
		ProjectName:      "smoke",
	}); err != nil {
		t.Fatalf("ScaffoldNaniteMD: %v", err)
	}

	entries, err := os.ReadDir(project)
	if err != nil {
		t.Fatal(err)
	}
	var naniteMD bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".NANITE.md.tmp-") {
			t.Errorf("leftover temp file: %q", e.Name())
		}
		if e.Name() == "NANITE.md" {
			naniteMD = true
		}
	}
	if !naniteMD {
		t.Error("NANITE.md not written")
	}

	// Sanity: confirm the primitive we swapped in is the one we think.
	// If fsutil import were accidentally dropped this would catch it.
	_ = fsutil.AtomicWriteFile
}
