package agent

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverOptions configures agent file discovery.
type DiscoverOptions struct {
	// CLIAgentPath is a single agent file specified via --agent flag (priority 1).
	CLIAgentPath string

	// WorkingDir is the project root for .nanite/agents/, .agentrc/agents/, .claude/agents/.
	WorkingDir string

	// PluginsDir is the root plugins directory for plugin-provided agents.
	PluginsDir string
}

// Discover scans all 6 locations in priority order and returns parsed Definitions.
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

	// Priority 1: CLI --agent flag (single file).
	if opts.CLIAgentPath != "" {
		def, err := ParseMDFile(opts.CLIAgentPath)
		if err != nil {
			return nil, err // CLI flag error is fatal, not silently skipped.
		}
		def.Source = "cli"
		add(def)
	}

	// Priority 2: .nanite/agents/ (project).
	if opts.WorkingDir != "" {
		for _, def := range discoverDir(filepath.Join(opts.WorkingDir, ".nanite", "agents"), "project") {
			add(def)
		}
	}

	// Priority 3: ~/.nanite/agents/ (user).
	if home, err := os.UserHomeDir(); err == nil {
		for _, def := range discoverDir(filepath.Join(home, ".nanite", "agents"), "user") {
			add(def)
		}
	}

	// Priority 4: plugins/*/agents/ (plugin-provided).
	if opts.PluginsDir != "" {
		discoverPluginAgents(opts.PluginsDir, func(def *Definition) {
			add(def)
		})
	}

	// Priority 5: .agentrc/agents/ (agentrc ecosystem).
	if opts.WorkingDir != "" {
		for _, def := range discoverDir(filepath.Join(opts.WorkingDir, ".agentrc", "agents"), "agentrc") {
			add(def)
		}
	}

	// Priority 6: .claude/agents/ (Claude Code ecosystem).
	if opts.WorkingDir != "" {
		for _, def := range discoverDir(filepath.Join(opts.WorkingDir, ".claude", "agents"), "claude") {
			add(def)
		}
	}

	return defs, nil
}

// discoverDir reads all *.md files from a directory, parses them, and sets Source.
// Returns nil on missing or unreadable directories.
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
			log.Printf("agent: skipping %s: %v", path, err)
			continue
		}
		def.Source = source
		defs = append(defs, def)
	}
	return defs
}

// discoverPluginAgents scans plugins/{name}/agents/*.md for plugin-provided agents.
func discoverPluginAgents(pluginsDir string, add func(*Definition)) {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return // silent skip
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		agentsDir := filepath.Join(pluginsDir, e.Name(), "agents")
		for _, def := range discoverDir(agentsDir, "plugin") {
			add(def)
		}
	}
}
