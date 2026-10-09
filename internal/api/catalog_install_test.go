package api

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
	sharedcatalog "github.com/hollis-labs/plugins-catalog"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/install"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
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
type catalogTestState struct {
	*catalogState
	store *store.Store
}

func setupCatalogTestState(t *testing.T) (*catalogTestState, string) {
	t.Helper()
	pluginsDir := t.TempDir()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")
	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	return &catalogTestState{store: s, catalogState: &catalogState{
		sources: service.NewCatalogSourceService(s), cleanup: service.NewPluginCleanupService(s),
		fetcher:           naniteplugin.NewCatalogFetcher(5*time.Minute, filepath.Join(pluginsDir, ".cache")),
		pluginsDir:        pluginsDir,
		pluginHost:        nil,
		archiveDownloader: &install.HTTPDownloader{AllowLocalhost: true},
	}}, pluginsDir
}

// addCatalogSource registers a custom catalog source pointing at
// srv.URL+"/catalog.yaml".
func addCatalogSource(t *testing.T, cs *catalogTestState, srv *httptest.Server) *store.CatalogSource {
	t.Helper()
	src, err := cs.store.CreateCatalogSource(context.Background(), "Test Source", srv.URL+"/catalog.yaml", "custom", 100)
	if err != nil {
		t.Fatalf("CreateCatalogSource: %v", err)
	}
	return src
}

func minimalCatalogPluginManifest(t *testing.T, id string, extra ...map[string]string) string {
	t.Helper()
	files := []manifest.ArtifactFile{}
	contentsByPath := map[string]string{"bin/plugin": catalogExecutable, "README.md": "hello"}
	for _, additions := range extra {
		for path, contents := range additions {
			contentsByPath[path] = contents
		}
	}
	for path, contents := range contentsByPath {
		digest := sha256.Sum256([]byte(contents))
		files = append(files, manifest.ArtifactFile{Path: path, SHA256: hex.EncodeToString(digest[:]), Executable: strings.HasPrefix(path, "bin/")})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	tree, err := manifest.TreeDigest(files)
	if err != nil {
		t.Fatal(err)
	}
	declaration := manifest.Manifest{SchemaVersion: 2, ID: id, Name: "Test Plugin", Version: "1.0.0", Description: "test plugin", License: "MIT", Runtime: "subprocess", Protocol: 2, Server: manifest.Server{Runtime: "binary", Engines: map[string]manifest.HostRange{"binary": {Min: "0.0.0"}}, Entry: "bin/plugin"}, Artifact: manifest.Artifact{Files: files, TreeSHA256: tree}, Hosts: map[string]manifest.HostRange{"nanite": {Min: pluginapi.Version}}, Nanite: json.RawMessage(`{}`)}
	var out strings.Builder
	if err := manifest.Encode(&out, declaration); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

const catalogExecutable = "#!/bin/sh\n"

func catalogInstallFixture(t *testing.T, id, url, checksum string, size int64) string {
	t.Helper()
	doc := sharedcatalog.Document{SchemaVersion: 2, CatalogVersion: "0.1.0", GeneratedAt: "2026-10-01T00:00:00Z", Plugins: []sharedcatalog.Plugin{}}
	if id != "" {
		digest := sha256.Sum256([]byte(minimalCatalogPluginManifest(t, id)))
		doc.Plugins = append(doc.Plugins, sharedcatalog.Plugin{ID: id, Name: "Test Plugin", Version: "1.0.0", Source: sharedcatalog.Source{Type: "git", Repo: "https://github.com/example/plugins", Tag: "v1.0.0"}, Archives: []sharedcatalog.Archive{{Platform: runtime.GOOS + "-" + runtime.GOARCH, URL: url, SHA256: checksum, Size: size}}, ManifestSHA256: hex.EncodeToString(digest[:]), Directory: sharedcatalog.Directory{Status: "active"}})
		if err := json.Unmarshal([]byte(`{"nanite":{"min":"0.1.0"}}`), &doc.Plugins[0].Hosts); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestHandleCatalogInstall_BlocksPrivateArchiveDestination is GO-API-003's
// remaining regression after AD-04 already supplied the timeout and size cap.
// The catalog itself is operator-configured, but its archive URL must not turn
// the Nanite process into a probe of private, loopback, link-local, or IMDS
// destinations.
func TestHandleCatalogInstall_BlocksPrivateArchiveDestination(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)
	dialed := false
	cs.archiveDownloader = &install.HTTPDownloader{
		Resolver: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		},
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, fmt.Errorf("blocked destination reached dialer")
		},
	}

	catalogYAML := catalogInstallFixture(t, "imdsplug", "https://release.example/imdsplug.tar.gz", strings.Repeat("a", 64), 123)
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(catalogYAML)) })
	srv := httptest.NewTLSServer(mux)
	cs.fetcher = naniteplugin.NewCatalogFetcherWithClient(5*time.Minute, filepath.Join(cs.pluginsDir, ".cache"), srv.Client())
	cs.archiveDownloader.Client = srv.Client()
	defer srv.Close()
	addCatalogSource(t, cs, srv)

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)
	body, _ := json.Marshal(map[string]string{"name": "imdsplug"})
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 for blocked archive destination, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "blocked destination") {
		t.Fatalf("response does not report blocked destination: %s", rec.Body.String())
	}
	if dialed {
		t.Fatal("blocked archive destination reached the dialer")
	}
	if fileExists(filepath.Join(pluginsDir, "imdsplug", "plugin.yaml")) {
		t.Fatal("blocked archive destination installed a plugin")
	}
}

// buildTarGzArchive builds an in-memory .tar.gz with the given files at the
// archive root (no wrapper directory — install.Installer's Validator step
// expects plugin.yaml directly at the extraction root, matching the CLI's
// own TarGzExtractor contract).
func buildTarGzArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	for name := range files {
		if filepath.Base(name) == "plugin.yaml" {
			files[filepath.Join(filepath.Dir(name), "bin/plugin")] = catalogExecutable
			files[filepath.Join(filepath.Dir(name), "README.md")] = "hello"
		}
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		mode := int64(0644)
		if strings.Contains("/"+name, "/bin/") {
			mode = 0755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(content)), Mode: mode}); err != nil {
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
	for name := range files {
		if filepath.Base(name) == "plugin.yaml" {
			files[filepath.Join(filepath.Dir(name), "bin/plugin")] = catalogExecutable
			files[filepath.Join(filepath.Dir(name), "README.md")] = "hello"
		}
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		mode := os.FileMode(0644)
		if strings.Contains("/"+name, "/bin/") {
			mode = 0755
		}
		header.SetMode(mode)
		w, err := zw.CreateHeader(header)
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

// TestHandleCatalogInstall_PathTraversal is the GO-PLUGIN-002 regression
// test: a catalog entry named with a path-traversal payload must be
// rejected with 400 before any file is written outside pluginsDir, mirroring
// TestHandleInstall_PathTraversal (plugins_install_test.go).
func TestHandleCatalogInstall_PathTraversal(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)

	catalogYAML := catalogInstallFixture(t, "traversal-fixture", "https://example.invalid/x.tar.gz", strings.Repeat("a", 64), 123)
	catalogYAML = strings.Replace(catalogYAML, `"id":"traversal-fixture"`, `"id":"../../etc/passwd"`, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })
	srv := httptest.NewTLSServer(mux)
	cs.fetcher = naniteplugin.NewCatalogFetcherWithClient(5*time.Minute, filepath.Join(cs.pluginsDir, ".cache"), srv.Client())
	cs.archiveDownloader.Client = srv.Client()
	defer srv.Close()

	addCatalogSource(t, cs, srv)

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
// test: a checksummed .tar.gz catalog entry installs
// successfully end-to-end through the converged pipeline. Protects against
// the fail-closed fix (above) becoming fail-always.
func TestHandleCatalogInstall_Success_TarGz(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)

	archive := buildTarGzArchive(t, map[string]string{"plugin.yaml": minimalCatalogPluginManifest(t, "testplug")})
	sum := sha256.Sum256(archive)
	shaHex := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/testplug.tar.gz", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	srv := httptest.NewTLSServer(mux)
	cs.fetcher = naniteplugin.NewCatalogFetcherWithClient(5*time.Minute, filepath.Join(cs.pluginsDir, ".cache"), srv.Client())
	cs.archiveDownloader.Client = srv.Client()
	defer srv.Close()

	catalogYAML := catalogInstallFixture(t, "testplug", srv.URL+"/testplug.tar.gz", shaHex, int64(len(archive)))
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })

	addCatalogSource(t, cs, srv)

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "testplug"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)
	digest := assertInstallReview(t, rec, pluginsDir, "")
	var accepted map[string]string
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatal(err)
	}
	accepted["approved_digest"] = digest
	body, _ = json.Marshal(accepted)
	rec = httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plugins/catalog/install", bytes.NewReader(body)))

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
// regression test: a checksummed .zip catalog entry (as
// opposed to .tar.gz) also installs successfully — the format-dispatching
// catalogExtractor must not silently drop zip support the way a naive
// convergence onto the CLI's tar.gz-only TarGzExtractor would have.
func TestHandleCatalogInstall_Success_Zip(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)

	archive := buildZipArchive(t, map[string]string{"plugin.yaml": minimalCatalogPluginManifest(t, "zipplug")})
	sum := sha256.Sum256(archive)
	shaHex := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/zipplug.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	srv := httptest.NewTLSServer(mux)
	cs.fetcher = naniteplugin.NewCatalogFetcherWithClient(5*time.Minute, filepath.Join(cs.pluginsDir, ".cache"), srv.Client())
	cs.archiveDownloader.Client = srv.Client()
	defer srv.Close()

	catalogYAML := catalogInstallFixture(t, "zipplug", srv.URL+"/zipplug.zip", shaHex, int64(len(archive)))
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })

	addCatalogSource(t, cs, srv)

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "zipplug"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)
	digest := assertInstallReview(t, rec, pluginsDir, "")
	var accepted map[string]string
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatal(err)
	}
	accepted["approved_digest"] = digest
	body, _ = json.Marshal(accepted)
	rec = httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plugins/catalog/install", bytes.NewReader(body)))

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

	// plugin.yaml (and a sibling file, to confirm the whole subtree
	// flattens, not just the manifest) live inside a single wrapper
	// directory rather than at the archive root.
	archive := buildZipArchive(t, map[string]string{
		"wrapplug-main/plugin.yaml": minimalCatalogPluginManifest(t, "wrapplug"),
		"wrapplug-main/README.md":   "hello",
	})
	sum := sha256.Sum256(archive)
	shaHex := hex.EncodeToString(sum[:])

	mux := http.NewServeMux()
	mux.HandleFunc("/wrapplug.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	srv := httptest.NewTLSServer(mux)
	cs.fetcher = naniteplugin.NewCatalogFetcherWithClient(5*time.Minute, filepath.Join(cs.pluginsDir, ".cache"), srv.Client())
	cs.archiveDownloader.Client = srv.Client()
	defer srv.Close()

	catalogYAML := catalogInstallFixture(t, "wrapplug", srv.URL+"/wrapplug.zip", shaHex, int64(len(archive)))
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })

	addCatalogSource(t, cs, srv)

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "wrapplug"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)
	digest := assertInstallReview(t, rec, pluginsDir, "")
	var accepted map[string]string
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatal(err)
	}
	accepted["approved_digest"] = digest
	body, _ = json.Marshal(accepted)
	rec = httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plugins/catalog/install", bytes.NewReader(body)))

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

// TestHandleCatalogInstall_AlreadyInstalled mirrors
// TestHandleInstallLocal_AlreadyInstalled's 409 coverage for the catalog
// path's own pre-flight existence check.
func TestHandleCatalogInstall_AlreadyInstalled(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)
	createTestPlugin(t, pluginsDir, "testplug")

	catalogYAML := catalogInstallFixture(t, "testplug", "https://example.invalid/testplug.tar.gz", strings.Repeat("a", 64), 123)
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })
	srv := httptest.NewTLSServer(mux)
	cs.fetcher = naniteplugin.NewCatalogFetcherWithClient(5*time.Minute, filepath.Join(cs.pluginsDir, ".cache"), srv.Client())
	cs.archiveDownloader.Client = srv.Client()
	defer srv.Close()

	addCatalogSource(t, cs, srv)

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

	catalogYAML := catalogInstallFixture(t, "", "", "", 0)
	mux := http.NewServeMux()
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })
	srv := httptest.NewTLSServer(mux)
	cs.fetcher = naniteplugin.NewCatalogFetcherWithClient(5*time.Minute, filepath.Join(cs.pluginsDir, ".cache"), srv.Client())
	cs.archiveDownloader.Client = srv.Client()
	defer srv.Close()

	addCatalogSource(t, cs, srv)

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

// TestHandleCatalogInstall_WrongChecksum proves integrity verification stays in the API pipeline.
func TestHandleCatalogInstall_WrongChecksum(t *testing.T) {
	cs, pluginsDir := setupCatalogTestState(t)

	archive := buildTarGzArchive(t, map[string]string{"plugin.yaml": minimalCatalogPluginManifest(t, "testplug")})
	shaHex := strings.Repeat("0", 64)

	mux := http.NewServeMux()
	mux.HandleFunc("/testplug.tar.gz", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	srv := httptest.NewTLSServer(mux)
	cs.fetcher = naniteplugin.NewCatalogFetcherWithClient(5*time.Minute, filepath.Join(cs.pluginsDir, ".cache"), srv.Client())
	cs.archiveDownloader.Client = srv.Client()
	defer srv.Close()

	catalogYAML := catalogInstallFixture(t, "testplug", srv.URL+"/testplug.tar.gz", shaHex, int64(len(archive)))
	mux.HandleFunc("/catalog.yaml", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(catalogYAML)) })

	addCatalogSource(t, cs, srv)

	handlerMux := http.NewServeMux()
	handlerMux.HandleFunc("POST /api/plugins/catalog/install", cs.handleCatalogInstall)

	body, _ := json.Marshal(map[string]string{"name": "testplug"})
	req := httptest.NewRequest("POST", "/api/plugins/catalog/install", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handlerMux.ServeHTTP(rec, req)

	if rec.Code >= 200 && rec.Code < 300 {
		t.Fatalf("expected a non-2xx response for a wrong checksum, got %d: %s", rec.Code, rec.Body.String())
	}
	if fileExists(filepath.Join(pluginsDir, "testplug", "plugin.yaml")) {
		t.Error("plugin must not be written when checksum verification fails")
	}
}

func assertInstallReview(t *testing.T, rec *httptest.ResponseRecorder, root, existingID string) string {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected review, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Status string                     `json:"status"`
		Review naniteplugin.InstallReview `json:"review"`
		Digest string                     `json:"review_digest"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "review_required" || response.Digest != response.Review.Digest() {
		t.Fatalf("invalid review: %s", rec.Body.String())
	}
	if existingID == "" {
		if fileExists(filepath.Join(root, response.Review.ID, "plugin.yaml")) {
			t.Fatal("preview installed plugin")
		}
		if _, err := naniteplugin.ReadInstallApproval(root, response.Review.ID); err == nil {
			t.Fatal("preview approved plugin")
		}
	}
	return response.Digest
}
