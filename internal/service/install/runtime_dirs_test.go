package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureRuntimeDirs_FreshHome verifies that EnsureRuntimeDirs creates all
// expected subdirectories and their README.md files when starting from an
// empty nanite home.
func TestEnsureRuntimeDirs_FreshHome(t *testing.T) {
	home := t.TempDir()

	report, err := EnsureRuntimeDirs(home)
	if err != nil {
		t.Fatalf("EnsureRuntimeDirs: %v", err)
	}

	if report.DirsCreated != len(runtimeDirs) {
		t.Errorf("DirsCreated = %d, want %d", report.DirsCreated, len(runtimeDirs))
	}
	if report.DirsExisted != 0 {
		t.Errorf("DirsExisted = %d, want 0 on fresh home", report.DirsExisted)
	}
	if report.READMEsCreated != len(runtimeDirs) {
		t.Errorf("READMEsCreated = %d, want %d", report.READMEsCreated, len(runtimeDirs))
	}
	if report.READMEsSkipped != 0 {
		t.Errorf("READMEsSkipped = %d, want 0 on fresh home", report.READMEsSkipped)
	}

	// Spot-check: every directory and README should exist.
	for _, rd := range runtimeDirs {
		dirPath := filepath.Join(home, rd.name)
		if info, err := os.Stat(dirPath); err != nil {
			t.Errorf("dir %s missing after EnsureRuntimeDirs: %v", rd.name, err)
		} else if !info.IsDir() {
			t.Errorf("path %s exists but is not a directory", rd.name)
		}

		readmePath := filepath.Join(dirPath, "README.md")
		data, err := os.ReadFile(readmePath)
		if err != nil {
			t.Errorf("README.md missing in %s: %v", rd.name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("README.md in %s is empty", rd.name)
		}
		// Heading should match the dir name.
		if !strings.Contains(string(data), "# "+rd.name) {
			t.Errorf("README.md in %s does not contain expected heading '# %s'", rd.name, rd.name)
		}
	}
}

// TestEnsureRuntimeDirs_Idempotent verifies that a second call is a no-op:
// directories already exist and READMEs are not re-written.
func TestEnsureRuntimeDirs_Idempotent(t *testing.T) {
	home := t.TempDir()

	if _, err := EnsureRuntimeDirs(home); err != nil {
		t.Fatalf("first EnsureRuntimeDirs: %v", err)
	}

	report2, err := EnsureRuntimeDirs(home)
	if err != nil {
		t.Fatalf("second EnsureRuntimeDirs: %v", err)
	}

	if report2.DirsCreated != 0 {
		t.Errorf("second run DirsCreated = %d, want 0", report2.DirsCreated)
	}
	if report2.DirsExisted != len(runtimeDirs) {
		t.Errorf("second run DirsExisted = %d, want %d", report2.DirsExisted, len(runtimeDirs))
	}
	if report2.READMEsCreated != 0 {
		t.Errorf("second run READMEsCreated = %d, want 0", report2.READMEsCreated)
	}
	if report2.READMEsSkipped != len(runtimeDirs) {
		t.Errorf("second run READMEsSkipped = %d, want %d", report2.READMEsSkipped, len(runtimeDirs))
	}
}

// TestEnsureRuntimeDirs_SkipsUserModifiedREADME verifies that a README.md
// the user has customized is preserved verbatim — EnsureRuntimeDirs must not
// overwrite it.
func TestEnsureRuntimeDirs_SkipsUserModifiedREADME(t *testing.T) {
	home := t.TempDir()

	// Run once to set up the directory structure.
	if _, err := EnsureRuntimeDirs(home); err != nil {
		t.Fatalf("first EnsureRuntimeDirs: %v", err)
	}

	// User edits the sandboxes README.
	customContent := "# my custom content — do not overwrite\n"
	sandboxReadme := filepath.Join(home, "sandboxes", "README.md")
	if err := os.WriteFile(sandboxReadme, []byte(customContent), 0o644); err != nil {
		t.Fatalf("write custom README: %v", err)
	}

	// Second run must leave the custom README alone.
	if _, err := EnsureRuntimeDirs(home); err != nil {
		t.Fatalf("second EnsureRuntimeDirs: %v", err)
	}

	after, err := os.ReadFile(sandboxReadme)
	if err != nil {
		t.Fatalf("read README after second run: %v", err)
	}
	if string(after) != customContent {
		t.Errorf("custom README was overwritten: got %q, want %q", string(after), customContent)
	}
}

// TestEnsureRuntimeDirs_DirsAlreadyExistNoReadme verifies that if the
// directory pre-exists (e.g. created by the runtime) but has no README, the
// README is still created without touching the directory itself.
func TestEnsureRuntimeDirs_DirsAlreadyExistNoReadme(t *testing.T) {
	home := t.TempDir()

	// Pre-create all dirs without READMEs, as the runtime would.
	for _, rd := range runtimeDirs {
		if err := os.MkdirAll(filepath.Join(home, rd.name), 0o755); err != nil {
			t.Fatalf("pre-create %s: %v", rd.name, err)
		}
	}

	report, err := EnsureRuntimeDirs(home)
	if err != nil {
		t.Fatalf("EnsureRuntimeDirs: %v", err)
	}

	if report.DirsCreated != 0 {
		t.Errorf("DirsCreated = %d, want 0 (dirs already exist)", report.DirsCreated)
	}
	if report.DirsExisted != len(runtimeDirs) {
		t.Errorf("DirsExisted = %d, want %d", report.DirsExisted, len(runtimeDirs))
	}
	if report.READMEsCreated != len(runtimeDirs) {
		t.Errorf("READMEsCreated = %d, want %d (READMEs were missing)", report.READMEsCreated, len(runtimeDirs))
	}
}

// TestInstallHome_CreatesRuntimeDirs verifies the end-to-end integration: a
// fresh InstallHome call creates the runtime dirs with READMEs.
func TestInstallHome_CreatesRuntimeDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".nanite")

	svc := New()
	if _, err := svc.InstallHome(InstallHomeOptions{Target: target}); err != nil {
		t.Fatalf("InstallHome: %v", err)
	}

	for _, rd := range runtimeDirs {
		readmePath := filepath.Join(target, rd.name, "README.md")
		if _, err := os.Stat(readmePath); err != nil {
			t.Errorf("README.md missing in runtime dir %s after InstallHome: %v", rd.name, err)
		}
	}
}
