package api

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
)

// Regression coverage for handleCatalogInstall (internal/api/catalog.go),
// added as part of AD-04's convergence of the GUI/API catalog-install path
// onto internal/plugin/install.Installer
// (TASKS/audit-remediation/01-plugin-install-convergence/01-unify-plugin-
// catalog-install-pipeline.md). Before this, handleCatalogInstall had zero
// test coverage at all (confirmed by the 2026-08-21 go-quality audit).

// setupCatalogTestState builds a catalogState backed by a real (temp-file)
// *store.Store — handleCatalogInstall needs catalog_sources rows, which
// pluginManagerState-style in-memory-struct test setups (see
// plugins_install_test.go's setupPluginTestState) don't have a slot for.
func setupCatalogTestState(t *testing.T) (*catalogState, string) {
	t.Helper()
	pluginsDir := t.TempDir()
	dbDir := t.TempDir()
	s, err := store.New(context.Background(), filepath.Join(dbDir, "test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	return &catalogState{
		store:      s,
		fetcher:    naniteplugin.NewCatalogFetcher(5*time.Minute, filepath.Join(pluginsDir, ".cache")),
		pluginsDir: pluginsDir,
		pluginHost: nil,
	}, pluginsDir
}

// addCatalogSource registers a custom catalog source pointing at
// srv.URL+"/catalog.yaml", optionally with a trusted public key.
func addCatalogSource(t *testing.T, cs *catalogState, srv *httptest.Server, publicKeyHex string) *store.CatalogSource {
	t.Helper()
	src, err := cs.store.CreateCatalogSource(context.Background(), "Test Source", srv.URL+"/catalog.yaml", "custom", 100)
	if err != nil {
		t.Fatalf("CreateCatalogSource: %v", err)
	}
	if publicKeyHex != "" {
		if err := cs.store.SetCatalogSourcePublicKey(context.Background(), src.ID, publicKeyHex); err != nil {
			t.Fatalf("SetCatalogSourcePublicKey: %v", err)
		}
	}
	return src
}

// minimalCatalogPluginManifest returns a plugin.yaml that satisfies the v1
// JSON Schema's required fields (schema_version, id, name, version,
// description, author, license, runtime, protocol, nanite_compat) with a
// builtin runtime so no entrypoint/registers are needed.
func minimalCatalogPluginManifest(id string) string {
	return fmt.Sprintf(`schema_version: 1
id: %s
name: Test Plugin
version: 1.0.0
description: test plugin
author: Acme
license: MIT
runtime: builtin
protocol: 1
nanite_compat:
  min: "0.9.0"
`, id)
}

// buildTarGzArchive builds an in-memory .tar.gz with the given files at the
// archive root (no wrapper directory — install.Installer's Validator step
// expects plugin.yaml directly at the extraction root, matching the CLI's
// own TarGzExtractor contract).
func buildTarGzArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(content)), Mode: 0644}); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// buildZipArchive builds an in-memory .zip with the given files at the
// archive root.
func buildZipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// TestHandleCatalogInstall_RejectsUnsignedEntry is the GO-PLUGIN-001
// regression test: a catalog entry with no signature, from a source with no
// configured public key (today's default-seeded "official" source state —
// store.Store.Seed's INSERT never sets catalog_sources.public_key), must be
// rejected outright in a production (non-devmode) build/test run — not
// silently installed. This test file has no `devmode` build tag, so `go
// test ./internal/api/...` (this task's own baseline check) exercises
// exactly the production posture.
func TestHandleCatalogInstall_RejectsUnsignedEntry(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)

	archive := buildTarGzArchive(t, map[string]string{"plugin.yaml": minimalCatalogPluginManifest("testplug")})
	sum := sha256.Sum256(archive)
	shaHex := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/testplug.tar.gz", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// No `signature:` field at all -- the entry carries an integrity
	// checksum but no signature, and its source has no trusted key either.
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

	if rec.Code >= 200 && rec.Code < 300 {
		t.Fatalf("expected a non-2xx response for an unsigned catalog entry, got %d: %s", rec.Code, rec.Body.String())
	}
	if fileExists(filepath.Join(pluginsDir, "testplug", "plugin.yaml")) {
		t.Error("plugin must not be written to pluginsDir when signature verification is rejected")
	}
}

// TestHandleCatalogInstall_PathTraversal is the GO-PLUGIN-002 regression
// test: a catalog entry named with a path-traversal payload must be
// rejected with 400 before any file is written outside pluginsDir, mirroring
// TestHandleInstall_PathTraversal (plugins_install_test.go).
func TestHandleCatalogInstall_PathTraversal(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)

	catalogYAML := `version: 1
plugins:
  - name: "../../etc/passwd"
    version: "1.0.0"
    description: malicious entry
    archive_url: http://example.invalid/x.tar.gz
`
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	addCatalogSource(t, cs, srv, "")

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "../../etc/passwd"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a traversal name, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(resp["error"], "invalid plugin name") {
		t.Errorf("expected error to mention 'invalid plugin name', got %q", resp["error"])
	}

	// Nothing should have been written under (or outside) pluginsDir.
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		t.Fatalf("read pluginsDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != ".cache" {
			t.Errorf("unexpected entry written to pluginsDir: %s", e.Name())
		}
	}
}

// TestHandleCatalogInstall_Success_TarGz is the positive-path regression
// test: a validly-signed, checksummed .tar.gz catalog entry installs
// successfully end-to-end through the converged pipeline. Protects against
// the fail-closed fix (above) becoming fail-always.
func TestHandleCatalogInstall_Success_TarGz(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	archive := buildTarGzArchive(t, map[string]string{"plugin.yaml": minimalCatalogPluginManifest("testplug")})
	sum := sha256.Sum256(archive)
	shaHex := hex.EncodeToString(sum[:])
	sig := ed25519.Sign(priv, archive)
	sigHex := hex.EncodeToString(sig)

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
    signature: "%s"
`, srv.URL, shaHex, sigHex)
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })

	addCatalogSource(t, cs, srv, hex.EncodeToString(pub))

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "testplug"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["plugin"] != "testplug" {
		t.Errorf("expected plugin 'testplug', got %q", resp["plugin"])
	}
	if !fileExists(filepath.Join(pluginsDir, "testplug", "plugin.yaml")) {
		t.Error("expected plugin.yaml to be installed under pluginsDir")
	}
}

// TestHandleCatalogInstall_Success_Zip is AD-04 item 2's required
// regression test: a validly-signed, checksummed .zip catalog entry (as
// opposed to .tar.gz) also installs successfully — the format-dispatching
// catalogExtractor must not silently drop zip support the way a naive
// convergence onto the CLI's tar.gz-only TarGzExtractor would have.
func TestHandleCatalogInstall_Success_Zip(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	archive := buildZipArchive(t, map[string]string{"plugin.yaml": minimalCatalogPluginManifest("zipplug")})
	sum := sha256.Sum256(archive)
	shaHex := hex.EncodeToString(sum[:])
	sig := ed25519.Sign(priv, archive)
	sigHex := hex.EncodeToString(sig)

	mux := http.NewServeMux()
	mux.HandleFunc("/zipplug.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	catalogYAML := fmt.Sprintf(`version: 1
plugins:
  - name: zipplug
    version: "1.0.0"
    description: test zip plugin
    archive_url: %s/zipplug.zip
    checksum: "sha256:%s"
    signature: "%s"
`, srv.URL, shaHex, sigHex)
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })

	addCatalogSource(t, cs, srv, hex.EncodeToString(pub))

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "zipplug"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !fileExists(filepath.Join(pluginsDir, "zipplug", "plugin.yaml")) {
		t.Error("expected plugin.yaml to be installed from a .zip archive")
	}
}

// TestHandleCatalogInstall_Success_WrapperDirectory is the regression test
// for the operator-directed fix restoring the pre-convergence handler's
// "plugin.yaml at root OR single top-level subdirectory" tolerance (git
// show main:internal/api/catalog.go:399-410) — the shape a plain GitHub
// "Download ZIP" produces (reponame-branch/plugin.yaml, not plugin.yaml at
// the archive root). catalogExtractor must detect the single wrapper
// directory and flatten its contents into the plugin's install dir, not
// nest them under a wrapper-dir subpath.
func TestHandleCatalogInstall_Success_WrapperDirectory(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	// plugin.yaml (and a sibling file, to confirm the whole subtree
	// flattens, not just the manifest) live inside a single wrapper
	// directory rather than at the archive root.
	archive := buildZipArchive(t, map[string]string{
		"wrapplug-main/plugin.yaml": minimalCatalogPluginManifest("wrapplug"),
		"wrapplug-main/README.md":   "hello",
	})
	sum := sha256.Sum256(archive)
	shaHex := hex.EncodeToString(sum[:])
	sig := ed25519.Sign(priv, archive)
	sigHex := hex.EncodeToString(sig)

	mux := http.NewServeMux()
	mux.HandleFunc("/wrapplug.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	catalogYAML := fmt.Sprintf(`version: 1
plugins:
  - name: wrapplug
    version: "1.0.0"
    description: test wrapper-directory plugin
    archive_url: %s/wrapplug.zip
    checksum: "sha256:%s"
    signature: "%s"
`, srv.URL, shaHex, sigHex)
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })

	addCatalogSource(t, cs, srv, hex.EncodeToString(pub))

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "wrapplug"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	pluginDir := filepath.Join(pluginsDir, "wrapplug")
	if !fileExists(filepath.Join(pluginDir, "plugin.yaml")) {
		t.Error("expected plugin.yaml to be flattened directly under the plugin install dir, not nested under a wrapper directory")
	}
	if !fileExists(filepath.Join(pluginDir, "README.md")) {
		t.Error("expected README.md to be flattened alongside plugin.yaml")
	}
	if fileExists(filepath.Join(pluginDir, "wrapplug-main", "plugin.yaml")) {
		t.Error("plugin.yaml must not remain nested under the wrapper directory name")
	}

	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		t.Fatalf("read plugin dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "catalog-extract-scratch-") {
			t.Errorf("scratch extraction dir leaked into the committed plugin dir: %s", e.Name())
		}
	}
}

// TestHandleCatalogInstall_WrongSignature asserts a signed-but-tampered (or
// wrong-key-signed) entry is rejected — signature verification must
// actually check the signature, not just require its presence.
func TestHandleCatalogInstall_WrongSignature(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)

	pub, _, err := ed25519.GenerateKey(rand.Reader) // note: unrelated signer
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	archive := buildTarGzArchive(t, map[string]string{"plugin.yaml": minimalCatalogPluginManifest("testplug")})
	sum := sha256.Sum256(archive)
	shaHex := hex.EncodeToString(sum[:])
	// Signed with a DIFFERENT key than the one the source trusts.
	sig := ed25519.Sign(otherPriv, archive)
	sigHex := hex.EncodeToString(sig)

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
    signature: "%s"
`, srv.URL, shaHex, sigHex)
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })

	addCatalogSource(t, cs, srv, hex.EncodeToString(pub))

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "testplug"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)

	if rec.Code >= 200 && rec.Code < 300 {
		t.Fatalf("expected a non-2xx response for a wrong-key signature, got %d: %s", rec.Code, rec.Body.String())
	}
	if fileExists(filepath.Join(pluginsDir, "testplug", "plugin.yaml")) {
		t.Error("plugin must not be written when signature verification fails")
	}
}

// TestHandleCatalogInstall_AlreadyInstalled mirrors
// TestHandleInstallLocal_AlreadyInstalled's 409 coverage for the catalog
// path's own pre-flight existence check.
func TestHandleCatalogInstall_AlreadyInstalled(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)
	createTestPlugin(t, pluginsDir, "testplug")

	catalogYAML := `version: 1
plugins:
  - name: testplug
    version: "1.0.0"
    description: test plugin
    archive_url: http://example.invalid/testplug.tar.gz
`
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	addCatalogSource(t, cs, srv, "")

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "testplug"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandleCatalogInstall_NotFound asserts a name absent from every
// configured catalog source's entries returns 404.
func TestHandleCatalogInstall_NotFound(t *testing.T) {
	cs, _ := setupCatalogTestState(t)

	catalogYAML := `version: 1
plugins: []
`
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	addCatalogSource(t, cs, srv, "")

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "does-not-exist"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
