package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader loads and manages workflow definitions from YAML files.
type Loader struct {
	dir       string
	workflows map[string]*WorkflowDef
}

// NewLoader creates a new Loader that reads workflow files from the given directory.
func NewLoader(dir string) *Loader {
	return &Loader{
		dir:       dir,
		workflows: make(map[string]*WorkflowDef),
	}
}

// LoadAll reads all YAML workflow files from the configured directory.
// Files must have .yaml or .yml extension.
func (l *Loader) LoadAll() error {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no workflow directory yet — that's fine
		}
		return fmt.Errorf("read workflow dir %s: %w", l.dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		path := filepath.Join(l.dir, entry.Name())
		def, err := l.loadFile(path)
		if err != nil {
			return fmt.Errorf("load workflow %s: %w", entry.Name(), err)
		}

		// Use filename without extension as the key if name is not set.
		name := def.Name
		if name == "" {
			name = strings.TrimSuffix(entry.Name(), ext)
			def.Name = name
		}

		l.workflows[name] = def
	}

	return nil
}

// loadFile parses a single YAML workflow file.
func (l *Loader) loadFile(path string) (*WorkflowDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var def WorkflowDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	return &def, nil
}

// List returns all loaded workflow definitions.
func (l *Loader) List() []*WorkflowDef {
	out := make([]*WorkflowDef, 0, len(l.workflows))
	for _, def := range l.workflows {
		out = append(out, def)
	}
	return out
}

// Get returns a workflow definition by name.
func (l *Loader) Get(name string) (*WorkflowDef, bool) {
	def, ok := l.workflows[name]
	return def, ok
}
