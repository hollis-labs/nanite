package api

import (
	"encoding/json"
	"net/http"
	"path"
	"sync"

	goplugin "github.com/hollis-labs/plugin-sdk"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
)

// RegistryEnvelopeEntry is one envelope entry in the /api/plugins/registry
// response. Mirrors the shape in plan §B.7 / finding #6.
type RegistryEnvelopeEntry struct {
	PluginID  string `json:"plugin_id"`
	Component string `json:"component"`
	Version   int    `json:"version"`
	SchemaURL string `json:"schema_url,omitempty"`
}

// RegistryWidgetEntry is one widget entry in the /api/plugins/registry
// response. Widgets are a subset of host.GetUIComponents filtered to type
// "widget" — other UIComponentType values (action, view, workflow, envelope)
// are not exposed under this key.
type RegistryWidgetEntry struct {
	PluginID    string `json:"plugin_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// RegistrySlotEntry is one slot entry in the /api/plugins/registry response.
// The outer shape maps slot name → ordered list of entries; order is the
// priority-sorted order the host already maintains.
type RegistrySlotEntry struct {
	ID        string                 `json:"id"`
	PluginID  string                 `json:"plugin_id"`
	Label     string                 `json:"label,omitempty"`
	Icon      string                 `json:"icon,omitempty"`
	Priority  int                    `json:"priority,omitempty"`
	Component string                 `json:"component,omitempty"`
	Action    string                 `json:"action,omitempty"`
	Props     map[string]interface{} `json:"props,omitempty"`
}

// RegistryPluginEntry is one plugin entry in the /api/plugins/registry
// response describing bundle + stylesheet URLs and metadata the frontend
// needs to load the plugin's UI.
type RegistryPluginEntry struct {
	BundleURL     string `json:"bundle_url,omitempty"`
	StylesheetURL string `json:"stylesheet_url,omitempty"`
	BundleHash    string `json:"bundle_hash"`    // always present; empty until manifest schema grows a field (see backlog)
	ReactVersion  string `json:"react_version"`  // always present; empty when manifest omits ui.react_version
}

// RegistryResponse is the top-level shape for GET /api/plugins/registry.
// All four keys are non-nil maps even when empty so the frontend can treat
// them as stable dictionaries.
type RegistryResponse struct {
	Envelopes map[string]RegistryEnvelopeEntry `json:"envelopes"`
	Widgets   map[string]RegistryWidgetEntry   `json:"widgets"`
	Slots     map[string][]RegistrySlotEntry   `json:"slots"`
	Plugins   map[string]RegistryPluginEntry   `json:"plugins"`
}

// registryCache caches the last-computed response keyed by the host's
// registry version counter. On cache miss (version changed, or first call)
// the handler recomputes and atomically swaps. B.8 can invalidate from the
// outside via host.BumpRegistryVersion without touching this cache directly.
//
// A per-handler struct (not a package-level singleton) keeps state owned by
// the route registration so tests that spin up multiple hosts don't share
// cached state across them.
type registryCache struct {
	mu      sync.Mutex
	version uint64
	payload []byte // serialized JSON
	present bool
}

// registerPluginsRegistryRoute wires GET /api/plugins/registry onto mux.
// Split out of RegisterPluginManagementRoutes so the aggregation/cache code
// lives in its own file per plan §B.7.
func registerPluginsRegistryRoute(mux *http.ServeMux, host *naniteplugin.Host, pluginsDir string) {
	cache := &registryCache{}
	mux.HandleFunc("GET /api/plugins/registry", func(w http.ResponseWriter, r *http.Request) {
		payload, err := cache.serve(host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	})
}

// serve returns the cached JSON payload, recomputing when the host's
// registryVersion has advanced since the last call. The host may be nil in
// tests that exercise an empty response — in that case we emit the bare-skeleton
// response with all maps present-but-empty.
func (c *registryCache) serve(host *naniteplugin.Host) ([]byte, error) {
	var version uint64
	if host != nil {
		version = host.RegistryVersion()
	}

	c.mu.Lock()
	if c.present && c.version == version {
		payload := c.payload
		c.mu.Unlock()
		return payload, nil
	}
	c.mu.Unlock()

	resp := buildRegistryResponse(host)
	buf, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	// Only install if we still have the version that produced this payload —
	// a concurrent Load that bumped version between our compute and install
	// would otherwise sticky-cache a stale snapshot. If version moved, drop
	// this result and let the next caller recompute.
	currentVersion := uint64(0)
	if host != nil {
		currentVersion = host.RegistryVersion()
	}
	if currentVersion == version {
		c.version = version
		c.payload = buf
		c.present = true
	}
	c.mu.Unlock()
	return buf, nil
}

// buildRegistryResponse computes the registry response from the host's
// in-memory registries. Extracted so tests can call it directly without a
// live HTTP handler. Never returns nil maps — the frontend relies on the
// four top-level keys always being present.
func buildRegistryResponse(host *naniteplugin.Host) RegistryResponse {
	resp := RegistryResponse{
		Envelopes: make(map[string]RegistryEnvelopeEntry),
		Widgets:   make(map[string]RegistryWidgetEntry),
		Slots:     make(map[string][]RegistrySlotEntry),
		Plugins:   make(map[string]RegistryPluginEntry),
	}
	if host == nil {
		return resp
	}

	// Envelopes — keyed by envelope type. schema_url is a best-effort URL under
	// the existing /api/plugins/{name}/ui/... serving route; if no schema path
	// is recorded we omit the field.
	for _, e := range host.GetEnvelopes() {
		entry := RegistryEnvelopeEntry{
			PluginID:  e.PluginID,
			Component: e.Component,
			Version:   e.Version,
		}
		if e.SchemaPath != "" {
			entry.SchemaURL = path.Join("/api/plugins", e.PluginID, "ui", e.SchemaPath)
		}
		resp.Envelopes[e.Type] = entry
	}

	// Widgets — type == "widget" filter over UI components. Other types
	// (action, view, workflow, envelope) are not surfaced under this key; the
	// envelope map already covers envelopes, and the remaining types are not
	// part of the B.7 shape per finding #6.
	for _, comp := range host.GetUIComponentsWithOwners() {
		if comp.Type != goplugin.UIComponentTypeWidget {
			continue
		}
		resp.Widgets[comp.ID] = RegistryWidgetEntry{
			PluginID:    comp.PluginID,
			Name:        comp.Name,
			Description: comp.Description,
		}
	}

	// Slots — keep the host's priority-sorted ordering.
	for slot, entries := range host.GetAllSlots() {
		out := make([]RegistrySlotEntry, 0, len(entries))
		for _, entry := range entries {
			out = append(out, RegistrySlotEntry{
				ID:        entry.ID,
				PluginID:  entry.PluginID,
				Label:     entry.Label,
				Icon:      entry.Icon,
				Priority:  entry.Priority,
				Component: entry.Component,
				Action:    entry.Action,
				Props:     entry.Props,
			})
		}
		resp.Slots[string(slot)] = out
	}

	// Plugins — bundle / stylesheet URLs derive from ui.bundle_dir + ui.entry
	// and ui.stylesheet in the plugin manifest. Match the existing serving
	// route GET /api/plugins/{name}/ui/{file...} (see plugins.go). When the
	// manifest has no ui block we still emit the plugin entry with empty
	// string fields so the frontend can reason about presence uniformly.
	//
	// bundle_hash: the v1 manifest schema does not yet include a checksum
	// field for the bundle. Returning empty string + filing a backlog item
	// rather than extending schema mid-B.7.
	for pluginID, manifest := range host.GetManifests() {
		entry := RegistryPluginEntry{}
		ui := manifest.UI
		if ui.Entry != "" {
			entry.BundleURL = buildUIURL(pluginID, ui.BundleDir, ui.Entry)
		}
		if ui.Stylesheet != "" {
			entry.StylesheetURL = buildUIURL(pluginID, ui.BundleDir, ui.Stylesheet)
		}
		entry.ReactVersion = ui.ReactVersion
		// BundleHash stays empty until the manifest schema gains a field. See
		// backlog: "v1 manifest needs ui.bundle_hash / release.bundle_sha256
		// for the B.7 registry endpoint".
		resp.Plugins[pluginID] = entry
	}
	return resp
}

// buildUIURL composes the /api/plugins/{plugin}/ui/{file} path served by the
// existing plugins.go handler. bundleDir is the in-repo subdir (e.g. "ui/dist")
// — the host-side serving route at plugins.go:83-96 rewrites /ui/... to the
// plugin's on-disk ui/ directory, so we need to strip any leading "ui/"
// segment from bundleDir before joining (otherwise the frontend would fetch
// /api/plugins/foo/ui/ui/dist/index.js which the serving route treats as
// plugins/foo/ui/ui/dist/index.js on disk).
func buildUIURL(pluginID, bundleDir, file string) string {
	rel := path.Join(bundleDir, file)
	// Strip leading "ui/" if present — the serve route already prefixes "ui/".
	if len(rel) >= 3 && rel[:3] == "ui/" {
		rel = rel[3:]
	}
	return path.Join("/api/plugins", pluginID, "ui", rel)
}
