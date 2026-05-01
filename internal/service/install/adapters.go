package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/hollis-labs/nanite/internal/agent"
	"github.com/hollis-labs/nanite/internal/fsutil"
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
		if err := fsutil.AtomicWriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("write snapshot %s: %w", dst, err)
		}
	}
	return nil
}

// snapshotAdapterTargetsForRefresh is the BLG-20260412-002 customization-
// overwrite backup path: when nanite-agent init re-runs against a project
// that already has a managed section in one of the adapter target files,
// we snapshot the current rendered file into a refresh archive directory
// before the sync re-renders over it.
//
// Unlike snapshotAdapterTargets (migration path), this helper:
//   - resolves its own archive dir under the shared archive base
//     (~/Projects-apps/.archived) using a "<basename>-refresh-YYYY-MM-DD..."
//     prefix so it doesn't collide with or shadow migration archives
//   - only archives files that actually contain a Nanite managed block
//     (presence of the start marker) — unmanaged user files are left alone
//   - returns the archive dir path so the caller can surface it in reports
//
// Returns ("", nil) if no adapter target has a managed section (no backup
// needed). Returns the archive dir and nil on success; the dir may be empty
// if all managed targets were identical no-ops, but its existence is the
// audit trail. The atomic write of each snapshot means a mid-write crash
// cannot leave a partial snapshot; the re-render that follows is also
// atomic (fsutil.AtomicWriteFile inside WriteManagedSection's package is
// tracked separately, but the install-side snapshot already gives us a
// recoverable prior copy).
func snapshotAdapterTargetsForRefresh(projectDir string, ts time.Time) (string, error) {
	// Determine whether any adapter target actually has a managed section;
	// if none do, skip the backup entirely.
	any := false
	for _, name := range adapterTargetFiles {
		data, err := os.ReadFile(filepath.Join(projectDir, name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read %s for refresh snapshot: %w", name, err)
		}
		if containsManagedMarker(data) {
			any = true
			break
		}
	}
	if !any {
		return "", nil
	}

	base, err := ExpandArchiveBase(archiveBaseOverride())
	if err != nil {
		return "", err
	}
	basename := filepath.Base(projectDir) + "-refresh"
	archiveDir, err := ResolveArchiveDir(base, basename, ts)
	if err != nil {
		return "", fmt.Errorf("resolve refresh archive dir: %w", err)
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir refresh archive: %w", err)
	}

	for _, name := range adapterTargetFiles {
		src := filepath.Join(projectDir, name)
		data, err := os.ReadFile(src)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return archiveDir, fmt.Errorf("read %s for refresh snapshot: %w", src, err)
		}
		if !containsManagedMarker(data) {
			continue
		}
		dst := filepath.Join(archiveDir, name+".pre-refresh")
		if err := fsutil.AtomicWriteFile(dst, data, 0o644); err != nil {
			return archiveDir, fmt.Errorf("write refresh snapshot %s: %w", dst, err)
		}
	}
	return archiveDir, nil
}

// containsManagedMarker reports whether data holds the Nanite managed-
// section start marker. Kept byte-local so we don't take an import cycle
// on the agent package just to read a constant.
func containsManagedMarker(data []byte) bool {
	return bytes.Contains(data, []byte("<!-- nanite:start -->"))
}

func archiveBaseOverride() string {
	if v := os.Getenv("NANITE_ARCHIVE_BASE"); v != "" {
		return v
	}
	return ArchiveBase
}

// syncAdaptersForProject runs SyncAllProjectRootsFiltered against the
// built-in adapter registry, parsing the agents list from
// `<projectDir>/.nanite/config.yaml`. Only the adapters whose Name() is
// in allowedAdapters are run; nanite-native is always included.
//
// If the config file is missing or empty, the agents list is empty and
// each (allowed) adapter writes a placeholder section.
func syncAdaptersForProject(projectDir string, allowedAdapters []string) error {
	cfgPath := filepath.Join(projectDir, ".nanite", "config.yaml")
	agents, err := extractAgentsFromConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("extract agents: %w", err)
	}
	reg := newBuiltinAdapterRegistry()
	if err := reg.SyncAllProjectRootsFiltered(projectDir, agents, allowedAdapters); err != nil {
		return fmt.Errorf("sync adapters: %w", err)
	}
	return nil
}
