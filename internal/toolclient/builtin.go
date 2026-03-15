package toolclient

import (
	"sync"

	"github.com/hollis-labs/conduit/internal/provider"
)

// BuiltinToolRegistry holds built-in tool definitions that are always available
// regardless of MCP server status. Built-in tools are not subject to progressive
// discovery limits.
type BuiltinToolRegistry struct {
	mu    sync.RWMutex
	tools map[string][]provider.ToolDefinition // category -> tools
}

// NewBuiltinToolRegistry creates an empty BuiltinToolRegistry.
func NewBuiltinToolRegistry() *BuiltinToolRegistry {
	return &BuiltinToolRegistry{
		tools: make(map[string][]provider.ToolDefinition),
	}
}

// RegisterBuiltins registers a set of tool definitions under the given category.
// Supported categories: "dev", "general".
func (r *BuiltinToolRegistry) RegisterBuiltins(category string, tools []provider.ToolDefinition) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[category] = tools
}

// GetBuiltins returns all registered built-in tools across all categories.
func (r *BuiltinToolRegistry) GetBuiltins() []provider.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var all []provider.ToolDefinition
	for _, tools := range r.tools {
		all = append(all, tools...)
	}
	return all
}

// GetBuiltinsByCategory returns built-in tools for a specific category.
// Returns nil if the category is not registered.
func (r *BuiltinToolRegistry) GetBuiltinsByCategory(category string) []provider.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[category]
}

// Count returns the total number of registered built-in tools.
func (r *BuiltinToolRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	n := 0
	for _, tools := range r.tools {
		n += len(tools)
	}
	return n
}
