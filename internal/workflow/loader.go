package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Loader loads and manages workflow definitions from YAML files and the database.
type Loader struct {
	dir       string
	db        *sql.DB
	workflows map[string]*WorkflowDef
}

// NewLoader creates a new Loader that reads workflow files from the given directory.
func NewLoader(dir string) *Loader {
	return &Loader{
		dir:       dir,
		workflows: make(map[string]*WorkflowDef),
	}
}

// SetDB sets the database connection for loading workflows from the workflows table.
func (l *Loader) SetDB(db *sql.DB) {
	l.db = db
}

// LoadAll reads all YAML workflow files from the configured directory
// and also loads enabled workflows from the database.
func (l *Loader) LoadAll() error {
	// Load from YAML files.
	if err := l.loadFromFiles(); err != nil {
		return err
	}

	// Load from database (if connected).
	if l.db != nil {
		if err := l.loadFromDB(); err != nil {
			return err
		}
	}

	return nil
}

// loadFromFiles reads workflow YAML files from the directory.
func (l *Loader) loadFromFiles() error {
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

// loadFromDB reads enabled workflow definitions from the workflows table.
func (l *Loader) loadFromDB() error {
	rows, err := l.db.Query(
		`SELECT name, definition FROM workflows WHERE is_enabled = TRUE`,
	)
	if err != nil {
		return fmt.Errorf("query workflows from db: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			return fmt.Errorf("scan workflow row: %w", err)
		}

		var def WorkflowDef
		// Try JSON first, then YAML.
		if err := json.Unmarshal([]byte(definition), &def); err != nil {
			if err := yaml.Unmarshal([]byte(definition), &def); err != nil {
				return fmt.Errorf("parse workflow %q definition: %w", name, err)
			}
		}

		if def.Name == "" {
			def.Name = name
		}

		// DB workflows do not overwrite file-based workflows.
		if _, exists := l.workflows[def.Name]; !exists {
			l.workflows[def.Name] = &def
		}
	}

	return rows.Err()
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
