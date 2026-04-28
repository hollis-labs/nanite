package plugin

import (
	"fmt"
	"regexp"
	"sync"
)

// panelIDRE validates panel IDs. Must match the same pattern as card_type and
// envelope type so all plugin-declared slugs share one constraint.
var panelIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// PanelEntry is a registered right-rail panel held in the host registry.
// Built-in panels are registered by the host at startup (tier=0). Plugin
// panels are registered at plugin load time (tier=1) and removed at unload.
//
// J9 (CW-20260426-0007): registration + manifest schema only. The render
// function for plugin panels is deferred to a follow-up ticket.
type PanelEntry struct {
	// ID is the stable panel identifier. Referenced by J8 panel_open/panel_close.
	ID string
	// Title is the human-readable tab label.
	Title string
	// PluginID is the plugin that registered this panel. Empty for built-ins.
	PluginID string
	// Description is optional documentation text.
	Description string
	// Icon is an optional Lucide icon name for the tab strip (e.g. "layers").
	Icon string
	// DefaultVisible controls whether the panel appears in the tab strip by
	// default. Built-in panels default to true; plugin panels default to false.
	DefaultVisible bool
	// Order is a numeric sort hint. Built-ins use 0–49; plugin panels 100+.
	Order int
	// tier controls built-in (0) vs plugin (1) precedence for conflict detection.
	tier int
}

// panelRegistry is the host-side right-rail panel registry. Built-in panels
// are registered at startup (tier=0); plugin panels at load time (tier=1).
// Consumers (the /api/plugins/registry endpoint, frontend via REST) read from
// this registry to populate the right-rail tab strip.
//
// The registry is embedded in Host. External callers use the Host surface
// methods: RegisterPanel, UnregisterPluginPanels, GetPanels.
type panelRegistry struct {
	mu      sync.RWMutex
	entries []PanelEntry
}

// registerBuiltin adds a built-in (tier=0) panel. Called at host startup.
func (r *panelRegistry) registerBuiltin(entry PanelEntry) {
	entry.tier = 0
	r.mu.Lock()
	r.entries = append(r.entries, entry)
	r.mu.Unlock()
}

// registerPlugin adds a plugin-owned (tier=1) panel.
// Returns an error when:
//   - id is empty or violates ^[a-z][a-z0-9-]*$
//   - a built-in already owns this id (panels cannot override built-ins)
//   - another plugin already registered this id
func (r *panelRegistry) registerPlugin(entry PanelEntry) error {
	if entry.ID == "" {
		return fmt.Errorf("panel id is required")
	}
	if !panelIDRE.MatchString(entry.ID) {
		return fmt.Errorf("panel id %q must match %s", entry.ID, panelIDRE)
	}
	entry.tier = 1

	r.mu.Lock()
	defer r.mu.Unlock()

	for i, existing := range r.entries {
		if existing.ID != entry.ID {
			continue
		}
		if existing.tier == 0 {
			return fmt.Errorf("panel id %q is owned by a built-in; plugins cannot override built-ins", entry.ID)
		}
		if existing.PluginID != entry.PluginID {
			return fmt.Errorf("panel id %q already registered by plugin %q (caller: %q)", entry.ID, existing.PluginID, entry.PluginID)
		}
		// Same plugin re-registering on reload — idempotent update.
		r.entries[i] = entry
		return nil
	}

	r.entries = append(r.entries, entry)
	return nil
}

// removeByPlugin removes all panels owned by pluginID and returns the count.
func (r *panelRegistry) removeByPlugin(pluginID string) int {
	if pluginID == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	kept := r.entries[:0]
	n := 0
	for _, e := range r.entries {
		if e.PluginID == pluginID {
			n++
		} else {
			kept = append(kept, e)
		}
	}
	r.entries = kept
	return n
}

// snapshot returns a copy of the current entry list.
func (r *panelRegistry) snapshot() []PanelEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PanelEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

// ---- Host surface -------------------------------------------------------

// RegisterPanel adds a plugin-owned right-rail panel to the host registry.
// Called by applyManifestRegistrations at plugin load time.
// Returns an error when the panel declaration is invalid or conflicts.
func (h *Host) RegisterPanel(entry PanelEntry) error {
	if err := h.panels.registerPlugin(entry); err != nil {
		return err
	}
	h.logger.Info("registered panel", "id", entry.ID, "plugin", entry.PluginID, "title", entry.Title)
	return nil
}

// UnregisterPluginPanels removes all panels owned by pluginID.
// Called by UnloadPlugin during hot-unload. Returns the number removed.
func (h *Host) UnregisterPluginPanels(pluginID string) int {
	n := h.panels.removeByPlugin(pluginID)
	if n > 0 {
		h.logger.Debug("plugin unload: removed panels", "plugin", pluginID, "count", n)
	}
	return n
}

// GetPanels returns a snapshot of all registered panels (built-in + plugin).
// Consumed by the /api/plugins/registry endpoint and by the frontend panel
// prefs API so the right-rail tab strip reflects installed plugin panels.
func (h *Host) GetPanels() []PanelEntry {
	return h.panels.snapshot()
}

// registerManifestPanels registers all panels declared in the plugin manifest.
// Mirrors registerManifestCardRules.
func registerManifestPanels(host *Host, manifest *PluginManifest, pluginID string) error {
	for i, p := range manifest.Registers.Panels {
		if p.ID == "" {
			return fmt.Errorf("plugin %q: panels[%d] missing id", pluginID, i)
		}
		order := p.Order
		if order == 0 {
			order = 100 // plugin panels default to 100+ in the sort order
		}
		entry := PanelEntry{
			ID:             p.ID,
			Title:          p.Title,
			PluginID:       pluginID,
			Description:    p.Description,
			Icon:           p.Icon,
			DefaultVisible: p.DefaultVisible,
			Order:          order,
		}
		if err := host.RegisterPanel(entry); err != nil {
			return fmt.Errorf("plugin %q: register panel %q: %w", pluginID, p.ID, err)
		}
	}
	return nil
}
