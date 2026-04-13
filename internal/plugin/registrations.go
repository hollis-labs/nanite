package plugin

import (
	"fmt"
	"regexp"
	"sync"

	goplugin "github.com/hollis-labs/go-plugin"
)

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

func registerEnvelopeType(envelopeType string) {
	envelopeRegistrarMu.RLock()
	fn := envelopeRegistrar
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
func applyManifestRegistrations(host *Host, manifest *PluginManifest, p goplugin.Plugin) error {
	if manifest == nil {
		return nil
	}
	pluginID := p.ID()

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

	// 1. Envelopes — record side-map + chat.RegisterEnvelopeType.
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

	// 5. Categories deferred to B.5 / B.6 — no-op with TODO log.
	// These need handler-proxy construction (subprocess RPC or in-process
	// bridge) that the B.4 scope explicitly leaves to the next tasks.
	skipped := 0
	if len(reg.Commands) > 0 {
		host.logger.Info("manifest commands: yaml-driven registration deferred to B.5/B.6 proxy work", "plugin", pluginID, "count", len(reg.Commands))
		skipped += len(reg.Commands)
	}
	if len(reg.Events) > 0 {
		host.logger.Info("manifest events: yaml-driven registration deferred to B.5/B.6 proxy work", "plugin", pluginID, "count", len(reg.Events))
		skipped += len(reg.Events)
	}
	if len(reg.Crud) > 0 {
		host.logger.Info("manifest crud: yaml-driven registration deferred to B.5/B.6 proxy work", "plugin", pluginID, "count", len(reg.Crud))
		skipped += len(reg.Crud)
	}
	if len(reg.HttpRoutes) > 0 {
		host.logger.Info("manifest http_routes: yaml-driven registration deferred to B.6 mutable-mux", "plugin", pluginID, "count", len(reg.HttpRoutes))
		skipped += len(reg.HttpRoutes)
	}
	if len(reg.McpServers) > 0 {
		host.logger.Info("manifest mcp_servers: yaml-driven registration deferred to B.5 PluginMCPTransport", "plugin", pluginID, "count", len(reg.McpServers))
		skipped += len(reg.McpServers)
	}
	if len(reg.AgentProfiles) > 0 {
		host.logger.Info("manifest agent_profiles: yaml-driven registration deferred (follow-up B.4 task)", "plugin", pluginID, "count", len(reg.AgentProfiles))
		skipped += len(reg.AgentProfiles)
	}
	if skipped > 0 {
		host.logger.Info("manifest registrations applied (subset)", "plugin", pluginID, "deferred", skipped)
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
