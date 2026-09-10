package agent

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

	// WorkingDir was the project root passed through to adapter-based
	// discovery. Nothing reads it now that the adapter tier is gone; it is
	// retained only so callers (internal/service/container.go) keep
	// compiling unchanged, and is a candidate for removal alongside
	// CLIAgentPath whenever that dead tier is finally cut.
	WorkingDir string

	// The adapter tier that used to live here is gone (CW-20260910-0012).
	//
	// It survived TASKS/phase-0/16 and phase-2/06 as an always-empty loop,
	// kept "as a live extension point... a future plugin-provided adapter
	// could still return real Definitions." That future caller has now
	// arrived, and it is NOT this function: CLIAgentAdapter.Discover became
	// Import(path), an explicit operator-initiated read driven by
	// `nanite agent install` (internal/agentimport). Calling it from here
	// would hand every adapter a working directory nobody named, on every
	// boot — which is exactly the directory-scan tier phase-1/08 removed,
	// rebuilt under a new method name.
	//
	// The AdapterRegistry itself is very much alive; it is simply not a
	// discovery input anymore. internal/service/container.go still builds
	// one for the unrelated PopulateAllSandboxes / SyncAllProjectRoots
	// directions, and internal/agentimport's RegistryParser drives Import
	// from the CLI and REST triggers.
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

	// There is no second tier. See DiscoverOptions' comment on the removed
	// adapter tier: registration is import, and import is not discovery.

	return defs, nil
}

// discoverDir -- a directory walk that read every *.md in a directory and
// stamped a Source on each -- is deleted (CW-20260910-0012). Its last caller
// was the adapter tier above; the project/user/plugin tiers that used to call
// it went with TASKS/phase-1/08.
//
// Nothing in internal/agent walks a directory anymore. That is the property
// worth keeping: the package that defines what an agent IS no longer contains
// the machinery for finding agents lying around on disk. Reading a directory
// of definitions is a format question, and it now lives with the format --
// see adapter-claude's Import.
