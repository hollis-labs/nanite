package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
