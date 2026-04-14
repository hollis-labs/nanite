package plugin

import (
	"fmt"
	"sync"

	fplugin "github.com/hollis-labs/plugin-sdk"
)

// PluginConstructor creates a new plugin instance.
type PluginConstructor func() fplugin.Plugin

// registry is the global plugin registry.
var (
	registryMu   sync.RWMutex
	pluginRegistry = map[string]PluginConstructor{}
)

// RegisterPlugin registers a plugin constructor by ID.
// Plugins call this (typically from init()) to make themselves discoverable.
func RegisterPlugin(id string, constructor PluginConstructor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := pluginRegistry[id]; exists {
		panic(fmt.Sprintf("plugin %q already registered", id))
	}
	pluginRegistry[id] = constructor
}

// GetRegistered returns a copy of all registered plugin constructors.
func GetRegistered() map[string]PluginConstructor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make(map[string]PluginConstructor, len(pluginRegistry))
	for k, v := range pluginRegistry {
		out[k] = v
	}
	return out
}

// UnregisterPluginForTest removes a plugin constructor from the registry.
// Intended for test cleanup only (see t.Cleanup usage in package tests).
// The registry intentionally lacks a production Unregister to avoid
// accidental removal of compiled-in plugins.
func UnregisterPluginForTest(id string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(pluginRegistry, id)
}

// LookupConstructor returns the constructor for a plugin ID, if registered.
func LookupConstructor(id string) (PluginConstructor, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	c, ok := pluginRegistry[id]
	return c, ok
}
