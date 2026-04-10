package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// loadProjectConfig reads and parses the project's .nanite/config.yaml
// file, returning a *projectConfig that preserves the tri-state nature
// of the Adapters field (nil = absent, empty pointer = explicit none,
// populated = active list).
//
// Returns an empty *projectConfig (no error) if the file doesn't exist
// — the caller treats this the same as a fresh install.
func loadProjectConfig(path string) (*projectConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &projectConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read project config %s: %w", path, err)
	}
	var cfg projectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse project config %s: %w", path, err)
	}
	return &cfg, nil
}
