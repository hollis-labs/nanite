package install

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/fsutil"
	"github.com/hollis-labs/nanite/internal/pathsafe"
)

func TestInstallProjectOptions_Normalized_DefaultsNilIOStreams(t *testing.T) {
	opts := InstallProjectOptions{
		// Stdin and Stdout intentionally nil
	}
	norm := opts.normalized()
	if norm.Stdin == nil {
		t.Error("Stdin should default to os.Stdin")
	}
	if norm.Stdout == nil {
		t.Error("Stdout should default to os.Stdout")
	}
	if norm.Stdin != os.Stdin {
		t.Error("Stdin should be os.Stdin")
	}
	if norm.Stdout != os.Stdout {
		t.Error("Stdout should be os.Stdout")
	}
}

func TestInstallProjectOptions_Normalized_PreservesSetIOStreams(t *testing.T) {
	var buf bytes.Buffer
	in := bytes.NewBufferString("hi")
	opts := InstallProjectOptions{
		Stdin:  in,
		Stdout: &buf,
	}
	norm := opts.normalized()
	if norm.Stdin != in {
		t.Error("Stdin should be preserved when set")
	}
	if norm.Stdout != &buf {
		t.Error("Stdout should be preserved when set")
	}
}

func TestService_InstallHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := mustTestPath(t, home, ".nanite")

	svc := New()
	report, err := svc.InstallHome(InstallHomeOptions{Target: target})
	if err != nil {
		t.Fatalf("InstallHome: %v", err)
	}
	if report.Created == 0 {
		t.Error("expected Created > 0 on fresh install")
	}

	// VERSION file should have been extracted.
	if _, err := os.Stat(filepath.Join(target, "VERSION")); err != nil {
		t.Errorf("VERSION missing after install: %v", err)
	}

	// Second run should be idempotent.
	report2, err := svc.InstallHome(InstallHomeOptions{Target: target})
	if err != nil {
		t.Fatalf("second InstallHome: %v", err)
	}
	if report2.Created != 0 {
		t.Errorf("second run Created = %d, want 0", report2.Created)
	}
	if report2.Unchanged == 0 {
		t.Error("second run expected Unchanged > 0")
	}
}

func TestService_InstallHome_DefaultTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	svc := New()
	// Empty Target should default to ~/.nanite (resolved from the HOME env var).
	report, err := svc.InstallHome(InstallHomeOptions{})
	if err != nil {
		t.Fatalf("InstallHome: %v", err)
	}
	if report.Created == 0 {
		t.Error("expected Created > 0")
	}
	if _, err := os.Stat(filepath.Join(home, ".nanite", "VERSION")); err != nil {
		t.Errorf("VERSION missing at default target: %v", err)
	}
}

func TestService_InstallHome_UpgradesLegacyStockAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := mustTestPath(t, home, ".nanite")
	svc := New()
	if _, err := svc.InstallHome(InstallHomeOptions{Target: target}); err != nil {
		t.Fatalf("fresh InstallHome: %v", err)
	}

	legacy, err := os.ReadFile("../../assets/testdata/legacy-2.3.0/commands/doc-note.md") // #nosec G304 -- fixed repository test fixture.
	if err != nil {
		t.Fatalf("read legacy fixture: %v", err)
	}
	legacy = bytes.TrimSuffix(legacy, []byte("\n"))
	stockPath := mustTestPath(t, target, "commands/doc-note.md")
	if writeErr := fsutil.AtomicWriteFile(stockPath, legacy, 0o644); writeErr != nil {
		t.Fatalf("write legacy stock asset: %v", writeErr)
	}
	customPath := mustTestPath(t, target, "commands/doc-search.md")
	custom := []byte("user-owned search instructions\n")
	if writeErr := fsutil.AtomicWriteFile(customPath, custom, 0o644); writeErr != nil {
		t.Fatalf("write customized asset: %v", writeErr)
	}
	userFile := mustTestPath(t, target, "user-notes.md")
	if writeErr := fsutil.AtomicWriteFile(userFile, []byte("preserve me\n"), 0o644); writeErr != nil {
		t.Fatalf("write user file: %v", writeErr)
	}

	report, err := svc.InstallHome(InstallHomeOptions{Target: target})
	if err != nil {
		t.Fatalf("legacy upgrade: %v", err)
	}
	if report.Updated != 1 || report.Skipped != 1 || len(report.ConflictFiles) != 2 {
		t.Fatalf("unexpected legacy upgrade report: %+v", report)
	}
	got, err := os.ReadFile(customPath) // #nosec G304 -- confined beneath t.TempDir by mustTestPath.
	if err != nil || !bytes.Equal(got, custom) {
		t.Fatalf("customized asset changed: got %q, err %v", got, err)
	}
	// #nosec G304 -- confined beneath t.TempDir by mustTestPath.
	userData, readUserErr := os.ReadFile(userFile)
	if readUserErr != nil || string(userData) != "preserve me\n" {
		t.Fatalf("user-created file changed: got %q, err %v", userData, readUserErr)
	}
	before := snapshotTree(t, target)

	second, err := svc.InstallHome(InstallHomeOptions{Target: target})
	if err != nil {
		t.Fatalf("repeat legacy upgrade: %v", err)
	}
	if second.Updated != 0 || second.Skipped != 1 || len(second.ConflictFiles) != 2 {
		t.Fatalf("unexpected repeat report: %+v", second)
	}
	after := snapshotTree(t, target)
	if !bytes.Equal(before, after) {
		t.Fatal("repeat refresh changed the installed tree")
	}
}

func snapshotTree(t *testing.T, root string) []byte {
	t.Helper()
	var snapshot bytes.Buffer
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// #nosec G304 G122 -- filepath.Walk supplied a path beneath the isolated test root.
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&snapshot, "%s\x00%d\x00", filepath.ToSlash(rel), len(data))
		snapshot.Write(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot tree: %v", err)
	}
	return snapshot.Bytes()
}

func mustTestPath(t *testing.T, root, rel string) string {
	t.Helper()
	path, err := pathsafe.ResolveUnder(root, rel)
	if err != nil {
		t.Fatalf("resolve test path %s: %v", rel, err)
	}
	return path
}
