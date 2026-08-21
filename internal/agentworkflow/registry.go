package agentworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Registry is a name-keyed collection of WorkflowDefinitions loaded from a
// directory of YAML files (CW-20260813-0014). It is the lookup a
// dispatch-routed or directly-invoked "run this named workflow" call
// resolves against — nothing in agentworkflow itself calls
// LoadDefinitionYAMLFile/ParseDefinitionYAML in production before this.
//
// mu guards definitions. LoadRegistryDir/NewRegistry build a Registry
// once, read-only thereafter, in every pre-Teams caller — but
// TASKS/teams/08-team-run-launcher.md's Register (below) makes runtime
// registration a real, concurrent-safe operation: a TeamRun compiles and
// registers its own WorkflowDefinition at launch time, and more than one
// TeamRun can launch concurrently against the same shared *Registry
// (the one instance cmd/nanite/main.go constructs and hands to
// WorkflowLauncher/TaskManager/AgentCardGenerator alike). Get/Names/
// Register all take the lock so a concurrent launch-time write can never
// race a concurrent lookup.
type Registry struct {
	mu          sync.RWMutex
	definitions map[string]WorkflowDefinition
}

// NewRegistry wraps a pre-built name→definition map. Exposed for tests and
// for callers that assemble definitions from a source other than a YAML
// directory.
func NewRegistry(definitions map[string]WorkflowDefinition) *Registry {
	if definitions == nil {
		definitions = map[string]WorkflowDefinition{}
	}
	return &Registry{definitions: definitions}
}

// LoadRegistryDir reads every *.yaml/*.yml file directly under dir (no
// recursion — matches the config/agents/*.md file-SoT convention), parses
// each as a WorkflowDefinition via LoadDefinitionYAMLFile (which validates
// the DAG at load time), and indexes the result by its Name field.
//
// An empty dir returns an empty, inert Registry — no error — mirroring
// BootProfileCatalogPath's "no catalog configured → no behavior change"
// rule. A configured-but-missing directory is also treated as empty+inert
// (logged by the caller, not here — this package has no logger dependency)
// so a stale/unset path never blocks startup. A malformed file, a duplicate
// Name across files, or a cyclic/invalid definition IS an error: silently
// dropping a broken workflow definition would let an operator believe a
// workflow exists when it doesn't.
func LoadRegistryDir(dir string) (*Registry, error) {
	reg := NewRegistry(nil)
	if dir == "" {
		return reg, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return reg, nil
		}
		return nil, fmt.Errorf("agentworkflow: read registry dir %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(dir, name)
		wf, err := LoadDefinitionYAMLFile(path)
		if err != nil {
			return nil, err
		}
		if wf.Name == "" {
			return nil, fmt.Errorf("agentworkflow: %s: workflow definition has no name", path)
		}
		if _, dup := reg.definitions[wf.Name]; dup {
			return nil, fmt.Errorf("agentworkflow: %s: duplicate workflow name %q", path, wf.Name)
		}
		reg.definitions[wf.Name] = wf
	}
	return reg, nil
}

// Get returns the named WorkflowDefinition and whether it was found.
func (r *Registry) Get(name string) (WorkflowDefinition, bool) {
	if r == nil {
		return WorkflowDefinition{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	wf, ok := r.definitions[name]
	return wf, ok
}

// Names returns every registered workflow name, sorted.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.definitions))
	for name := range r.definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Register adds a dynamically-built WorkflowDefinition to the registry
// under its own Name field, making it reachable via Get (and therefore via
// WorkflowLauncher.Launch's WorkflowName lookup) without going through
// LoadRegistryDir's YAML-file loading path.
//
// TASKS/teams/08-team-run-launcher.md is the first real caller: a TeamRun
// launch compiles a Team's phase sequence into a plain WorkflowDefinition
// (internal/service/team_compiler.go's CompileTeam) that has no YAML file
// on disk at all — it exists only for the lifetime of that one launch, so
// LoadRegistryDir's directory-scan model doesn't fit it. Register is the
// minimal, additive seam that lets a compiled-in-memory definition become
// launchable through the exact same WorkflowLauncher.Launch(WorkflowName)
// path every YAML-authored workflow already uses — not a second launch
// mechanism, just a second way to populate the same lookup table.
//
// wf.Name must be non-empty and not already registered — a caller that
// wants a fresh, always-launchable definition (e.g. one TeamRun launch)
// should give it a unique name (TeamRun launch appends a fresh ID) rather
// than rely on Register silently overwriting a prior entry. wf must also
// pass Validate — the same DAG-shape check LoadRegistryDir already applies
// to every YAML-loaded definition, so a caller can't register something
// WorkflowLauncher.Launch would only fail on later, deeper in the call
// stack.
func (r *Registry) Register(wf WorkflowDefinition) error {
	if r == nil {
		return fmt.Errorf("agentworkflow: register: nil registry")
	}
	if wf.Name == "" {
		return fmt.Errorf("agentworkflow: register: workflow definition has no name")
	}
	if err := Validate(wf); err != nil {
		return fmt.Errorf("agentworkflow: register %q: %w", wf.Name, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.definitions == nil {
		r.definitions = map[string]WorkflowDefinition{}
	}
	if _, dup := r.definitions[wf.Name]; dup {
		return fmt.Errorf("agentworkflow: register: duplicate workflow name %q", wf.Name)
	}
	r.definitions[wf.Name] = wf
	return nil
}

// Unregister removes name from the registry, if present. A no-op — not an
// error — when name isn't currently registered, so a caller that races
// itself (or simply calls this defensively) never needs to guard against a
// double-unregister.
//
// TASKS/teams/11-team-run-launch-api.md's own required prerequisite: task
// 08's LaunchTeamRun registers a permanently-unique-named compiled
// TeamRun WorkflowDefinition (Register, above) on every launch and never
// removed it — an unbounded-growth gap a fresh reviewer flagged before this
// task wired LaunchTeamRun into real, callable traffic. Unregister is the
// primitive that closes it: TeamRunLauncher.LaunchTeamRun calls this once a
// launched TeamRun's WorkflowResult comes back in a terminal status
// (completed/failed/cancelled) — at that point nothing will ever call
// registry.Get(wfName) again (a2a_task_manager.go's resumeWorkflowRun only
// re-fetches by name for a run that's still RunStatusWaiting/
// RunStatusWaitingOnFlex). A still-waiting TeamRun (the common case per
// 15-teams.md's own illustrative examples, every one of which opens on a
// flex step) is deliberately left registered here — Resume needs the
// definition reachable by name later — so this alone does not fully bound
// registry growth for long-lived TeamRuns. See
// AgentCardGenerator.Generate's own use of IsTeamRunDefinitionName (below)
// for the resolution that DOES apply unconditionally, regardless of run
// status: excluding every per-launch compiled TeamRun definition from the
// public A2A skill-discovery enumeration outright, so a still-registered
// (waiting) TeamRun definition never pollutes /.well-known/agent-card.json
// even though it remains reachable by name for Resume.
func (r *Registry) Unregister(name string) {
	if r == nil || name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.definitions, name)
}

// TeamRunDefinitionNamePrefix is the literal namespace prefix
// TASKS/teams/08-team-run-launcher.md's LaunchTeamRun stamps on every
// per-launch compiled TeamRun WorkflowDefinition it registers
// (internal/service/team_run_launcher.go builds the full name as
// fmt.Sprintf("team-run:%s:%s", team.Name, ulid.Make().String())) — never
// present on a YAML-authored, LoadRegistryDir-loaded workflow definition
// (those are named by their own authors; ":" is not a character this
// codebase's own workflow-name convention uses elsewhere in a definition's
// Name). Exported as a shared constant, not duplicated as a literal string
// in both the producer (team_run_launcher.go) and the consumer
// (a2a_agent_card.go's IsTeamRunDefinitionName-filtered skill enumeration),
// so the two stay in lock-step by construction.
const TeamRunDefinitionNamePrefix = "team-run:"

// IsTeamRunDefinitionName reports whether name is a per-launch compiled
// TeamRun WorkflowDefinition's own Name, per TeamRunDefinitionNamePrefix.
func IsTeamRunDefinitionName(name string) bool {
	return strings.HasPrefix(name, TeamRunDefinitionNamePrefix)
}
