package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func newTestAPI(t *testing.T) (*API, *http.ServeMux) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("create isolated test home: %v", err)
	}
	// NewContainer resolves its embedded Tesseract store independently of the
	// Nanite test DB. Pin every XDG root plus the explicit Tesseract DB before
	// construction so an API test can never open the operator's real store.
	for env, dir := range map[string]string{
		"HOME":            home,
		"XDG_DATA_HOME":   filepath.Join(root, "xdg", "data"),
		"XDG_STATE_HOME":  filepath.Join(root, "xdg", "state"),
		"XDG_CACHE_HOME":  filepath.Join(root, "xdg", "cache"),
		"XDG_CONFIG_HOME": filepath.Join(root, "xdg", "config"),
	} {
		t.Setenv(env, dir)
	}
	tesseractDB := filepath.Join(root, "tesseract", "main.db")
	t.Setenv("TESSERACT_DB_PATH", tesseractDB)
	t.Setenv("TESSERACT_WORKSPACE", "api-test")
	layout, err := config.ResolveTesseractLayout()
	if err != nil {
		t.Fatalf("ResolveTesseractLayout: %v", err)
	}
	assertTestPathUnder(t, root, layout.MainDB())
	if layout.MainDB() != tesseractDB {
		t.Fatalf("resolved Tesseract DB = %q, want explicit test DB %q", layout.MainDB(), tesseractDB)
	}

	dbPath := filepath.Join(root, "test.db")
	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:      s,
		Providers:  provider.NewRegistry(),
		WorkingDir: root,
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	t.Cleanup(func() { svc.Shutdown() })

	a := New(svc)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "0.1.0"})
	})
	a.RegisterRoutes(mux)
	return a, mux
}

func assertTestPathUnder(t *testing.T, root, path string) {
	t.Helper()
	if testPathUnder(root, path) {
		return
	}
	canonicalRoot, rootErr := filepath.EvalSymlinks(root)
	canonicalPath, pathErr := filepath.EvalSymlinks(path)
	if rootErr == nil && pathErr == nil && testPathUnder(canonicalRoot, canonicalPath) {
		return
	}
	t.Fatalf("test path %q escapes temp root %q (canonical root=%q err=%v; path=%q err=%v)", path, root, canonicalRoot, rootErr, canonicalPath, pathErr)
}

func testPathUnder(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func TestNewTestAPI_TesseractDBIsTempIsolated(t *testing.T) {
	a, _ := newTestAPI(t)
	var dbFile string
	rows, err := a.Services.Tesseract.MemoryStore().DB().Query("PRAGMA database_list")
	if err != nil {
		t.Fatalf("PRAGMA database_list: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var seq int
		var name, path string
		if err := rows.Scan(&seq, &name, &path); err != nil {
			t.Fatalf("scan database_list: %v", err)
		}
		if name == "main" {
			dbFile = path
			break
		}
	}
	if dbFile == "" {
		t.Fatal("Tesseract main DB path not found")
	}
	assertTestPathUnder(t, a.Services.WorkingDir, dbFile)
}

func TestHealthEndpoint(t *testing.T) {
	_, mux := newTestAPI(t)

	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", resp["status"])
	}
}

func TestListProjectsEmpty(t *testing.T) {
	_, mux := newTestAPI(t)

	req := httptest.NewRequest("GET", "/api/projects", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Must be "[]" (JSON array), not "null".
	if body == "null\n" || body == "null" {
		t.Fatal("expected JSON array [], got null")
	}

	var projects []store.Project
	if err := json.Unmarshal([]byte(body), &projects); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(projects))
	}
}

func TestCreateAndListSessions(t *testing.T) {
	a, mux := newTestAPI(t)

	// Seed an agent so EnsureSessionAgent doesn't fail on FK constraint.
	if err := a.Services.Store.CreateAgent(context.Background(), &store.AgentProfile{
		ID:           "mentat-001",
		Name:         "Mentat",
		Slug:         "mentat",
		SystemPrompt: "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// POST /api/sessions
	body, _ := json.Marshal(map[string]string{})
	req := httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/sessions: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}

	var created store.Session
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created session: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected session ID to be set")
	}

	// GET /api/sessions
	req = httptest.NewRequest("GET", "/api/sessions", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/sessions: expected 200, got %d", w.Code)
	}

	var sessions []store.Session
	if err := json.NewDecoder(w.Body).Decode(&sessions); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != created.ID {
		t.Errorf("session ID mismatch: got %q, want %q", sessions[0].ID, created.ID)
	}
}

func TestListCommands(t *testing.T) {
	a, mux := newTestAPI(t)

	// Wire up a command registry with built-in commands.
	a.Services.Commands = chat.NewCommandRegistry()

	req := httptest.NewRequest("GET", "/api/commands", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var commands []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&commands); err != nil {
		t.Fatalf("decode commands: %v", err)
	}
	if len(commands) == 0 {
		t.Fatal("expected at least one command")
	}

	// Verify each command has required fields. Values can be strings or arrays
	// (e.g. structured `args`), so decode as `any` and assert string fields by type.
	for _, cmd := range commands {
		if name, _ := cmd["name"].(string); name == "" {
			t.Error("command missing 'name' field")
		}
		if desc, _ := cmd["description"].(string); desc == "" {
			t.Error("command missing 'description' field")
		}
	}
}
