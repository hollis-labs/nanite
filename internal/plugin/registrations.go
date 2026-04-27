package plugin

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	goplugin "github.com/hollis-labs/plugin-sdk"

	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
)

// maxPluginHTTPBodyBytes caps incoming request bodies on subprocess
// plugin HTTP routes. Chosen conservatively; plugins needing more should
// stream over a different channel. A future manifest field may expose
// this per-route — not wired today.
const maxPluginHTTPBodyBytes = 10 << 20 // 10 MiB

// sensitivePluginHTTPHeaders names request headers that must NOT be
// forwarded to untrusted plugin subprocesses. Keys are in
// http.CanonicalHeaderKey form. Denylist approach for expedience; a
// future manifest opt-in will let plugins receive specific auth headers.
var sensitivePluginHTTPHeaders = map[string]struct{}{
	"Authorization":       {},
	"Cookie":              {},
	"Set-Cookie":          {},
	"Proxy-Authorization": {},
	"X-Csrf-Token":        {},
	"X-Xsrf-Token":        {},
	"X-Api-Key":           {},
}

// envelopeTypeRE mirrors the plugin.schema.v1 pattern for envelope types.
// Runtime registration must enforce it directly because compiled-in builtins
// bypass install-time schema validation — without this check a bad type would
// be accepted by RegisterEnvelope and propagated to chat validation.
var envelopeTypeRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// envelopeTypeRegistrar is the hook the chat package installs so the plugin
// package can register envelope types without importing chat (which itself
// imports plugin, causing a cycle). chat wires this at init via
// plugin.SetEnvelopeTypeRegistrar(chat.RegisterEnvelopeType).
var (
	envelopeRegistrarMu sync.RWMutex
	envelopeRegistrar   func(envelopeType string)
	envelopeUnregistrar func(envelopeType string)
)

// SetEnvelopeTypeRegistrar installs a callback that registers an envelope
// type with the chat validation registry. If never set (e.g., in tests that
// only exercise plugin loading), envelope types are still recorded in the
// host's side-map but chat validation is a no-op.
func SetEnvelopeTypeRegistrar(fn func(envelopeType string)) {
	envelopeRegistrarMu.Lock()
	envelopeRegistrar = fn
	envelopeRegistrarMu.Unlock()
}

// SetEnvelopeTypeUnregistrar installs the counterpart hook used by
// UnloadPlugin to remove plugin-owned envelope types from the chat validation
// registry. Wired from main.go alongside SetEnvelopeTypeRegistrar.
func SetEnvelopeTypeUnregistrar(fn func(envelopeType string)) {
	envelopeRegistrarMu.Lock()
	envelopeUnregistrar = fn
	envelopeRegistrarMu.Unlock()
}

func registerEnvelopeType(envelopeType string) {
	envelopeRegistrarMu.RLock()
	fn := envelopeRegistrar
	envelopeRegistrarMu.RUnlock()
	if fn != nil {
		fn(envelopeType)
	}
}

func unregisterEnvelopeType(envelopeType string) {
	envelopeRegistrarMu.RLock()
	fn := envelopeUnregistrar
	envelopeRegistrarMu.RUnlock()
	if fn != nil {
		fn(envelopeType)
	}
}

// EnvelopeRegistryEntry records a registered envelope type along with the
// plugin that owns it and the metadata the frontend needs to render it.
// The host stores these in a side-map so the B.7 registry endpoint can serve
// them without re-parsing plugin manifests.
type EnvelopeRegistryEntry struct {
	Type       string `json:"type"`
	PluginID   string `json:"plugin_id"`
	Component  string `json:"component"`
	Version    int    `json:"version"`
	SchemaPath string `json:"schema_path,omitempty"` // relative path inside the plugin dir
}

// RegisterEnvelope records a plugin-owned envelope type in the host's
// envelope registry side-map AND calls chat.RegisterEnvelopeType so validation
// accepts the type at runtime.
func (h *Host) RegisterEnvelope(entry EnvelopeRegistryEntry) error {
	if entry.Type == "" {
		return fmt.Errorf("envelope type is required")
	}
	if !envelopeTypeRE.MatchString(entry.Type) {
		return fmt.Errorf("envelope type %q must match %s", entry.Type, envelopeTypeRE)
	}
	h.mu.Lock()
	if h.envelopes == nil {
		h.envelopes = make(map[string]EnvelopeRegistryEntry)
	}
	// Cross-plugin collision check: a different plugin already owns this type.
	if existing, ok := h.envelopes[entry.Type]; ok && existing.PluginID != entry.PluginID {
		h.mu.Unlock()
		return fmt.Errorf("envelope type %q already registered by plugin %q (caller: %q)", entry.Type, existing.PluginID, entry.PluginID)
	}
	h.envelopes[entry.Type] = entry
	h.mu.Unlock()

	registerEnvelopeType(entry.Type)
	h.logger.Info("registered envelope", "type", entry.Type, "plugin", entry.PluginID, "component", entry.Component)
	return nil
}

// GetEnvelopes returns a snapshot of registered envelope types.
// Consumed by the B.7 /api/plugins/registry endpoint.
func (h *Host) GetEnvelopes() []EnvelopeRegistryEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]EnvelopeRegistryEntry, 0, len(h.envelopes))
	for _, e := range h.envelopes {
		out = append(out, e)
	}
	return out
}

// recordManifest stores the plugin's parsed manifest in the host side-map and
// bumps the registry version counter. Called at the top of
// applyManifestRegistrations so GetManifest / GetManifests can serve the B.7
// endpoint without re-reading plugin.yaml off disk.
func (h *Host) recordManifest(pluginID string, m *PluginManifest) {
	h.mu.Lock()
	if h.manifests == nil {
		h.manifests = make(map[string]*PluginManifest)
	}
	h.manifests[pluginID] = m
	h.registryVersion++
	h.mu.Unlock()
}

// GetManifest returns the parsed plugin.yaml for pluginID, or nil if not
// recorded (e.g. builtin plugins that don't implement ManifestProvider).
func (h *Host) GetManifest(pluginID string) *PluginManifest {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.manifests[pluginID]
}

// GetManifests returns a shallow copy of the plugin ID → manifest map. The
// returned pointers still reference the same underlying structs — callers must
// not mutate them.
func (h *Host) GetManifests() map[string]*PluginManifest {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]*PluginManifest, len(h.manifests))
	for k, v := range h.manifests {
		out[k] = v
	}
	return out
}

// RegistryVersion returns the monotonic counter that bumps on every change to
// what GET /api/plugins/registry would return (plugin load, manifest record,
// plugin unload). The B.7 handler uses this as a cache key. B.8 lifecycle
// event emitters should call BumpRegistryVersion when they fire events that
// affect the exposed shape (e.g., enable/disable toggles that don't go through
// Load/UnloadPlugin).
func (h *Host) RegistryVersion() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.registryVersion
}

// BumpRegistryVersion increments the registry version counter. Intended for
// the B.8 lifecycle event layer; internal load/unload paths bump directly
// under the host lock.
func (h *Host) BumpRegistryVersion() {
	h.mu.Lock()
	h.registryVersion++
	h.mu.Unlock()
}

// bumpRegistryVersionLocked increments the registry version counter. Callers
// MUST already hold h.mu (write). Used by internal load/unload paths that
// mutate registry-visible state inside the host lock.
func (h *Host) bumpRegistryVersionLocked() {
	h.registryVersion++
}

// applyManifestRegistrations iterates manifest.Registers.* and performs the
// declarative host registrations on behalf of the plugin. This is the
// yaml-authoritative path per plan §B.4 — builtins and subprocess plugins
// share the same wiring instead of each builtin calling Register* from Load().
//
// The plugin's ID is taken from p.ID(); the host sets h.activePlugin during
// LoadPlugin so ownership tracking remains intact for the inner Register calls.
//
// Scope note (B.4 first-cut): categories that require a handler proxy built
// on top of subprocess JSON-RPC or builtin in-process dispatch (commands,
// events, crud, http_routes, mcp_servers) are logged as TODO and deferred
// to B.5/B.6 which land the transport and unregister primitives these need.
// Purely declarative categories (envelopes, slots, keybindings, components)
// are fully wired here — that is enough to migrate bookmarks off direct
// Register calls as the B.4 acceptance proof.
func applyManifestRegistrations(host *Host, manifest *PluginManifest, p goplugin.Plugin, pluginDir string) error {
	if manifest == nil {
		return nil
	}
	pluginID := p.ID()

	// Record the manifest for the B.7 /api/plugins/registry endpoint. Done
	// before registrations so the side-map reflects the plugin even if a
	// later registration fails and surfaces an error — UnloadPlugin cleans
	// up on rollback.
	host.recordManifest(pluginID, manifest)

	// Set activePlugin so downstream Register* calls pick up ownership.
	host.mu.Lock()
	prev := host.activePlugin
	host.activePlugin = pluginID
	host.mu.Unlock()
	defer func() {
		host.mu.Lock()
		host.activePlugin = prev
		host.mu.Unlock()
	}()

	reg := manifest.Registers

	// 1. Envelopes — record side-map + chat.RegisterEnvelopeType. When the
	// manifest declares a schema file and the plugin dir is known, compile
	// the JSON Schema and register it for B.11 strict validation. Missing
	// schema files are logged and skipped; if a plugin-dir isn't available
	// (compiled-in builtins without on-disk assets) schema loading is a
	// no-op and validation for those types degrades to "declared but
	// schema-less" which ValidatePluginEnvelope treats as pass-through.
	for _, e := range reg.Envelopes {
		if err := host.RegisterEnvelope(EnvelopeRegistryEntry{
			Type:       e.Type,
			PluginID:   pluginID,
			Component:  e.Component,
			Version:    e.Version,
			SchemaPath: e.Schema,
		}); err != nil {
			return fmt.Errorf("envelope %q: %w", e.Type, err)
		}
		if e.Schema != "" && pluginDir != "" {
			schemaFile, err := resolvePluginAssetPath(pluginDir, e.Schema)
			if err != nil {
				host.logger.Warn("envelope schema path rejected",
					"plugin", pluginID, "type", e.Type, "schema", e.Schema, "error", err.Error())
				continue
			}
			if err := host.registerPluginEnvelopeSchemaFromFile(pluginID, e.Type, schemaFile); err != nil {
				// Log and continue — install-time validation already catches
				// missing schema files; a late failure here shouldn't block
				// plugin load, but the envelope type will only have advisory
				// validation until the schema resolves.
				host.logger.Warn("envelope schema load failed",
					"plugin", pluginID, "type", e.Type, "schema", schemaFile, "error", err.Error())
			}
		}
	}

	// 2. Components — RegisterUIComponent. The yaml carries just a name +
	// type + description; Props/Handler are not expressible declaratively.
	for _, c := range reg.Components {
		comp := goplugin.UIComponent{
			ID:          c.Name,
			Type:        goplugin.UIComponentType(c.Type),
			Name:        c.Name,
			Description: c.Description,
		}
		if err := host.RegisterUIComponent(comp); err != nil {
			return fmt.Errorf("component %q: %w", c.Name, err)
		}
	}

	// 3. Slots.
	for _, s := range reg.Slots {
		entry := UISlotEntry{
			ID:        s.ID,
			PluginID:  pluginID,
			Slot:      UISlotName(s.Slot),
			Label:     firstNonEmpty(s.ID, s.Component),
			Priority:  s.Priority,
			Component: s.Component,
			Props:     s.Props,
		}
		if err := host.RegisterSlot(entry); err != nil {
			return fmt.Errorf("slot %q: %w", s.ID, err)
		}
	}

	// 4. Keybindings.
	for _, kb := range reg.Keybindings {
		def := KeybindingDef{
			ID:          kb.ID,
			Key:         kb.Keys,
			Action:      "command",
			ActionValue: kb.Command,
			Label:       firstNonEmpty(kb.Description, kb.ID),
			Description: kb.Description,
		}
		if err := host.RegisterKeybinding(def); err != nil {
			return fmt.Errorf("keybinding %q: %w", kb.ID, err)
		}
	}

	// 5. Commands — build a SlashCommandDef per manifest entry and wire a
	// subprocess proxy handler that forwards to MethodCommandExecute.
	// Builtins that expose commands register them directly from their Load;
	// this path only fires for subprocess plugins.
	skipped := 0
	if len(reg.Commands) > 0 {
		if err := registerManifestCommands(host, pluginID, reg.Commands, p); err != nil {
			return err
		}
	}
	if len(reg.Events) > 0 {
		if err := registerManifestEvents(host, pluginID, reg.Events, p); err != nil {
			return err
		}
	}
	if len(reg.Crud) > 0 {
		host.logger.Info("manifest crud: yaml-driven registration deferred to B.5/B.6 proxy work", "plugin", pluginID, "count", len(reg.Crud))
		skipped += len(reg.Crud)
	}
	if len(reg.HttpRoutes) > 0 {
		if err := registerManifestHTTPRoutes(host, pluginID, reg.HttpRoutes, p); err != nil {
			return err
		}
	}
	if len(reg.McpServers) > 0 {
		if err := registerManifestMCPServers(host, pluginID, reg.McpServers, p); err != nil {
			return err
		}
	}
	if len(reg.AgentProfiles) > 0 {
		host.logger.Info("manifest agent_profiles: yaml-driven registration deferred (follow-up B.4 task)", "plugin", pluginID, "count", len(reg.AgentProfiles))
		skipped += len(reg.AgentProfiles)
	}
	// 6. Card rules (J5 — CW-20260421-0013). Compile and register each rule
	// into the host's Stage 1 detection registry. Built-in rules have already
	// been registered at host startup (tier=0); these plugin rules land at
	// tier=1 and can only ADD new card types, never override built-ins.
	// pluginDir is passed to compileCardRule so output_schema rules can resolve
	// their JSON Schema files. When pluginDir is empty (in-process builtin with
	// no on-disk dir) schema-based rules are compiled without the schema file
	// and will simply never match — this mirrors the envelope schema behavior.
	if len(reg.CardRules) > 0 {
		if err := registerManifestCardRules(host, manifest, pluginID, pluginDir); err != nil {
			return err
		}
	}
	if skipped > 0 {
		host.logger.Info("manifest registrations applied (subset)", "plugin", pluginID, "deferred", skipped)
	}
	return nil
}

// registerManifestCardRules compiles and registers all card_rules entries from
// the manifest into the host's Stage 1 card detection registry.
// Errors on invalid card_type, bad regex, or schema collision with built-ins.
func registerManifestCardRules(host *Host, manifest *PluginManifest, pluginID, pluginDir string) error {
	for i, r := range manifest.Registers.CardRules {
		if r.CardType == "" {
			return fmt.Errorf("plugin %q: card_rules[%d] missing card_type", pluginID, i)
		}
		entry, err := compileCardRule(r, pluginID, pluginDir)
		if err != nil {
			return fmt.Errorf("plugin %q: card_rules[%d]: %w", pluginID, i, err)
		}
		if err := host.RegisterCardRule(entry); err != nil {
			return fmt.Errorf("plugin %q: register card rule %q: %w", pluginID, r.CardType, err)
		}
	}
	return nil
}

// registerManifestCommands wires each manifest commands entry into the host's
// slash-command registry. For subprocess plugins each command becomes a
// SlashCommandDef whose Handler proxies execution over JSON-RPC via
// SubprocessPlugin.MakeCommandHandler (MethodCommandExecute). Builtins that
// expose commands register them directly from their own Load (they have the
// in-process Go handler); this path only fires for subprocess plugins.
//
// This restores the behavior that B.10 accidentally removed along with the
// old registerSubprocessExtensions helper — without this, subprocess-plugin
// slash commands declared in plugin.yaml never reach the command registry.
func registerManifestCommands(host *Host, pluginID string, entries []CommandRegistration, p goplugin.Plugin) error {
	sp, isSubprocess := p.(*subprocess.SubprocessPlugin)
	if !isSubprocess {
		host.logger.Info("manifest commands: builtin plugin — skipping (builtins register commands directly)",
			"plugin", pluginID, "count", len(entries))
		return nil
	}

	for _, entry := range entries {
		if entry.Name == "" {
			return fmt.Errorf("plugin %q: commands entry missing name", pluginID)
		}
		args := make([]CommandArg, 0, len(entry.Args))
		for _, a := range entry.Args {
			args = append(args, CommandArg{
				Name:        a.Name,
				Description: a.Description,
				Required:    a.Required,
				Type:        a.Type,
			})
		}
		def := SlashCommandDef{
			Name:        entry.Name,
			Description: entry.Description,
			Args:        args,
			Handler:     sp.MakeCommandHandler(entry.Name),
		}
		if err := host.RegisterCommand(def); err != nil {
			return fmt.Errorf("plugin %q: register command %q: %w", pluginID, entry.Name, err)
		}
	}
	return nil
}

// registerManifestEvents wires each manifest events entry into the host's
// event-hook registry via subprocess.NewEventHook. Only subprocess plugins
// use this path — builtins that react to events register hooks directly
// from their own Load().
//
// Each hook receives the subprocess's lock-protected transport (sp.Transport())
// and a closure over sp.EnvelopeFilter so B.11 strict validation runs on
// any envelopes the plugin returns from post-hook events. The envelope
// consumer (session-scoped chat SSE delivery, see BLG-20260413-012) is
// read from the host; when no consumer is installed, validated envelopes
// are dropped downstream.
//
// Each EventRegistration.Types slice is handed to host.RegisterEventHook
// (which applies NormalizeEventType per entry). EventRegistration.Handler
// and Priority fields are currently informational — the subprocess
// dispatches all subscribed event types into a single EventHandle method.
func registerManifestEvents(host *Host, pluginID string, entries []EventRegistration, p goplugin.Plugin) error {
	sp, isSubprocess := p.(*subprocess.SubprocessPlugin)
	if !isSubprocess {
		host.logger.Info("manifest events: builtin plugin — skipping (builtins register event hooks directly)",
			"plugin", pluginID, "count", len(entries))
		return nil
	}

	consumer := host.envelopeConsumerSnapshot()
	filter := sp.EnvelopeFilter()

	for i, entry := range entries {
		if len(entry.Types) == 0 {
			return fmt.Errorf("plugin %q: events[%d] missing types", pluginID, i)
		}
		hook := subprocess.NewEventHook(pluginID, append([]string(nil), entry.Types...), sp.Transport(), filter, consumer)
		if err := host.RegisterEventHook(entry.Types, hook); err != nil {
			return fmt.Errorf("plugin %q: register event hook for %v: %w", pluginID, entry.Types, err)
		}
	}
	return nil
}

// registerManifestMCPServers wires each manifest mcp_servers entry into the
// host's MCP manager via the MCPRegistrar interface. Only subprocess plugins
// use this path — builtins that expose MCP servers register directly via
// mcp.Manager.AddServer during their own Load().
//
// If the host has no MCPRegistrar installed (e.g. CLI-only host, tests) or
// the plugin is a builtin, entries are logged and skipped rather than
// erroring — the manifest is declarative, and a builtin may register its MCP
// server through a different path.
func registerManifestMCPServers(host *Host, pluginID string, entries []MCPServerRegistration, p goplugin.Plugin) error {
	sp, isSubprocess := p.(*subprocess.SubprocessPlugin)
	if !isSubprocess {
		host.logger.Info("manifest mcp_servers: builtin plugin — skipping (builtins register MCP servers directly)",
			"plugin", pluginID, "count", len(entries))
		return nil
	}

	host.mu.RLock()
	reg := host.mcpRegistrar
	host.mu.RUnlock()
	if reg == nil {
		host.logger.Warn("manifest mcp_servers: host has no MCPRegistrar installed — skipping",
			"plugin", pluginID, "count", len(entries))
		return nil
	}

	transport := sp.Transport()
	if transport == nil {
		return fmt.Errorf("plugin %q: subprocess transport not ready for mcp_servers registration", pluginID)
	}

	for _, entry := range entries {
		if entry.Name == "" {
			return fmt.Errorf("plugin %q: mcp_servers entry missing name", pluginID)
		}
		if err := reg.AddPluginServer(pluginID, entry.Name, transport); err != nil {
			return fmt.Errorf("plugin %q: register mcp server %q: %w", pluginID, entry.Name, err)
		}
		host.logger.Info("registered plugin mcp server", "plugin", pluginID, "server", entry.Name, "tools", len(entry.Tools))
	}
	return nil
}

// registerManifestHTTPRoutes wires each manifest http_routes entry into the
// host's MutablePluginMux. For subprocess plugins each route becomes an
// http.Handler that proxies the request over JSON-RPC via the plugin's
// Transport using MethodHTTPHandle (B.10). Builtins that want to serve
// plugin-owned HTTP routes call host.RegisterHTTPRoute directly from their
// Load — this path only handles yaml-declared routes for subprocess plugins.
func registerManifestHTTPRoutes(host *Host, pluginID string, entries []HTTPRouteRegistration, p goplugin.Plugin) error {
	sp, isSubprocess := p.(*subprocess.SubprocessPlugin)
	if !isSubprocess {
		host.logger.Info("manifest http_routes: builtin plugin — skipping (builtins register HTTP routes directly)",
			"plugin", pluginID, "count", len(entries))
		return nil
	}

	transport := sp.Transport()
	if transport == nil {
		return fmt.Errorf("plugin %q: subprocess transport not ready for http_routes registration", pluginID)
	}

	for _, entry := range entries {
		if entry.Pattern == "" {
			return fmt.Errorf("plugin %q: http_routes entry missing pattern", pluginID)
		}
		handler := newSubprocessHTTPHandler(transport, entry.Handler)
		pattern := entry.Pattern
		if entry.Method != "" {
			pattern = strings.ToUpper(entry.Method) + " " + entry.Pattern
		}
		host.RegisterHTTPHandler(pattern, handler)
		host.logger.Info("registered plugin http route",
			"plugin", pluginID, "pattern", pattern, "handler", entry.Handler)
	}
	return nil
}

// newSubprocessHTTPHandler builds an http.Handler that forwards the HTTP
// request to a subprocess plugin via MethodHTTPHandle and writes the plugin's
// HTTPResponse back to the ResponseWriter. Streaming is not supported on this
// path (documented in plugin-sdk); plugins wanting streaming use SSE envelopes
// via EventHandleResult. B.11 hardens policy (timeouts, body caps, header
// allowlists); B.10 only lands the wire path.
func newSubprocessHTTPHandler(transport *subprocess.Transport, handlerName string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			// Cap the request body so a hostile/malformed client can't
			// OOM the host by streaming arbitrary bytes into a plugin
			// route. MaxBytesReader surfaces the overflow as an error
			// on the next Read, which we translate to 413.
			limited := http.MaxBytesReader(w, r.Body, maxPluginHTTPBodyBytes)
			b, err := io.ReadAll(limited)
			if err != nil {
				if _, ok := err.(*http.MaxBytesError); ok {
					http.Error(w, "request body exceeds plugin route limit", http.StatusRequestEntityTooLarge)
					return
				}
				http.Error(w, "read request body: "+err.Error(), http.StatusBadRequest)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(b))
			body = b
		}

		query := make(map[string]string, len(r.URL.Query()))
		for k, vs := range r.URL.Query() {
			if len(vs) > 0 {
				query[k] = vs[0]
			}
		}
		// Filter and flatten request headers before forwarding to the
		// subprocess. Sensitive headers (auth, cookies, CSRF tokens)
		// are dropped — plugins are untrusted. Multi-value headers are
		// joined with ", " (HTTP-standard combining) so information
		// isn't silently lost; the wire shape is still map[string]string
		// per plugin-sdk v0.2.0 HTTPRequest.
		headers := make(map[string]string, len(r.Header))
		for k, vs := range r.Header {
			ck := http.CanonicalHeaderKey(k)
			if _, sensitive := sensitivePluginHTTPHeaders[ck]; sensitive {
				continue
			}
			if len(vs) == 0 {
				continue
			}
			headers[ck] = strings.Join(vs, ", ")
		}

		req := &subprocess.HTTPRequest{
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   query,
			Headers: headers,
			Body:    body,
		}
		// handlerName is intentionally unused here today — the plugin
		// side dispatches on Method+Path and handlerName is preserved
		// on the wire registration for future per-route handler
		// identifiers landing downstream.
		_ = handlerName

		resp, err := subprocess.CallResult[subprocess.HTTPResponse](
			transport, r.Context(), subprocess.MethodHTTPHandle, req,
		)
		if err != nil {
			http.Error(w, "plugin http handler: "+err.Error(), http.StatusBadGateway)
			return
		}
		for k, v := range resp.Headers {
			w.Header().Set(k, v)
		}
		status := resp.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if len(resp.Body) > 0 {
			_, _ = w.Write(resp.Body)
		}
	})
}

// resolvePluginAssetPath resolves a manifest-declared plugin asset (e.g. an
// envelope JSON Schema) to an absolute filesystem path confined to pluginDir.
// Absolute paths and relative paths that escape pluginDir via ".." segments
// are rejected so a hostile plugin.yaml cannot coerce the host into reading
// arbitrary files at load time. Install-time validation catches the same
// class of issue, but this is defense-in-depth — dev-mode installs only warn
// on suspicious schema paths, and a compiled-in builtin path with a crafted
// manifest would otherwise bypass that check.
func resolvePluginAssetPath(pluginDir, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute asset path %q not permitted", rel)
	}
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "..\\") {
		return "", fmt.Errorf("asset path %q escapes plugin directory", rel)
	}
	absPluginDir, err := filepath.Abs(pluginDir)
	if err != nil {
		return "", fmt.Errorf("resolve plugin dir: %w", err)
	}
	joined := filepath.Join(absPluginDir, cleaned)
	relToPlugin, err := filepath.Rel(absPluginDir, joined)
	if err != nil {
		return "", fmt.Errorf("relate asset to plugin dir: %w", err)
	}
	if relToPlugin == ".." || strings.HasPrefix(relToPlugin, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("asset path %q escapes plugin directory", rel)
	}
	return joined, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
