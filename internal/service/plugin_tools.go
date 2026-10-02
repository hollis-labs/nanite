package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/plugin-sdk/manifest"
)

// PluginToolRegistrar keeps manifest tools on the existing MCP/agent roster
// surface. Availability persists; grants survive unload and upgrades.
type PluginToolRegistrar struct {
	manager *mcp.Manager
	store   *store.Store
}

func NewPluginToolRegistrar(manager *mcp.Manager, st *store.Store) *PluginToolRegistrar {
	return &PluginToolRegistrar{manager: manager, store: st}
}
func (r *PluginToolRegistrar) AddPluginServer(owner, name string, transport *subprocess.Transport) error {
	return r.manager.AddPluginServer(owner, name, transport)
}
func (r *PluginToolRegistrar) AddPluginTools(owner string, tools []manifest.Tool, loadType string, child *subprocess.SubprocessPlugin, consumer subprocess.EnvelopeConsumer) error {
	if err := r.manager.AddPluginTools(owner, tools, loadType, child, consumer); err != nil {
		return err
	}
	for _, tool := range r.manager.PluginToolDefinitions(owner) {
		if _, err := r.store.UpsertKnownTool(context.Background(), tool.Name, "mcp", "available", tool.Description); err != nil {
			return fmt.Errorf("persist plugin tool availability: %w", err)
		}
	}
	return nil
}
func (r *PluginToolRegistrar) RemoveServersByPlugin(owner string) int {
	tools := r.manager.PluginToolDefinitions(owner)
	removed := r.manager.RemoveServersByPlugin(owner)
	for _, tool := range tools {
		if _, err := r.store.UpsertKnownTool(context.Background(), tool.Name, "mcp", "unavailable", tool.Description); err != nil {
			slog.Warn("plugin tool availability update failed", "tool", tool.Name, "err", err)
		}
	}
	return removed
}
