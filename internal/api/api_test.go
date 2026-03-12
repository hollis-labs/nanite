package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/mentat/internal/store"
)

func newTestAPI(t *testing.T) (*API, *http.ServeMux) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	a := New(s, nil) // nil engine — we only test handlers that don't need it

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "0.1.0"})
	})
	a.RegisterRoutes(mux)
	return a, mux
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

func TestListWorkspacesEmpty(t *testing.T) {
	_, mux := newTestAPI(t)

	req := httptest.NewRequest("GET", "/api/workspaces", nil)
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

	var workspaces []store.Workspace
	if err := json.Unmarshal([]byte(body), &workspaces); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(workspaces) != 0 {
		t.Errorf("expected 0 workspaces, got %d", len(workspaces))
	}
}

func TestCreateAndListSessions(t *testing.T) {
	a, mux := newTestAPI(t)

	// Create a workspace first.
	ws := &store.Workspace{ID: "ws-api", Name: "API Test"}
	if err := a.Store.CreateWorkspace(ws); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	// Seed an agent so EnsureSessionAgent doesn't fail on FK constraint.
	if err := a.Store.CreateAgent(&store.AgentProfile{
		ID:           "mentat-001",
		Name:         "Mentat",
		Slug:         "mentat",
		SystemPrompt: "test",
	}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// POST /api/sessions
	body, _ := json.Marshal(map[string]string{"workspace_id": "ws-api"})
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

	// GET /api/sessions?workspace_id=ws-api
	req = httptest.NewRequest("GET", "/api/sessions?workspace_id=ws-api", nil)
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
	_, mux := newTestAPI(t)

	req := httptest.NewRequest("GET", "/api/commands", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var commands []map[string]string
	if err := json.NewDecoder(w.Body).Decode(&commands); err != nil {
		t.Fatalf("decode commands: %v", err)
	}
	if len(commands) == 0 {
		t.Fatal("expected at least one command")
	}

	// Verify each command has required fields.
	for _, cmd := range commands {
		if cmd["name"] == "" {
			t.Error("command missing 'name' field")
		}
		if cmd["description"] == "" {
			t.Error("command missing 'description' field")
		}
	}
}
