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
  name: nanite
  root: ~/Projects-apps/nanite
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
	if cfg.Project.Name != "nanite" {
		t.Errorf("Project.Name = %q, want %q", cfg.Project.Name, "nanite")
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

// TestUserConfigPath_XDGEnvSet verifies that an explicitly set XDG_CONFIG_HOME
// is honored as the parent for the nanite/config.yaml file (CW-20260430-0010,
// Option C — XDG Base Directory Spec compliance).
func TestUserConfigPath_XDGEnvSet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	got, err := UserConfigPath()
	if err != nil {
		t.Fatalf("UserConfigPath: %v", err)
	}
	want := filepath.Join(dir, "nanite", "config.yaml")
	if got != want {
		t.Errorf("UserConfigPath() = %q, want %q", got, want)
	}
}

// TestUserConfigPath_DefaultFallback verifies the fallback to
// ~/.config/nanite/config.yaml when XDG_CONFIG_HOME is unset / empty.
func TestUserConfigPath_DefaultFallback(t *testing.T) {
	// Empty XDG_CONFIG_HOME → fall back to ~/.config (XDG spec § "If
	// $XDG_CONFIG_HOME is either not set or empty, a default equal to
	// $HOME/.config should be used.").
	t.Setenv("XDG_CONFIG_HOME", "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	got, err := UserConfigPath()
	if err != nil {
		t.Fatalf("UserConfigPath: %v", err)
	}
	want := filepath.Join(home, ".config", "nanite", "config.yaml")
	if got != want {
		t.Errorf("UserConfigPath() = %q, want %q", got, want)
	}
}

// TestUserConfigPath_OldPathNotUsed asserts the legacy ~/.nanite/nanite.yaml
// path is NOT what UserConfigPath() returns — Option C is a clean break with
// no fallback to the old location.
func TestUserConfigPath_OldPathNotUsed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	got, err := UserConfigPath()
	if err != nil {
		t.Fatalf("UserConfigPath: %v", err)
	}
	legacy := filepath.Join(home, ".nanite", "nanite.yaml")
	if got == legacy {
		t.Errorf("UserConfigPath() returned legacy path %q; clean break to XDG required", got)
	}
}

// TestLoad_MissingUserConfigIsNonError verifies the long-standing behavior
// that a missing user-config file is silently treated as "no user config"
// (the readConfig helper short-circuits on os.IsNotExist).
func TestLoad_MissingUserConfigIsNonError(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	tmpCwd := t.TempDir()
	if err := os.Chdir(tmpCwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with missing user/project config: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load returned nil config")
	}
	if cfg.Version != 0 {
		t.Errorf("Version = %d, want 0 for empty config", cfg.Version)
	}
}

// TestLoad_XDGUserConfigRead verifies Load() actually reads the user-config
// file from the XDG location end-to-end.
func TestLoad_XDGUserConfigRead(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	cfgDir := filepath.Join(xdg, "nanite")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	userYAML := `
version: 1
role: xdg-test-role
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(userYAML), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	tmpCwd := t.TempDir()
	if err := os.Chdir(tmpCwd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Role != "xdg-test-role" {
		t.Errorf("Role = %q, want %q (user config not read from XDG location)", cfg.Role, "xdg-test-role")
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
		Project: ProjectConfig{Root: "~/Projects-apps/nanite"},
	}
	got := cfg.ProjectRoot()
	want := filepath.Join(home, "Projects-apps/nanite")
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

// TestResolvedDevToolsAllowedPaths exercises the user-configurable allow-list
// for the dev_* MCP tools added in CW-20260430-0005. The accessor must
// expand leading ~/ entries and return nil when the field is unset so the
// runtime can fall back to its hardcoded defaults.
func TestResolvedDevToolsAllowedPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	t.Run("nil when unset", func(t *testing.T) {
		cfg := &Config{}
		if got := cfg.ResolvedDevToolsAllowedPaths(); got != nil {
			t.Errorf("ResolvedDevToolsAllowedPaths() = %v, want nil", got)
		}
	})

	t.Run("tilde-expanded entries", func(t *testing.T) {
		cfg := &Config{
			DevToolsAllowedPaths: []string{
				"~/Projects-apps",
				"~/.nanite",
				"/opt/shared",
			},
		}
		got := cfg.ResolvedDevToolsAllowedPaths()
		want := []string{
			filepath.Join(home, "Projects-apps"),
			filepath.Join(home, ".nanite"),
			"/opt/shared",
		}
		if len(got) != len(want) {
			t.Fatalf("len = %d, want %d (got %v)", len(got), len(want), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("empty entries dropped", func(t *testing.T) {
		cfg := &Config{
			DevToolsAllowedPaths: []string{"", "~/.nanite", ""},
		}
		got := cfg.ResolvedDevToolsAllowedPaths()
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1 (got %v)", len(got), got)
		}
		if got[0] != filepath.Join(home, ".nanite") {
			t.Errorf("[0] = %q, want %q", got[0], filepath.Join(home, ".nanite"))
		}
	})
}

// TestLoadFrom_DevToolsAllowedPaths_Merge verifies that the project-level
// dev_tools_allowed_paths list replaces the user-level list (not merge),
// matching the existing override semantics for write_paths/protected_paths.
func TestLoadFrom_DevToolsAllowedPaths_Merge(t *testing.T) {
	dir := t.TempDir()
	userFile := filepath.Join(dir, "user.yaml")
	projectFile := filepath.Join(dir, "project.yaml")

	userYAML := `
dev_tools_allowed_paths:
  - ~/Projects-apps
  - ~/.nanite
`
	projectYAML := `
dev_tools_allowed_paths:
  - ~/Projects-apps/scoped-project
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
	if len(cfg.DevToolsAllowedPaths) != 1 {
		t.Fatalf("expected project list to replace user list, got %v", cfg.DevToolsAllowedPaths)
	}
	if cfg.DevToolsAllowedPaths[0] != "~/Projects-apps/scoped-project" {
		t.Errorf("DevToolsAllowedPaths[0] = %q, want %q",
			cfg.DevToolsAllowedPaths[0], "~/Projects-apps/scoped-project")
	}
}
