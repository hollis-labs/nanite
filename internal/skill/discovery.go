package skill

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverOptions configures skill file discovery.
type DiscoverOptions struct {
	// WorkingDir is the project root for .nanite/skills/, .claude/skills/.
	WorkingDir string

	// PluginsDir is the root plugins directory for plugin-provided skills.
	PluginsDir string
}

// Discover scans all 4 locations in priority order and returns parsed Definitions.
// First slug wins — lower-priority locations do not override higher-priority ones.
// Missing directories are silently skipped.
func Discover(opts DiscoverOptions) ([]*Definition, error) {
	seen := make(map[string]bool)
	var defs []*Definition

	add := func(def *Definition) {
		if seen[def.Slug] {
			return
		}
		seen[def.Slug] = true
		defs = append(defs, def)
	}

	// Priority 1: .nanite/skills/ (project).
	if opts.WorkingDir != "" {
		for _, def := range discoverDir(filepath.Join(opts.WorkingDir, ".nanite", "skills"), "project") {
			add(def)
		}
	}

	// Priority 2: ~/.nanite/skills/ (user).
	if home, err := os.UserHomeDir(); err == nil {
		for _, def := range discoverDir(filepath.Join(home, ".nanite", "skills"), "user") {
			add(def)
		}
	}

	// Priority 3: .claude/skills/ (Claude Code ecosystem).
	if opts.WorkingDir != "" {
		for _, def := range discoverDir(filepath.Join(opts.WorkingDir, ".claude", "skills"), "claude") {
			add(def)
		}
	}

	// Priority 4: plugins/*/skills/ (plugin-provided).
	if opts.PluginsDir != "" {
		discoverPluginSkills(opts.PluginsDir, func(def *Definition) {
			add(def)
		})
	}

	return defs, nil
}

// discoverDir reads all *.md files from a directory, parses them, and sets Source.
func discoverDir(dir, source string) []*Definition {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // silent skip
	}

	var defs []*Definition
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		def, err := ParseMDFile(path)
		if err != nil {
			log.Printf("skill: skipping %s: %v", path, err)
			continue
		}
		def.Source = source
		defs = append(defs, def)
	}
	return defs
}

// discoverPluginSkills scans plugins/{name}/skills/*.md for plugin-provided skills.
func discoverPluginSkills(pluginsDir string, add func(*Definition)) {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return // silent skip
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillsDir := filepath.Join(pluginsDir, e.Name(), "skills")
		for _, def := range discoverDir(skillsDir, "plugin") {
			add(def)
		}
	}
}
