package plugin

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PluginRepo describes a plugin entry in the repos.yaml registry file.
type PluginRepo struct {
	Name        string `yaml:"name"`
	Repo        string `yaml:"repo"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"` // "core" or "user"
}

// reposFile is the top-level structure of repos.yaml.
type reposFile struct {
	Plugins []PluginRepo `yaml:"plugins"`
}

// LoadRepos parses a repos.yaml file and returns the plugin entries.
func LoadRepos(path string) ([]PluginRepo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read repos file: %w", err)
	}

	var rf reposFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("parse repos file: %w", err)
	}

	return rf.Plugins, nil
}
