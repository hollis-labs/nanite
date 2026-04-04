package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/version"
	"github.com/hollis-labs/plugin"
)

// SubprocessPlugin implements plugin.Plugin by proxying all operations over
// JSON-RPC to a plugin running as a separate process. It is backward-compatible
// with the existing plugin system — the Host sees it as a regular Plugin.
type SubprocessPlugin struct {
	mu sync.RWMutex

	// Identity fields populated during init handshake.
	id          string
	name        string
	version     string
	description string
	deps        []string

	// Runtime state.
	status plugin.PluginStatus
	mgr    *Manager

	// Registration manifest from the load handshake.
	manifest *LoadResult

	// Config passed to the subprocess during init.
	config map[string]string

	// pluginDir is the directory containing plugin.yaml and the executable.
	pluginDir string
}

// NewSubprocessPlugin creates a new subprocess plugin with the given manager config.
// The plugin is not started until Load() is called.
func NewSubprocessPlugin(pluginDir string, config map[string]string, mgrCfg ManagerConfig) *SubprocessPlugin {
	mgr := NewManager(mgrCfg)

	sp := &SubprocessPlugin{
		pluginDir: pluginDir,
		config:    config,
		mgr:       mgr,
		status: plugin.PluginStatus{
			Enabled: true,
		},
	}

	mgr.onCrash = func(err error) {
		sp.mu.Lock()
		sp.status.LastError = err.Error()
		sp.mu.Unlock()
	}

	return sp
}

// --- plugin.Plugin interface ---

func (sp *SubprocessPlugin) ID() string {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.id
}

func (sp *SubprocessPlugin) Name() string {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.name
}

func (sp *SubprocessPlugin) Version() string {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.version
}

func (sp *SubprocessPlugin) Description() string {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.description
}

func (sp *SubprocessPlugin) Dependencies() []string {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.deps
}

func (sp *SubprocessPlugin) Status() plugin.PluginStatus {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.status
}

// Load starts the subprocess, performs the init handshake, and registers
// all capabilities declared in the plugin's load manifest with the host.
func (sp *SubprocessPlugin) Load(host plugin.Host) error {
	// Use a separate timeout context for the init handshake only.
	// The process itself must not be tied to this context — canceling it
	// would kill the subprocess as soon as Load returns.
	transport, err := sp.mgr.Start(host.Context())
	if err != nil {
		return fmt.Errorf("start subprocess: %w", err)
	}

	// Short-lived context for the handshake RPCs only.
	ctx, cancel := context.WithTimeout(host.Context(), sp.mgr.cfg.StartupTimeout)
	defer cancel()

	// 2. Init handshake — send config, receive identity.
	initResult, err := CallResult[InitResult](transport, ctx, MethodInit, &InitParams{
		PluginDir: sp.pluginDir,
		Config:    sp.config,
		HostInfo: HostInfo{
			Version:  version.Version,
			Protocol: ProtocolVersion,
		},
	})
	if err != nil {
		sp.mgr.Stop()
		return fmt.Errorf("init handshake: %w", err)
	}

	sp.mu.Lock()
	sp.id = initResult.ID
	sp.name = initResult.Name
	sp.version = initResult.Version
	sp.description = initResult.Description
	sp.mu.Unlock()

	// 3. Load — receive registration manifest.
	loadResult, err := CallResult[LoadResult](transport, ctx, MethodLoad, &LoadParams{})
	if err != nil {
		sp.mgr.Stop()
		return fmt.Errorf("load handshake: %w", err)
	}

	sp.mu.Lock()
	sp.manifest = loadResult
	sp.deps = loadResult.Dependencies
	sp.mu.Unlock()

	// 4. Register everything from the manifest with the host.
	if err := sp.registerManifest(host, loadResult, transport); err != nil {
		sp.mgr.Stop()
		return fmt.Errorf("register manifest: %w", err)
	}

	sp.mu.Lock()
	sp.status.Loaded = true
	sp.status.LoadedAt = time.Now()
	sp.status.LastError = ""
	sp.mu.Unlock()

	return nil
}

// Unload stops the subprocess gracefully.
func (sp *SubprocessPlugin) Unload() error {
	sp.mu.Lock()
	sp.status.Loaded = false
	sp.mu.Unlock()
	return sp.mgr.Stop()
}

// registerManifest translates the LoadResult into host Register* calls.
func (sp *SubprocessPlugin) registerManifest(host plugin.Host, lr *LoadResult, transport *Transport) error {
	logger := host.Logger()

	// Register config schema.
	if len(lr.ConfigSchema) > 0 {
		if err := host.RegisterConfigSchema(lr.ConfigSchema); err != nil {
			return fmt.Errorf("register config schema: %w", err)
		}
	}

	// Register slash commands with proxy handlers.
	for _, cmd := range lr.Commands {
		def := plugin.SlashCommandDef{
			Name:        cmd.Name,
			Description: cmd.Description,
			Category:    cmd.Category,
			Args:        cmd.Args,
			Permission:  cmd.Permission,
			Handler:     sp.makeCommandHandler(cmd.Name, transport),
		}
		if err := host.RegisterCommand(def); err != nil {
			logger.Warn("failed to register command", "name", cmd.Name, "error", err)
		}
	}

	// Register UI slot entries.
	for _, slot := range lr.Slots {
		if err := host.RegisterSlot(slot); err != nil {
			logger.Warn("failed to register slot", "id", slot.ID, "error", err)
		}
	}

	// Register UI components.
	for _, comp := range lr.Components {
		uiComp := plugin.UIComponent{
			ID:          comp.ID,
			Type:        comp.Type,
			Name:        comp.Name,
			Description: comp.Description,
			Props:       comp.Props,
			// No Handler — subprocess plugins provide UI via frontend ESM loading.
		}
		if err := host.RegisterUIComponent(uiComp); err != nil {
			logger.Warn("failed to register component", "id", comp.ID, "error", err)
		}
	}

	// Register keybindings.
	for _, kb := range lr.Keybindings {
		if err := host.RegisterKeybinding(kb); err != nil {
			logger.Warn("failed to register keybinding", "id", kb.ID, "error", err)
		}
	}

	// Register event subscriptions as a proxy EventHook.
	if len(lr.EventSubscriptions) > 0 {
		hook := &subprocessEventHook{
			eventTypes: lr.EventSubscriptions,
			transport:  transport,
		}
		if err := host.RegisterEventHook(lr.EventSubscriptions, hook); err != nil {
			return fmt.Errorf("register event hook: %w", err)
		}
	}

	// Register CRUD handlers.
	for _, resourceType := range lr.CRUDResources {
		handler := &subprocessCRUDHandler{
			resourceType: resourceType,
			transport:    transport,
		}
		if err := host.RegisterCRUDHandler(resourceType, handler); err != nil {
			logger.Warn("failed to register CRUD handler", "resource", resourceType, "error", err)
		}
	}

	return nil
}

// makeCommandHandler creates a slash command handler that proxies to the subprocess.
func (sp *SubprocessPlugin) makeCommandHandler(name string, transport *Transport) func(ctx context.Context, sessionID, args string) (map[string]interface{}, error) {
	return func(ctx context.Context, sessionID, args string) (map[string]interface{}, error) {
		result, err := CallResult[CommandExecResult](transport, ctx, MethodCommandExecute, &CommandExecParams{
			Name:      name,
			SessionID: sessionID,
			Args:      args,
		})
		if err != nil {
			return map[string]interface{}{
				"action":  "error",
				"content": fmt.Sprintf("subprocess plugin error: %v", err),
			}, nil
		}
		return map[string]interface{}{
			"action":  result.Action,
			"content": result.Content,
		}, nil
	}
}

// --- Event hook proxy ---

// subprocessEventHook implements plugin.EventHook by forwarding events to the subprocess.
type subprocessEventHook struct {
	eventTypes []string
	transport  *Transport
}

func (h *subprocessEventHook) EventTypes() []string {
	return h.eventTypes
}

func (h *subprocessEventHook) Handle(ctx context.Context, event plugin.Event) error {
	// Determine if this is a pre-hook event (requires response).
	preHook := isPreHookEvent(event.Type)

	params := &EventHandleParams{
		Type:      event.Type,
		Source:    event.Source,
		Data:      event.Data,
		SessionID: event.SessionID,
		PreHook:   preHook,
	}

	if !preHook {
		// Fire-and-forget notification for regular events.
		return h.transport.Notify(MethodEventHandle, params)
	}

	// Pre-hook: wait for response to check cancellation.
	result, err := CallResult[EventHandleResult](h.transport, ctx, MethodEventHandle, params)
	if err != nil {
		// If the subprocess is unreachable, don't block the action.
		return nil
	}

	if result.Cancel {
		return plugin.ErrCancelled
	}
	return nil
}

// isPreHookEvent returns true for events where the host expects a cancel/allow response.
func isPreHookEvent(eventType string) bool {
	switch eventType {
	case "message.sending", "tool.executing", "mode.changing":
		return true
	default:
		return false
	}
}

// --- CRUD handler proxy ---

// subprocessCRUDHandler implements plugin.CRUDHandler by proxying to the subprocess.
type subprocessCRUDHandler struct {
	resourceType string
	transport    *Transport
}

func (h *subprocessCRUDHandler) Create(ctx context.Context, resource interface{}) (interface{}, error) {
	data, err := toMap(resource)
	if err != nil {
		return nil, err
	}
	result, err := CallResult[CRUDResult](h.transport, ctx, MethodCRUDCreate, &CRUDParams{
		ResourceType: h.resourceType,
		Data:         data,
	})
	if err != nil {
		return nil, mapRPCError(err)
	}
	var out interface{}
	_ = json.Unmarshal(result.Data, &out)
	return out, nil
}

func (h *subprocessCRUDHandler) Read(ctx context.Context, id string) (interface{}, error) {
	result, err := CallResult[CRUDResult](h.transport, ctx, MethodCRUDRead, &CRUDParams{
		ResourceType: h.resourceType,
		ID:           id,
	})
	if err != nil {
		return nil, mapRPCError(err)
	}
	var out interface{}
	_ = json.Unmarshal(result.Data, &out)
	return out, nil
}

func (h *subprocessCRUDHandler) Update(ctx context.Context, id string, resource interface{}) (interface{}, error) {
	data, err := toMap(resource)
	if err != nil {
		return nil, err
	}
	result, err := CallResult[CRUDResult](h.transport, ctx, MethodCRUDUpdate, &CRUDParams{
		ResourceType: h.resourceType,
		ID:           id,
		Data:         data,
	})
	if err != nil {
		return nil, mapRPCError(err)
	}
	var out interface{}
	_ = json.Unmarshal(result.Data, &out)
	return out, nil
}

func (h *subprocessCRUDHandler) Delete(ctx context.Context, id string) error {
	_, err := CallResult[json.RawMessage](h.transport, ctx, MethodCRUDDelete, &CRUDParams{
		ResourceType: h.resourceType,
		ID:           id,
	})
	return mapRPCError(err)
}

func (h *subprocessCRUDHandler) List(ctx context.Context, filters map[string]interface{}) ([]interface{}, error) {
	result, err := CallResult[CRUDListResult](h.transport, ctx, MethodCRUDList, &CRUDParams{
		ResourceType: h.resourceType,
		Filters:      filters,
	})
	if err != nil {
		return nil, mapRPCError(err)
	}
	items := make([]interface{}, len(result.Items))
	for i, raw := range result.Items {
		var item interface{}
		_ = json.Unmarshal(raw, &item)
		items[i] = item
	}
	return items, nil
}

// --- Helpers ---

// toMap converts an interface{} to map[string]interface{} via JSON round-trip.
func toMap(v interface{}) (map[string]interface{}, error) {
	if m, ok := v.(map[string]interface{}); ok {
		return m, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal to map: %w", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal to map: %w", err)
	}
	return m, nil
}

// mapRPCError converts JSON-RPC error codes to plugin.PluginError types
// so the host's CRUD error handling (crudErrorResp) works correctly.
func mapRPCError(err error) error {
	if err == nil {
		return nil
	}
	rpcErr, ok := err.(*RPCError)
	if !ok {
		return err
	}
	switch rpcErr.Code {
	case ErrCodeNotFound:
		return plugin.ErrNotFound(rpcErr.Message)
	case ErrCodeConflict:
		return plugin.ErrConflict(rpcErr.Message)
	case ErrCodeValidation:
		return plugin.ErrValidation(rpcErr.Message)
	case ErrCodeCancelled:
		return plugin.ErrCancelled
	default:
		return err
	}
}
