package agentworkflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Registry is a name-keyed collection of WorkflowDefinitions loaded from a
// directory of YAML files (CW-20260813-0014). It is the lookup a
// dispatch-routed or directly-invoked "run this named workflow" call
// resolves against — nothing in agentworkflow itself calls
// LoadDefinitionYAMLFile/ParseDefinitionYAML in production before this.
type Registry struct {
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
	wf, ok := r.definitions[name]
	return wf, ok
}

// Names returns every registered workflow name, sorted.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.definitions))
	for name := range r.definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
