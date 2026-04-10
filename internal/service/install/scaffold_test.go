package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldNaniteDir_Fresh(t *testing.T) {
	project := t.TempDir()
	globalHome := t.TempDir()
	// Simulate ~/.nanite/ with roles/, skills/, commands/ directories.
	for _, sub := range []string{"roles", "skills", "commands"} {
		if err := os.MkdirAll(filepath.Join(globalHome, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	src := ScaffoldSource{
		FrameworkVersion: "2.3.0",
		ProjectName:      "testproj",
	}
	if err := ScaffoldNaniteDir(project, globalHome, src); err != nil {
		t.Fatalf("ScaffoldNaniteDir: %v", err)
	}

	// Verify .nanite/config.yaml exists and contains the version.
	cfg, err := os.ReadFile(filepath.Join(project, ".nanite", "config.yaml"))
	if err != nil {
		t.Fatalf("config.yaml missing: %v", err)
	}
	if !strings.Contains(string(cfg), "nanite_version: 2.3.0") {
		t.Errorf("config.yaml missing version: %q", cfg)
	}

	// Verify all three symlinks exist and point at globalHome.
	for _, sub := range []string{"roles", "skills", "commands"} {
		link := filepath.Join(project, ".nanite", sub)
		info, err := os.Lstat(link)
		if err != nil {
			t.Errorf("%s link missing: %v", sub, err)
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s is not a symlink", sub)
			continue
		}
		target, err := os.Readlink(link)
		if err != nil {
			t.Errorf("readlink %s: %v", sub, err)
			continue
		}
		want := filepath.Join(globalHome, sub)
		if target != want {
			t.Errorf("%s target = %q, want %q", sub, target, want)
		}
	}

	// Verify agents/ directory created.
	info, err := os.Stat(filepath.Join(project, ".nanite", "agents"))
	if err != nil {
		t.Errorf("agents/ missing: %v", err)
	} else if !info.IsDir() {
		t.Errorf("agents/ is not a directory")
	}
}

func TestScaffoldNaniteDir_Idempotent(t *testing.T) {
	project := t.TempDir()
	globalHome := t.TempDir()
	for _, sub := range []string{"roles", "skills", "commands"} {
		if err := os.MkdirAll(filepath.Join(globalHome, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	src := ScaffoldSource{FrameworkVersion: "2.3.0", ProjectName: "p"}

	// Run twice.
	if err := ScaffoldNaniteDir(project, globalHome, src); err != nil {
		t.Fatal(err)
	}
	cfgBefore, _ := os.ReadFile(filepath.Join(project, ".nanite", "config.yaml"))

	// Modify the generated config to verify the second run preserves it.
	modified := "nanite_version: 99.0.0\n# user edit\n"
	if err := os.WriteFile(filepath.Join(project, ".nanite", "config.yaml"), []byte(modified), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ScaffoldNaniteDir(project, globalHome, src); err != nil {
		t.Fatal(err)
	}
	cfgAfter, _ := os.ReadFile(filepath.Join(project, ".nanite", "config.yaml"))
	if string(cfgAfter) != modified {
		t.Errorf("second run overwrote user-edited config:\nbefore modification: %q\nafter second run: %q", cfgBefore, cfgAfter)
	}
}

func TestScaffoldNaniteMD_Fresh(t *testing.T) {
	project := t.TempDir()

	err := ScaffoldNaniteMD(project, ScaffoldSource{
		FrameworkVersion: "2.3.0",
		ProjectName:      "testproj",
	})
	if err != nil {
		t.Fatalf("ScaffoldNaniteMD: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(project, "NANITE.md"))
	if err != nil {
		t.Fatalf("NANITE.md missing: %v", err)
	}
	if !strings.Contains(string(data), "testproj") {
		t.Errorf("NANITE.md missing project name: %q", data)
	}
	if !strings.Contains(string(data), "First-time setup") {
		t.Error("NANITE.md missing First-time setup section")
	}
	if !strings.Contains(string(data), "offer to help the user") {
		t.Error("NANITE.md missing agent setup instruction")
	}
}

func TestScaffoldNaniteMD_PreservesExisting(t *testing.T) {
	project := t.TempDir()
	original := "# Custom NANITE.md\n\nUser content.\n"
	if err := os.WriteFile(filepath.Join(project, "NANITE.md"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ScaffoldNaniteMD(project, ScaffoldSource{ProjectName: "x"}); err != nil {
		t.Fatalf("ScaffoldNaniteMD: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(project, "NANITE.md"))
	if string(data) != original {
		t.Errorf("existing NANITE.md was overwritten: %q", data)
	}
}
