package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// DiscoverOptions configures agent discovery.
//
// TASKS/phase-1/08 ("Kill the file-reingest-on-boot pattern, in full"): files
// are not agent storage going forward, except the compiled-in builtin/seed
// profiles (loaded separately via internal/agent/builtin, not through this
// function). The project (.nanite/agents/), user (~/.nanite/agents/), and
// plugin (plugins/*/agents/*.md) directory-scan tiers that used to live here
// were removed — a file dropped in any of those locations is no longer
// discovered or auto-ingested into agent_profiles at boot.
type DiscoverOptions struct {
	// CLIAgentPath is a single agent file specified via --agent flag
	// (highest priority when set). Investigated during this task's Round 2:
	// no real CLI flag in cmd/nanite/ currently sets this field (the
	// "--agent" flags that do exist take an agent ID/slug for selecting an
	// existing agent, a different mechanism) — the tier is unreachable dead
	// code in production today. Per this task's own default guidance
	// ("leave it alone unless you find it's silently writing a persisted
	// row the same way the cut tiers do"), it is left in place rather than
	// removed: it never actually persists anything today because it is
	// never invoked. See the task's Work Log Round 2 entry for the full
	// investigation.
	CLIAgentPath string

	// WorkingDir is the project root passed through to adapter-based
	// discovery (e.g. the nanite-native adapter's .nanite/config.yaml
	// agents: block). No directory-scan tier of its own reads it directly
	// anymore.
	WorkingDir string

	// Adapters is an optional AdapterRegistry for adapter-based discovery.
	// External-ecosystem-format adapters (claude/codex/gemini/opencode)
	// already have a no-op Discover() as of Phase 0's cut of external agent
	// import; the nanite-native adapter's own discovery is unaffected by
	// this task and stays live.
	Adapters *AdapterRegistry
}

// Discover returns parsed Definitions from every remaining discovery source
// in priority order. First slug wins — a lower-priority source does not
// override a higher-priority one already added.
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

	// Priority 1: CLI --agent flag (single file). See DiscoverOptions'
	// CLIAgentPath doc comment — currently unreachable in production, kept
	// per this task's own default guidance.
	if opts.CLIAgentPath != "" {
		def, err := ParseMDFile(opts.CLIAgentPath)
		if err != nil {
			return nil, err // CLI flag error is fatal, not silently skipped.
		}
		def.Source = "cli"
		add(def)
	}

	// Priority 2+: adapter-discovered agents (e.g. the nanite-native
	// adapter's .nanite/config.yaml agents: block). External-ecosystem-format
	// adapters were already cut to no-ops by Phase 0's external-agent-import
	// removal.
	if opts.Adapters != nil {
		adapterDefs, err := opts.Adapters.DiscoverAll(opts.WorkingDir)
		if err != nil {
			slog.Warn("agent: adapter discovery failed", "err", err)
		} else {
			for i := range adapterDefs {
				add(&adapterDefs[i])
			}
		}
	}

	return defs, nil
}

// discoverDir reads all *.md files from a directory, parses them, and sets
// Source. Returns nil on missing or unreadable directories.
//
// Retained for adapter-discovery tests that simulate a directory-scan tier
// (see discovery_test.go's testDirAdapter) — no tier in Discover() itself
// calls this directly anymore; the project/user/plugin directory scans that
// used to call it were removed by TASKS/phase-1/08.
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
			slog.Warn("agent: skipping", "path", path, "err", err)
			continue
		}
		def.Source = source
		defs = append(defs, def)
	}
	return defs
}
