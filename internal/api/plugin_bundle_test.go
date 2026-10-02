package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestPluginBundle_ServesDeclaredPathsAndConfinesAssets(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "example")
	block, err := pluginapi.EncodeBlock(pluginapi.Block{UI: pluginapi.UI{Bundle: "web/plugin.js", Stylesheet: "sheets/style.css"}, Registers: pluginapi.Registrations{Envelopes: []pluginapi.Envelope{{Type: "example-card", Component: "Card", Version: 1, Schema: "schemas/card.json"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var declaration map[string]json.RawMessage
	if decodeErr := json.Unmarshal([]byte(minimalCatalogPluginManifest("example")), &declaration); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	declaration["nanite"] = block
	raw, err := json.Marshal(declaration)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"plugin.yaml": string(raw), "web/plugin.js": "export {};", "web/chunk.js": "export {};", "sheets/style.css": "body {}", "config.yaml": "private", "schemas/card.json": `{"type":"object"}`}
	for name, content := range files {
		target := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.js")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "web/escape.js")); err != nil {
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
		{"bundle/web/plugin.js", 200}, {"bundle/web/chunk.js", 200}, {"bundle/sheets/style.css", 200},
		{"bundle/config.yaml", 404}, {"bundle/plugin.yaml", 404}, {"bundle/schemas/card.json", 404},
		{"bundle/web/escape.js", 403}, {"schema/example-card", 200}, {"schema/missing", 404},
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
