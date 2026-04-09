package agent

import (
	"fmt"
	"sort"
	"sync"

	"github.com/hollis-labs/nanite/internal/store"
)

// CLIAgentAdapter abstracts CLI-specific agent discovery, sandbox population,
// and project root synchronization for a particular agent framework (e.g.,
// nanite-native, claude, codex).
type CLIAgentAdapter interface {
	// Name returns the adapter identifier (e.g., "nanite-native", "claude", "codex").
	Name() string

	// Discover scans projectDir for agent definitions and returns normalized Definitions.
	Discover(projectDir string) ([]Definition, error)

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
// for adapters that support role/skill-based prompt assembly (e.g., agentrc).
type AgentComposer interface {
	CLIAgentAdapter

	// ComposePrompt assembles a system prompt from the given agent config,
	// combining roles, skills, and project context.
	ComposePrompt(agentConfig interface{}, projectDir string) (string, error)

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
	Overrides  map[string]interface{}
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
func (r *AdapterRegistry) Register(a CLIAgentAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.adapters = append(r.adapters, a)
	sort.Slice(r.adapters, func(i, j int) bool {
		return r.adapters[i].Priority() < r.adapters[j].Priority()
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

// DiscoverAll iterates all registered adapters in priority order and returns
// deduplicated definitions. The first adapter to define a slug wins.
func (r *AdapterRegistry) DiscoverAll(projectDir string) ([]Definition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]bool)
	var defs []Definition

	for _, a := range r.adapters {
		found, err := a.Discover(projectDir)
		if err != nil {
			return nil, fmt.Errorf("adapter %s: %w", a.Name(), err)
		}
		for _, d := range found {
			if seen[d.Slug] {
				continue
			}
			seen[d.Slug] = true
			defs = append(defs, d)
		}
	}

	return defs, nil
}

// PopulateAllSandboxes calls PopulateSandbox on every registered adapter.
func (r *AdapterRegistry) PopulateAllSandboxes(sandboxDir string, agent store.AgentProfile, session SandboxContext) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, a := range r.adapters {
		if err := a.PopulateSandbox(sandboxDir, agent, session); err != nil {
			return fmt.Errorf("adapter %s: populate sandbox: %w", a.Name(), err)
		}
	}
	return nil
}

// SyncAllProjectRoots calls SyncProjectRoot on every registered adapter.
func (r *AdapterRegistry) SyncAllProjectRoots(projectDir string, agents []store.AgentProfile) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, a := range r.adapters {
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
