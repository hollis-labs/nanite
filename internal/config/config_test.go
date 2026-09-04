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
tesseract:
  command: /usr/local/bin/tesseract
  server_name: tesseract
  env_allowlist: [PATH, HOME]
`
	projectYAML := `
project:
  name: nanite
  root: ~/Projects-apps/nanite
role: ops-managed
tesseract:
  command: /opt/tesseract
  env: [OPENAI_API_KEY=test-key]
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
	if cfg.Role != "ops-managed" {
		t.Errorf("Role = %q, want %q", cfg.Role, "ops-managed")
	}

	// Tesseract merges per-field: project overrides Command and Env, while the
	// user's ServerName and EnvAllowlist (unset by project) are preserved.
	if cfg.Tesseract.Command != "/opt/tesseract" {
		t.Errorf("Tesseract.Command = %q, want %q (project overrides user)", cfg.Tesseract.Command, "/opt/tesseract")
	}
	if cfg.Tesseract.ServerName != "tesseract" {
		t.Errorf("Tesseract.ServerName = %q, want %q (user-only field preserved)", cfg.Tesseract.ServerName, "tesseract")
	}
	if len(cfg.Tesseract.Env) != 1 || cfg.Tesseract.Env[0] != "OPENAI_API_KEY=test-key" {
		t.Errorf("Tesseract.Env = %v, want project override", cfg.Tesseract.Env)
	}
	if len(cfg.Tesseract.EnvAllowlist) != 2 || cfg.Tesseract.EnvAllowlist[1] != "HOME" {
		t.Errorf("Tesseract.EnvAllowlist = %v, want user value preserved", cfg.Tesseract.EnvAllowlist)
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
	if cfg.Role != "" {
		t.Errorf("Role = %q, want empty for missing config", cfg.Role)
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
	if cfg.Role != "" {
		t.Errorf("Role = %q, want empty for missing config", cfg.Role)
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

// TestLoadFrom_StaleKeysSilentlyIgnored is the 18a-cut-dead-storage-and-config
// acceptance check: nanite.yaml/config.yaml files that still set the fields
// cut from RuntimeConfig (version, boot_profiles, write_paths, protected_paths,
// executor, defaults, projects, hooks_dir) must not error on load —
// gopkg.in/yaml.v3's default Unmarshal silently ignores keys with no
// matching struct field (it only errors on unknown keys via the stricter
// Decoder.KnownFields(true) path, which this package does not use).
func TestLoadFrom_StaleKeysSilentlyIgnored(t *testing.T) {
	dir := t.TempDir()
	userFile := filepath.Join(dir, "user.yaml")
	projectFile := filepath.Join(dir, "project.yaml")

	staleYAML := `
version: 1
role: still-works
boot_profiles:
  - worker
write_paths:
  - internal/
protected_paths:
  - .agentrc/state/
executor:
  mode: direct
  unsafe_mode: true
  timeout_seconds: 900
  max_agent_depth: 5
defaults:
  executor:
    mode: direct
  boot_profiles:
    - worker
projects:
  alpha:
    root: ~/alpha
    description: Alpha project
hooks_dir: ~/.agentrc/hooks/shared
`
	if err := os.WriteFile(userFile, []byte(staleYAML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectFile, []byte(staleYAML), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFrom(userFile, projectFile)
	if err != nil {
		t.Fatalf("LoadFrom with stale (removed) YAML keys should not error, got: %v", err)
	}
	if cfg.Role != "still-works" {
		t.Errorf("Role = %q, want %q (a real field alongside stale keys must still load)", cfg.Role, "still-works")
	}
}

func TestProjectRoot_ExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home dir")
	}

	cfg := &RuntimeConfig{
		Project: ProjectConfig{Root: "~/Projects-apps/nanite"},
	}
	got := cfg.ProjectRoot()
	want := filepath.Join(home, "Projects-apps/nanite")
	if got != want {
		t.Errorf("ProjectRoot() = %q, want %q", got, want)
	}
}

func TestProjectRoot_EmptyRoot(t *testing.T) {
	cfg := &RuntimeConfig{}
	if got := cfg.ProjectRoot(); got != "" {
		t.Errorf("ProjectRoot() = %q, want empty string", got)
	}
}

func TestProjectRoot_AbsolutePath(t *testing.T) {
	cfg := &RuntimeConfig{
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
		cfg := &RuntimeConfig{}
		if got := cfg.ResolvedDevToolsAllowedPaths(); got != nil {
			t.Errorf("ResolvedDevToolsAllowedPaths() = %v, want nil", got)
		}
	})

	t.Run("tilde-expanded entries", func(t *testing.T) {
		cfg := &RuntimeConfig{
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
		cfg := &RuntimeConfig{
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
// matching the existing override semantics.
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
