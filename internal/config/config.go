// Package config loads and merges agentrc.yaml configuration from
// user-level (~/.agentrc/agentrc.yaml) and project-level (./agentrc.yaml).
// Project-level values override user-level values for any field that is set.
package config

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level agentrc configuration.
type Config struct {
	Version        int                      `yaml:"version"`
	Project        ProjectConfig            `yaml:"project"`
	Role           string                   `yaml:"role"`
	BootProfiles   []string                 `yaml:"boot_profiles"`
	WritePaths     []string                 `yaml:"write_paths"`
	ProtectedPaths []string                 `yaml:"protected_paths"`
	Executor       ExecutorConfig           `yaml:"executor"`
	Defaults       DefaultsConfig           `yaml:"defaults"`
	Projects       map[string]ProjectEntry  `yaml:"projects"`
	HooksDir       string                   `yaml:"hooks_dir"`
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
// (~/.agentrc/agentrc.yaml) as a base, then overlays the project-level config
// (./agentrc.yaml relative to the working directory). Project values override
// user values for any field that is set.
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	userPath := filepath.Join(home, ".agentrc", "agentrc.yaml")
	projectPath := "agentrc.yaml" // relative to cwd

	return LoadFrom(userPath, projectPath)
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
	if project.HooksDir != "" {
		out.HooksDir = project.HooksDir
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
