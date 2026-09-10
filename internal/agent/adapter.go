package agent

import (
	"fmt"
	"sort"
	"sync"

	"github.com/hollis-labs/nanite/internal/store"
)

// CLIAgentAdapter abstracts CLI-specific agent import, sandbox population,
// and project root synchronization for a particular agent framework (e.g.,
// nanite-native, claude, codex).
type CLIAgentAdapter interface {
	// Name returns the adapter identifier (e.g., "nanite-native", "claude", "codex").
	Name() string

	// Import reads one operator-named path and returns normalized
	// Definitions. It replaces the former Discover(projectDir), and the
	// difference is the whole point rather than a rename:
	//
	//   - Discover was implicit, boot-time and directory-shaped. It ran on
	//     every start against a working directory nobody named, which is
	//     what produced the drift between a file and the row derived from
	//     it that TASKS/phase-1/08 cut out.
	//   - Import is explicit, operator-initiated and single-target. It runs
	//     only when someone names a path (CW-20260910-0009's
	//     `nanite agent install`, or its REST twin), reads once, and
	//     remembers nothing about where it read from.
	//
	// Nothing in Nanite's boot path calls this. If a caller ever appears in
	// internal/agent/discovery.go again, the cut has been undone.
	//
	// Returning (nil, nil) means "this path is not my format" — a registry
	// uses that to try the next adapter. Returning an error means "this
	// path IS mine and it is broken," which stops the import.
	//
	// An adapter MAY expand a directory into N Definitions, and MAY treat a
	// directory as ONE Definition (a materialized boot directory is a
	// single agent, not a folder of them). That choice belongs here because
	// it is a question about the format, not about importing.
	//
	// Two rules bind every implementation:
	//
	//   - Never write to path. Import is one-way; the source is read and
	//     forgotten. See internal/agentimport's package doc.
	//   - Leave Definition.Model blank unless the source names a real
	//     Nanite model ID. A foreign format's model alias is not one, and
	//     store.ResolveProviderAndModel re-resolves per request anyway
	//     (parser.go's Model doc comment: ten profiles made this mistake).
	//
	// Definition.Source names the ecosystem the definition came FROM (e.g.
	// "claude"); the import pipeline records it as provenance and stamps
	// its own value into the stored row. An adapter does not get to declare
	// an imported agent operator-owned.
	Import(path string) ([]Definition, error)

	// PopulateSandbox writes CLI-specific config files into sandboxDir for the
	// given agent profile and session context.
	PopulateSandbox(sandboxDir string, agent store.AgentProfile, session SandboxContext) error

	// SyncProjectRoot writes or updates managed sections in the project root
	// config files for the given agents.
	SyncProjectRoot(projectDir string, agents []store.AgentProfile) error

	// Priority returns the discovery order. Lower values are checked first.
	Priority() int
}

// AgentComposer extends CLIAgentAdapter with prompt composition capabilities
// for adapters that support role/skill-based prompt assembly (e.g., nanite-native).
type AgentComposer interface {
	CLIAgentAdapter

	// ComposePrompt assembles a system prompt from the given agent config,
	// combining roles, skills, and project context.
	ComposePrompt(agentConfig any, projectDir string) (string, error)

	// ListRoles returns available roles known to this adapter.
	ListRoles() ([]RoleInfo, error)

	// ListSkills returns available skills known to this adapter.
	ListSkills() ([]SkillInfo, error)
}

// SandboxContext carries session-level information needed by adapters when
// populating a sandbox directory.
type SandboxContext struct {
	SessionID  string
	WorkingDir string
	DBPath     string
	MCPServers []string
	Overrides  map[string]any
}

// RoleInfo describes a role available within an adapter.
type RoleInfo struct {
	Name        string
	Type        string
	Description string
	FilePath    string
}

// SkillInfo describes a skill available within an adapter.
type SkillInfo struct {
	Name        string
	Description string
	FilePath    string
}

// AdapterRegistry holds registered CLIAgentAdapters and provides aggregate
// operations (discover all, populate all sandboxes, sync all project roots).
// All methods are safe for concurrent use.
type AdapterRegistry struct {
	mu       sync.RWMutex
	adapters []CLIAgentAdapter
}

// NewAdapterRegistry creates an empty AdapterRegistry.
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{}
}

// Register adds an adapter and re-sorts the internal list by priority (ascending).
// Ties are broken by adapter name to keep ordering deterministic.
func (r *AdapterRegistry) Register(a CLIAgentAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.adapters = append(r.adapters, a)
	sort.Slice(r.adapters, func(i, j int) bool {
		pi, pj := r.adapters[i].Priority(), r.adapters[j].Priority()
		if pi != pj {
			return pi < pj
		}
		return r.adapters[i].Name() < r.adapters[j].Name()
	})
}

// Adapters returns a copy of the registered adapters sorted by priority.
func (r *AdapterRegistry) Adapters() []CLIAgentAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]CLIAgentAdapter, len(r.adapters))
	copy(out, r.adapters)
	return out
}

// ImportAll offers path to each registered adapter in priority order and
// returns the first non-empty result, along with the name of the adapter that
// produced it.
//
// First non-empty wins rather than merge-everything, because a path has one
// format: two adapters both claiming it means one of them guessed, and
// merging two guesses produces an agent roster nobody authored. Priority
// ordering is what makes "first" deterministic, and a caller that knows better
// names the adapter directly via GetAdapter.
//
// Dedup-by-slug survives the change, now scoped within the winning adapter's
// own result: a directory holding two definitions under one slug keeps the
// first, matching the priority rule at the finer grain rather than silently
// importing both.
//
// Returns ("", nil, nil) when no adapter claims the path. The adapter slice is
// copied before iteration to avoid holding the lock during filesystem I/O.
func (r *AdapterRegistry) ImportAll(path string) (string, []Definition, error) {
	adapters := r.Adapters() // copy under lock, then release

	for _, a := range adapters {
		found, err := a.Import(path)
		if err != nil {
			return a.Name(), nil, fmt.Errorf("adapter %s: %w", a.Name(), err)
		}
		if len(found) == 0 {
			continue
		}

		seen := make(map[string]bool, len(found))
		defs := make([]Definition, 0, len(found))
		for _, d := range found {
			if seen[d.Slug] {
				continue
			}
			seen[d.Slug] = true
			defs = append(defs, d)
		}
		return a.Name(), defs, nil
	}

	return "", nil, nil
}

// PopulateAllSandboxes calls PopulateSandbox on every registered adapter.
// The adapter slice is copied before iteration to avoid holding the lock
// during I/O.
func (r *AdapterRegistry) PopulateAllSandboxes(sandboxDir string, agent store.AgentProfile, session SandboxContext) error {
	adapters := r.Adapters()

	for _, a := range adapters {
		if err := a.PopulateSandbox(sandboxDir, agent, session); err != nil {
			return fmt.Errorf("adapter %s: populate sandbox: %w", a.Name(), err)
		}
	}
	return nil
}

// SyncAllProjectRoots calls SyncProjectRoot on every registered adapter.
// The adapter slice is copied before iteration to avoid holding the lock
// during I/O.
func (r *AdapterRegistry) SyncAllProjectRoots(projectDir string, agents []store.AgentProfile) error {
	adapters := r.Adapters()

	for _, a := range adapters {
		if err := a.SyncProjectRoot(projectDir, agents); err != nil {
			return fmt.Errorf("adapter %s: sync project root: %w", a.Name(), err)
		}
	}
	return nil
}

// SyncAllProjectRootsFiltered is the same as SyncAllProjectRoots but
// only runs the adapters whose Name() is present in the allowed slice.
// The "nanite-native" adapter is always run regardless of the allowed
// list — it manages .nanite/ and NANITE.md, which are not user-selectable.
//
// The adapter slice is copied before iteration to avoid holding the lock
// during I/O.
func (r *AdapterRegistry) SyncAllProjectRootsFiltered(projectDir string, agents []store.AgentProfile, allowed []string) error {
	allowSet := make(map[string]bool, len(allowed)+1)
	for _, name := range allowed {
		allowSet[name] = true
	}
	allowSet["nanite-native"] = true

	adapters := r.Adapters()

	for _, a := range adapters {
		if !allowSet[a.Name()] {
			continue
		}
		if err := a.SyncProjectRoot(projectDir, agents); err != nil {
			return fmt.Errorf("adapter %s: sync project root: %w", a.Name(), err)
		}
	}
	return nil
}

// GetAdapter returns the adapter with the given name, or false if not found.
func (r *AdapterRegistry) GetAdapter(name string) (CLIAgentAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, a := range r.adapters {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}
