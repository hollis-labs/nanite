package plugin

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/hollis-labs/conduit/internal/store"
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
	connectors   map[string]plugin.Connector
	services     map[string]interface{}
	configs      map[string]*PluginConfig // per-plugin config, keyed by plugin ID
	activePlugin string                   // ID of the plugin currently being loaded
	store        *store.Store             // DB-backed plugin settings (nil if unavailable)
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
		connectors:   make(map[string]plugin.Connector),
		services:     make(map[string]interface{}),
		configs:      make(map[string]*PluginConfig),
		router:       router,
		logger:       logger,
		ctx:          ctx,
		ctxCancel:    cancel,
	}
}

// NewHostWithStore creates a minimal plugin host with just a store service.
// Used by CLI commands (e.g. conduit plugin uninstall) that need to run
// plugin lifecycle methods without a full server.
func NewHostWithStore(store interface{}) *Host {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Host{
		plugins:      make(map[string]plugin.Plugin),
		eventHooks:   make(map[string][]plugin.EventHook),
		crudHandlers: make(map[string]plugin.CRUDHandler),
		uiComponents: []plugin.UIComponent{},
		connectors:   make(map[string]plugin.Connector),
		services:     make(map[string]interface{}),
		configs:      make(map[string]*PluginConfig),
		router:       http.NewServeMux(),
		logger:       NewLogger("plugin-cli"),
		ctx:          ctx,
		ctxCancel:    cancel,
	}
	h.services["store"] = store
	return h
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

	_, alreadyRegistered := h.crudHandlers[resourceType]
	h.crudHandlers[resourceType] = handler

	// Only wire HTTP routes the first time — ServeMux doesn't support re-registration,
	// but the handler closures read from h.crudHandlers so they'll pick up the new handler.
	if alreadyRegistered {
		return nil
	}

	// Wire HTTP routes for this resource type
	basePath := fmt.Sprintf("/api/plugins/%s", resourceType)

	// List resources: GET /api/plugins/{resourceType}
	h.router.HandleFunc(fmt.Sprintf("GET %s", basePath), func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		h2 := h.crudHandlers[resourceType]
		h.mu.RUnlock()
		h.handleCRUDList(w, r, h2)
	})

	// Create resource: POST /api/plugins/{resourceType}
	h.router.HandleFunc(fmt.Sprintf("POST %s", basePath), func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		h2 := h.crudHandlers[resourceType]
		h.mu.RUnlock()
		h.handleCRUDCreate(w, r, h2)
	})

	// Get resource: GET /api/plugins/{resourceType}/{id}
	h.router.HandleFunc(fmt.Sprintf("GET %s/{id}", basePath), func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		h2 := h.crudHandlers[resourceType]
		h.mu.RUnlock()
		h.handleCRUDRead(w, r, h2)
	})

	// Update resource: PUT /api/plugins/{resourceType}/{id}
	h.router.HandleFunc(fmt.Sprintf("PUT %s/{id}", basePath), func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		h2 := h.crudHandlers[resourceType]
		h.mu.RUnlock()
		h.handleCRUDUpdate(w, r, h2)
	})

	// Delete resource: DELETE /api/plugins/{resourceType}/{id}
	h.router.HandleFunc(fmt.Sprintf("DELETE %s/{id}", basePath), func(w http.ResponseWriter, r *http.Request) {
		h.mu.RLock()
		h2 := h.crudHandlers[resourceType]
		h.mu.RUnlock()
		h.handleCRUDDelete(w, r, h2)
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

	// Replace existing component with same ID, or append.
	alreadyRegistered := false
	for i, existing := range h.uiComponents {
		if existing.ID == component.ID {
			h.uiComponents[i] = component
			alreadyRegistered = true
			break
		}
	}
	if !alreadyRegistered {
		h.uiComponents = append(h.uiComponents, component)
	}

	// If the component has a server-side handler, register the route (only once).
	if component.Handler != nil && !alreadyRegistered {
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

// SetStore sets the database store for DB-backed plugin settings.
func (h *Host) SetStore(s *store.Store) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.store = s
}

// GetConfig returns a configuration value for the currently-loading plugin.
// Resolution order: env var -> DB setting -> config file override -> default from schema.
func (h *Host) GetConfig(key string) (string, error) {
	h.mu.RLock()
	id := h.activePlugin
	cfg := h.configs[id]
	s := h.store
	h.mu.RUnlock()

	// Try DB first (if store is available).
	if s != nil && id != "" {
		if val, err := s.GetPluginSettingValue(id, key); err == nil && val != "" {
			return val, nil
		}
	}

	// Fall back to file-based config.
	if cfg == nil {
		return "", fmt.Errorf("no config loaded for plugin %q", id)
	}
	return cfg.Get(key)
}

// SetConfig persists a configuration value for the currently-loading plugin.
func (h *Host) SetConfig(key string, value string) error {
	h.mu.RLock()
	id := h.activePlugin
	s := h.store
	h.mu.RUnlock()

	if id == "" {
		return fmt.Errorf("no active plugin context for SetConfig")
	}
	if s == nil {
		return fmt.Errorf("no store available for plugin config persistence")
	}

	// Read existing settings, merge, write back.
	existing, err := s.GetPluginSettings(id)
	settings := make(map[string]any)
	if err == nil && existing != nil {
		settings = existing.Settings
	}
	if settings == nil {
		settings = make(map[string]any)
	}
	settings[key] = value
	return s.UpsertPluginSettings(id, settings)
}

// RegisterConfigSchema registers config field definitions for the currently-loading plugin.
func (h *Host) RegisterConfigSchema(fields []plugin.ConfigFieldDef) error {
	h.mu.RLock()
	id := h.activePlugin
	s := h.store
	h.mu.RUnlock()

	if id == "" {
		return fmt.Errorf("no active plugin context for RegisterConfigSchema")
	}
	if s == nil {
		return fmt.Errorf("no store available for plugin schema persistence")
	}

	// Convert SDK type to store type.
	storeFields := make([]store.ConfigField, len(fields))
	for i, f := range fields {
		storeFields[i] = store.ConfigField{
			Key:         f.Key,
			Type:        f.Type,
			Label:       f.Label,
			Description: f.Description,
			Default:     f.Default,
			Required:    f.Required,
			Options:     f.Options,
			Component:   f.Component,
		}
	}
	return s.UpsertPluginSchema(id, storeFields)
}

// RegisterConnector registers a named connector for outbound integrations.
func (h *Host) RegisterConnector(name string, connector plugin.Connector) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connectors[name] = connector
	h.logger.Info("registered connector", "name", name)
	return nil
}

// GetConnector retrieves a registered connector by name.
func (h *Host) GetConnector(name string) (plugin.Connector, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.connectors[name]
	return c, ok
}

// ListConnectors returns all registered connector names.
func (h *Host) ListConnectors() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	names := make([]string, 0, len(h.connectors))
	for name := range h.connectors {
		names = append(names, name)
	}
	return names
}

// RegisterProvider registers a runtime LLM provider from a plugin.
// The provider must implement the provider.Provider interface; the host
// registers it with the provider registry via the "provider-registry" service.
func (h *Host) RegisterProvider(name string, prov interface{}) error {
	h.mu.RLock()
	regSvc, exists := h.services["provider-registry"]
	h.mu.RUnlock()

	if !exists {
		return fmt.Errorf("provider registry service not available")
	}

	// The provider registry exposes a Register(name, provider) method.
	type registerer interface {
		Register(name string, p interface{})
	}
	reg, ok := regSvc.(registerer)
	if !ok {
		return fmt.Errorf("provider registry does not support Register")
	}
	reg.Register(name, prov)
	h.logger.Info("registered plugin provider", "name", name)
	return nil
}

// RegisterCLIAdapter registers a runtime CLI adapter from a plugin.
// The adapter must implement the CLIAdapter interface; the host stores it
// for the bridge layer to discover.
func (h *Host) RegisterCLIAdapter(name string, adapter interface{}) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Store as a service so the bridge layer can discover plugin-registered adapters.
	h.services["cli-adapter:"+name] = adapter
	h.logger.Info("registered plugin CLI adapter", "name", name)
	return nil
}

// PlaceArtifact creates an artifact with origin="placed" for the calling plugin.
// This is used when a plugin wants to deliberately surface a file to the user
// (reports, exports, generated content).
func (h *Host) PlaceArtifact(sessionID, messageID, name, mimeType, storagePath string) error {
	h.mu.RLock()
	pluginID := h.activePlugin
	s := h.store
	h.mu.RUnlock()

	if s == nil {
		return fmt.Errorf("no store available for artifact creation")
	}

	artifact := &store.Artifact{
		SessionID:      sessionID,
		MessageID:      messageID,
		Name:           name,
		MimeType:       mimeType,
		StoragePath:    storagePath,
		Origin:         store.ArtifactOriginPlaced,
		SourcePluginID: pluginID,
	}
	return s.CreateArtifact(artifact)
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