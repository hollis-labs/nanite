package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

// This optional fixture serves a real loaded SDK subprocess and bundle for the
// browser's live HTTP test. TestMain isolates home/data/cache; no live DB opens.
func TestPluginsRegistryLiveBrowserSmoke(t *testing.T) {
	readyPath := os.Getenv("NANITE_PLUGIN_BROWSER_SMOKE_READY")
	if readyPath == "" {
		t.Skip("live browser fixture is opt-in")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "browser-live")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "ui.js"), []byte(`export function LiveCard() { return "Loaded from Nanite"; }`), 0600); err != nil {
		t.Fatal(err)
	}
	writeAPIPluginBundle(t, directory, "browser-live", "Live browser fixture", pluginapi.Block{UI: pluginapi.UI{Bundle: "ui.js", ReactVersion: "^19.0.0"}, Registers: pluginapi.Registrations{Envelopes: []pluginapi.Envelope{{Type: "browser-live-card", Component: "LiveCard", Version: 1, Schema: "schema.json"}}}})
	mux := http.NewServeMux()
	host := naniteplugin.NewHost(mux, naniteplugin.NewLogger("test"))
	discovered, err := naniteplugin.DiscoverPlugins(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded, failures := naniteplugin.LoadDiscovered(host, discovered); len(failures) > 0 || len(loaded) != 1 {
		t.Fatalf("fixture load: %v %v", loaded, failures)
	}
	t.Cleanup(func() { _ = host.UnloadPlugin("browser-live") })
	RegisterPluginManagementRoutes(mux, root, nil, host)
	stopped := make(chan struct{})
	var once sync.Once
	mux.HandleFunc("POST /smoke/stop", func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(stopped) })
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	file, err := os.OpenFile(readyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) // #nosec G304 G703 -- explicit opt-in test fixture's private readiness file; O_EXCL prevents overwrites.
	if err != nil {
		t.Fatal(err)
	}
	if _, writeErr := file.WriteString(server.URL); writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("browser did not finish the smoke test")
	}
}
