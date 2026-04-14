package plugin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	fplugin "github.com/hollis-labs/plugin-sdk"
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
				return nil, fmt.Errorf("plugin %q: runtime is subprocess but no entrypoint specified", manifest.Identifier())
			}
			discovered = append(discovered, DiscoveredPlugin{
				Manifest: manifest,
				Dir:      dir,
			})
			continue
		}

		// Builtin (default): look up the compiled-in constructor by canonical id.
		constructor, ok := LookupConstructor(manifest.Identifier())
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
		pluginID := dp.Manifest.Identifier()

		// Build config for this plugin.
		cfg, err := NewPluginConfig(pluginID, dp.Dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("config for %s: %w", pluginID, err))
			continue
		}

		// Store config on host so GetConfig works during Load.
		host.SetPluginConfig(pluginID, cfg)

		var p fplugin.Plugin

		if dp.IsSubprocess() {
			// Subprocess plugin: create a SubprocessPlugin that bridges via JSON-RPC.
			sp, err := newSubprocessPluginFromManifest(dp)
			if err != nil {
				wrapped := fmt.Errorf("create subprocess plugin %s: %w", pluginID, err)
				errs = append(errs, wrapped)
				host.EmitPluginLoadFailed(pluginID, wrapped.Error())
				continue
			}
			// Post-B.10 LoadResult no longer carries Dependencies; seed them
			// from the yaml manifest so Plugin.Dependencies() reports the same
			// list the topological-sort path used above.
			if len(dp.Manifest.Dependencies) > 0 {
				sp.SetDependencies(append([]string(nil), dp.Manifest.Dependencies...))
			}
			p = sp
		} else {
			// Builtin plugin: use the compiled-in constructor.
			p = dp.Constructor()
		}

		if err := host.LoadPlugin(p); err != nil {
			errs = append(errs, fmt.Errorf("load %s: %w", pluginID, err))
			host.EmitPluginLoadFailed(pluginID, err.Error())
			continue
		}

		// B.11: install the envelope strict-validation filter on subprocess
		// plugins. The filter closes over the owning pluginID so the host can
		// resolve the plugin's declared envelope types + compiled schemas.
		// Builtin plugins don't flow envelopes back through this path (they
		// call chat APIs directly), so there's no equivalent hook for them.
		if sp, ok := p.(*subprocess.SubprocessPlugin); ok {
			id := pluginID
			sp.SetEnvelopeFilter(func(envs []fplugin.EnvelopeOut) []fplugin.EnvelopeOut {
				return host.FilterPluginEnvelopes(id, envs)
			})
		}

		// Apply yaml-authoritative declarative registrations from the manifest.
		// This is the B.4 unification path — the host registers envelopes /
		// slots / keybindings / components on behalf of the plugin so that
		// builtin and subprocess plugins flow through the same wiring.
		if err := applyManifestRegistrations(host, dp.Manifest, p, dp.Dir); err != nil {
			errs = append(errs, fmt.Errorf("apply manifest for %s: %w", pluginID, err))
			// The manifest was already recorded and some registrations may have
			// partially applied. Best-effort UnloadPlugin to clean up the manifest
			// side-map + any registered extensions so we don't leave the plugin
			// "live" with a load_failed event. UnloadPlugin emits
			// plugin.uninstalled internally, which is the correct signal that
			// the plugin is no longer present.
			if unloadErr := host.UnloadPlugin(pluginID); unloadErr != nil {
				host.logger.Warn("loader: rollback unload failed",
					"plugin", pluginID, "error", unloadErr)
			}
			host.EmitPluginLoadFailed(pluginID, err.Error())
			continue
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

	return subprocess.NewSubprocessPlugin(dp.Dir, m.Identifier(), config, mgrCfg), nil
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
			host.EmitPluginLoadFailed(id, err.Error())
			continue
		}

		// If this builtin ships an embedded plugin.yaml via ManifestProvider,
		// apply the same yaml-authoritative registrations so builtin and
		// discovered plugins share the wiring path (B.4).
		if mp, ok := p.(ManifestProvider); ok {
			if manifest := mp.Manifest(); manifest != nil {
				// Builtin plugins have no on-disk plugin dir — pass "" so
				// envelope schema loading is a no-op. Builtins that want
				// strict envelope validation can embed schemas via
				// RegisterPluginEnvelopeSchema during their Load.
				if err := applyManifestRegistrations(host, manifest, p, ""); err != nil {
					errs = append(errs, fmt.Errorf("apply manifest for builtin %s: %w", id, err))
					// Best-effort rollback: remove manifest side-map + partial
					// registrations so load_failed accurately reflects the
					// absence of this plugin (see LoadDiscovered for rationale).
					if unloadErr := host.UnloadPlugin(id); unloadErr != nil {
						host.logger.Warn("loader: rollback unload failed (builtin)",
							"plugin", id, "error", unloadErr)
					}
					host.EmitPluginLoadFailed(id, err.Error())
					continue
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
	// Build index: canonical plugin id → DiscoveredPlugin.
	byName := make(map[string]int, len(plugins))
	for i, p := range plugins {
		byName[p.Manifest.Identifier()] = i
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
				cycled = append(cycled, plugins[i].Manifest.Identifier())
			}
		}
		return nil, fmt.Errorf("dependency cycle detected among plugins: %v", cycled)
	}

	return sorted, nil
}
