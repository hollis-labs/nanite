// Package config loads and merges nanite configuration from
// user-level (XDG: $XDG_CONFIG_HOME/nanite/config.yaml, default
// ~/.config/nanite/config.yaml) and project-level (./nanite.yaml).
// Project-level values override user-level values for any field that is set.
//
// The user-level path follows the XDG Base Directory Specification:
// https://specifications.freedesktop.org/basedir-spec/0.8/
//
// CW-20260430-0010 (Option C): the legacy ~/.nanite/nanite.yaml location is
// no longer read. Pre-release migration is manual — copy your existing
// config to ~/.config/nanite/config.yaml.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// RuntimeConfig is the user/project runtime configuration loaded from the XDG
// user config and project-root nanite.yaml.
type RuntimeConfig struct {
	Project ProjectConfig `yaml:"project"`
	// Role is read exactly once, at boot, for a log line only — it does not
	// currently branch on anything or feed into agent resolution. Kept as-is
	// (18a-cut-dead-storage-and-config's field-by-field verification flagged
	// it as a real, if thin, read site — not zero-callers like the fields
	// that were cut alongside it).
	Role string `yaml:"role"`
	// DevToolsAllowedPaths is the user-configurable allow-list of filesystem
	// roots agents may access. It governs two surfaces:
	//
	//   - the in-process dev_* MCP tools (dev_read, dev_glob, dev_grep,
	//     dev_write, dev_edit, dev_bash) — the path-safety escape check;
	//   - CLI-launch boot dirs (CW-20260518-0075) — threaded into the
	//     planted provider config as codex's [sandbox_workspace_write]
	//     writable_roots and claude's permissions.additionalDirectories,
	//     so a codex/claude CLI agent can write beyond its throwaway boot
	//     dir cwd.
	//
	// Entries support a leading ~/ for the user's home directory and are
	// tilde-expanded at load time.
	//
	// When unset (nil) the runtime falls back to the project root if one is
	// configured (see cmd/nanite/main.go resolveDevToolsAllowedPaths). When
	// set, the user's list REPLACES the default — set explicitly to widen or
	// narrow the scope. The path-safety escape check (internal/pathsafe)
	// still runs on every dev_* call regardless of how the allow-list was
	// sourced; this knob only widens which roots qualify, it never disables
	// traversal protection.
	DevToolsAllowedPaths []string `yaml:"dev_tools_allowed_paths"`
	// WorkflowDefinitionsPath is the on-disk directory internal/agentworkflow's
	// registry loader reads at startup — one WorkflowDefinition per *.yaml
	// file (CW-20260813-0014), keyed by the definition's Name field. The
	// path may use a leading ~/ for the user's home directory; expansion
	// happens in ResolvedWorkflowDefinitionsPath.
	//
	// When unset (empty string) the registry stays empty — workflow_run
	// self-tool calls fail with "unknown workflow" but nothing else changes
	// ("no catalog → no behavior change").
	WorkflowDefinitionsPath string `yaml:"workflow_definitions_path"`
	// Tesseract is the optional Tesseract v0.9 stdio MCP process. A non-empty
	// command registers it under the tesseract server name.
	Tesseract TesseractConfig `yaml:"tesseract"`
}

// TesseractConfig holds Tesseract MCP server connection details. Loaded from the
// user-level XDG config file ($XDG_CONFIG_HOME/nanite/config.yaml, default
// ~/.config/nanite/config.yaml) or project-level ./nanite.yaml. Token may
// also be supplied via the NANITE_TESSERACT_TOKEN environment variable, which
// overrides any value in the config file (so the secret never has to live
// in YAML).
type TesseractConfig struct {
	// Command is the released Tesseract executable. A non-empty value enables
	// the external stdio MCP mode; Nanite invokes it as `tesseract mcp`.
	Command string `yaml:"command"`
	// Token is the optional capability token passed to `tesseract mcp`.
	// Set NANITE_TESSERACT_TOKEN to keep the secret out of YAML.
	Token string `yaml:"token"`
	// TrustTier overrides the default trust tier for Tesseract. Allowed values
	// are the four mcp.TrustTier constants: builtin, plugin_stdio (default),
	// plugin_http, third_party_http. Most users should leave this
	// unset.
	TrustTier string `yaml:"trust_tier"`
	// ServerName overrides the registered MCP server name. Defaults to
	// "tesseract" — only set this if it collides with another registered
	// server in your environment (rare).
	ServerName string `yaml:"server_name"`
	// Env contains explicit KEY=VALUE entries for the child process.
	Env []string `yaml:"env"`
	// EnvAllowlist names host variables the child may inherit. When omitted,
	// Nanite passes only PATH/HOME and Tesseract's XDG/path overrides.
	EnvAllowlist []string `yaml:"env_allowlist"`
}

// ProjectConfig identifies the current project.
type ProjectConfig struct {
	Name string `yaml:"name"`
	Root string `yaml:"root"`
}

// Load reads and merges configuration. It first reads the user-level config
// from the XDG-compliant location as a base, then overlays the project-level
// config (./nanite.yaml relative to the working directory). Project values
// override user values for any field that is set.
//
// User-config path resolution (XDG Base Directory Spec):
//   - if $XDG_CONFIG_HOME is set: $XDG_CONFIG_HOME/nanite/config.yaml
//   - otherwise:                  ~/.config/nanite/config.yaml
//
// A missing user-config file is not an error — Load returns the project
// config alone (or a zero RuntimeConfig if neither file exists).
func Load() (*RuntimeConfig, error) {
	userPath, err := UserConfigPath()
	if err != nil {
		return nil, err
	}

	projectPath := "nanite.yaml" // relative to cwd

	return LoadFrom(userPath, projectPath)
}

// UserConfigPath returns the resolved absolute path to the user-level config
// file per the XDG Base Directory Specification:
//
//   - if $XDG_CONFIG_HOME is set and non-empty: $XDG_CONFIG_HOME/nanite/config.yaml
//   - otherwise:                                ~/.config/nanite/config.yaml
//
// The file is not required to exist — callers (including Load) treat a
// missing file as "no user config" without error. This function only
// returns an error if the user's home directory cannot be determined and
// $XDG_CONFIG_HOME is unset.
func UserConfigPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "nanite", "config.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "nanite", "config.yaml"), nil
}

// LoadFrom reads and merges configuration from explicit file paths.
// userPath is the base config; projectPath overrides it.
// Either file may be missing — a missing file is silently skipped.
func LoadFrom(userPath, projectPath string) (*RuntimeConfig, error) {
	base, err := readConfig(userPath)
	if err != nil {
		return nil, err
	}

	proj, err := readConfig(projectPath)
	if err != nil {
		return nil, err
	}

	merged := merge(base, proj)
	return merged, nil
}

// ProjectRoot resolves the project.root field, expanding ~ to the user's home
// directory. Returns an empty string if project.root is unset.
func (c *RuntimeConfig) ProjectRoot() string {
	return expandHome(c.Project.Root)
}

// ResolvedWorkflowDefinitionsPath returns the configured
// WorkflowDefinitionsPath with a leading ~/ tilde-expanded to the user's
// home directory. Returns an empty string when the field is unset, which
// the workflow-definitions registry treats as "no directory configured"
// (empty registry).
func (c *RuntimeConfig) ResolvedWorkflowDefinitionsPath() string {
	return expandHome(c.WorkflowDefinitionsPath)
}

// ResolvedDevToolsAllowedPaths returns the configured DevToolsAllowedPaths
// list with leading ~/ entries tilde-expanded to the user's home directory.
// Empty entries are dropped. Returns nil only when the field was never
// configured, so callers can distinguish "unset → fall back to defaults"
// from "explicitly empty → no allowed paths". An empty-but-configured
// list (`dev_tools_allowed_paths: []` in YAML) returns a non-nil empty
// slice.
func (c *RuntimeConfig) ResolvedDevToolsAllowedPaths() []string {
	if c.DevToolsAllowedPaths == nil {
		return nil
	}
	out := make([]string, 0, len(c.DevToolsAllowedPaths))
	for _, p := range c.DevToolsAllowedPaths {
		expanded := expandHome(p)
		if expanded == "" {
			continue
		}
		out = append(out, expanded)
	}
	return out
}

// readConfig reads a single YAML config file. Returns a zero RuntimeConfig if the
// file does not exist.
func readConfig(path string) (*RuntimeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RuntimeConfig{}, nil
		}
		return nil, err
	}
	var cfg RuntimeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// merge overlays project config on top of user config.
// For scalar fields, project wins if non-zero. For slices/maps, project
// replaces user if the project slice/map is non-nil.
func merge(user, project *RuntimeConfig) *RuntimeConfig {
	out := *user // shallow copy of user as base

	if project.Project.Name != "" {
		out.Project.Name = project.Project.Name
	}
	if project.Project.Root != "" {
		out.Project.Root = project.Project.Root
	}
	if project.Role != "" {
		out.Role = project.Role
	}
	if project.DevToolsAllowedPaths != nil {
		out.DevToolsAllowedPaths = project.DevToolsAllowedPaths
	}
	if project.WorkflowDefinitionsPath != "" {
		out.WorkflowDefinitionsPath = project.WorkflowDefinitionsPath
	}

	// Tesseract: per-field merge so a project file can override Command alone
	// without resetting Token/TrustTier/ServerName the user set globally.
	if project.Tesseract.Command != "" {
		out.Tesseract.Command = project.Tesseract.Command
	}
	if project.Tesseract.Token != "" {
		out.Tesseract.Token = project.Tesseract.Token
	}
	if project.Tesseract.TrustTier != "" {
		out.Tesseract.TrustTier = project.Tesseract.TrustTier
	}
	if project.Tesseract.ServerName != "" {
		out.Tesseract.ServerName = project.Tesseract.ServerName
	}
	if project.Tesseract.Env != nil {
		out.Tesseract.Env = append([]string(nil), project.Tesseract.Env...)
	}
	if project.Tesseract.EnvAllowlist != nil {
		out.Tesseract.EnvAllowlist = append([]string(nil), project.Tesseract.EnvAllowlist...)
	}

	return &out
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
