package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"sync"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	goplugin "github.com/hollis-labs/plugin-sdk"
	"github.com/hollis-labs/plugin-sdk/registry"
)

// RegistryEnvelopeEntry is one envelope entry in the /api/plugins/registry
// contribution metadata; the shared SDK owns the surrounding wire shape.
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
// Contributions use a flat slot-name/entry-ID key; the metadata retains
// Nanite presentation fields and priority.
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

// RegistryPanelEntry holds Nanite presentation metadata for a panel export.
type RegistryPanelEntry struct {
	Title          string `json:"title"`
	Description    string `json:"description,omitempty"`
	Icon           string `json:"icon,omitempty"`
	DefaultVisible bool   `json:"default_visible"`
	Order          int    `json:"order"`
}

// RegistryResponse uses the released host-neutral browser wire contract.
type RegistryResponse = registry.Response

// registryCache caches the last-computed response keyed by the host's
// registry version counter. On cache miss (version changed, or first call)
// the handler recomputes and atomically swaps. B.8 can invalidate from the
// outside via host.BumpRegistryVersion without touching this cache directly.
//
// A per-handler struct (not a package-level singleton) keeps state owned by
// the route registration so tests that spin up multiple hosts don't share
// cached state across them.
type registryCache struct {
	mu         sync.Mutex
	version    uint64
	payload    []byte // serialized JSON
	present    bool
	pluginsDir string
}

// registerPluginsRegistryRoute wires GET /api/plugins/registry onto mux.
// Split out of RegisterPluginManagementRoutes so the aggregation/cache code
// lives in its own file per plan §B.7.
func registerPluginsRegistryRoute(mux *http.ServeMux, host *naniteplugin.Host, pluginsDir string) {
	cache := &registryCache{pluginsDir: pluginsDir}
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
// shared response with both maps present-but-empty.
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

	resp, buildErr := buildRegistryResponse(host, c.pluginsDir)
	if buildErr != nil {
		return nil, buildErr
	}
	if err := resp.Validate(); err != nil {
		return nil, err
	}
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
// shared top-level maps always being present.
func buildRegistryResponse(host *naniteplugin.Host, pluginsDir string) (RegistryResponse, error) {
	response := registry.NewResponse()
	if host == nil {
		return response, nil
	}
	for id, declaration := range host.GetManifests() {
		item := registry.Plugin{}
		if declaration.UI.Entry != "" {
			item.BundleURL = buildUIURL(id, declaration.UI.BundleDir, declaration.UI.Entry)
		}
		if declaration.UI.Stylesheet != "" {
			item.StylesheetURL = buildUIURL(id, declaration.UI.BundleDir, declaration.UI.Stylesheet)
		}
		if declaration.UI.ReactVersion != "" {
			item.Runtime = &registry.Runtime{Name: "react", Version: declaration.UI.ReactVersion}
		}
		if declaration.Shared != nil {
			if declaration.UI.Entry != "" {
				item.BundleURL = path.Join("/api/plugins", id, "bundle", declaration.UI.Entry)
			}
			if declaration.UI.Stylesheet != "" {
				item.StylesheetURL = path.Join("/api/plugins", id, "bundle", declaration.UI.Stylesheet)
			}
			if pluginsDir != "" {
				if approval, err := naniteplugin.ReadInstallApproval(pluginsDir, id); err == nil {
					item.BundleVersion = approval.Review.BundleDigest
					if item.StylesheetURL != "" {
						item.StylesheetURL += "?v=" + url.QueryEscape(item.BundleVersion)
					}
				}
			}
		}
		response.Plugins[id] = item
	}
	for _, envelope := range host.GetEnvelopes() {
		if envelope.Component == "" || envelope.PluginID == "" {
			continue
		}
		metadata := RegistryEnvelopeEntry{PluginID: envelope.PluginID, Component: envelope.Component, Version: envelope.Version}
		if envelope.SchemaPath != "" {
			metadata.SchemaURL = path.Join("/api/plugins", envelope.PluginID, "ui", envelope.SchemaPath)
			if declaration := host.GetManifest(envelope.PluginID); declaration != nil && declaration.Shared != nil {
				metadata.SchemaURL = path.Join("/api/plugins", envelope.PluginID, "schema", envelope.Type)
			}
		}
		if contributionErr := addRegistryContribution(&response, "envelope", envelope.Type, envelope.PluginID, envelope.Component, metadata); contributionErr != nil {
			return registry.Response{}, contributionErr
		}
	}
	for _, component := range host.GetUIComponentsWithOwners() {
		if component.Type != goplugin.UIComponentTypeWidget {
			continue
		}
		// An explicit export belongs to the host declaration. Display names and
		// widget IDs cannot be guessed into JavaScript identifiers.
		declaration := host.GetManifest(component.PluginID)
		if declaration == nil {
			continue
		}
		for _, declared := range declaration.Registers.Components {
			if declared.Name == component.ID && declared.Export != "" {
				if contributionErr := addRegistryContribution(&response, "widget", component.ID, component.PluginID, declared.Export, RegistryWidgetEntry{PluginID: component.PluginID, Name: component.Name, Description: component.Description}); contributionErr != nil {
					return registry.Response{}, contributionErr
				}
			}
		}
	}
	for slot, entries := range host.GetAllSlots() {
		for _, entry := range entries {
			if entry.Component == "" || entry.PluginID == "" {
				continue
			}
			metadata := struct {
				RegistrySlotEntry
				Slot string `json:"slot"`
			}{RegistrySlotEntry: RegistrySlotEntry{ID: entry.ID, PluginID: entry.PluginID, Label: entry.Label, Icon: entry.Icon, Priority: entry.Priority, Component: entry.Component, Action: entry.Action, Props: entry.Props}, Slot: string(slot)}
			if contributionErr := addRegistryContribution(&response, "slot", string(slot)+"/"+entry.ID, entry.PluginID, entry.Component, metadata); contributionErr != nil {
				return registry.Response{}, contributionErr
			}
		}
	}
	for _, panel := range host.GetPanels() {
		if panel.PluginID == "" || panel.Component == "" {
			continue
		}
		metadata := RegistryPanelEntry{Title: panel.Title, Description: panel.Description, Icon: panel.Icon, DefaultVisible: panel.DefaultVisible, Order: panel.Order}
		if contributionErr := addRegistryContribution(&response, "panel", panel.ID, panel.PluginID, panel.Component, metadata); contributionErr != nil {
			return registry.Response{}, contributionErr
		}
	}
	return response, nil
}

func addRegistryContribution(response *registry.Response, kind, key, owner, export string, metadata any) error {
	raw, err := registry.Meta(metadata)
	if err != nil {
		return err
	}
	if _, exists := response.Plugins[owner]; !exists {
		response.Plugins[owner] = registry.Plugin{}
	}
	response.Set(kind, key, registry.Contribution{PluginID: owner, Export: export, Meta: raw})
	return nil
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
