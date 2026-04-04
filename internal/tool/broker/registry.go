package broker

import (
	"sync"

	"github.com/hollis-labs/nanite/internal/tool"
)

// Registry is a thread-safe tool registry. The broker resolves tools from here.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]tool.Tool // keyed by tool name
	order []string             // insertion order for deterministic iteration
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]tool.Tool),
	}
}

// Register adds a tool. If a tool with the same name exists, it is replaced.
func (r *Registry) Register(t tool.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := t.Name()
	if _, exists := r.tools[name]; !exists {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
}

// RegisterAll adds multiple tools.
func (r *Registry) RegisterAll(tools []tool.Tool) {
	for _, t := range tools {
		r.Register(t)
	}
}

// Get returns a tool by name, or nil if not found.
func (r *Registry) Get(name string) tool.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tools[name]
}

// GetByNames returns tools matching the given names (in order).
// Missing names are silently skipped.
func (r *Registry) GetByNames(names []string) []tool.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []tool.Tool
	for _, name := range names {
		if t, ok := r.tools[name]; ok {
			result = append(result, t)
		}
	}
	return result
}

// All returns all registered tools in insertion order.
func (r *Registry) All() []tool.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]tool.Tool, 0, len(r.order))
	for _, name := range r.order {
		if t, ok := r.tools[name]; ok {
			result = append(result, t)
		}
	}
	return result
}

// ByCategory returns tools matching the given category.
func (r *Registry) ByCategory(category string) []tool.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []tool.Tool
	for _, name := range r.order {
		if t, ok := r.tools[name]; ok && t.Category() == category {
			result = append(result, t)
		}
	}
	return result
}

// ByTag returns tools that have the given tag.
func (r *Registry) ByTag(tag string) []tool.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []tool.Tool
	for _, name := range r.order {
		t, ok := r.tools[name]
		if !ok {
			continue
		}
		for _, tt := range t.Tags() {
			if tt == tag {
				result = append(result, t)
				break
			}
		}
	}
	return result
}

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}
