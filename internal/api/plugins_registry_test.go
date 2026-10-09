package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/pkg/pluginapi"

	goplugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/registry"
	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
)

func TestPluginPanelUpdatesInvalidateRegistryCache(t *testing.T) {
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	mux := http.NewServeMux()
	root := t.TempDir()
	pluginDir := filepath.Join(root, "docs-plugin")
	if err := os.Mkdir(pluginDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := writeAPIPluginBundle(t, pluginDir, "docs-plugin", "Docs", pluginapi.Block{UI: pluginapi.UI{Bundle: "ui/index.js"}, Registers: pluginapi.Registrations{Panels: []pluginapi.Panel{{ID: "docs", Title: "Docs", Component: "First"}}}})
	state := &pluginManagerState{pluginHost: host}
	if !state.runPluginLoadIntoHost(path, pluginDir) {
		t.Fatal("fixture load failed")
	}
	t.Cleanup(func() { _ = host.UnloadPlugin("docs-plugin") })
	registerPluginsRegistryRoute(mux, host, root)
	read := func() RegistryResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/plugins/registry", nil))
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Body.String())
		}
		var response RegistryResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	_ = read()
	if err := host.RegisterPanel(naniteplugin.PanelEntry{ID: "docs", PluginID: "docs-plugin", Component: "First"}); err != nil {
		t.Fatal(err)
	}
	if actual := read().Contributions["panel"][registry.QualifiedKey("docs-plugin", "docs")].Component.Export; actual != "First" {
		t.Fatalf("export = %q", actual)
	}
	if err := host.RegisterPanel(naniteplugin.PanelEntry{ID: "docs", PluginID: "docs-plugin", Component: "Second"}); err != nil {
		t.Fatal(err)
	}
	if actual := read().Contributions["panel"][registry.QualifiedKey("docs-plugin", "docs")].Component.Export; actual != "Second" {
		t.Fatalf("export = %q", actual)
	}
	host.UnregisterPluginPanels("docs-plugin")
	if _, present := read().Contributions["panel"][registry.QualifiedKey("docs-plugin", "docs")]; present {
		t.Fatal("removed panel remained cached")
	}
}

// TestPluginsRegistry_EmptyHost asserts the response carries both shared
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
	if resp.Plugins == nil || resp.Contributions == nil {
		t.Fatal("registry maps must be present")
	}
	if len(resp.Plugins) != 0 || len(resp.Contributions) != 0 {
		t.Fatalf("nonempty registry: %+v", resp)
	}
	if err := resp.Validate(); err != nil {
		t.Fatal(err)
	}
}

// TestPluginsRegistry_EnvelopeAndSlotFromDiscovered drops a synthetic plugin
// on disk with envelope + slot registrations and a ui block, runs it through
// DiscoverPlugins / LoadDiscovered, then asserts the three populated-map
// shared contributions and runtime metadata.
func TestPluginsRegistry_EnvelopeAndSlotFromDiscovered(t *testing.T) {
	pluginsDir := t.TempDir()
	pluginID := "synth-registry-plugin"
	pluginPath := filepath.Join(pluginsDir, pluginID)
	if err := os.MkdirAll(pluginPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAPIPluginBundle(t, pluginPath, pluginID, "Synthetic Plugin", pluginapi.Block{UI: pluginapi.UI{Bundle: "ui/dist/index.js", Stylesheet: "ui/dist/style.css", ReactVersion: "^19.0.0"}, Registers: pluginapi.Registrations{Envelopes: []pluginapi.Envelope{{Type: "synth-card", Component: "SynthCard", Version: 1, Schema: "schemas/synth-card.json"}}, Slots: []pluginapi.Slot{{ID: "synth-slot-entry", Slot: "composer-toolbar", Component: "SynthToolbarButton", Priority: 10}}, Panels: []pluginapi.Panel{{ID: "synth-panel", Title: "Synthetic panel", Component: "SynthPanel", Icon: "file-text", Order: 120, DefaultVisible: true}}}})

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

	resp, buildErr := buildRegistryResponse(host, pluginsDir)
	if buildErr != nil {
		t.Fatal(buildErr)
	}

	env, ok := resp.Contributions["envelope"][registry.QualifiedKey(pluginID, "synth-card")]
	if !ok {
		t.Fatalf("envelopes['synth-card'] missing; got %+v", resp.Contributions["envelope"])
	}
	if env.Component.Export != "SynthCard" || env.OwnerID != pluginID {
		t.Errorf("envelope fields wrong: %+v", env)
	}
	var envelopeMeta RegistryEnvelopeEntry
	if decodeErr := json.Unmarshal(env.Metadata, &envelopeMeta); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if envelopeMeta.SchemaURL == "" {
		t.Error("schema_url should be populated when manifest includes a schema path")
	}

	slotEntry, ok := resp.Contributions["slot"][registry.QualifiedKey(pluginID, "slot-"+hex.EncodeToString([]byte("composer-toolbar/synth-slot-entry")))]
	if !ok || slotEntry.Component.Export != "SynthToolbarButton" || slotEntry.OwnerID != pluginID {
		t.Fatalf("slot contribution: %+v", slotEntry)
	}

	panel, ok := resp.Contributions["panel"][registry.QualifiedKey(pluginID, "synth-panel")]
	if !ok || panel.Component.Export != "SynthPanel" || panel.OwnerID != pluginID {
		t.Fatalf("panel = %+v", panel)
	}
	var panelMeta RegistryPanelEntry
	if decodeErr := json.Unmarshal(panel.Metadata, &panelMeta); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if panelMeta.Title != "Synthetic panel" || panelMeta.Icon != "file-text" || panelMeta.Order != 120 || !panelMeta.DefaultVisible {
		t.Fatalf("panel metadata = %+v", panelMeta)
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
	if !strings.HasPrefix(pl.StylesheetURL, wantSheet+"?v=") {
		t.Errorf("stylesheet_url mismatch: got %q want %q", pl.StylesheetURL, wantSheet)
	}
	if len(pl.Runtime) != 0 {
		t.Fatalf("caret range was misrepresented as SDK inclusive bounds: %+v", pl.Runtime)
	}
	var metadata map[string]any
	if decodeErr := json.Unmarshal(panel.Metadata, &metadata); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if metadata["react_range"] != "^19.0.0" {
		t.Fatalf("range lost: %+v", metadata)
	}
	bundleRoot, openErr := os.OpenRoot(pluginPath)
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() {
		if closeErr := bundleRoot.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	js, err := bundleRoot.ReadFile("ui/dist/index.js")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(js)
	if pl.BundleVersion != "sha256:"+hex.EncodeToString(digest[:]) {
		t.Fatalf("bundle digest does not identify actual JavaScript bytes: %q", pl.BundleVersion)
	}
	if resp.HostInstance == "" || pl.OwnerGeneration == "" || resp.RegistryVersion != registry.RegistryVersion {
		t.Fatalf("missing actual lifecycle identity: %+v", resp)
	}
	if err := resp.Validate(); err != nil {
		t.Fatal(err)
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
	if _, present := after.Contributions["panel"][registry.QualifiedKey(pluginID, "synth-panel")]; present {
		t.Fatal("panel remained after unload")
	}
	if _, ok := after.Contributions["envelope"][registry.QualifiedKey(pluginID, "synth-card")]; ok {
		t.Errorf("envelope 'synth-card' should be gone after unload")
	}
	if _, ok := after.Contributions["slot"][registry.QualifiedKey(pluginID, "slot-"+hex.EncodeToString([]byte("composer-toolbar/synth-slot-entry")))]; ok {
		t.Errorf("slot 'composer-toolbar' should be empty after unload: %+v", after.Contributions["slot"])
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

func TestPluginsRegistryRejectsUnserializableHostMetadata(t *testing.T) {
	host := naniteplugin.NewHost(http.NewServeMux(), naniteplugin.NewLogger("test"))
	if err := host.RegisterSlot(naniteplugin.UISlotEntry{ID: "bad-meta", PluginID: "example", Slot: "composer-toolbar", Label: "Bad", Component: "Bad", Props: map[string]any{"invalid": make(chan int)}}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerPluginsRegistryRoute(mux, host, t.TempDir())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/plugins/registry", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("invalid metadata silently dropped: %d %s", rec.Code, rec.Body.String())
	}
}
