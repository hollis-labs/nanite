package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/nanite/internal/agent"
	adapterclaude "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-claude"
	adaptercodex "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-codex"
	adaptergemini "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-gemini"
	nanitenative "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-nanite-native"
	adapteropencode "github.com/hollis-labs/nanite/internal/plugin/builtin/adapter-opencode"
	"github.com/hollis-labs/nanite/internal/store"
)

// adapterTargetFiles is the set of project-root markdown files that the
// built-in adapters write managed sections into. Used by the install
// service to snapshot pre-edit state for rollback and to drive cleanup
// during rollback.
var adapterTargetFiles = []string{
	"CLAUDE.md",
	"AGENTS.md",
	"GEMINI.md",
	"OPENCODE.md",
}

// newBuiltinAdapterRegistry constructs a fresh AdapterRegistry populated
// with the five built-in CLI adapters (claude, codex, gemini, opencode,
// nanite-native). The adapters are stateless and don't require the plugin
// host, the database, or any other Nanite infrastructure — they only need
// a project directory and a list of agents passed to SyncProjectRoot.
//
// This factory lives in the install package (not internal/agent) because
// the adapter packages already depend on internal/agent, so the import
// would cycle if the factory lived alongside the adapter interface.
func newBuiltinAdapterRegistry() *agent.AdapterRegistry {
	reg := agent.NewAdapterRegistry()
	reg.Register(adapterclaude.New().Adapter())
	reg.Register(adaptercodex.New().Adapter())
	reg.Register(adaptergemini.New().Adapter())
	reg.Register(adapteropencode.New().Adapter())
	reg.Register(nanitenative.New().Adapter())
	return reg
}

// projectConfig is the minimal subset of .nanite/config.yaml that the
// install service needs in order to extract an agents list for adapter
// sync and to read/write the adapters selection list. We define our own
// struct (rather than reusing the one in adapter-nanite-native) to avoid
// coupling.
type projectConfig struct {
	Adapters *[]string               `yaml:"adapters,omitempty"`
	Agents   map[string]projectAgent `yaml:"agents"`
}

type projectAgent struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// extractAgentsFromConfig reads a .nanite/config.yaml file and returns the
// agents declared inside it as a minimal []store.AgentProfile slice
// suitable for passing to AdapterRegistry.SyncAllProjectRoots.
//
// Only Name, Slug, and Description are populated — that's all the
// built-in adapters consume from AgentProfile in their SyncProjectRoot
// implementations. The slug comes from the YAML map key.
//
// Returns an empty slice (no error) if the file doesn't exist, has no
// agents block, or the agents block is empty. Returns an error only on
// I/O failures or YAML parse errors.
func extractAgentsFromConfig(path string) ([]store.AgentProfile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []store.AgentProfile{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read project config %s: %w", path, err)
	}
	var cfg projectConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse project config %s: %w", path, err)
	}
	out := make([]store.AgentProfile, 0, len(cfg.Agents))
	for slug, a := range cfg.Agents {
		name := a.Name
		if name == "" {
			name = slug
		}
		out = append(out, store.AgentProfile{
			ID:          "file-" + slug,
			Slug:        slug,
			Name:        name,
			Description: a.Description,
		})
	}
	return out, nil
}

// snapshotAdapterTargets copies any existing project-root CLI files
// (CLAUDE.md, AGENTS.md, GEMINI.md, OPENCODE.md) into the archive
// directory as `.pre-edit` snapshots so Rollback can restore them.
//
// Files that don't exist in the project are skipped — Rollback knows
// "no snapshot" means "the file was created by the installer" and will
// remove the file (after stripping the managed section) instead of
// restoring it.
func snapshotAdapterTargets(projectDir, archiveDir string) error {
	for _, name := range adapterTargetFiles {
		src := filepath.Join(projectDir, name)
		data, err := os.ReadFile(src)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read %s for snapshot: %w", src, err)
		}
		dst := filepath.Join(archiveDir, name+".pre-edit")
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("write snapshot %s: %w", dst, err)
		}
	}
	return nil
}

// syncAdaptersForProject runs SyncAllProjectRoots against the built-in
// adapter registry, parsing the agents list from
// `<projectDir>/.nanite/config.yaml`. Returns nil on success.
//
// If the config file is missing or empty, the agents list is empty and
// each adapter's SyncProjectRoot returns nil immediately (the built-in
// adapters short-circuit on empty agent lists).
func syncAdaptersForProject(projectDir string) error {
	cfgPath := filepath.Join(projectDir, ".nanite", "config.yaml")
	agents, err := extractAgentsFromConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("extract agents: %w", err)
	}
	reg := newBuiltinAdapterRegistry()
	if err := reg.SyncAllProjectRoots(projectDir, agents); err != nil {
		return fmt.Errorf("sync adapters: %w", err)
	}
	return nil
}
