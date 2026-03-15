package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFrom_MergeProjectOverridesUser(t *testing.T) {
	dir := t.TempDir()

	userFile := filepath.Join(dir, "user.yaml")
	projectFile := filepath.Join(dir, "project.yaml")

	userYAML := `
version: 1
defaults:
  executor:
    mode: direct
    timeout_seconds: 600
    max_agent_depth: 3
  boot_profiles:
    - worker
projects:
  alpha:
    root: ~/alpha
    description: Alpha project
hooks_dir: ~/.agentrc/hooks/shared
`
	projectYAML := `
version: 1
project:
  name: conduit
  root: ~/Projects-apps/fragments-engine/conduit
role: volon-managed
boot_profiles:
  - worker
write_paths:
  - internal/
  - cmd/
protected_paths:
  - .agentrc/state/
executor:
  mode: direct
  unsafe_mode: true
  timeout_seconds: 900
  max_agent_depth: 5
`
	if err := os.WriteFile(userFile, []byte(userYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectFile, []byte(projectYAML), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(userFile, projectFile)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	// Project scalars override user.
	if cfg.Project.Name != "conduit" {
		t.Errorf("Project.Name = %q, want %q", cfg.Project.Name, "conduit")
	}
	if cfg.Role != "volon-managed" {
		t.Errorf("Role = %q, want %q", cfg.Role, "volon-managed")
	}

	// Executor merged from project.
	if cfg.Executor.TimeoutSeconds != 900 {
		t.Errorf("Executor.TimeoutSeconds = %d, want 900", cfg.Executor.TimeoutSeconds)
	}
	if cfg.Executor.MaxAgentDepth != 5 {
		t.Errorf("Executor.MaxAgentDepth = %d, want 5", cfg.Executor.MaxAgentDepth)
	}
	if !cfg.Executor.UnsafeMode {
		t.Error("Executor.UnsafeMode = false, want true")
	}

	// Slices from project replace user.
	if len(cfg.WritePaths) != 2 {
		t.Errorf("WritePaths len = %d, want 2", len(cfg.WritePaths))
	}

	// User-only fields preserved.
	if cfg.HooksDir != "~/.agentrc/hooks/shared" {
		t.Errorf("HooksDir = %q, want %q", cfg.HooksDir, "~/.agentrc/hooks/shared")
	}

	// User defaults preserved (project didn't set defaults).
	if cfg.Defaults.Executor.TimeoutSeconds != 600 {
		t.Errorf("Defaults.Executor.TimeoutSeconds = %d, want 600", cfg.Defaults.Executor.TimeoutSeconds)
	}

	// User projects preserved when project file has no projects map.
	if _, ok := cfg.Projects["alpha"]; !ok {
		t.Error("expected user project 'alpha' to be preserved")
	}
}

func TestLoadFrom_MissingFiles(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadFrom(
		filepath.Join(dir, "nonexistent-user.yaml"),
		filepath.Join(dir, "nonexistent-project.yaml"),
	)
	if err != nil {
		t.Fatalf("LoadFrom with missing files: %v", err)
	}
	if cfg.Version != 0 {
		t.Errorf("Version = %d, want 0 for empty config", cfg.Version)
	}
}

func TestLoadFrom_ProjectMapMerge(t *testing.T) {
	dir := t.TempDir()

	userFile := filepath.Join(dir, "user.yaml")
	projectFile := filepath.Join(dir, "project.yaml")

	userYAML := `
projects:
  alpha:
    root: ~/alpha
    description: Alpha
  beta:
    root: ~/beta
    description: Beta
`
	projectYAML := `
projects:
  beta:
    root: ~/beta-override
    description: Beta Override
  gamma:
    root: ~/gamma
    description: Gamma
`
	os.WriteFile(userFile, []byte(userYAML), 0644)
	os.WriteFile(projectFile, []byte(projectYAML), 0644)

	cfg, err := LoadFrom(userFile, projectFile)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	if cfg.Projects["alpha"].Root != "~/alpha" {
		t.Error("user-only project 'alpha' should be preserved")
	}
	if cfg.Projects["beta"].Root != "~/beta-override" {
		t.Errorf("beta.Root = %q, want ~/beta-override (project overrides user)", cfg.Projects["beta"].Root)
	}
	if cfg.Projects["gamma"].Root != "~/gamma" {
		t.Error("project-only entry 'gamma' should be present")
	}
}

func TestProjectRoot_ExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	cfg := &Config{
		Project: ProjectConfig{Root: "~/Projects-apps/fragments-engine/conduit"},
	}
	got := cfg.ProjectRoot()
	want := filepath.Join(home, "Projects-apps/fragments-engine/conduit")
	if got != want {
		t.Errorf("ProjectRoot() = %q, want %q", got, want)
	}
}

func TestProjectRoot_EmptyRoot(t *testing.T) {
	cfg := &Config{}
	if got := cfg.ProjectRoot(); got != "" {
		t.Errorf("ProjectRoot() = %q, want empty string", got)
	}
}

func TestProjectRoot_AbsolutePath(t *testing.T) {
	cfg := &Config{
		Project: ProjectConfig{Root: "/opt/myproject"},
	}
	if got := cfg.ProjectRoot(); got != "/opt/myproject" {
		t.Errorf("ProjectRoot() = %q, want /opt/myproject", got)
	}
}
