package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	fplugin "github.com/hollis-labs/fragments-engine/plugin"
)

// DiscoveredPlugin holds metadata parsed from a plugin.yaml plus the
// constructor looked up from the registry.
type DiscoveredPlugin struct {
	Manifest    *PluginManifest
	Dir         string
	Constructor PluginConstructor
}

// DiscoverPlugins scans the given directory for subdirectories containing
// plugin.yaml, parses each manifest, and looks up the registered constructor.
// Returns only plugins that have a matching constructor in the registry.
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

		constructor, ok := LookupConstructor(manifest.Name)
		if !ok {
			// Plugin directory exists but no Go code registered — skip with a warning.
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
// respecting dependency order.  Returns the list of successfully loaded plugins.
func LoadDiscovered(host *Host, discovered []DiscoveredPlugin) ([]fplugin.Plugin, []error) {
	// Sort by dependencies: plugins with no deps first.
	sorted := sortByDeps(discovered)

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

		// Instantiate and load.
		p := dp.Constructor()
		if err := host.LoadPlugin(p); err != nil {
			errs = append(errs, fmt.Errorf("load %s: %w", dp.Manifest.Name, err))
			continue
		}
		loaded = append(loaded, p)
	}

	return loaded, errs
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
		loaded = append(loaded, p)
	}

	return loaded, errs
}

// sortByDeps performs a simple topological sort: plugins with no dependencies
// come first.  For this initial implementation we do a single-pass stable sort
// by dependency count which is sufficient when dep chains are shallow.
func sortByDeps(plugins []DiscoveredPlugin) []DiscoveredPlugin {
	out := make([]DiscoveredPlugin, len(plugins))
	copy(out, plugins)
	sort.SliceStable(out, func(i, j int) bool {
		return len(out[i].Manifest.Dependencies) < len(out[j].Manifest.Dependencies)
	})
	return out
}
