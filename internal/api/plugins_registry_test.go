package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/pkg/pluginapi"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	goplugin "github.com/hollis-labs/plugin-sdk"
)

// TestPluginsRegistry_EmptyHost asserts the response shape carries all four
// top-level keys as non-nil empty maps when no plugins are loaded. The
// frontend treats these keys as stable dictionaries; nulls here would be a
// breaking regression.
func TestPluginsRegistry_EmptyHost(t *testing.T) {
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))

	mux := http.NewServeMux()
	registerPluginsRegistryRoute(mux, host, t.TempDir())

	req := httptest.NewRequest("GET", "/api/plugins/registry", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp RegistryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Envelopes == nil {
		t.Error("envelopes must be {} not null")
	}
	if resp.Widgets == nil {
		t.Error("widgets must be {} not null")
	}
	if resp.Slots == nil {
		t.Error("slots must be {} not null")
	}
	if resp.Plugins == nil {
		t.Error("plugins must be {} not null")
	}
	if len(resp.Envelopes) != 0 || len(resp.Widgets) != 0 || len(resp.Slots) != 0 || len(resp.Plugins) != 0 {
		t.Errorf("expected all maps empty, got %+v", resp)
	}
}

// TestPluginsRegistry_EnvelopeAndSlotFromDiscovered drops a synthetic plugin
// on disk with envelope + slot registrations and a ui block, runs it through
// DiscoverPlugins / LoadDiscovered, then asserts the three populated-map
// paths (envelopes, slots, plugins.bundle_url/stylesheet/react_version).
func TestPluginsRegistry_EnvelopeAndSlotFromDiscovered(t *testing.T) {
	pluginsDir := t.TempDir()
	pluginID := "synth-registry-plugin"
	pluginPath := filepath.Join(pluginsDir, pluginID)
	if err := os.MkdirAll(pluginPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAPIPluginBundle(t, pluginPath, pluginID, "Synthetic Plugin", pluginapi.Block{UI: pluginapi.UI{Bundle: "ui/dist/index.js", Stylesheet: "ui/dist/style.css", ReactVersion: "^19.0.0"}, Registers: pluginapi.Registrations{Envelopes: []pluginapi.Envelope{{Type: "synth-card", Component: "SynthCard", Version: 1, Schema: "schemas/synth-card.json"}}, Slots: []pluginapi.Slot{{ID: "synth-slot-entry", Slot: "composer-toolbar", Component: "SynthToolbarButton", Priority: 10}}}})

	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	discovered, err := naniteplugin.DiscoverPlugins(pluginsDir)
	if err != nil {
		t.Fatalf("DiscoverPlugins: %v", err)
	}
	t.Cleanup(func() { _ = host.UnloadPlugin(pluginID) })
	loaded, loadErrs := naniteplugin.LoadDiscovered(host, discovered)
	if len(loadErrs) > 0 {
		t.Fatalf("LoadDiscovered errs: %v", loadErrs)
	}
	if len(loaded) == 0 {
		t.Fatal("LoadDiscovered loaded no plugins")
	}

	resp := buildRegistryResponse(host, "")

	env, ok := resp.Envelopes["synth-card"]
	if !ok {
		t.Fatalf("envelopes['synth-card'] missing; got %+v", resp.Envelopes)
	}
	if env.Component != "SynthCard" || env.Version != 1 || env.PluginID != pluginID {
		t.Errorf("envelope fields wrong: %+v", env)
	}
	if env.SchemaURL == "" {
		t.Error("schema_url should be populated when manifest includes a schema path")
	}

	slotEntries, ok := resp.Slots["composer-toolbar"]
	if !ok || len(slotEntries) == 0 {
		t.Fatalf("slots['composer-toolbar'] missing/empty; got %+v", resp.Slots)
	}
	if slotEntries[0].ID != "synth-slot-entry" || slotEntries[0].Component != "SynthToolbarButton" {
		t.Errorf("slot entry fields wrong: %+v", slotEntries[0])
	}

	pl, ok := resp.Plugins[pluginID]
	if !ok {
		t.Fatalf("plugins[%q] missing; got %+v", pluginID, resp.Plugins)
	}
	wantBundle := "/api/plugins/" + pluginID + "/bundle/ui/dist/index.js"
	if pl.BundleURL != wantBundle {
		t.Errorf("bundle_url mismatch: got %q want %q", pl.BundleURL, wantBundle)
	}
	wantSheet := "/api/plugins/" + pluginID + "/bundle/ui/dist/style.css"
	if pl.StylesheetURL != wantSheet {
		t.Errorf("stylesheet_url mismatch: got %q want %q", pl.StylesheetURL, wantSheet)
	}
	if pl.ReactVersion != "^19.0.0" {
		t.Errorf("react_version mismatch: got %q", pl.ReactVersion)
	}
	// bundle_hash stays empty — manifest v1 does not carry this field yet.
	if pl.BundleHash != "" {
		t.Errorf("bundle_hash should be empty until schema extends; got %q", pl.BundleHash)
	}

	// End-to-end: exercise the HTTP handler before and after UnloadPlugin to
	// confirm cache invalidation via RegistryVersion flips entries off.
	mux := http.NewServeMux()
	registerPluginsRegistryRoute(mux, host, pluginsDir)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/plugins/registry", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/plugins/registry: %d %s", rec.Code, rec.Body.String())
	}
	var live RegistryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &live); err != nil {
		t.Fatalf("decode live resp: %v", err)
	}
	if _, ok := live.Plugins[pluginID]; !ok {
		t.Errorf("HTTP handler missing plugin %q: %+v", pluginID, live.Plugins)
	}

	prevVersion := host.RegistryVersion()
	if err := host.UnloadPlugin(pluginID); err != nil {
		t.Fatalf("UnloadPlugin: %v", err)
	}
	if host.RegistryVersion() == prevVersion {
		t.Error("UnloadPlugin did not bump RegistryVersion")
	}

	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest("GET", "/api/plugins/registry", nil))
	var after RegistryResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode post-unload resp: %v", err)
	}
	if _, ok := after.Plugins[pluginID]; ok {
		t.Errorf("plugin %q should be gone from registry after unload", pluginID)
	}
	if _, ok := after.Envelopes["synth-card"]; ok {
		t.Errorf("envelope 'synth-card' should be gone after unload")
	}
	if entries, ok := after.Slots["composer-toolbar"]; ok && len(entries) > 0 {
		t.Errorf("slot 'composer-toolbar' should be empty after unload: %+v", entries)
	}
}

// synthPlugin is a minimal plugin.Plugin used by the discovered-loader test.
type synthPlugin struct {
	id string
}

func (s *synthPlugin) ID() string                 { return s.id }
func (s *synthPlugin) Name() string               { return s.id }
func (s *synthPlugin) Version() string            { return "0.0.1" }
func (s *synthPlugin) Description() string        { return "synth" }
func (s *synthPlugin) Dependencies() []string     { return nil }
func (s *synthPlugin) Load(h goplugin.Host) error { return nil }
func (s *synthPlugin) Unload() error              { return nil }
func (s *synthPlugin) Status() goplugin.PluginStatus {
	return goplugin.PluginStatus{Loaded: true, Enabled: true}
}
