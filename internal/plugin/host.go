package plugin

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/hollis-labs/fragments-engine/plugin"
)

// Host implements the plugin.Host interface for Conduit.
// It provides the runtime environment and services for plugins.
type Host struct {
	mu           sync.RWMutex
	plugins      map[string]plugin.Plugin
	eventHooks   map[string][]plugin.EventHook
	crudHandlers map[string]plugin.CRUDHandler
	uiComponents []plugin.UIComponent
	services     map[string]interface{}
	configs      map[string]*PluginConfig // per-plugin config, keyed by plugin ID
	activePlugin string                   // ID of the plugin currently being loaded
	router       *http.ServeMux
	logger       plugin.Logger
	ctx          context.Context
	ctxCancel    context.CancelFunc
}

// NewHost creates a new plugin host for Conduit.
func NewHost(router *http.ServeMux, logger plugin.Logger) *Host {
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{
		plugins:      make(map[string]plugin.Plugin),
		eventHooks:   make(map[string][]plugin.EventHook),
		crudHandlers: make(map[string]plugin.CRUDHandler),
		uiComponents: []plugin.UIComponent{},
		services:     make(map[string]interface{}),
		configs:      make(map[string]*PluginConfig),
		router:       router,
		logger:       logger,
		ctx:          ctx,
		ctxCancel:    cancel,
	}
}

// SetRouter sets the HTTP router for the plugin host.
func (h *Host) SetRouter(router *http.ServeMux) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.router = router
}

// GetPlugin retrieves another loaded plugin by ID.
func (h *Host) GetPlugin(id string) (plugin.Plugin, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, exists := h.plugins[id]
	return p, exists
}

// RegisterCRUDHandler registers a CRUD handler for a resource type.
// This wires the handler into the HTTP router at /api/plugins/{resourceType}/*.
func (h *Host) RegisterCRUDHandler(resourceType string, handler plugin.CRUDHandler) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.crudHandlers[resourceType]; exists {
		return fmt.Errorf("CRUD handler for resource type %q already registered", resourceType)
	}

	h.crudHandlers[resourceType] = handler

	// Wire HTTP routes for this resource type
	basePath := fmt.Sprintf("/api/plugins/%s", resourceType)

	// List resources: GET /api/plugins/{resourceType}
	h.router.HandleFunc(fmt.Sprintf("GET %s", basePath), func(w http.ResponseWriter, r *http.Request) {
		h.handleCRUDList(w, r, handler)
	})

	// Create resource: POST /api/plugins/{resourceType}
	h.router.HandleFunc(fmt.Sprintf("POST %s", basePath), func(w http.ResponseWriter, r *http.Request) {
		h.handleCRUDCreate(w, r, handler)
	})

	// Get resource: GET /api/plugins/{resourceType}/{id}
	h.router.HandleFunc(fmt.Sprintf("GET %s/{id}", basePath), func(w http.ResponseWriter, r *http.Request) {
		h.handleCRUDRead(w, r, handler)
	})

	// Update resource: PUT /api/plugins/{resourceType}/{id}
	h.router.HandleFunc(fmt.Sprintf("PUT %s/{id}", basePath), func(w http.ResponseWriter, r *http.Request) {
		h.handleCRUDUpdate(w, r, handler)
	})

	// Delete resource: DELETE /api/plugins/{resourceType}/{id}
	h.router.HandleFunc(fmt.Sprintf("DELETE %s/{id}", basePath), func(w http.ResponseWriter, r *http.Request) {
		h.handleCRUDDelete(w, r, handler)
	})

	h.logger.Info("registered CRUD handler", "resourceType", resourceType, "basePath", basePath)
	return nil
}

// RegisterEventHook registers an event hook for specific event types.
func (h *Host) RegisterEventHook(eventTypes []string, hook plugin.EventHook) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, eventType := range eventTypes {
		h.eventHooks[eventType] = append(h.eventHooks[eventType], hook)
	}

	h.logger.Info("registered event hook", "eventTypes", eventTypes, "hookTypes", hook.EventTypes())
	return nil
}

// RegisterUIComponent registers UI components for the frontend.
func (h *Host) RegisterUIComponent(component plugin.UIComponent) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Check for duplicate IDs
	for _, existing := range h.uiComponents {
		if existing.ID == component.ID {
			return fmt.Errorf("UI component with ID %q already registered", component.ID)
		}
	}

	h.uiComponents = append(h.uiComponents, component)

	// If the component has a server-side handler, register it
	if component.Handler != nil {
		path := fmt.Sprintf("/api/plugins/ui/%s", component.ID)
		h.router.Handle(path, component.Handler)
		h.logger.Info("registered UI component handler", "id", component.ID, "path", path)
	}

	h.logger.Info("registered UI component", "id", component.ID, "type", component.Type)
	return nil
}

// GetService provides access to core services (MCP clients, databases, etc.).
func (h *Host) GetService(name string) (interface{}, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	service, exists := h.services[name]
	if !exists {
		return nil, fmt.Errorf("service %q not found", name)
	}

	return service, nil
}

// RegisterService registers a core service for plugin access.
func (h *Host) RegisterService(name string, service interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.services[name] = service
	h.logger.Info("registered service", "name", name)
}

// Logger provides a logger instance for the plugin.
func (h *Host) Logger() plugin.Logger {
	return h.logger
}

// Context returns the plugin's execution context.
func (h *Host) Context() context.Context {
	return h.ctx
}

// SetPluginConfig stores configuration for a plugin, typically called before
// loading the plugin so that GetConfig works during Load().
func (h *Host) SetPluginConfig(pluginID string, cfg *PluginConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.configs[pluginID] = cfg
}

// GetConfig returns a configuration value for the currently-loading plugin.
// Resolution order: env var -> config file override -> default from schema.
func (h *Host) GetConfig(key string) (string, error) {
	h.mu.RLock()
	id := h.activePlugin
	cfg := h.configs[id]
	h.mu.RUnlock()

	if cfg == nil {
		return "", fmt.Errorf("no config loaded for plugin %q", id)
	}
	return cfg.Get(key)
}

// LoadPlugin loads a plugin into the host.
func (h *Host) LoadPlugin(p plugin.Plugin) error {
	id := p.ID()

	// Pre-checks under lock.
	h.mu.Lock()
	if _, exists := h.plugins[id]; exists {
		h.mu.Unlock()
		return fmt.Errorf("plugin %q already loaded", id)
	}
	for _, dep := range p.Dependencies() {
		if _, exists := h.plugins[dep]; !exists {
			h.mu.Unlock()
			return fmt.Errorf("plugin %q depends on %q which is not loaded", id, dep)
		}
	}
	h.mu.Unlock()

	// Set active plugin so GetConfig knows which plugin is calling.
	h.mu.Lock()
	h.activePlugin = id
	h.mu.Unlock()

	// Load the plugin WITHOUT holding the lock — Load calls back into
	// RegisterCRUDHandler / RegisterUIComponent / etc. which acquire h.mu.
	if err := p.Load(h); err != nil {
		return fmt.Errorf("failed to load plugin %q: %w", id, err)
	}

	// Store the loaded plugin.
	h.mu.Lock()
	h.plugins[id] = p
	h.mu.Unlock()

	h.logger.Info("loaded plugin", "id", id, "name", p.Name(), "version", p.Version())
	return nil
}

// UnloadPlugin unloads a plugin from the host.
func (h *Host) UnloadPlugin(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	p, exists := h.plugins[id]
	if !exists {
		return fmt.Errorf("plugin %q not found", id)
	}

	// Check if any other plugins depend on this one
	for _, other := range h.plugins {
		if other.ID() == id {
			continue
		}
		for _, dep := range other.Dependencies() {
			if dep == id {
				return fmt.Errorf("cannot unload plugin %q: plugin %q depends on it", id, other.ID())
			}
		}
	}

	if err := p.Unload(); err != nil {
		return fmt.Errorf("failed to unload plugin %q: %w", id, err)
	}

	delete(h.plugins, id)
	h.logger.Info("unloaded plugin", "id", id)
	return nil
}

// EmitEvent emits an event to all registered hooks.
func (h *Host) EmitEvent(event plugin.Event) {
	h.mu.RLock()
	hooks, exists := h.eventHooks[event.Type]
	h.mu.RUnlock()

	if !exists {
		return // No hooks for this event type
	}

	// Process hooks concurrently
	var wg sync.WaitGroup
	for _, hook := range hooks {
		wg.Add(1)
		go func(hook plugin.EventHook) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
			defer cancel()

			if err := hook.Handle(ctx, event); err != nil {
				h.logger.Error("event hook failed", "eventType", event.Type, "error", err)
			}
		}(hook)
	}
	wg.Wait()
}

// GetUIComponents returns all registered UI components.
func (h *Host) GetUIComponents() []plugin.UIComponent {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Return a copy to prevent modification
	components := make([]plugin.UIComponent, len(h.uiComponents))
	copy(components, h.uiComponents)
	return components
}

// GetCRUDHandlers returns all registered CRUD handlers.
func (h *Host) GetCRUDHandlers() map[string]plugin.CRUDHandler {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Return a copy to prevent modification
	handlers := make(map[string]plugin.CRUDHandler)
	for k, v := range h.crudHandlers {
		handlers[k] = v
	}
	return handlers
}

// ListPlugins returns all loaded plugins.
func (h *Host) ListPlugins() []plugin.Plugin {
	h.mu.RLock()
	defer h.mu.RUnlock()

	plugins := make([]plugin.Plugin, 0, len(h.plugins))
	for _, p := range h.plugins {
		plugins = append(plugins, p)
	}
	return plugins
}

// Shutdown gracefully shuts down the plugin host and all loaded plugins.
func (h *Host) Shutdown() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	var errors []string
	for id, p := range h.plugins {
		if err := p.Unload(); err != nil {
			errors = append(errors, fmt.Sprintf("failed to unload plugin %q: %v", id, err))
		}
	}

	h.ctxCancel()

	if len(errors) > 0 {
		return fmt.Errorf("shutdown errors: %v", errors)
	}

	h.logger.Info("plugin host shutdown complete")
	return nil
}