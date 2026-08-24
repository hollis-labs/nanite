package api

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// setupPluginTestState creates a temp directory and a pluginManagerState for testing.
func setupPluginTestState(t *testing.T) (*pluginManagerState, string) {
	t.Helper()
	pluginsDir := t.TempDir()
	return &pluginManagerState{
		pluginsDir: pluginsDir,
		reposPath:  filepath.Join(pluginsDir, "repos.yaml"),
	}, pluginsDir
}

// createTestPlugin creates a minimal plugin directory with a plugin.yaml.
func createTestPlugin(t *testing.T, dir, name string) string {
	t.Helper()
	pluginDir := filepath.Join(dir, name)
	os.MkdirAll(pluginDir, 0755)
	manifest := `name: ` + name + `
version: 1.0.0
description: Test plugin
`
	os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(manifest), 0644)
	os.WriteFile(filepath.Join(pluginDir, "README.md"), []byte("# "+name), 0644)
	return pluginDir
}

func TestHandleInstallLocal(t *testing.T) {
	pms, pluginsDir := setupPluginTestState(t)

	// Create a source plugin directory outside of pluginsDir.
	srcDir := t.TempDir()
	createTestPlugin(t, srcDir, "test-local-plugin")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plugins/install-local", pms.handleInstallLocal)

	body, _ := json.Marshal(map[string]string{"path": filepath.Join(srcDir, "test-local-plugin")})
	req := httptest.NewRequest("POST", "/api/plugins/install-local", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["plugin"] != "test-local-plugin" {
		t.Errorf("expected plugin name 'test-local-plugin', got %q", resp["plugin"])
	}
	if resp["source"] != "local" {
		t.Errorf("expected source 'local', got %q", resp["source"])
	}

	// Verify the plugin was copied.
	if !fileExists(filepath.Join(pluginsDir, "test-local-plugin", "plugin.yaml")) {
		t.Error("plugin.yaml not found in target")
	}
	if !fileExists(filepath.Join(pluginsDir, "test-local-plugin", "README.md")) {
		t.Error("README.md not found in target")
	}
}

func TestHandleInstallLocal_NoManifest(t *testing.T) {
	pms, _ := setupPluginTestState(t)

	emptyDir := t.TempDir()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plugins/install-local", pms.handleInstallLocal)

	body, _ := json.Marshal(map[string]string{"path": emptyDir})
	req := httptest.NewRequest("POST", "/api/plugins/install-local", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleInstallLocal_AlreadyInstalled(t *testing.T) {
	pms, pluginsDir := setupPluginTestState(t)

	// Pre-install the plugin.
	createTestPlugin(t, pluginsDir, "dup-plugin")

	// Try installing from another source.
	srcDir := t.TempDir()
	createTestPlugin(t, srcDir, "dup-plugin")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plugins/install-local", pms.handleInstallLocal)

	body, _ := json.Marshal(map[string]string{"path": filepath.Join(srcDir, "dup-plugin")})
	req := httptest.NewRequest("POST", "/api/plugins/install-local", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleInstallLocal_RejectsSourceFileSymlink(t *testing.T) {
	pms, pluginsDir := setupPluginTestState(t)

	srcRoot := t.TempDir()
	pluginDir := createTestPlugin(t, srcRoot, "symlink-source-plugin")
	outside := filepath.Join(t.TempDir(), "outside-secret.txt")
	wantOutside := []byte("must not be copied through a source symlink")
	if err := os.WriteFile(outside, wantOutside, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(pluginDir, "external.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plugins/install-local", pms.handleInstallLocal)
	body, err := json.Marshal(map[string]string{"path": pluginDir})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/plugins/install-local", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("install-local accepted a source-tree file symlink: %s", rec.Body.String())
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "symlink") {
		t.Fatalf("install-local error does not identify the rejected symlink: %s", rec.Body.String())
	}
	if _, err := os.Lstat(filepath.Join(pluginsDir, "symlink-source-plugin")); !os.IsNotExist(err) {
		t.Fatalf("partial plugin target survived rejected symlink (stat err = %v)", err)
	}
	gotOutside, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("outside symlink target was removed: %v", err)
	}
	if !bytes.Equal(gotOutside, wantOutside) {
		t.Fatalf("outside symlink target mutated: got %q, want %q", gotOutside, wantOutside)
	}
}

func TestHandleInstallArchive_TarGz(t *testing.T) {
	pms, pluginsDir := setupPluginTestState(t)

	// Build a .tar.gz in memory with a plugin inside a top-level dir.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	manifest := `name: archive-plugin
version: 2.0.0
description: From archive
`
	// Directory entry.
	tw.WriteHeader(&tar.Header{Name: "archive-plugin/", Typeflag: tar.TypeDir, Mode: 0755})
	// plugin.yaml
	tw.WriteHeader(&tar.Header{Name: "archive-plugin/plugin.yaml", Size: int64(len(manifest)), Mode: 0644})
	tw.Write([]byte(manifest))
	// A data file.
	data := "hello from archive"
	tw.WriteHeader(&tar.Header{Name: "archive-plugin/data.txt", Size: int64(len(data)), Mode: 0644})
	tw.Write([]byte(data))

	tw.Close()
	gz.Close()

	// Write to a temp file for multipart upload.
	tmpFile, _ := os.CreateTemp(t.TempDir(), "test-*.tar.gz")
	tmpFile.Write(buf.Bytes())
	tmpFile.Close()

	// Build multipart request.
	var reqBody bytes.Buffer
	mp := multipart.NewWriter(&reqBody)
	part, _ := mp.CreateFormFile("archive", "archive-plugin.tar.gz")
	f, _ := os.Open(tmpFile.Name())
	io.Copy(part, f)
	f.Close()
	mp.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plugins/install-archive", pms.handleInstallArchive)

	req := httptest.NewRequest("POST", "/api/plugins/install-archive", &reqBody)
	req.Header.Set("Content-Type", mp.FormDataContentType())
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["plugin"] != "archive-plugin" {
		t.Errorf("expected plugin 'archive-plugin', got %q", resp["plugin"])
	}
	if resp["source"] != "archive" {
		t.Errorf("expected source 'archive', got %q", resp["source"])
	}

	// Verify files were extracted.
	if !fileExists(filepath.Join(pluginsDir, "archive-plugin", "plugin.yaml")) {
		t.Error("plugin.yaml not found")
	}
	if !fileExists(filepath.Join(pluginsDir, "archive-plugin", "data.txt")) {
		t.Error("data.txt not found")
	}
}

// TestHandleInstall_PathTraversal asserts that handleInstall rejects names
// that would resolve outside pluginsDir via ".." segments. Regression for
// the audit Critical finding: plugin install target was joined raw.
func TestHandleInstall_PathTraversal(t *testing.T) {
	pms, _ := setupPluginTestState(t)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plugins/install", pms.handleInstall)

	body, _ := json.Marshal(map[string]string{"name": "../../etc/passwd"})
	req := httptest.NewRequest("POST", "/api/plugins/install", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for traversal name, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "escapes root") && !strings.Contains(resp["error"], "invalid plugin name") {
		t.Errorf("expected pathsafe escape error in body, got %q", resp["error"])
	}
}

// TestPluginMutationHandlersRejectNonCanonicalNames locks the management
// boundary at the HTTP layer. Before this regression, ../outside-plugin was
// joined directly in uninstall and in the enable/disable management sink;
// uninstall could delete the outside directory, while enable/disable could
// manage the outside manifest's database identity (and migrate a legacy
// disabled manifest in place).
func TestPluginMutationHandlersRejectNonCanonicalNames(t *testing.T) {
	handlers := map[string]func(*pluginManagerState, http.ResponseWriter, *http.Request){
		"uninstall": (*pluginManagerState).handleUninstall,
		"disable":   (*pluginManagerState).handleDisable,
		"enable":    (*pluginManagerState).handleEnable,
	}
	badNames := []string{
		"../outside-plugin",
		"nested/plugin",
		filepath.Join(t.TempDir(), "absolute-plugin"),
	}

	for action, handler := range handlers {
		action, handler := action, handler
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			pluginsDir := filepath.Join(root, "plugins")
			if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			outsideDir := filepath.Join(root, "outside-plugin")
			if err := os.MkdirAll(outsideDir, 0o755); err != nil {
				t.Fatal(err)
			}
			manifestPath := filepath.Join(outsideDir, "plugin.yaml")
			manifest := []byte("name: outside-plugin\nversion: 1.0.0\ndescription: outside sentinel\n")
			if err := os.WriteFile(manifestPath, manifest, 0o640); err != nil {
				t.Fatal(err)
			}

			db, err := storetest.New(t, context.Background(), filepath.Join(root, "test.db"))
			if err != nil {
				t.Fatalf("store.New: %v", err)
			}
			naniteplugin.SetPluginStateStore(db)
			t.Cleanup(func() {
				naniteplugin.SetPluginStateStore(nil)
				_ = db.Close(context.Background())
			})
			pms := &pluginManagerState{pluginsDir: pluginsDir, reposPath: filepath.Join(pluginsDir, "repos.yaml"), store: db}

			for _, name := range badNames {
				body, err := json.Marshal(pluginActionReq{Name: name})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest(http.MethodPost, "/api/plugins/"+action, bytes.NewReader(body))
				rec := httptest.NewRecorder()
				handler(pms, rec, req)
				if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid plugin name") {
					t.Errorf("name %q: status/body = %d %q, want 400 invalid plugin name", name, rec.Code, rec.Body.String())
				}
			}

			got, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatalf("outside plugin.yaml was removed: %v", err)
			}
			if !bytes.Equal(got, manifest) {
				t.Fatalf("outside plugin.yaml mutated: got %q, want %q", got, manifest)
			}
			info, err := os.Stat(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o640 {
				t.Fatalf("outside plugin.yaml mode mutated to %o", info.Mode().Perm())
			}
		})
	}
}

// TestHandleInstallLocal_ManifestTraversal asserts that a manifest whose name
// contains traversal segments is refused.
func TestHandleInstallLocal_ManifestTraversal(t *testing.T) {
	pms, _ := setupPluginTestState(t)

	// Create a source with a malicious manifest name.
	srcDir := t.TempDir()
	pluginDir := filepath.Join(srcDir, "evil")
	os.MkdirAll(pluginDir, 0755)
	os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"),
		[]byte("name: ../../etc/evil\nversion: 1.0.0\n"), 0644)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plugins/install-local", pms.handleInstallLocal)

	body, _ := json.Marshal(map[string]string{"path": pluginDir})
	req := httptest.NewRequest("POST", "/api/plugins/install-local", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for traversal manifest name, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestPluginUIRouteRejectsSymlinkEscape is GO-API-008's regression. The old
// Clean+HasPrefix check accepted a symlink planted under ui/ even when its
// target lived outside the installed plugin directory.
func TestPluginUIRouteRejectsSymlinkEscape(t *testing.T) {
	pluginsDir := t.TempDir()
	uiDir := filepath.Join(pluginsDir, "ui-plugin", "ui")
	if err := os.MkdirAll(uiDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "secret.js")
	if err := os.WriteFile(outside, []byte("window.SECRET = true"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(uiDir, "escape.js")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	mux := http.NewServeMux()
	RegisterPluginManagementRoutes(mux, pluginsDir, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/plugins/ui-plugin/ui/escape.js", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for plugin UI symlink escape, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "window.SECRET") {
		t.Fatalf("response leaked symlink target: %s", rec.Body.String())
	}
}

func TestCopyDir(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "sub"), 0755)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("bbb"), 0644)

	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	// Verify.
	data, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil || string(data) != "aaa" {
		t.Errorf("a.txt: got %q, err %v", data, err)
	}
	data, err = os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
	if err != nil || string(data) != "bbb" {
		t.Errorf("sub/b.txt: got %q, err %v", data, err)
	}
}

// writeTarGzToFile is a helper that builds a tar.gz from the provided
// (name,size,mode) entries and writes it to path. If declaredSize > 0 the
// tar header Size is set to that value independently of actual payload;
// payload is len(payload) zero-bytes. Used to craft both honest and
// header-lying bomb archives.
func writeTarGzToFile(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	zeroChunk := make([]byte, 1024*1024) // 1 MiB of zeros; compresses tiny
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: tar.TypeReg, Mode: 0644, Size: e.declaredSize}
		if e.isDir {
			hdr.Typeflag = tar.TypeDir
			hdr.Mode = 0755
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", e.name, err)
		}
		if e.isDir {
			continue
		}
		// tar requires exactly Size bytes of payload per entry. We write
		// explicit payload first if set, then pad with zeros to reach
		// declaredSize so the archive is well-formed.
		written := int64(0)
		if len(e.payload) > 0 {
			n, err := tw.Write(e.payload)
			if err != nil {
				t.Fatalf("tar write %s: %v", e.name, err)
			}
			written = int64(n)
		}
		remaining := e.declaredSize - written
		for remaining > 0 {
			n := int64(len(zeroChunk))
			if n > remaining {
				n = remaining
			}
			w, err := tw.Write(zeroChunk[:n])
			if err != nil {
				t.Fatalf("tar pad %s: %v", e.name, err)
			}
			remaining -= int64(w)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
}

type tarEntry struct {
	name         string
	declaredSize int64
	payload      []byte
	isDir        bool
}

// TestExtractTarGz_RejectsOversizedFile regression for G110: a single
// tar entry with a declared Size exceeding maxArchiveFileSize must be
// rejected before any bytes are written to disk.
func TestExtractTarGz_RejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "bomb.tar.gz")
	writeTarGzToFile(t, archive, []tarEntry{
		{name: "huge.bin", declaredSize: maxArchiveFileSize + 1, payload: []byte("x")},
	})
	dst := filepath.Join(dir, "out")
	if err := os.MkdirAll(dst, 0755); err != nil {
		t.Fatal(err)
	}
	err := extractTarGz(archive, dst)
	if err == nil {
		t.Fatal("expected error for oversized file, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected size-cap error, got %v", err)
	}
}

// TestExtractTarGz_RejectsTooManyEntries regression for G110: an archive
// with more than maxArchiveFileCount entries must be rejected mid-stream.
func TestExtractTarGz_RejectsTooManyEntries(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "many.tar.gz")
	entries := make([]tarEntry, maxArchiveFileCount+10)
	for i := range entries {
		entries[i] = tarEntry{name: fmt.Sprintf("f-%d", i), declaredSize: 1, payload: []byte("x")}
	}
	writeTarGzToFile(t, archive, entries)
	dst := filepath.Join(dir, "out")
	if err := os.MkdirAll(dst, 0755); err != nil {
		t.Fatal(err)
	}
	err := extractTarGz(archive, dst)
	if err == nil {
		t.Fatal("expected error for too many entries, got nil")
	}
	if !strings.Contains(err.Error(), "too many entries") {
		t.Fatalf("expected entry-count error, got %v", err)
	}
}

// TestExtractZip_RejectsOversizedFile regression for G110 on the zip
// path: an entry with UncompressedSize64 above maxArchiveFileSize must be
// rejected.
func TestExtractZip_RejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "bomb.zip")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// A huge-but-zero-byte entry is hard to craft honestly via the
	// writer (it'd set UncompressedSize64 to actual payload). Instead,
	// craft a real over-cap payload: 100 MiB + 1 byte of zeros.
	//
	// To keep the test fast and memory-thrift, use the LimitReader
	// trip: write maxArchiveFileSize+1 bytes (zeros compress well, so
	// the archive itself stays small) and confirm the cap trips.
	w, err := zw.Create("huge.bin")
	if err != nil {
		t.Fatalf("zw.Create: %v", err)
	}
	// Write in 1 MiB chunks of zeros.
	chunk := make([]byte, 1024*1024)
	total := maxArchiveFileSize + 1
	for total > 0 {
		n := int64(len(chunk))
		if n > total {
			n = total
		}
		if _, err := w.Write(chunk[:n]); err != nil {
			t.Fatalf("zip write: %v", err)
		}
		total -= n
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	if err := os.WriteFile(archive, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	dst := filepath.Join(dir, "out")
	if err := os.MkdirAll(dst, 0755); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(archive, dst); err == nil {
		t.Fatal("expected oversized file error, got nil")
	} else if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected size-cap error, got %v", err)
	}
}

// TestExtractZip_RejectsTraversal asserts that pathsafe.ResolveUnder
// rejects zip entries whose names contain .. segments. Complements the
// TestHandleInstall_PathTraversal test that hits handleInstall.
func TestExtractZip_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "trav.zip")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../../escape.txt")
	if err != nil {
		t.Fatalf("zw.Create: %v", err)
	}
	_, _ = w.Write([]byte("pwn"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "out")
	if err := os.MkdirAll(dst, 0755); err != nil {
		t.Fatal(err)
	}
	err = extractZip(archive, dst)
	if err == nil {
		t.Fatal("expected traversal error, got nil")
	}
	if !strings.Contains(err.Error(), "illegal path") {
		t.Fatalf("expected illegal-path error, got %v", err)
	}
}

// TestExtractTarGz_SurfacesCloseError regression for Copilot review on PR
// #19: the previous code discarded out.Close()'s return, so any error
// flushing the extracted file was silently swallowed and extraction
// reported success on a truncated artifact. The fix captures closeErr
// and returns it after the io.Copy error-check; this test injects a
// close failure via the extractedFileClose hook and asserts the error
// bubbles out.
func TestExtractTarGz_SurfacesCloseError(t *testing.T) {
	origClose := extractedFileClose
	defer func() { extractedFileClose = origClose }()
	sentinel := fmt.Errorf("injected close failure")
	extractedFileClose = func(f *os.File) error {
		_ = f.Close() // still release the fd
		return sentinel
	}

	dir := t.TempDir()
	archive := filepath.Join(dir, "ok.tar.gz")
	writeTarGzToFile(t, archive, []tarEntry{
		{name: "a.txt", declaredSize: 5, payload: []byte("hello")},
	})
	dst := filepath.Join(dir, "out")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	err := extractTarGz(archive, dst)
	if err == nil {
		t.Fatal("expected close error to bubble, got nil")
	}
	if !strings.Contains(err.Error(), "injected close failure") {
		t.Fatalf("error does not wrap close failure: %v", err)
	}
}

// TestExtractZip_SurfacesCloseError is the zip-branch twin of
// TestExtractTarGz_SurfacesCloseError.
func TestExtractZip_SurfacesCloseError(t *testing.T) {
	origClose := extractedFileClose
	defer func() { extractedFileClose = origClose }()
	sentinel := fmt.Errorf("injected close failure")
	extractedFileClose = func(f *os.File) error {
		_ = f.Close()
		return sentinel
	}

	dir := t.TempDir()
	archive := filepath.Join(dir, "ok.zip")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("a.txt")
	if err != nil {
		t.Fatalf("zw.Create: %v", err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("zw.Write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	if err := os.WriteFile(archive, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "out")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(archive, dst); err == nil {
		t.Fatal("expected close error to bubble, got nil")
	} else if !strings.Contains(err.Error(), "injected close failure") {
		t.Fatalf("error does not wrap close failure: %v", err)
	}
}
