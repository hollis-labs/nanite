package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	fplugin "github.com/hollis-labs/go-plugin"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
)

// DiscoveredPlugin holds metadata parsed from a plugin.yaml plus the
// constructor looked up from the registry. For subprocess plugins, the
// constructor is nil — a SubprocessPlugin is created at load time instead.
type DiscoveredPlugin struct {
	Manifest    *PluginManifest
	Dir         string
	Constructor PluginConstructor // nil for subprocess plugins
}

// IsSubprocess returns true if this plugin uses the subprocess runtime.
func (dp DiscoveredPlugin) IsSubprocess() bool {
	return dp.Manifest.Runtime == "subprocess"
}

// DiscoverPlugins scans the given directory for subdirectories containing
// plugin.yaml, parses each manifest, and looks up the registered constructor.
// Returns both builtin plugins (with constructors) and subprocess plugins
// (with nil constructors — they are instantiated at load time).
func DiscoverPlugins(pluginsDir string) ([]DiscoveredPlugin, error) {
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read plugins directory: %w", err)
	}

	var discovered []DiscoveredPlugin
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dir := filepath.Join(pluginsDir, entry.Name())
		manifestPath := filepath.Join(dir, "plugin.yaml")

		if _, err := os.Stat(manifestPath); err != nil {
			continue // no plugin.yaml — skip
		}

		manifest, err := ParseManifest(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", manifestPath, err)
		}

		// Subprocess plugins don't need a compiled-in constructor.
		if manifest.Runtime == "subprocess" {
			if manifest.Entrypoint == "" {
				return nil, fmt.Errorf("plugin %q: runtime is subprocess but no entrypoint specified", manifest.Name)
			}
			discovered = append(discovered, DiscoveredPlugin{
				Manifest: manifest,
				Dir:      dir,
			})
			continue
		}

		// Builtin (default): look up the compiled-in constructor.
		constructor, ok := LookupConstructor(manifest.Name)
		if !ok {
			// Plugin directory exists but no Go code registered — skip.
			continue
		}

		discovered = append(discovered, DiscoveredPlugin{
			Manifest:    manifest,
			Dir:         dir,
			Constructor: constructor,
		})
	}

	return discovered, nil
}

// LoadDiscovered instantiates and loads all discovered plugins into the host,
// respecting dependency order. Supports both builtin and subprocess plugins.
// Returns the list of successfully loaded plugins.
func LoadDiscovered(host *Host, discovered []DiscoveredPlugin) ([]fplugin.Plugin, []error) {
	// Topological sort: plugins load after their dependencies.
	sorted, cycleErr := sortByDeps(discovered)
	if cycleErr != nil {
		return nil, []error{cycleErr}
	}

	var loaded []fplugin.Plugin
	var errs []error

	for _, dp := range sorted {
		// Build config for this plugin.
		cfg, err := NewPluginConfig(dp.Manifest.Name, dp.Dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("config for %s: %w", dp.Manifest.Name, err))
			continue
		}

		// Store config on host so GetConfig works during Load.
		host.SetPluginConfig(dp.Manifest.Name, cfg)

		var p fplugin.Plugin

		if dp.IsSubprocess() {
			// Subprocess plugin: create a SubprocessPlugin that bridges via JSON-RPC.
			p, err = newSubprocessPluginFromManifest(dp)
			if err != nil {
				errs = append(errs, fmt.Errorf("create subprocess plugin %s: %w", dp.Manifest.Name, err))
				continue
			}
		} else {
			// Builtin plugin: use the compiled-in constructor.
			p = dp.Constructor()
		}

		if err := host.LoadPlugin(p); err != nil {
			errs = append(errs, fmt.Errorf("load %s: %w", dp.Manifest.Name, err))
			continue
		}

		// Apply yaml-authoritative declarative registrations from the manifest.
		// This is the B.4 unification path — the host registers envelopes /
		// slots / keybindings / components on behalf of the plugin so that
		// builtin and subprocess plugins flow through the same wiring.
		if err := applyManifestRegistrations(host, dp.Manifest, p); err != nil {
			errs = append(errs, fmt.Errorf("apply manifest for %s: %w", dp.Manifest.Name, err))
		}

		loaded = append(loaded, p)
	}

	return loaded, errs
}

// ManifestProvider is an optional interface that compiled-in builtins can
// implement to expose their plugin.yaml to the host via //go:embed. When a
// builtin implements this interface, LoadRegisteredBuiltins runs the same
// yaml-authoritative registration path as DiscoverPlugins, allowing the
// builtin's plugin.yaml to drive host registrations instead of the builtin's
// Load() method making direct Register* calls. See plan §B.4.
type ManifestProvider interface {
	Manifest() *PluginManifest
}

// newSubprocessPluginFromManifest creates a SubprocessPlugin from a discovered
// plugin manifest with runtime: subprocess.
func newSubprocessPluginFromManifest(dp DiscoveredPlugin) (*subprocess.SubprocessPlugin, error) {
	m := dp.Manifest

	// Resolve the entrypoint command and args.
	command, args := parseEntrypoint(m.Entrypoint, dp.Dir)

	// Verify the command exists.
	if _, err := exec.LookPath(command); err != nil {
		// Try as relative path from plugin dir.
		absCmd := filepath.Join(dp.Dir, command)
		if _, err := exec.LookPath(absCmd); err != nil {
			return nil, fmt.Errorf("entrypoint %q not found: %w", m.Entrypoint, err)
		}
		command = absCmd
	}

	// Resolve config values for the subprocess.
	config := make(map[string]string)
	for key, entry := range m.Config {
		if entry.EnvVar != "" {
			if v := os.Getenv(entry.EnvVar); v != "" {
				config[key] = v
				continue
			}
		}
		if entry.Default != "" {
			config[key] = entry.Default
		}
	}

	mgrCfg := subprocess.DefaultManagerConfig(command, dp.Dir)
	mgrCfg.Args = args

	return subprocess.NewSubprocessPlugin(dp.Dir, config, mgrCfg), nil
}

// parseEntrypoint splits an entrypoint string like "python3 plugin.py" into
// a command and args. If the entrypoint is a single token (e.g., "./my-plugin"),
// args is nil.
func parseEntrypoint(entrypoint, pluginDir string) (string, []string) {
	parts := strings.Fields(entrypoint)
	if len(parts) == 0 {
		return entrypoint, nil
	}
	return parts[0], parts[1:]
}

// LoadRegisteredBuiltins loads all registered plugin constructors that are not
// already loaded in the host. This ensures compiled-in plugins without a
// plugins/ directory (no plugin.yaml) are still loaded and visible.
func LoadRegisteredBuiltins(host *Host) ([]fplugin.Plugin, []error) {
	registered := GetRegistered()

	var loaded []fplugin.Plugin
	var errs []error

	for id, constructor := range registered {
		// Skip if already loaded (e.g. via DiscoverPlugins).
		if _, exists := host.GetPlugin(id); exists {
			continue
		}

		p := constructor()
		if err := host.LoadPlugin(p); err != nil {
			errs = append(errs, fmt.Errorf("load builtin %s: %w", id, err))
			continue
		}

		// If this builtin ships an embedded plugin.yaml via ManifestProvider,
		// apply the same yaml-authoritative registrations so builtin and
		// discovered plugins share the wiring path (B.4).
		if mp, ok := p.(ManifestProvider); ok {
			if manifest := mp.Manifest(); manifest != nil {
				if err := applyManifestRegistrations(host, manifest, p); err != nil {
					errs = append(errs, fmt.Errorf("apply manifest for builtin %s: %w", id, err))
				}
			}
		}

		loaded = append(loaded, p)
	}

	return loaded, errs
}

// sortByDeps performs a topological sort using Kahn's algorithm so that
// plugins are loaded after all of their dependencies.  Returns an error
// if a dependency cycle is detected.
func sortByDeps(plugins []DiscoveredPlugin) ([]DiscoveredPlugin, error) {
	// Build index: plugin name → DiscoveredPlugin.
	byName := make(map[string]int, len(plugins))
	for i, p := range plugins {
		byName[p.Manifest.Name] = i
	}

	// In-degree: how many (present) deps each plugin has.
	inDeg := make([]int, len(plugins))
	// Adjacency: dependencyIdx → []dependentIdx.
	adj := make([][]int, len(plugins))

	for i, p := range plugins {
		for _, dep := range p.Manifest.Dependencies {
			depIdx, ok := byName[dep]
			if !ok {
				continue // external dep — checked at load time
			}
			adj[depIdx] = append(adj[depIdx], i)
			inDeg[i]++
		}
	}

	// Seed queue with zero-in-degree nodes.
	queue := make([]int, 0, len(plugins))
	for i, d := range inDeg {
		if d == 0 {
			queue = append(queue, i)
		}
	}

	sorted := make([]DiscoveredPlugin, 0, len(plugins))
	for len(queue) > 0 {
		idx := queue[0]
		queue = queue[1:]
		sorted = append(sorted, plugins[idx])
		for _, depIdx := range adj[idx] {
			inDeg[depIdx]--
			if inDeg[depIdx] == 0 {
				queue = append(queue, depIdx)
			}
		}
	}

	if len(sorted) != len(plugins) {
		// Find the cycle participants for a useful error message.
		var cycled []string
		for i, d := range inDeg {
			if d > 0 {
				cycled = append(cycled, plugins[i].Manifest.Name)
			}
		}
		return nil, fmt.Errorf("dependency cycle detected among plugins: %v", cycled)
	}

	return sorted, nil
}
