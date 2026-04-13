package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/secrets"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/go-plugin"
)

// validComponentID matches alphanumeric + hyphens, 2-64 chars, no leading/trailing hyphens.
var validComponentID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)

// validComponentTypes is the set of allowed UIComponentType values.
var validComponentTypes = map[plugin.UIComponentType]bool{
	plugin.UIComponentTypeWidget:   true,
	plugin.UIComponentTypeEnvelope: true,
	plugin.UIComponentTypeAction:   true,
	plugin.UIComponentTypeWorkflow: true,
	plugin.UIComponentTypeView:     true,
}

// CommandRegistrar is the interface for registering slash commands into a
// unified registry. Implemented by chat.CommandRegistry. Defined here to
// avoid importing the chat package.
//
// RemoveByPlugin drops every command whose Source equals pluginID and returns
// the number removed. Plugin-registered commands carry their owning pluginID
// in SlashCommand.Source (set by RegisterPluginCommand), so the registry can
// perform the sweep without an extra side map. Builtin/skill/file commands
// use reserved Source values ("builtin", "skill", "file") and are unaffected.
type CommandRegistrar interface {
	RegisterPluginCommand(cmd SlashCommandDef, source string)
	RemoveByPlugin(pluginID string) int
}

// MCPRegistrar is the interface for registering MCP servers backed by a
// subprocess plugin's JSON-RPC transport. Implemented by *mcp.Manager and
// declared here so the plugin host can wire subprocess plugin MCP servers
// during yaml-driven registration without importing internal/mcp (which
// would create a cycle via internal/service/install).
type MCPRegistrar interface {
	AddPluginServer(pluginID, name string, transport *subprocess.Transport) error
	RemoveServersByPlugin(pluginID string) int
}

// pendingRoute is an HTTP route registration deferred until the router is available.
type pendingRoute struct {
	pattern  string
	handler  http.Handler
	pluginID string
}

// ConnectorStatus tracks the health state of a registered connector.
type ConnectorStatus struct {
	Name               string    `json:"name"`
	PluginID           string    `json:"plugin_id"`
	Healthy            bool      `json:"healthy"`
	LastCheckAt        time.Time `json:"last_check_at,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
	LastErrorAt        time.Time `json:"last_error_at,omitempty"`
	ConsecutiveFailures int      `json:"consecutive_failures"`
}

// Host implements the plugin.Host interface for Nanite.
// It provides the runtime environment and services for plugins.
// eventHookEntry wraps a registered EventHook with the plugin ID that owns it
// so UnloadPlugin can sweep plugin-scoped hooks without requiring the external
// go-plugin.EventHook interface to expose owner information. Core (non-plugin)
// event hook registrations leave pluginID == "" and are never swept.
type eventHookEntry struct {
	hook     plugin.EventHook
	pluginID string
}

// crudHandlerEntry wraps a CRUDHandler with its owning plugin ID. Same
// rationale as eventHookEntry — keep plugin-ownership on the host side because
// go-plugin.CRUDHandler is plugin-agnostic.
type crudHandlerEntry struct {
	handler  plugin.CRUDHandler
	pluginID string
}

type Host struct {
	mu            sync.RWMutex
	plugins       map[string]plugin.Plugin
	eventHooks    map[string][]eventHookEntry
	crudHandlers  map[string]crudHandlerEntry
	uiComponents  []plugin.UIComponent
	uiOwners      map[string]string // component ID → plugin ID that registered it
	connectors    map[string]plugin.Connector
	connectorOwners  map[string]string          // connector name → plugin ID
	connectorHealth  map[string]*ConnectorStatus // connector name → health status
	commands      CommandRegistrar // unified command registry (nil-safe)
	mcpRegistrar  MCPRegistrar     // MCP server registrar (nil-safe; set via SetMCPRegistrar)
	keybindings   map[string]KeybindingDef   // keybinding ID → definition
	kbOwners      map[string]string                 // keybinding ID → plugin ID
	slots         map[UISlotName][]UISlotEntry // slot name → entries, sorted by priority
	services      map[string]interface{}
	serviceOwners map[string]string // service name → plugin ID (only non-core, plugin-registered services)
	// B.6b host-side owner maps for categories whose underlying registry cannot
	// carry plugin-ID context (interfaces live in external/pinned modules, or
	// are plugin-agnostic by design — task.Service, store.Store). Event hooks
	// and CRUD handlers carry their owner inline via eventHookEntry /
	// crudHandlerEntry; see those types above.
	taskBackendOwners  map[string]string   // task backend name → plugin ID
	providerOwners     map[string]string   // provider name → plugin ID (forward-compat; see providerUnregistrar)
	configSchemaOwners map[string]struct{} // plugin IDs with a persisted config schema (clear on unload)
	configs       map[string]*PluginConfig // per-plugin config, keyed by plugin ID
	activePlugin  string                   // ID of the plugin currently being loaded
	store         *store.Store             // DB-backed plugin settings (nil if unavailable)
	router        *http.ServeMux
	pluginMux     *MutablePluginMux // mutable wrapper that owns all plugin-registered routes
	routePatterns map[string]bool   // patterns already wired on core router (forwarder installed)
	pendingRoutes []pendingRoute // routes queued before router was set
	triggers      *TriggerDispatcher // event → connector dispatch
	filters       *FilterRegistry    // named filter chains
	eventSubs     []chan plugin.Event // SSE subscribers for event streaming
	envelopes     map[string]EnvelopeRegistryEntry // envelope type → registry entry (B.4)
	logger        plugin.Logger
	ctx           context.Context
	ctxCancel     context.CancelFunc
}

// NewHost creates a new plugin host for Nanite.
func NewHost(router *http.ServeMux, logger plugin.Logger) *Host {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Host{
		plugins:            make(map[string]plugin.Plugin),
		eventHooks:         make(map[string][]eventHookEntry),
		crudHandlers:       make(map[string]crudHandlerEntry),
		uiComponents:       []plugin.UIComponent{},
		uiOwners:           make(map[string]string),
		connectors:         make(map[string]plugin.Connector),
		connectorOwners:    make(map[string]string),
		connectorHealth:    make(map[string]*ConnectorStatus),
		keybindings:        make(map[string]KeybindingDef),
		kbOwners:           make(map[string]string),
		slots:              make(map[UISlotName][]UISlotEntry),
		services:           make(map[string]interface{}),
		serviceOwners:      make(map[string]string),
		taskBackendOwners:  make(map[string]string),
		providerOwners:     make(map[string]string),
		configSchemaOwners: make(map[string]struct{}),
		configs:            make(map[string]*PluginConfig),
		envelopes:          make(map[string]EnvelopeRegistryEntry),
		router:             router,
		pluginMux:          NewMutablePluginMux(),
		routePatterns:      make(map[string]bool),
		logger:             logger,
		ctx:                ctx,
		ctxCancel:          cancel,
	}
	h.triggers = NewTriggerDispatcher(h)
	h.filters = NewFilterRegistry()
	return h
}

// NewHostWithStore creates a minimal plugin host with just a store service.
// Used by CLI commands (e.g. nanite plugin uninstall) that need to run
// plugin lifecycle methods without a full server.
func NewHostWithStore(store interface{}) *Host {
	ctx, cancel := context.WithCancel(context.Background())
	h := &Host{
		plugins:            make(map[string]plugin.Plugin),
		eventHooks:         make(map[string][]eventHookEntry),
		crudHandlers:       make(map[string]crudHandlerEntry),
		uiComponents:       []plugin.UIComponent{},
		uiOwners:           make(map[string]string),
		connectors:         make(map[string]plugin.Connector),
		connectorOwners:    make(map[string]string),
		connectorHealth:    make(map[string]*ConnectorStatus),
		keybindings:        make(map[string]KeybindingDef),
		kbOwners:           make(map[string]string),
		slots:              make(map[UISlotName][]UISlotEntry),
		services:           make(map[string]interface{}),
		serviceOwners:      make(map[string]string),
		taskBackendOwners:  make(map[string]string),
		providerOwners:     make(map[string]string),
		configSchemaOwners: make(map[string]struct{}),
		configs:            make(map[string]*PluginConfig),
		envelopes:          make(map[string]EnvelopeRegistryEntry),
		router:             http.NewServeMux(),
		pluginMux:          NewMutablePluginMux(),
		routePatterns:      make(map[string]bool),
		logger:             NewLogger("plugin-cli"),
		ctx:                ctx,
		ctxCancel:          cancel,
	}
	h.triggers = NewTriggerDispatcher(h)
	h.filters = NewFilterRegistry()
	h.services["store"] = store
	return h
}

// SetRouter sets the HTTP router for the plugin host and replays any
// route registrations that were queued while the router was nil.
//
// Plugin-owned routes live in h.pluginMux (a MutablePluginMux) so that
// UnloadPlugin can drop them. To integrate with the core *http.ServeMux, we
// install a thin forwarder handler on the core mux — one per distinct pattern
// — that delegates to h.pluginMux.ServeHTTP. Forwarders stay in place for the
// server's lifetime (ServeMux forbids re-registration), but the plugin mux
// behind them is mutable.
func (h *Host) SetRouter(router *http.ServeMux) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.router = router

	// Replay any routes that were queued before the router was available.
	for _, pr := range h.pendingRoutes {
		h.installForwarderLocked(pr.pattern)
		h.pluginMux.Handle(pr.pluginID, pr.pattern, pr.handler)
		h.logger.Info("replayed pending route", "pattern", pr.pattern, "plugin", pr.pluginID)
	}
	h.pendingRoutes = nil
}

// installForwarderLocked installs a stable forwarder on the core router for
// the given pattern, if one isn't already installed. Caller must hold h.mu.
// Safe when the core router is nil (pending-route path calls this after
// router set).
func (h *Host) installForwarderLocked(pattern string) {
	if h.router == nil {
		return
	}
	if h.routePatterns[pattern] {
		return
	}
	h.routePatterns[pattern] = true
	// The forwarder is a single handler per pattern that delegates into the
	// mutable plugin mux. Route lookup inside pluginMux happens at request
	// time, so removing a plugin's routes from the mux causes this forwarder
	// to 404 — which is the correct behavior for a removed handler.
	pluginMux := h.pluginMux
	h.router.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pluginMux.ServeHTTP(w, r)
	}))
}

// registerRoute registers an HTTP route owned by the currently-loading plugin.
// The route is stored in h.pluginMux (so UnloadPlugin can remove it) and a
// forwarder is installed on the core router if one isn't already in place.
// If the core router isn't set yet, the registration is queued; the queue is
// flushed by SetRouter.
//
// Caller is expected to hold h.mu (at least the write path, since routes may
// be appended to pendingRoutes).
func (h *Host) registerRoute(pattern string, handler http.Handler) {
	pluginID := h.activePlugin
	if h.router == nil {
		h.pendingRoutes = append(h.pendingRoutes, pendingRoute{pattern: pattern, handler: handler, pluginID: pluginID})
		h.logger.Info("queued route (router not yet available)", "pattern", pattern, "plugin", pluginID)
		return
	}
	h.installForwarderLocked(pattern)
	h.pluginMux.Handle(pluginID, pattern, handler)
}

// RegisterHTTPHandler registers a custom HTTP route on the plugin host's
// router. Use this for non-CRUD endpoints that don't fit the standard
// CRUD handler pattern. Pattern follows net/http method routing syntax
// (e.g., "GET /api/plugins/engine/sprints").
func (h *Host) RegisterHTTPHandler(pattern string, handler http.Handler) {
	h.registerRoute(pattern, handler)
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
	h.crudHandlers[resourceType] = crudHandlerEntry{handler: handler, pluginID: h.activePlugin}

	// Only wire HTTP routes the first time — ServeMux doesn't support re-registration,
	// but the handler closures read from h.crudHandlers so they'll pick up the new handler.
	if alreadyRegistered {
		return nil
	}

	// Wire HTTP routes for this resource type.
	// Uses registerRoute so routes are queued if the router isn't set yet.
	basePath := fmt.Sprintf("/api/plugins/%s", resourceType)

	// withHandler resolves the current handler under a read lock and invokes fn
	// with it, or writes 404 if the resource type has been swept (e.g., after
	// the owning plugin was unloaded). Forwarders stay installed for the
	// process lifetime, so a missing handler must surface as 404 rather than
	// panicking on a nil deref.
	withHandler := func(w http.ResponseWriter, fn func(plugin.CRUDHandler)) {
		h.mu.RLock()
		entry, ok := h.crudHandlers[resourceType]
		h.mu.RUnlock()
		if !ok {
			http.NotFound(w, nil)
			return
		}
		fn(entry.handler)
	}

	// List resources: GET /api/plugins/{resourceType}
	h.registerRoute(fmt.Sprintf("GET %s", basePath), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		withHandler(w, func(h2 plugin.CRUDHandler) { h.handleCRUDList(w, r, h2) })
	}))

	// Create resource: POST /api/plugins/{resourceType}
	h.registerRoute(fmt.Sprintf("POST %s", basePath), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		withHandler(w, func(h2 plugin.CRUDHandler) { h.handleCRUDCreate(w, r, h2) })
	}))

	// Get resource: GET /api/plugins/{resourceType}/{id}
	h.registerRoute(fmt.Sprintf("GET %s/{id}", basePath), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		withHandler(w, func(h2 plugin.CRUDHandler) { h.handleCRUDRead(w, r, h2) })
	}))

	// Update resource: PUT /api/plugins/{resourceType}/{id}
	h.registerRoute(fmt.Sprintf("PUT %s/{id}", basePath), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		withHandler(w, func(h2 plugin.CRUDHandler) { h.handleCRUDUpdate(w, r, h2) })
	}))

	// Delete resource: DELETE /api/plugins/{resourceType}/{id}
	h.registerRoute(fmt.Sprintf("DELETE %s/{id}", basePath), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		withHandler(w, func(h2 plugin.CRUDHandler) { h.handleCRUDDelete(w, r, h2) })
	}))

	h.logger.Info("registered CRUD handler", "resourceType", resourceType, "basePath", basePath)
	return nil
}

// RegisterEventHook registers an event hook for specific event types.
// Event types are normalized so plugins may use either Nanite names
// (e.g. "tool.executing") or Claude Code hook names (e.g. "PreToolUse").
func (h *Host) RegisterEventHook(eventTypes []string, hook plugin.EventHook) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	pluginID := h.activePlugin
	normalized := make([]string, len(eventTypes))
	for i, eventType := range eventTypes {
		normalized[i] = NormalizeEventType(eventType)
		h.eventHooks[normalized[i]] = append(h.eventHooks[normalized[i]], eventHookEntry{hook: hook, pluginID: pluginID})
	}

	h.logger.Info("registered event hook", "eventTypes", normalized, "hookTypes", hook.EventTypes(), "plugin", pluginID)
	return nil
}

// RegisterUIComponent registers UI components for the frontend.
func (h *Host) RegisterUIComponent(component plugin.UIComponent) error {
	// Validate ID format: lowercase alphanumeric + hyphens, 2-64 chars.
	if !validComponentID.MatchString(component.ID) {
		return fmt.Errorf("invalid UI component ID %q: must be 2-64 chars, lowercase alphanumeric and hyphens, no leading/trailing hyphens", component.ID)
	}

	// Validate type is a known UIComponentType.
	if !validComponentTypes[component.Type] {
		return fmt.Errorf("invalid UI component type %q for %q: must be widget, envelope, action, workflow, or view", component.Type, component.ID)
	}

	// Validate name length.
	if len(component.Name) == 0 || len(component.Name) > 128 {
		return fmt.Errorf("UI component %q name must be 1-128 characters", component.ID)
	}
	if len(component.Description) > 512 {
		return fmt.Errorf("UI component %q description must be <= 512 characters", component.ID)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	callerPlugin := h.activePlugin

	// Check for cross-plugin ID collision: reject if a different plugin already owns this ID.
	if owner, exists := h.uiOwners[component.ID]; exists && owner != callerPlugin {
		return fmt.Errorf("UI component ID %q already registered by plugin %q (caller: %q)", component.ID, owner, callerPlugin)
	}

	// Replace existing component with same ID (same plugin re-registering), or append.
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

	// Track ownership.
	if callerPlugin != "" {
		h.uiOwners[component.ID] = callerPlugin
	}

	// If the component has a server-side handler, register the route (only once).
	if component.Handler != nil && !alreadyRegistered {
		path := fmt.Sprintf("/api/plugins/ui/%s", component.ID)
		h.registerRoute(path, component.Handler)
		h.logger.Info("registered UI component handler", "id", component.ID, "path", path)
	}

	// Warn if widget type registered without a handler (data endpoint).
	if component.Type == plugin.UIComponentTypeWidget && component.Handler == nil {
		h.logger.Info("widget registered without data handler", "id", component.ID, "plugin", callerPlugin)
	}

	h.logger.Info("registered UI component", "id", component.ID, "type", component.Type, "plugin", callerPlugin)

	// Emit widget.loaded event (fire-and-forget). Extract slot from Props if
	// the plugin provided it; otherwise empty string.
	slot := ""
	if s, ok := component.Props["slot"].(string); ok {
		slot = s
	}
	compID := component.ID
	compType := string(component.Type)
	safego.Go(h.ctx, "plugin.host.emit.widget-loaded", func() {
		h.EmitWidgetLoaded(compID, compType, slot)
	})
	return nil
}

// RegisterFilter adds a filter handler to the named filter chain at the given
// priority. Lower priority values execute earlier in the chain. The calling
// plugin is identified by the active plugin context during Load().
func (h *Host) RegisterFilter(name string, priority int, fn FilterFunc) error {
	h.mu.RLock()
	pluginID := h.activePlugin
	h.mu.RUnlock()

	h.logger.Info("registering filter", "name", name, "priority", priority, "plugin", pluginID)
	return h.filters.Register(name, pluginID, priority, fn)
}

// RegisterFilterWithView registers a filter handler with an explicit view that
// controls what subset of data the handler sees. Use FilterViewReasoningBlind
// for safety classifiers that must not see assistant reasoning content.
func (h *Host) RegisterFilterWithView(name string, priority int, view FilterView, fn FilterFunc) error {
	h.mu.RLock()
	pluginID := h.activePlugin
	h.mu.RUnlock()

	h.logger.Info("registering filter with view", "name", name, "priority", priority, "view", view, "plugin", pluginID)
	return h.filters.RegisterWithView(name, pluginID, priority, view, fn)
}

// ApplyFilter runs the filter chain for the named filter point. Returns the
// transformed data or an error if any handler in the chain fails (aborting
// the rest of the chain). If no handlers are registered, data passes through
// unchanged.
func (h *Host) ApplyFilter(name string, data interface{}, ctx FilterContext) (interface{}, error) {
	return h.filters.Apply(name, data, ctx)
}

// FilterChainLen returns the number of handlers registered for a named filter.
func (h *Host) FilterChainLen(name string) int {
	return h.filters.Len(name)
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

// RegisterService registers a service for plugin access.
//
// If called during a plugin's Load() — i.e. while h.activePlugin is non-empty
// — the service is tagged as plugin-owned and dropped on UnloadPlugin. Core
// services registered from main.go (before any plugin loads) leave
// h.activePlugin == "" and are therefore treated as permanent. This lets us
// keep the single RegisterService method without a signature change and
// without breaking the five existing core-service call sites.
func (h *Host) RegisterService(name string, service interface{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.services[name] = service
	if h.activePlugin != "" {
		h.serviceOwners[name] = h.activePlugin
	}
	h.logger.Info("registered service", "name", name, "plugin", h.activePlugin)
}

// taskBackendRegistrar is the subset of task.Service needed to register backends.
// Defined here to avoid importing the task package. UnregisterBackend drops a
// named backend from the service and returns true if it was present — used by
// UnloadPlugin to sweep plugin-owned task backends (ownership is tracked in
// h.taskBackendOwners because the task package itself has no plugin concept).
type taskBackendRegistrar interface {
	RegisterBackend(name string, backend interface{})
	UnregisterBackend(name string) bool
}

// RegisterTaskBackend registers a named task backend via the task service.
// The backend must implement task.TaskBackend (checked at runtime by the task service).
// The task service must have been registered via RegisterService("tasks", ...) first.
func (h *Host) RegisterTaskBackend(name string, backend interface{}) error {
	h.mu.RLock()
	svc, ok := h.services["tasks"]
	pluginID := h.activePlugin
	h.mu.RUnlock()
	if !ok {
		return fmt.Errorf("register task backend %q: task service not available", name)
	}
	reg, ok := svc.(taskBackendRegistrar)
	if !ok {
		return fmt.Errorf("register task backend %q: task service does not support backend registration", name)
	}
	reg.RegisterBackend(name, backend)
	// Tag ownership for UnloadPlugin sweep. Core backends (e.g. "local")
	// registered at service construction time never go through this path, so
	// they're absent from taskBackendOwners and survive unload.
	if pluginID != "" {
		h.mu.Lock()
		h.taskBackendOwners[name] = pluginID
		h.mu.Unlock()
	}
	h.logger.Info("registered task backend", "name", name, "plugin", pluginID)
	return nil
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
// Resolution order: keychain (for secret fields) -> env var -> DB setting -> config file override -> default from schema.
func (h *Host) GetConfig(key string) (string, error) {
	h.mu.RLock()
	id := h.activePlugin
	cfg := h.configs[id]
	s := h.store
	h.mu.RUnlock()

	// Check keychain first (secret fields are stored there, not in DB).
	keychainKey := "plugin:" + id + ":" + key
	if val := secrets.Get(keychainKey); val != "" {
		return val, nil
	}

	// Try DB (if store is available).
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
	if err := s.UpsertPluginSettings(id, settings); err != nil {
		return err
	}

	// Emit config.changed event (fire-and-forget).
	valStr := fmt.Sprintf("%v", value)
	safego.Go(h.ctx, "plugin.host.emit.config-changed", func() {
		h.EmitConfigChanged(id, key, valStr)
	})
	return nil
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
	if err := s.UpsertPluginSchema(id, storeFields); err != nil {
		return err
	}
	// Record that this plugin has a persisted config schema so UnloadPlugin
	// can clear it. Settings values are preserved (reinstall continues to
	// read them); only the schema column is reset.
	h.mu.Lock()
	h.configSchemaOwners[id] = struct{}{}
	h.mu.Unlock()
	return nil
}

// RegisterConnector registers a named connector for outbound integrations.
func (h *Host) RegisterConnector(name string, connector plugin.Connector) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connectors[name] = connector
	h.connectorOwners[name] = h.activePlugin
	h.connectorHealth[name] = &ConnectorStatus{
		Name:     name,
		PluginID: h.activePlugin,
		Healthy:  true,
	}
	h.logger.Info("registered connector", "name", name, "plugin", h.activePlugin)
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

// GetConnectorStatuses returns health status for all registered connectors.
func (h *Host) GetConnectorStatuses() []ConnectorStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]ConnectorStatus, 0, len(h.connectorHealth))
	for _, status := range h.connectorHealth {
		cp := *status
		out = append(out, cp)
	}
	return out
}

// CheckConnectorHealth probes the Health() method on a named connector
// and updates its status.
func (h *Host) CheckConnectorHealth(name string) *ConnectorStatus {
	connector, ok := h.GetConnector(name)
	if !ok {
		return nil
	}

	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
	defer cancel()
	err := connector.Health(ctx)

	h.mu.Lock()
	defer h.mu.Unlock()

	status, exists := h.connectorHealth[name]
	if !exists {
		return nil
	}
	status.LastCheckAt = time.Now()
	if err != nil {
		status.Healthy = false
		status.LastError = err.Error()
		status.LastErrorAt = time.Now()
		status.ConsecutiveFailures++
	} else {
		status.Healthy = true
		status.LastError = ""
		status.ConsecutiveFailures = 0
	}
	return &ConnectorStatus{
		Name:                status.Name,
		PluginID:            status.PluginID,
		Healthy:             status.Healthy,
		LastCheckAt:         status.LastCheckAt,
		LastError:           status.LastError,
		LastErrorAt:         status.LastErrorAt,
		ConsecutiveFailures: status.ConsecutiveFailures,
	}
}

// CheckAllConnectorHealth probes Health() on every registered connector.
func (h *Host) CheckAllConnectorHealth() []ConnectorStatus {
	h.mu.RLock()
	names := make([]string, 0, len(h.connectors))
	for name := range h.connectors {
		names = append(names, name)
	}
	h.mu.RUnlock()

	out := make([]ConnectorStatus, 0, len(names))
	for _, name := range names {
		if s := h.CheckConnectorHealth(name); s != nil {
			out = append(out, *s)
		}
	}
	return out
}

// recordConnectorFailure marks a connector as unhealthy after send retries
// are exhausted. Called by TriggerDispatcher.
func (h *Host) recordConnectorFailure(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	status, exists := h.connectorHealth[name]
	if !exists {
		return
	}
	status.Healthy = false
	status.LastErrorAt = time.Now()
	status.ConsecutiveFailures++
}

// recordConnectorSuccess marks a connector as healthy after a successful send.
func (h *Host) recordConnectorSuccess(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	status, exists := h.connectorHealth[name]
	if !exists {
		return
	}
	status.Healthy = true
	status.LastError = ""
	status.ConsecutiveFailures = 0
}

// RegisterProvider registers a runtime LLM provider from a plugin.
// The provider must implement the provider.Provider interface; the host
// registers it with the provider registry via the "provider-registry" service.
func (h *Host) RegisterProvider(name string, prov interface{}) error {
	h.mu.Lock()
	regSvc, exists := h.services["provider-registry"]
	pluginID := h.activePlugin
	h.mu.Unlock()

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

	// Track ownership so UnloadPlugin can sweep this provider when the
	// registry grows an Unregister surface (providerUnregistrar). Only
	// tag plugin-registered providers; core/boot-time calls with empty
	// activePlugin are treated as permanent.
	if pluginID != "" {
		h.mu.Lock()
		h.providerOwners[name] = pluginID
		h.mu.Unlock()
	}
	h.logger.Info("registered plugin provider", "name", name, "plugin", pluginID)
	return nil
}

// providerUnregistrar is satisfied by future go-providers releases that add
// Unregister(name) bool to the provider registry. Until then, UnloadPlugin's
// provider sweep clears only the host-side owner map and logs a warning; once
// the go.mod bump lands, the type assertion succeeds and real removal happens
// with no other nanite code change.
type providerUnregistrar interface {
	Unregister(name string) bool
}

// RegisterCLIAdapter registers a runtime CLI adapter from a plugin.
// The adapter must implement the CLIAdapter interface; the host stores it
// for the bridge layer to discover.
func (h *Host) RegisterCLIAdapter(name string, adapter interface{}) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	// Store as a service so the bridge layer can discover plugin-registered adapters.
	// Ownership is tracked via serviceOwners using the same cli-adapter: key prefix,
	// so UnloadPlugin can sweep CLI adapters alongside other plugin-owned services.
	key := "cli-adapter:" + name
	h.services[key] = adapter
	if h.activePlugin != "" {
		h.serviceOwners[key] = h.activePlugin
	}
	h.logger.Info("registered plugin CLI adapter", "name", name, "plugin", h.activePlugin)
	return nil
}

// SetCommandRegistry sets the unified command registry. Called from main.go
// after both the Engine (which owns the registry) and the Host are created.
func (h *Host) SetCommandRegistry(reg CommandRegistrar) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.commands = reg
}

// SetMCPRegistrar installs the MCP server registrar (typically *mcp.Manager)
// so yaml-driven subprocess plugin MCP server registrations can reach it
// during applyManifestRegistrations. Nil-safe: if never set, mcp_servers
// entries in a subprocess plugin manifest are logged and skipped.
func (h *Host) SetMCPRegistrar(reg MCPRegistrar) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mcpRegistrar = reg
}

// RegisterCommand registers a slash command from a plugin into the unified
// command registry (shared with built-in commands). Source is set to the
// calling plugin's ID.
func (h *Host) RegisterCommand(cmd SlashCommandDef) error {
	h.mu.RLock()
	reg := h.commands
	source := h.activePlugin
	h.mu.RUnlock()

	if reg == nil {
		h.logger.Warn("RegisterCommand called but no command registry set", "name", cmd.Name)
		return nil
	}
	if source == "" {
		source = "plugin"
	}
	reg.RegisterPluginCommand(cmd, source)
	h.logger.Info("registered slash command", "name", cmd.Name, "category", cmd.Category, "source", source)
	return nil
}

// RegisterSlot registers a UI slot entry for a named mount point in the frontend.
// Entries are stored sorted by priority (descending — higher priority first).
func (h *Host) RegisterSlot(entry UISlotEntry) error {
	if entry.ID == "" {
		return fmt.Errorf("slot entry ID is required")
	}
	if entry.Slot == "" {
		return fmt.Errorf("slot name is required for entry %q", entry.ID)
	}
	if entry.Label == "" {
		return fmt.Errorf("slot entry %q must have a label", entry.ID)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Set plugin ID from active plugin context if not already set.
	if entry.PluginID == "" {
		entry.PluginID = h.activePlugin
	}

	// Replace existing entry with same ID, or append.
	entries := h.slots[entry.Slot]
	replaced := false
	for i, existing := range entries {
		if existing.ID == entry.ID {
			entries[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, entry)
	}

	// Sort by priority descending (higher priority first).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Priority > entries[j].Priority
	})

	h.slots[entry.Slot] = entries
	h.logger.Info("registered UI slot entry", "id", entry.ID, "slot", entry.Slot, "plugin", entry.PluginID)
	return nil
}

// GetSlotEntries returns all registered entries for a given slot, sorted by priority.
func (h *Host) GetSlotEntries(slot UISlotName) []UISlotEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	entries := h.slots[slot]
	out := make([]UISlotEntry, len(entries))
	copy(out, entries)
	return out
}

// GetAllSlots returns all slot entries grouped by slot name.
func (h *Host) GetAllSlots() map[UISlotName][]UISlotEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[UISlotName][]UISlotEntry, len(h.slots))
	for slot, entries := range h.slots {
		cp := make([]UISlotEntry, len(entries))
		copy(cp, entries)
		out[slot] = cp
	}
	return out
}

// coreKeybindings is the set of binding keys reserved by core. Plugin keybindings
// that collide with a core binding are rejected.
var coreKeybindings = map[string]bool{
	"mod+b":       true, // toggle left sidebar
	"mod+/":       true, // toggle right rail
	"mod+l":       true, // focus composer
	"mod+n":       true, // new session
	"mod+k":       true, // command palette
	"shift+shift": true, // search
	"mod+]":       true, // next session
	"mod+[":       true, // prev session
	"mod+d":       true, // bookmark
	"mod+.":       true, // toggle artifacts
}

// RegisterKeybinding registers a keyboard shortcut from a plugin. The frontend
// merges these with core bindings. Core bindings always win on collision.
func (h *Host) RegisterKeybinding(kb KeybindingDef) error {
	if kb.ID == "" {
		return fmt.Errorf("keybinding ID is required")
	}
	if kb.Key == "" {
		return fmt.Errorf("keybinding %q must have a key", kb.ID)
	}
	if kb.Label == "" {
		return fmt.Errorf("keybinding %q must have a label", kb.ID)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Check for collision with core bindings.
	if coreKeybindings[kb.Key] {
		h.logger.Warn("keybinding collides with core binding, rejected",
			"id", kb.ID, "key", kb.Key, "plugin", h.activePlugin)
		return fmt.Errorf("keybinding %q collides with core binding for key %q", kb.ID, kb.Key)
	}

	// Check for collision with other plugin bindings (different ID, same key).
	for existingID, existing := range h.keybindings {
		if existing.Key == kb.Key && existingID != kb.ID {
			owner := h.kbOwners[existingID]
			h.logger.Warn("keybinding key collision between plugins",
				"newID", kb.ID, "existingID", existingID, "key", kb.Key,
				"existingPlugin", owner, "newPlugin", h.activePlugin)
			return fmt.Errorf("keybinding key %q already registered by %q (ID: %s)", kb.Key, owner, existingID)
		}
	}

	h.keybindings[kb.ID] = kb
	h.kbOwners[kb.ID] = h.activePlugin
	h.logger.Info("registered keybinding", "id", kb.ID, "key", kb.Key, "plugin", h.activePlugin)
	return nil
}

// GetKeybindings returns all plugin-registered keybindings.
func (h *Host) GetKeybindings() []KeybindingDef {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]KeybindingDef, 0, len(h.keybindings))
	for _, kb := range h.keybindings {
		out = append(out, kb)
	}
	return out
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

	// For subprocess plugins, register Nanite-specific capabilities
	// (commands, slots, keybindings) that the subprocess can't register
	// directly because it only sees the generic plugin.Host interface.
	if sp, ok := p.(*subprocess.SubprocessPlugin); ok {
		h.registerSubprocessExtensions(sp)
	}

	// Store the loaded plugin.
	h.mu.Lock()
	h.plugins[id] = p
	h.mu.Unlock()

	h.logger.Info("loaded plugin", "id", id, "name", p.Name(), "version", p.Version())

	// Emit plugin.installed event (fire-and-forget).
	pName := p.Name()
	pVer := p.Version()
	safego.Go(h.ctx, "plugin.host.emit.plugin-installed", func() {
		h.EmitPluginInstalled(id, pName, pVer)
	})
	return nil
}

// registerSubprocessExtensions registers Nanite-specific capabilities from a
// subprocess plugin's manifest. The subprocess only sees plugin.Host (SDK) so
// it can't call RegisterCommand/Slot/Keybinding directly. We translate its
// wire types to our local types here.
func (h *Host) registerSubprocessExtensions(sp *subprocess.SubprocessPlugin) {
	lr := sp.Manifest()
	if lr == nil {
		return
	}

	for _, cmd := range lr.Commands {
		var args []CommandArg
		for _, a := range cmd.Args {
			args = append(args, CommandArg{
				Name:        a.Name,
				Description: a.Description,
				Required:    a.Required,
				Type:        a.Type,
				Options:     a.Options,
			})
		}
		def := SlashCommandDef{
			Name:        cmd.Name,
			Description: cmd.Description,
			Category:    cmd.Category,
			Args:        args,
			Permission:  cmd.Permission,
			Handler:     sp.MakeCommandHandler(cmd.Name),
		}
		if err := h.RegisterCommand(def); err != nil {
			h.logger.Warn("failed to register subprocess command", "name", cmd.Name, "error", err)
		}
	}

	for _, slot := range lr.Slots {
		entry := UISlotEntry{
			ID:        slot.ID,
			PluginID:  slot.PluginID,
			Slot:      UISlotName(slot.Slot),
			Label:     slot.Label,
			Icon:      slot.Icon,
			Priority:  slot.Priority,
			Component: slot.Component,
			Action:    slot.Action,
			Props:     slot.Props,
		}
		if err := h.RegisterSlot(entry); err != nil {
			h.logger.Warn("failed to register subprocess slot", "id", slot.ID, "error", err)
		}
	}

	for _, kb := range lr.Keybindings {
		def := KeybindingDef{
			ID:          kb.ID,
			Key:         kb.Key,
			Action:      kb.Action,
			ActionValue: kb.ActionValue,
			Label:       kb.Label,
			Description: kb.Description,
		}
		if err := h.RegisterKeybinding(def); err != nil {
			h.logger.Warn("failed to register subprocess keybinding", "id", kb.ID, "error", err)
		}
	}
}

// UnloadPlugin unloads a plugin from the host.
//
// The plugin's Unload() is invoked WITHOUT holding h.mu. Inner Unload calls
// may take arbitrary time (subprocess shutdown, network close) and may re-
// enter the host (e.g., to unregister routes), which would deadlock under
// the host mutex. We validate under lock, capture the plugin reference,
// release, then call Unload.
func (h *Host) UnloadPlugin(id string) error {
	h.mu.Lock()

	p, exists := h.plugins[id]
	if !exists {
		h.mu.Unlock()
		return fmt.Errorf("plugin %q not found", id)
	}

	// Check if any other plugins depend on this one
	for _, other := range h.plugins {
		if other.ID() == id {
			continue
		}
		for _, dep := range other.Dependencies() {
			if dep == id {
				h.mu.Unlock()
				return fmt.Errorf("cannot unload plugin %q: plugin %q depends on it", id, other.ID())
			}
		}
	}
	h.mu.Unlock()

	// Inner Unload call outside the lock.
	if err := p.Unload(); err != nil {
		return fmt.Errorf("failed to unload plugin %q: %w", id, err)
	}

	// MCP servers are removed OUTSIDE h.mu because Manager.RemoveServer takes
	// its own lock and may call Close() on the transport which can block.
	// Snapshot the registrar reference under h.mu first.
	h.mu.RLock()
	mcpReg := h.mcpRegistrar
	h.mu.RUnlock()
	if mcpReg != nil {
		if n := mcpReg.RemoveServersByPlugin(id); n > 0 {
			slog.Debug("plugin unload: removed mcp servers", "plugin", id, "count", n)
		}
	}

	// Snapshot registrars that must be invoked under/around h.mu. The
	// command registrar requires no lock coordination (its own mutex), so we
	// can capture the reference early and call it after dropping h.mu; the
	// task backend registrar lives in h.services under "tasks", read below.
	h.mu.RLock()
	cmdReg := h.commands
	h.mu.RUnlock()

	// Re-acquire to mutate host state.
	h.mu.Lock()
	delete(h.plugins, id)

	// B.6 full hot-unload sweep — 16 of 16 categories. Categories landed in
	// B.6a retained; B.6b added commands, task backends, config schemas,
	// event hooks, CRUD handlers. B.6 final adds providers via forward-
	// compatible providerUnregistrar adapter: host-side owner map always
	// cleared; upstream registry cleanup engages automatically once
	// go-providers ships Unregister (see providerUnregistrar above).
	var envelopeTypesToUnregister []string
	var taskBackendsToUnregister []string
	var clearSchemaPluginID string

	// 1. Filters.
	if n := h.filters.RemoveByPlugin(id); n > 0 {
		slog.Debug("plugin unload: removed filters", "plugin", id, "count", n)
	}

	// 2. Event hooks — sweep eventHookEntry whose pluginID matches. Core
	// (non-plugin) hooks registered with empty pluginID survive.
	ehCount := 0
	for eventType, entries := range h.eventHooks {
		kept := entries[:0]
		for _, e := range entries {
			if e.pluginID != id {
				kept = append(kept, e)
			} else {
				ehCount++
			}
		}
		if len(kept) == 0 {
			delete(h.eventHooks, eventType)
		} else {
			h.eventHooks[eventType] = kept
		}
	}
	if ehCount > 0 {
		slog.Debug("plugin unload: removed event hooks", "plugin", id, "count", ehCount)
	}

	// 3. UI components.
	n := 0
	cleaned := h.uiComponents[:0]
	for _, comp := range h.uiComponents {
		if h.uiOwners[comp.ID] != id {
			cleaned = append(cleaned, comp)
		} else {
			delete(h.uiOwners, comp.ID)
			n++
		}
	}
	h.uiComponents = cleaned
	if n > 0 {
		slog.Debug("plugin unload: removed ui components", "plugin", id, "count", n)
	}

	// 4. UI slots.
	slotCount := 0
	for slot, entries := range h.slots {
		kept := entries[:0]
		for _, e := range entries {
			if e.PluginID != id {
				kept = append(kept, e)
			} else {
				slotCount++
			}
		}
		if len(kept) == 0 {
			delete(h.slots, slot)
		} else {
			h.slots[slot] = kept
		}
	}
	if slotCount > 0 {
		slog.Debug("plugin unload: removed ui slots", "plugin", id, "count", slotCount)
	}

	// 5. Keybindings.
	kbCount := 0
	for kbID, owner := range h.kbOwners {
		if owner == id {
			delete(h.keybindings, kbID)
			delete(h.kbOwners, kbID)
			kbCount++
		}
	}
	if kbCount > 0 {
		slog.Debug("plugin unload: removed keybindings", "plugin", id, "count", kbCount)
	}

	// 6. Connectors.
	connCount := 0
	for name, owner := range h.connectorOwners {
		if owner == id {
			delete(h.connectors, name)
			delete(h.connectorOwners, name)
			delete(h.connectorHealth, name)
			connCount++
		}
	}
	if connCount > 0 {
		slog.Debug("plugin unload: removed connectors", "plugin", id, "count", connCount)
	}

	// 7+8. Services (includes CLI adapters under the cli-adapter: key prefix).
	svcCount := 0
	cliCount := 0
	for name, owner := range h.serviceOwners {
		if owner == id {
			delete(h.services, name)
			delete(h.serviceOwners, name)
			if strings.HasPrefix(name, "cli-adapter:") {
				cliCount++
			} else {
				svcCount++
			}
		}
	}
	if svcCount > 0 {
		slog.Debug("plugin unload: removed services", "plugin", id, "count", svcCount)
	}
	if cliCount > 0 {
		slog.Debug("plugin unload: removed cli adapters", "plugin", id, "count", cliCount)
	}

	// 9. Envelope types (side-map + chat validation registry).
	envCount := 0
	for t, entry := range h.envelopes {
		if entry.PluginID == id {
			delete(h.envelopes, t)
			envelopeTypesToUnregister = append(envelopeTypesToUnregister, t)
			envCount++
		}
	}
	if envCount > 0 {
		slog.Debug("plugin unload: removed envelope types", "plugin", id, "count", envCount)
	}

	// 10. HTTP routes — remove from mutable plugin mux. Forwarder entries on
	// the core router stay in place (http.ServeMux forbids re-registration),
	// but they delegate to the plugin mux which now returns 404 for this
	// plugin's patterns.
	if h.pluginMux != nil {
		if n := h.pluginMux.RemoveByPlugin(id); n > 0 {
			slog.Debug("plugin unload: removed http routes", "plugin", id, "count", n)
		}
	}

	// 11. CRUD handlers — drop plugin-owned entries from the map. HTTP
	// forwarders stay in place (see MutablePluginMux rationale) and the
	// per-route withHandler closures now fall through to 404 via the
	// map-miss path in RegisterCRUDHandler.
	crudCount := 0
	for rt, entry := range h.crudHandlers {
		if entry.pluginID == id {
			delete(h.crudHandlers, rt)
			crudCount++
		}
	}
	if crudCount > 0 {
		slog.Debug("plugin unload: removed crud handlers", "plugin", id, "count", crudCount)
	}

	// 12. Task backends — collect names to remove; actual UnregisterBackend
	// call happens after we drop h.mu to keep consistent with other outside-
	// lock cleanup (the task service takes its own mutex).
	for name, owner := range h.taskBackendOwners {
		if owner == id {
			taskBackendsToUnregister = append(taskBackendsToUnregister, name)
			delete(h.taskBackendOwners, name)
		}
	}

	// 13. Config schemas — mark for outside-lock clear via store.
	if _, ok := h.configSchemaOwners[id]; ok {
		clearSchemaPluginID = id
		delete(h.configSchemaOwners, id)
	}

	// Snapshot refs needed for outside-lock work.
	taskSvc, _ := h.services["tasks"].(taskBackendRegistrar)
	storeRef := h.store

	// 14. Providers — forward-compatible sweep. Host-side providerOwners is
	// always cleared. Upstream registry removal only happens when the
	// registry satisfies providerUnregistrar (a future go-providers release
	// adding Unregister). Until that ships, we warn once per unload if the
	// plugin had providers registered: they'll leak in the registry until
	// restart, but the host-side map stays consistent. When go-providers
	// grows Unregister and nanite bumps the module, this branch activates
	// with no additional code change.
	ownedProviders := make([]string, 0)
	for name, owner := range h.providerOwners {
		if owner != id {
			continue
		}
		delete(h.providerOwners, name)
		ownedProviders = append(ownedProviders, name)
	}
	providerRegSvc := h.services["provider-registry"]

	h.mu.Unlock()

	if len(ownedProviders) > 0 {
		removed := 0
		if un, ok := providerRegSvc.(providerUnregistrar); ok {
			for _, name := range ownedProviders {
				if un.Unregister(name) {
					removed++
				}
			}
		}
		if removed > 0 {
			slog.Debug("plugin unload: removed providers", "plugin", id, "count", removed)
		} else {
			slog.Warn("plugin unload: provider registry lacks Unregister; providers leak until restart",
				"plugin", id, "providers", ownedProviders)
		}
	}

	// Drop envelope types from chat validation registry OUTSIDE h.mu —
	// unregisterEnvelopeType takes the chat package lock and there's no need
	// to hold our own while doing so.
	for _, t := range envelopeTypesToUnregister {
		unregisterEnvelopeType(t)
	}

	// 15. Commands — sweep via CommandRegistrar.RemoveByPlugin, OUTSIDE h.mu
	// (CommandRegistry takes its own lock).
	if cmdReg != nil {
		if n := cmdReg.RemoveByPlugin(id); n > 0 {
			slog.Debug("plugin unload: removed commands", "plugin", id, "count", n)
		}
	}

	// Task backends — invoke UnregisterBackend outside h.mu (task service
	// has its own mutex). "local" is protected at the service layer; we
	// never add it to taskBackendOwners, so there's no risk of calling it.
	if taskSvc != nil {
		for _, name := range taskBackendsToUnregister {
			if taskSvc.UnregisterBackend(name) {
				slog.Debug("plugin unload: removed task backend", "plugin", id, "name", name)
			}
		}
	}

	// Config schema — clear via store outside h.mu. Store call does its
	// own DB I/O; absent store (CLI host with no store) is fine — the
	// configSchemaOwners side map was never populated either.
	if clearSchemaPluginID != "" && storeRef != nil {
		if err := storeRef.ClearPluginSchema(clearSchemaPluginID); err != nil {
			h.logger.Warn("plugin unload: clear config schema failed", "plugin", id, "error", err)
		} else {
			slog.Debug("plugin unload: cleared config schema", "plugin", id)
		}
	}

	h.logger.Info("unloaded plugin", "id", id)

	// Emit plugin.uninstalled event (fire-and-forget).
	safego.Go(h.ctx, "plugin.host.emit.plugin-uninstalled", func() {
		h.EmitPluginUninstalled(id)
	})
	return nil
}

// EmitEvent emits an event to all registered hooks, then dispatches
// matching trigger rules to connectors, and broadcasts to SSE subscribers.
func (h *Host) EmitEvent(event plugin.Event) {
	// 1. Dispatch to registered event hooks.
	h.mu.RLock()
	hooks := h.eventHooks[event.Type]
	h.mu.RUnlock()

	if len(hooks) > 0 {
		var wg sync.WaitGroup
		for _, hook := range hooks {
			wg.Add(1)
			hk := hook.hook
			safego.Go(h.ctx, "plugin.host.emit-event.hook", func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
				defer cancel()

				safego.Call(ctx, "plugin-hook.event-handle", func() {
					if err := hk.Handle(ctx, event); err != nil {
						h.logger.Error("event hook failed", "eventType", event.Type, "error", err)
					}
				})
			})
		}
		wg.Wait()
	}

	// 2. Dispatch to trigger rules (event → connector bindings).
	if h.triggers != nil {
		safego.Go(h.ctx, "plugin.host.emit-event.triggers-dispatch", func() {
			h.triggers.Dispatch(event)
		})
	}

	// 3. Broadcast to SSE event stream subscribers.
	h.broadcastEvent(event)
}

// UIComponentWithOwner wraps a UIComponent with its owning plugin ID.
type UIComponentWithOwner struct {
	plugin.UIComponent
	PluginID string `json:"plugin_id,omitempty"`
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

// GetUIComponentsWithOwners returns all registered UI components with plugin ownership info.
func (h *Host) GetUIComponentsWithOwners() []UIComponentWithOwner {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]UIComponentWithOwner, len(h.uiComponents))
	for i, c := range h.uiComponents {
		out[i] = UIComponentWithOwner{
			UIComponent: c,
			PluginID:    h.uiOwners[c.ID],
		}
	}
	return out
}

// GetCRUDHandlers returns all registered CRUD handlers.
func (h *Host) GetCRUDHandlers() map[string]plugin.CRUDHandler {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Return a copy to prevent modification
	handlers := make(map[string]plugin.CRUDHandler)
	for k, v := range h.crudHandlers {
		handlers[k] = v.handler
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
//
// Inner Unload() calls are made WITHOUT holding h.mu — see UnloadPlugin for
// the rationale. We snapshot the plugin map under lock, release, then unload
// each plugin.
func (h *Host) Shutdown() error {
	h.mu.Lock()
	type namedPlugin struct {
		id string
		p  plugin.Plugin
	}
	snapshot := make([]namedPlugin, 0, len(h.plugins))
	for id, p := range h.plugins {
		snapshot = append(snapshot, namedPlugin{id: id, p: p})
	}
	h.mu.Unlock()

	var errors []string
	for _, np := range snapshot {
		if err := np.p.Unload(); err != nil {
			errors = append(errors, fmt.Sprintf("failed to unload plugin %q: %v", np.id, err))
		}
	}

	h.mu.Lock()
	for _, np := range snapshot {
		delete(h.plugins, np.id)
	}
	h.mu.Unlock()

	h.ctxCancel()

	if len(errors) > 0 {
		return fmt.Errorf("shutdown errors: %v", errors)
	}

	h.logger.Info("plugin host shutdown complete")
	return nil
}