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

// Config is the top-level nanite configuration.
type Config struct {
	Version        int                      `yaml:"version"`
	Project        ProjectConfig            `yaml:"project"`
	Role           string                   `yaml:"role"`
	BootProfiles   []string                 `yaml:"boot_profiles"`
	WritePaths     []string                 `yaml:"write_paths"`
	ProtectedPaths []string                 `yaml:"protected_paths"`
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
	DevToolsAllowedPaths []string                 `yaml:"dev_tools_allowed_paths"`
	// BootProfileCatalogPath is the on-disk root that
	// internal/bootprofile.LoadCatalog reads when surfacing boot-profile-
	// backed entries in the provider/model dropdown (CW-20260514-0047)
	// and, in a follow-up ticket, when the chat runtime boots a session
	// against a profile-backed entry (CW-20260514-0048). The path may
	// use a leading ~/ for the user's home directory; expansion happens
	// in ResolvedBootProfileCatalogPath.
	//
	// When unset (empty string) the boot-profile registry stays inert —
	// the existing API/CLI provider behavior is unchanged and the
	// dropdown only shows DB-seeded rows. This satisfies the "no
	// catalog → no behavior change" acceptance criterion.
	BootProfileCatalogPath string                  `yaml:"boot_profile_catalog_path"`
	Executor       ExecutorConfig           `yaml:"executor"`
	Defaults       DefaultsConfig           `yaml:"defaults"`
	Projects       map[string]ProjectEntry  `yaml:"projects"`
	HooksDir       string                   `yaml:"hooks_dir"`
	// Vanta is the optional Vanta MCP server configuration (CW-20260501-0005
	// sub-ticket 2). When URL is non-empty, the chat harness registers a
	// `vanta` MCP server at startup so the chat agent can reach
	// memory_recall / memory_write / knowledge_* / context_* tools. Trust
	// tier defaults to plugin_http per docs/mcp-trust-model.md (Vanta is
	// the user's own infrastructure).
	Vanta          VantaConfig              `yaml:"vanta"`
}

// VantaConfig holds Vanta MCP server connection details. Loaded from the
// user-level XDG config file ($XDG_CONFIG_HOME/nanite/config.yaml, default
// ~/.config/nanite/config.yaml) or project-level ./nanite.yaml. Token may
// also be supplied via the NANITE_VANTA_TOKEN environment variable, which
// overrides any value in the config file (so the secret never has to live
// in YAML).
//
// CW-20260501-0005 sub-ticket 2.
type VantaConfig struct {
	// URL is the Vanta MCP HTTP endpoint, e.g. "http://localhost:6810/mcp".
	// Leave empty to disable Vanta integration.
	URL string `yaml:"url"`
	// Token is an optional Bearer token sent in the Authorization header on
	// every JSON-RPC request. Set via NANITE_VANTA_TOKEN env var to keep the
	// secret out of YAML.
	Token string `yaml:"token"`
	// TrustTier overrides the default trust tier for Vanta. Allowed values
	// are the four mcp.TrustTier constants: builtin, plugin_stdio,
	// plugin_http (default), third_party_http. Most users should leave this
	// unset.
	TrustTier string `yaml:"trust_tier"`
	// ServerName overrides the registered MCP server name. Defaults to
	// "vanta" — only set this if "vanta" collides with another registered
	// server in your environment (rare).
	ServerName string `yaml:"server_name"`
}

// ProjectConfig identifies the current project.
type ProjectConfig struct {
	Name string `yaml:"name"`
	Root string `yaml:"root"`
}

// ExecutorConfig controls agent execution behaviour.
type ExecutorConfig struct {
	Mode           string `yaml:"mode"`
	UnsafeMode     bool   `yaml:"unsafe_mode"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
	MaxAgentDepth  int    `yaml:"max_agent_depth"`
}

// DefaultsConfig holds shared default settings from the user-level config.
type DefaultsConfig struct {
	Executor     ExecutorConfig `yaml:"executor"`
	BootProfiles []string       `yaml:"boot_profiles"`
}

// ProjectEntry is one entry in the projects registry.
type ProjectEntry struct {
	Root        string `yaml:"root"`
	Description string `yaml:"description"`
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
// config alone (or a zero Config if neither file exists).
func Load() (*Config, error) {
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
func LoadFrom(userPath, projectPath string) (*Config, error) {
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
func (c *Config) ProjectRoot() string {
	return expandHome(c.Project.Root)
}

// ResolvedBootProfileCatalogPath returns the configured BootProfileCatalogPath
// with a leading ~/ tilde-expanded to the user's home directory. Returns an
// empty string when the field is unset, which the boot-profile registry
// treats as "no catalog configured" (inert).
func (c *Config) ResolvedBootProfileCatalogPath() string {
	return expandHome(c.BootProfileCatalogPath)
}

// ResolvedDevToolsAllowedPaths returns the configured DevToolsAllowedPaths
// list with leading ~/ entries tilde-expanded to the user's home directory.
// Empty entries are dropped. Returns nil only when the field was never
// configured, so callers can distinguish "unset → fall back to defaults"
// from "explicitly empty → no allowed paths". An empty-but-configured
// list (`dev_tools_allowed_paths: []` in YAML) returns a non-nil empty
// slice.
func (c *Config) ResolvedDevToolsAllowedPaths() []string {
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

// readConfig reads a single YAML config file. Returns a zero Config if the
// file does not exist.
func readConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// merge overlays project config on top of user config.
// For scalar fields, project wins if non-zero. For slices/maps, project
// replaces user if the project slice/map is non-nil.
func merge(user, project *Config) *Config {
	out := *user // shallow copy of user as base

	if project.Version != 0 {
		out.Version = project.Version
	}
	if project.Project.Name != "" {
		out.Project.Name = project.Project.Name
	}
	if project.Project.Root != "" {
		out.Project.Root = project.Project.Root
	}
	if project.Role != "" {
		out.Role = project.Role
	}
	if project.BootProfiles != nil {
		out.BootProfiles = project.BootProfiles
	}
	if project.WritePaths != nil {
		out.WritePaths = project.WritePaths
	}
	if project.ProtectedPaths != nil {
		out.ProtectedPaths = project.ProtectedPaths
	}
	if project.DevToolsAllowedPaths != nil {
		out.DevToolsAllowedPaths = project.DevToolsAllowedPaths
	}
	if project.BootProfileCatalogPath != "" {
		out.BootProfileCatalogPath = project.BootProfileCatalogPath
	}
	if project.HooksDir != "" {
		out.HooksDir = project.HooksDir
	}

	// Vanta: per-field merge so a project file can override URL alone without
	// resetting Token/TrustTier/ServerName the user set globally.
	if project.Vanta.URL != "" {
		out.Vanta.URL = project.Vanta.URL
	}
	if project.Vanta.Token != "" {
		out.Vanta.Token = project.Vanta.Token
	}
	if project.Vanta.TrustTier != "" {
		out.Vanta.TrustTier = project.Vanta.TrustTier
	}
	if project.Vanta.ServerName != "" {
		out.Vanta.ServerName = project.Vanta.ServerName
	}

	// Executor: merge field-by-field so partial overrides work.
	if project.Executor.Mode != "" {
		out.Executor.Mode = project.Executor.Mode
	}
	if project.Executor.UnsafeMode {
		out.Executor.UnsafeMode = project.Executor.UnsafeMode
	}
	if project.Executor.TimeoutSeconds != 0 {
		out.Executor.TimeoutSeconds = project.Executor.TimeoutSeconds
	}
	if project.Executor.MaxAgentDepth != 0 {
		out.Executor.MaxAgentDepth = project.Executor.MaxAgentDepth
	}

	// Defaults: project overrides if set.
	if project.Defaults.BootProfiles != nil {
		out.Defaults.BootProfiles = project.Defaults.BootProfiles
	}
	if project.Defaults.Executor.Mode != "" {
		out.Defaults.Executor.Mode = project.Defaults.Executor.Mode
	}
	if project.Defaults.Executor.TimeoutSeconds != 0 {
		out.Defaults.Executor.TimeoutSeconds = project.Defaults.Executor.TimeoutSeconds
	}
	if project.Defaults.Executor.MaxAgentDepth != 0 {
		out.Defaults.Executor.MaxAgentDepth = project.Defaults.Executor.MaxAgentDepth
	}

	// Projects map: merge entries (project entries override user entries).
	if project.Projects != nil {
		if out.Projects == nil {
			out.Projects = make(map[string]ProjectEntry)
		}
		// Copy user entries first (already in out via shallow copy, but maps
		// are reference types so we need a real copy).
		merged := make(map[string]ProjectEntry, len(out.Projects)+len(project.Projects))
		for k, v := range user.Projects {
			merged[k] = v
		}
		for k, v := range project.Projects {
			merged[k] = v
		}
		out.Projects = merged
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
