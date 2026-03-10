package builders

import (
	"fmt"
	"sort"
	"sync"

	"github.com/hollis-labs/mentat-chat/internal/store"
)

// Registry holds named builders and provides lookup and enumeration.
type Registry struct {
	mu       sync.RWMutex
	builders map[string]*Builder
	store    *store.Store
}

// NewRegistry creates a new empty builder registry backed by the given store.
func NewRegistry(s *store.Store) *Registry {
	return &Registry{
		builders: make(map[string]*Builder),
		store:    s,
	}
}

// Register adds a builder to the registry. Returns an error if a builder
// with the same name is already registered.
func (r *Registry) Register(b *Builder) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.builders[b.Name]; exists {
		return fmt.Errorf("builder %q already registered", b.Name)
	}
	r.builders[b.Name] = b
	return nil
}

// Get returns the builder with the given name, or nil if not found.
func (r *Registry) Get(name string) *Builder {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.builders[name]
}

// Store returns the store backing this registry.
func (r *Registry) Store() *store.Store {
	return r.store
}

// ListBuilders returns the names of all registered builders, sorted alphabetically.
func (r *Registry) ListBuilders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.builders))
	for name := range r.builders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DefaultRegistry returns a registry pre-loaded with the standard builders.
func DefaultRegistry(s *store.Store) *Registry {
	r := NewRegistry(s)
	r.Register(NewAgentBuilder(s))
	r.Register(NewSkillBuilder(s))
	r.Register(NewPromptTemplateBuilder(s))
	return r
}
