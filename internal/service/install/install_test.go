package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestService_InstallHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".nanite")

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
