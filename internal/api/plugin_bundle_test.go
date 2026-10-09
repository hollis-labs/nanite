package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/nanite/internal/plugin/plugintest"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestPluginBundle_ServesDeclaredPathsAndConfinesAssets(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "example")
	block, err := pluginapi.EncodeBlock(pluginapi.Block{UI: pluginapi.UI{Bundle: "ui/web/plugin.js", Stylesheet: "ui/sheets/style.css"}, Registers: pluginapi.Registrations{Envelopes: []pluginapi.Envelope{{Type: "example-card", Component: "Card", Version: 1, Schema: "schemas/card.json"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var declaration map[string]json.RawMessage
	if decodeErr := json.Unmarshal([]byte(minimalCatalogPluginManifest(t, "example")), &declaration); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	declaration["nanite"] = block
	declaration["ui"] = json.RawMessage(`{"bundle":"ui/web/plugin.js","stylesheet":"ui/sheets/style.css","isolation":"main-origin"}`)
	raw, err := json.Marshal(declaration)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"bin/plugin": catalogExecutable, "README.md": "hello", "ui/web/plugin.js": "export {};", "ui/web/chunk.js": "export {};", "ui/sheets/style.css": "body {}", "config.yaml": "private", "schemas/card.json": `{"type":"object"}`}
	for name, content := range files {
		target := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0600)
		if strings.HasPrefix(name, "bin/") {
			mode = 0700
		}
		if err := os.WriteFile(target, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	var common manifest.Manifest
	if err := json.Unmarshal(raw, &common); err != nil {
		t.Fatal(err)
	}
	plugintest.Inventory(t, &common, directory)
	var encoded strings.Builder
	if err := manifest.Encode(&encoded, common); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(encoded.String()), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.js")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "ui/web/escape.js")); err != nil {
		t.Fatal(err)
	}
	pms := &pluginManagerState{pluginsDir: root}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugins/{name}/bundle/{file...}", pms.handlePluginBundle)
	mux.HandleFunc("GET /api/plugins/{name}/schema/{type}", pms.handlePluginSchema)
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"bundle/ui/web/plugin.js", 200}, {"bundle/ui/web/chunk.js", 200}, {"bundle/ui/sheets/style.css", 200},
		{"bundle/config.yaml", 404}, {"bundle/plugin.yaml", 404}, {"bundle/schemas/card.json", 404},
		{"bundle/ui/web/escape.js", 403}, {"schema/example-card", 200}, {"schema/missing", 404},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/plugins/example/"+tc.path, nil))
		if rec.Code != tc.status {
			t.Errorf("%s: status %d want %d: %s", tc.path, rec.Code, tc.status, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "private") {
			t.Errorf("%s exposed private bytes", tc.path)
		}
	}
}
