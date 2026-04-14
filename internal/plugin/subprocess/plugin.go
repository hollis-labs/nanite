package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/slogx"
	"github.com/hollis-labs/nanite/internal/version"
	"github.com/hollis-labs/plugin-sdk"
	sdkplugin "github.com/hollis-labs/plugin-sdk"
)

// safePluginIDRE mirrors the manifest schema (internal/plugin/schemas/
// plugin.schema.v1.json) so buildInitParams cannot be coerced into
// assembling a DataDir/CacheDir outside the user's brand directory when
// the caller passes an attacker-controlled id (e.g. a malicious
// plugin.yaml with id: "../../etc"). The regex is intentionally a
// strict subset — any id rejected here should also be rejected at
// install time by the manifest validator.
var safePluginIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

// validatePluginID returns an error if id does not match the safe
// plugin-id pattern. Empty ids are allowed so buildInitParams retains
// its "no per-plugin dirs" fallback; callers that require an id must
// check separately.
func validatePluginID(id string) error {
	if id == "" {
		return nil
	}
	if !safePluginIDRE.MatchString(id) {
		return fmt.Errorf("invalid plugin id %q: must match %s", id, safePluginIDRE)
	}
	return nil
}


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
	manifest  *LoadResult
	transport *Transport // kept for parent package to build command handlers

	// Config passed to the subprocess during init.
	config map[string]string

	// pluginDir is the directory containing plugin.yaml and the executable.
	pluginDir string

	// envelopeFilter runs plugin-emitted envelopes through the host's B.11
	// strict validator before they fan out to chat/event consumers. Set by
	// the plugin loader via SetEnvelopeFilter after LoadPlugin so the closure
	// can bind the owning plugin id. Nil means "no filter installed" which
	// degrades to pass-through — expected in tests that construct a plugin
	// without a host.
	envelopeFilter func(envs []sdkplugin.EnvelopeOut) []sdkplugin.EnvelopeOut

	// manifestID is the canonical plugin identifier resolved from the
	// plugin.yaml before the init handshake. It is used to derive the
	// DataDir and CacheDir paths that get sent over plugin/init. May be
	// empty when the caller has no pre-handshake identity (e.g. legacy
	// test harnesses); buildInitParams treats an empty id as "no
	// per-plugin dirs" and omits DataDir/CacheDir.
	manifestID string
}

// NewSubprocessPlugin creates a new subprocess plugin with the given manager config.
// The plugin is not started until Load() is called.
//
// manifestID is the plugin identifier read from plugin.yaml (preferred
// over the init-handshake ID because it is known before the subprocess
// is started, and is used to derive per-plugin DataDir/CacheDir paths
// that are handed to the plugin during plugin/init). Pass "" only from
// test harnesses that do not need per-plugin directories.
func NewSubprocessPlugin(pluginDir string, manifestID string, config map[string]string, mgrCfg ManagerConfig) *SubprocessPlugin {
	mgr := NewManager(mgrCfg)

	sp := &SubprocessPlugin{
		pluginDir:  pluginDir,
		manifestID: manifestID,
		config:     config,
		mgr:        mgr,
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
	initParams, err := buildInitParams(sp.pluginDir, sp.manifestID, sp.config)
	if err != nil {
		sp.mgr.Stop()
		return fmt.Errorf("build init params: %w", err)
	}
	initResult, err := CallResult[InitResult](transport, ctx, MethodInit, initParams)
	if err != nil {
		sp.mgr.Stop()
		return fmt.Errorf("init handshake: %w", err)
	}

	// Enforce protocol version handshake. If the plugin reports a protocol
	// version different from the host's, fail fast rather than speak a
	// mismatched dialect and corrupt later RPC calls.
	if err := checkProtocolVersion(initResult.Protocol); err != nil {
		sp.mgr.Stop()
		return err
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
	sp.transport = transport
	sp.mu.Unlock()

	// Declarative registrations are yaml-authoritative as of plugin-sdk v0.2.0
	// (Track B.10). The host applies them from plugin.yaml via
	// applyManifestRegistrations; the plugin no longer returns them on Load.
	// The only runtime payload on LoadResult is SkippedRegistrations, which
	// the host propagates up via GetSkippedRegistrations so the parent loader
	// can emit informational logs. Today the parent's Load() only LOGS the
	// declined entries — it does NOT remove the yaml-applied host
	// registration for each skipped item. True removal requires the
	// per-category unregister primitives and is tracked under
	// BLG-20260413-008 ("subprocess skipped-registration unregister").

	if len(loadResult.SkippedRegistrations) > 0 {
		logger := host.Logger()
		for _, sr := range loadResult.SkippedRegistrations {
			logger.Info("plugin declined registration",
				"plugin_id", sp.id,
				"kind", sr.Kind,
				"registration_id", sr.ID,
				"reason", sr.Reason,
			)
		}
	}

	sp.mu.Lock()
	sp.status.Loaded = true
	sp.status.LoadedAt = time.Now()
	sp.status.LastError = ""
	sp.mu.Unlock()

	return nil
}

// buildInitParams assembles the InitParams payload sent on plugin/init.
// It resolves the per-plugin DataDir/CacheDir under the user's brand
// directory, creates them with 0o755 so the plugin can write into them,
// and pulls the host-current log level from slogx. pluginID is the
// canonical id from plugin.yaml; when empty, per-plugin DataDir and
// CacheDir are left unset (v0.1.1-style behavior).
func buildInitParams(pluginDir, pluginID string, config map[string]string) (*InitParams, error) {
	if err := validatePluginID(pluginID); err != nil {
		return nil, err
	}
	ip := &InitParams{
		PluginDir: pluginDir,
		Config:    config,
		LogLevel:  slogx.CurrentLevelString(),
		HostInfo: HostInfo{
			Version:  version.Version,
			Protocol: ProtocolVersion,
		},
	}

	if pluginID != "" {
		dataDir, err := brand.PluginDataDir(pluginID)
		if err != nil {
			return nil, fmt.Errorf("resolve plugin data dir: %w", err)
		}
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, fmt.Errorf("create plugin data dir %q: %w", dataDir, err)
		}
		ip.DataDir = dataDir

		cacheDir, err := brand.PluginCacheDir(pluginID)
		if err != nil {
			return nil, fmt.Errorf("resolve plugin cache dir: %w", err)
		}
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return nil, fmt.Errorf("create plugin cache dir %q: %w", cacheDir, err)
		}
		ip.CacheDir = cacheDir
	}

	return ip, nil
}

// checkProtocolVersion returns an error if the plugin's reported protocol
// version does not match the host's. Fail fast to avoid speaking a mismatched
// dialect that would corrupt later RPC calls.
func checkProtocolVersion(got int) error {
	if got != ProtocolVersion {
		return fmt.Errorf("plugin protocol version mismatch: got %d, want %d", got, ProtocolVersion)
	}
	return nil
}

// Unload stops the subprocess gracefully.
func (sp *SubprocessPlugin) Unload() error {
	sp.mu.Lock()
	sp.status.Loaded = false
	sp.mu.Unlock()
	return sp.mgr.Stop()
}

// Manifest returns the load manifest (nil if not loaded). Post-B.10 this only
// carries SkippedRegistrations; all declarative registrations are applied by
// the host from the plugin.yaml before/after Load.
func (sp *SubprocessPlugin) Manifest() *LoadResult {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.manifest
}

// SkippedRegistrations returns the list of yaml-declared registrations the
// plugin declined at load time. Empty unless the plugin populated them on
// plugin/load. Callers use this to surface the shortfall and (where possible)
// drop the corresponding yaml-applied host registration.
func (sp *SubprocessPlugin) SkippedRegistrations() []SkippedRegistration {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	if sp.manifest == nil {
		return nil
	}
	return sp.manifest.SkippedRegistrations
}

// CallTool proxies an MCP tool invocation to the subprocess plugin using
// plugin-sdk's canonical MCPCallRequest/MCPCallResult (B.10). This is the
// method constants added in v0.2.0; the internal/mcp package has a parallel
// PluginMCPTransport that wraps the older Server/Tool-namespaced wire shape
// used by Nanite's multi-server-per-plugin model. This helper is the
// single-server path any consumer can call when they already know the
// plugin owns exactly one namespace.
//
// Envelope propagation: MCPCallResult carries Envelopes; this method runs
// them through the B.11 strict validator (sp.filterEnvelopes) so the returned
// result holds only the envelopes that passed. Chat-stream injection for
// tool-result envelopes is out of scope for B.12 — callers that want to
// render them can pull from result.Envelopes directly.
func (sp *SubprocessPlugin) CallTool(ctx context.Context, req *MCPCallRequest) (*MCPCallResult, error) {
	sp.mu.RLock()
	t := sp.transport
	sp.mu.RUnlock()
	if t == nil {
		return nil, fmt.Errorf("subprocess plugin %q: transport not ready", sp.id)
	}
	result, err := CallResult[MCPCallResult](t, ctx, MethodMCPCallTool, req)
	if err != nil {
		return result, err
	}
	if len(result.Envelopes) > 0 {
		result.Envelopes = sp.filterEnvelopes(result.Envelopes)
	}
	return result, nil
}

// Migrate invokes plugin/migrate on the subprocess. Callers pass the
// installed-manifest version (FromVersion) and the target version
// (ToVersion). An empty result is equivalent to "no-op migration
// succeeded"; plugins that don't implement migration simply return an
// empty MigrateResult.
//
// B.10 lands only the wire path. There is no installed-version tracking
// in the host yet, so the loader cannot currently decide whether to call
// Migrate on each load — see backlog item plugin-version-history-tracking.
func (sp *SubprocessPlugin) Migrate(ctx context.Context, fromVersion, toVersion, dataDir string) (*MigrateResult, error) {
	sp.mu.RLock()
	t := sp.transport
	sp.mu.RUnlock()
	if t == nil {
		return nil, fmt.Errorf("subprocess plugin %q: transport not ready", sp.id)
	}
	return CallResult[MigrateResult](t, ctx, MethodMigrate, &MigrateParams{
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		DataDir:     dataDir,
	})
}

// SetDependencies overrides the dependency list reported by the plugin's
// Plugin.Dependencies method. Post-B.10 LoadResult no longer carries
// dependencies; the caller (loader) populates them from the yaml manifest
// before invoking host.LoadPlugin so the host's dependency-order checks see
// the same values as the topological sort.
func (sp *SubprocessPlugin) SetDependencies(deps []string) {
	sp.mu.Lock()
	sp.deps = deps
	sp.mu.Unlock()
}

// Transport returns the subprocess JSON-RPC transport, or nil if the plugin
// has not completed Load() yet. Exposed so host wiring (e.g. the MCP
// registrar in internal/plugin/registrations.go) can build proxy transports
// that reuse the plugin's existing RPC channel.
func (sp *SubprocessPlugin) Transport() *Transport {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.transport
}

// SetEnvelopeFilter installs the host-provided strict validator that runs on
// envelopes emitted by this plugin. The loader calls this after LoadPlugin so
// the filter closes over the owning plugin id and the host's envelope schema
// registry. Called at most once per SubprocessPlugin lifetime.
func (sp *SubprocessPlugin) SetEnvelopeFilter(fn func(envs []sdkplugin.EnvelopeOut) []sdkplugin.EnvelopeOut) {
	sp.mu.Lock()
	sp.envelopeFilter = fn
	sp.mu.Unlock()
}

// filterEnvelopes runs the installed envelope filter (B.11), returning the
// validated envelopes to forward downstream. When no filter is installed —
// e.g. in tests, or before the loader wires the plugin — envelopes pass
// through unchanged.
func (sp *SubprocessPlugin) filterEnvelopes(envs []sdkplugin.EnvelopeOut) []sdkplugin.EnvelopeOut {
	sp.mu.RLock()
	fn := sp.envelopeFilter
	sp.mu.RUnlock()
	if fn == nil {
		return envs
	}
	return fn(envs)
}

// EnvelopeFilter returns the B.11 strict-validation closure, for callers
// in the parent plugin package that need to pass it to NewEventHook.
// The returned func is a method value bound to sp, so it picks up any
// filter swap SetEnvelopeFilter performs — no further locking on the
// caller side is required.
func (sp *SubprocessPlugin) EnvelopeFilter() func([]sdkplugin.EnvelopeOut) []sdkplugin.EnvelopeOut {
	return sp.filterEnvelopes
}

// MakeCommandHandler creates a slash command handler that proxies to the subprocess.
//
// Post-B.10 the plugin may return structured Envelopes alongside Action/Content
// on CommandExecResult. We pass them through on the result map under the
// "envelopes" key so the chat engine can decide how to route them. Strict
// validation of envelope shape against the plugin's declared envelope schemas
// is B.11; propagation into the chat stream SSE is B.12. This site only
// plumbs the data through — no schema lookup, no rendering.
//
// B.11 strict validation runs via sp.filterEnvelopes before Envelopes reach
// the chat registry adapter. B.12 propagation happens in the chat layer — the
// registry's RegisterPluginCommand adapter pulls the "envelopes" key off this
// map and injects fenced envelope blocks into the assistant message.
func (sp *SubprocessPlugin) MakeCommandHandler(name string) func(ctx context.Context, sessionID, args string) (map[string]interface{}, error) {
	return func(ctx context.Context, sessionID, args string) (map[string]interface{}, error) {
		result, err := CallResult[CommandExecResult](sp.transport, ctx, MethodCommandExecute, &CommandExecParams{
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
		out := map[string]interface{}{
			"action":  result.Action,
			"content": result.Content,
		}
		filtered := sp.filterEnvelopes(result.Envelopes)
		if len(filtered) > 0 {
			out["envelopes"] = filtered
		}
		return out, nil
	}
}

// --- Event hook proxy ---

// EnvelopeConsumer is the host-side sink for plugin-emitted envelopes that
// originate from event hooks. The subprocess event-hook proxy delivers the
// validated envelopes for each post-hook event invocation via Deliver. The
// consumer decides whether (and how) to surface them — the expected binding
// is the chat SSE stream scoped to sessionID (BLG-20260413-012). Implementations
// MUST NOT block the caller: Deliver runs inside the plugin host's event
// dispatch goroutine and any back-pressure must be handled by the consumer
// (drop + counter, per BLG-012).
type EnvelopeConsumer interface {
	// Deliver hands validated envelopes to the session-scoped SSE consumer.
	// Returns false when no consumer is attached for sessionID — the caller
	// treats that as a drop and moves on.
	Deliver(sessionID string, envs []sdkplugin.EnvelopeOut) bool
}

// subprocessEventHook implements plugin.EventHook by forwarding events to the subprocess.
type subprocessEventHook struct {
	pluginID         string
	eventTypes       []string
	transport        *Transport
	envelopeFilter   func(envs []sdkplugin.EnvelopeOut) []sdkplugin.EnvelopeOut
	envelopeConsumer EnvelopeConsumer
}

// NewEventHook builds a subprocess event-hook proxy that forwards the given
// event types to the plugin subprocess over transport. pluginID identifies
// the owning plugin for PluginID() (used by host unregister sweeps). filter
// is the B.11 strict envelope validator bound to the owning plugin id (nil
// means pass-through; expected only in tests). consumer is the
// session-scoped delivery sink for post-hook envelopes (nil means drop —
// the hook still issues the request/response call so the plugin sees the
// event and can run its side effects, the returned envelopes are simply
// discarded).
func NewEventHook(pluginID string, eventTypes []string, transport *Transport, filter func([]sdkplugin.EnvelopeOut) []sdkplugin.EnvelopeOut, consumer EnvelopeConsumer) plugin.EventHook {
	return &subprocessEventHook{
		pluginID:         pluginID,
		eventTypes:       eventTypes,
		transport:        transport,
		envelopeFilter:   filter,
		envelopeConsumer: consumer,
	}
}

func (h *subprocessEventHook) EventTypes() []string {
	return h.eventTypes
}

func (h *subprocessEventHook) PluginID() string {
	return h.pluginID
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
		// Post-hook: BLG-20260413-012 — switch from fire-and-forget Notify to a
		// request/response call so EventHandleResult.Envelopes returned by the
		// plugin can be validated and routed into the chat SSE stream. Pre-hook
		// semantics (cancellation) stay in the branch below unchanged.
		result, err := CallResult[EventHandleResult](h.transport, ctx, MethodEventHandle, params)
		if err != nil {
			// Subprocess unreachable / RPC error: log-and-swallow. A failing
			// post-hook must not block the originating action (message send,
			// tool call, …) — same contract the previous Notify path offered.
			// Logged so operators can see plugin post-hook failures instead
			// of them disappearing silently (Copilot review, PR #36).
			slog.Warn("subprocess event hook: post-hook RPC failed",
				"event_type", event.Type,
				"session_id", event.SessionID,
				"err", err)
			return nil
		}
		if len(result.Envelopes) > 0 {
			envs := result.Envelopes
			if filter := h.envelopeFilter; filter != nil {
				envs = filter(envs)
			}
			if len(envs) > 0 && h.envelopeConsumer != nil {
				// Deliver is responsible for drop+counter when no SSE
				// consumer is attached for the session.
				h.envelopeConsumer.Deliver(event.SessionID, envs)
			}
		}
		return nil
	}

	// Pre-hook: wait for response to check cancellation.
	result, err := CallResult[EventHandleResult](h.transport, ctx, MethodEventHandle, params)
	if err != nil {
		// Subprocess unreachable / RPC error: log-and-swallow so operators
		// can see pre-hook plugin failures instead of them disappearing
		// silently. The action still proceeds (default allow-on-error
		// posture) — only an explicit Cancel from a healthy plugin
		// blocks, and we cannot trust a failed RPC to signal that.
		slog.Warn("subprocess event hook: pre-hook RPC failed",
			"event_type", event.Type,
			"session_id", event.SessionID,
			"err", err)
		return nil
	}

	// Pre-hook envelopes run through the B.11 filter for validation side
	// effects (log+drop invalid in prod, warn+pass in dev) but are not routed
	// downstream — the pre-hook API is a cancel/allow gate. Post-hook events
	// carry the envelope-delivery semantics (see post-hook branch above).
	if filter := h.envelopeFilter; filter != nil && len(result.Envelopes) > 0 {
		_ = filter(result.Envelopes)
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
