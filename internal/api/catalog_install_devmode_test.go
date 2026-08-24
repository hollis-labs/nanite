//go:build devmode

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// Regression coverage for AD-25
// (TASKS/audit-remediation/01-plugin-install-convergence/02-wire-allow-
// unsigned-plugins-setting.md): user_settings.allow_unsigned_plugins must
// reach handleCatalogInstall's SignatureVerifier construction, in a
// devmode-tagged build. This file has the `devmode` build tag, mirroring
// verify_dev_bypass_test.go's own pattern in internal/plugin/install.

// seedUserSettingsRow inserts the user_settings singleton row (id=1) so
// cs.store.GetUserSettings()/UpdateUserSettings() have a row to operate on.
// setupCatalogTestState's store.New (unlike cmdServe's boot path) does not
// run Store.Seed, and these tests register their catalog source explicitly.
func seedUserSettingsRow(t *testing.T, cs *catalogState) {
	t.Helper()
	if _, err := cs.store.DB.Exec(`INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); err != nil {
		t.Fatalf("seed user_settings row: %v", err)
	}
}

// TestHandleCatalogInstall_DevmodeAllowsUnsignedWhenSettingEnabled is the
// positive counterpart to TestHandleCatalogInstall_RejectsUnsignedEntry
// (catalog_install_test.go, production/no-tag): the exact same unsigned
// catalog entry that a production build rejects must install successfully
// in a devmode build once user_settings.allow_unsigned_plugins=true.
func TestHandleCatalogInstall_DevmodeAllowsUnsignedWhenSettingEnabled(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)
	seedUserSettingsRow(t, cs)

	us, err := cs.store.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	us.AllowUnsignedPlugins = true
	if err := cs.store.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}

	archive := buildTarGzArchive(t, map[string]string{"plugin.yaml": minimalCatalogPluginManifest("testplug")})
	sum := sha256.Sum256(archive)
	shaHex := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/testplug.tar.gz", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// No `signature:` field, same as TestHandleCatalogInstall_RejectsUnsignedEntry.
	catalogYAML := fmt.Sprintf(`version: 1
plugins:
  - name: testplug
    version: "1.0.0"
    description: test plugin
    archive_url: %s/testplug.tar.gz
    checksum: "sha256:%s"
`, srv.URL, shaHex)
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })

	addCatalogSource(t, cs, srv, "" /* no public key configured */)

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "testplug"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for an unsigned entry under devmode+allow_unsigned_plugins=true, got %d: %s", rec.Code, rec.Body.String())
	}
	if !fileExists(filepath.Join(pluginsDir, "testplug", "plugin.yaml")) {
		t.Error("expected plugin.yaml to be installed under pluginsDir")
	}
}

// TestHandleCatalogInstall_DevmodeStillRejectsUnsignedWhenSettingDisabled
// asserts the devmode build tag alone is not sufficient — the operator must
// also opt in via user_settings.allow_unsigned_plugins. Default (false, per
// the migration default and UpdateUserSettings's zero-value column) must
// still reject an unsigned entry even under a devmode build.
func TestHandleCatalogInstall_DevmodeStillRejectsUnsignedWhenSettingDisabled(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)
	seedUserSettingsRow(t, cs)
	// AllowUnsignedPlugins left at its default (false) -- confirm the row
	// really is false, since this test's whole point is that default.
	us, err := cs.store.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	if us.AllowUnsignedPlugins {
		t.Fatal("test precondition failed: expected AllowUnsignedPlugins to default to false")
	}

	archive := buildTarGzArchive(t, map[string]string{"plugin.yaml": minimalCatalogPluginManifest("testplug")})
	sum := sha256.Sum256(archive)
	shaHex := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/testplug.tar.gz", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	catalogYAML := fmt.Sprintf(`version: 1
plugins:
  - name: testplug
    version: "1.0.0"
    description: test plugin
    archive_url: %s/testplug.tar.gz
    checksum: "sha256:%s"
`, srv.URL, shaHex)
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })

	addCatalogSource(t, cs, srv, "")

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "testplug"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)

	if rec.Code >= 200 && rec.Code < 300 {
		t.Fatalf("expected a non-2xx response for an unsigned entry when allow_unsigned_plugins=false, got %d: %s", rec.Code, rec.Body.String())
	}
	if fileExists(filepath.Join(pluginsDir, "testplug", "plugin.yaml")) {
		t.Error("plugin must not be written when the setting is disabled, even under devmode")
	}
}
