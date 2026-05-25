package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/service"
)

func TestMetaHarnessesCRUDWritesCatalogFiles(t *testing.T) {
	root := t.TempDir()
	writeFixtureCatalog(t, root)
	_, mux := newTestAPIWithMetaHarnessCatalog(t, root)

	var listed []metaHarnessRecord
	doJSON(t, mux, http.MethodGet, "/api/meta-harnesses", nil, http.StatusOK, &listed)
	if len(listed) != 1 {
		t.Fatalf("expected fixture harness, got %d", len(listed))
	}

	create := metaHarnessRequest{
		ID:           "codex-main",
		DisplayName:  "Codex",
		UILabel:      "Codex (cli)",
		Provider:     "codex",
		Workdir:      "{{work_root}}",
		Args:         []string{"--json"},
		Env:          map[string]string{"CODEX_HOME": "/tmp/codex"},
		Role:         "writer",
		Project:      "nanite",
		WorkRoot:     "/tmp/nanite",
		TrackingRoot: "/tmp/nanite/.nanite",
		MCPServers:   []string{"filesystem"},
	}
	var created metaHarnessRecord
	doJSON(t, mux, http.MethodPost, "/api/meta-harnesses", create, http.StatusCreated, &created)
	if created.ID != "codex-main" || created.UILabel != "Codex (cli)" || created.Provider != "codex" {
		t.Fatalf("unexpected created record: %+v", created)
	}
	assertFileContains(t, filepath.Join(root, "boot-profiles", "codex-main.yaml"), "display_name: Codex")
	assertFileContains(t, filepath.Join(root, "launches", "codex-main.yaml"), "provider: codex")

	update := create
	update.UILabel = "Codex Project Agent (cli)"
	update.Provider = "opencode"
	var updated metaHarnessRecord
	doJSON(t, mux, http.MethodPut, "/api/meta-harnesses/codex-main", update, http.StatusOK, &updated)
	if updated.UILabel != "Codex Project Agent (cli)" || updated.Provider != "opencode" {
		t.Fatalf("unexpected updated record: %+v", updated)
	}
	assertFileContains(t, filepath.Join(root, "launches", "codex-main.yaml"), "provider: opencode")

	doJSON(t, mux, http.MethodDelete, "/api/meta-harnesses/codex-main", nil, http.StatusNoContent, nil)
	if _, err := os.Stat(filepath.Join(root, "boot-profiles", "codex-main.yaml")); !os.IsNotExist(err) {
		t.Fatalf("profile file should be deleted, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "launches", "codex-main.yaml")); !os.IsNotExist(err) {
		t.Fatalf("launch file should be deleted, stat err=%v", err)
	}
}

func TestMetaHarnessesReloadRegistryAfterCreate(t *testing.T) {
	root := t.TempDir()
	writeFixtureCatalog(t, root)
	svc, mux := newTestAPIWithMetaHarnessCatalog(t, root)

	encoded := bootprofile.EncodeProviderID("claude-code")
	if _, ok := svc.BootProfiles.Lookup(encoded); ok {
		t.Fatal("precondition: claude-code should not exist yet")
	}

	req := metaHarnessRequest{
		ID:          "claude-code",
		DisplayName: "Claude Code",
		UILabel:     "Claude Code (cli)",
		Provider:    "pty-claude",
	}
	var created metaHarnessRecord
	doJSON(t, mux, http.MethodPost, "/api/meta-harnesses", req, http.StatusCreated, &created)
	if _, ok := svc.BootProfiles.Lookup(encoded); !ok {
		t.Fatal("create did not reload boot-profile registry")
	}
}

func newTestAPIWithMetaHarnessCatalog(t *testing.T, root string) (*service.Container, *http.ServeMux) {
	t.Helper()
	svc, err := service.NewContainer(service.ContainerConfig{
		Store:                  newSeededStore(t),
		Providers:              provider.NewRegistry(),
		BootProfileCatalogPath: root,
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	return svc, mux
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path string, body any, wantStatus int, out any) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, w.Code, wantStatus, w.Body.String())
	}
	if out != nil {
		if err := json.NewDecoder(w.Body).Decode(out); err != nil {
			t.Fatalf("decode response: %v body=%s", err, w.Body.String())
		}
	}
}

func assertFileContains(t *testing.T, path, substr string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Contains(data, []byte(substr)) {
		t.Fatalf("%s does not contain %q:\n%s", path, substr, string(data))
	}
}
