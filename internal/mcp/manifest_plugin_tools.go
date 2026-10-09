package mcp

import (
	"context"
	"fmt"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
)

// AddPluginTools installs manifest-authoritative tools immediately, including
// hot loads after initial discovery. All declarations are validated before any
// registry mutation. Collisions refuse loading rather than replacing authority.
func (m *Manager) AddPluginTools(pluginID string, declarations []manifest.Tool, loadType string, child *subprocess.SubprocessPlugin, consumer subprocess.EnvelopeConsumer) error {
	if pluginID == "" || child == nil || child.ID() != pluginID {
		return fmt.Errorf("plugin tools require an owner and child")
	}
	if loadType != "" && loadType != "auto" && loadType != "opt-in" {
		return fmt.Errorf("invalid plugin tool load type")
	}
	transport, err := newManifestPluginTransport(child, declarations, consumer)
	if err != nil {
		return err
	}
	tools, err := transport.ListTools(context.Background())
	if err != nil {
		return err
	}
	server := "plugin_" + pluginID
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.servers[server]; exists {
		return fmt.Errorf("plugin tool namespace already registered")
	}
	for _, tool := range tools {
		if _, exists := m.uniformIndex[tool.Name]; exists {
			return fmt.Errorf("plugin tool %q conflicts with an existing owner", tool.Name)
		}
	}
	m.servers[server] = transport
	m.serverTiers[server] = TierPluginStdio
	m.pluginServers[pluginID] = append(m.pluginServers[pluginID], server)
	if m.pluginToolLoadTypes == nil {
		m.pluginToolLoadTypes = make(map[string]string)
	}
	m.pluginToolLoadTypes[server] = loadType
	for _, tool := range tools {
		entry := &toolEntry{serverName: server, uniformName: tool.Name, tool: tool}
		m.tools = append(m.tools, entry)
		m.uniformIndex[tool.Name] = entry
	}
	return nil
}

// PluginToolDefinitions returns owned metadata for durable availability sync.
// Hidden opt-in tools remain in the known catalog so users can grant them.
func (m *Manager) PluginToolDefinitions(pluginID string) []Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	servers := make(map[string]bool)
	for _, name := range m.pluginServers[pluginID] {
		servers[name] = true
	}
	var tools []Tool
	for _, entry := range m.tools {
		if servers[entry.serverName] {
			tools = append(tools, Tool{Name: entry.uniformName, Description: entry.tool.Description})
		}
	}
	return tools
}

// SetToolLoadPreferences atomically replaces the user layer. The input is
// copied so subsequent settings mutations cannot change live filtering.
func (m *Manager) SetToolLoadPreferences(preferences map[string]string) {
	snapshot := make(map[string]string, len(preferences))
	for name, value := range preferences {
		if value == "auto" || value == "opt-in" {
			snapshot[name] = value
		}
	}
	m.toolLoadPreferences.Store(snapshot)
}

func (m *Manager) toolLoadTypeLocked(name string) (value, source string) {
	if snapshot := m.toolLoadPreferences.Load(); snapshot != nil {
		if value := snapshot.(map[string]string)[name]; value != "" {
			return value, "user"
		}
	}
	if entry := m.uniformIndex[name]; entry != nil {
		if value := m.pluginToolLoadTypes[entry.serverName]; value != "" {
			return value, "manifest"
		}
	}
	return "auto", "default"
}

// ToolLoadType is the same resolution used by discovery and execution.
func (m *Manager) ToolLoadType(name string) (value, source string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.toolLoadTypeLocked(name)
}
