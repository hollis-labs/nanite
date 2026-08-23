package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestValidateProjectRepoPathPolicy(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	homeChild, err := os.MkdirTemp(home, ".nanite-repo-policy-")
	if err != nil {
		t.Fatalf("MkdirTemp under home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(homeChild) })

	if _, err := validateProjectRepoPath(""); err != nil {
		t.Fatalf("empty repo_path should remain allowed: %v", err)
	}
	if _, err := validateProjectRepoPath(homeChild); err != nil {
		t.Fatalf("home subdirectory should be allowed: %v", err)
	}
	for name, path := range map[string]string{
		"filesystem-root": string(filepath.Separator),
		"home-itself":     home,
		"missing":         filepath.Join(t.TempDir(), "missing"),
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := validateProjectRepoPath(path); err == nil {
				t.Fatalf("validateProjectRepoPath(%q) = %q, want rejection", path, got)
			}
		})
	}

	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := validateProjectRepoPath(file); err == nil {
		t.Fatal("regular file accepted as repo_path")
	}

	if runtime.GOOS != "windows" {
		for _, path := range []string{"/etc", "/usr", "/var", "/System"} {
			if got, err := validateProjectRepoPath(path); err == nil {
				t.Fatalf("validateProjectRepoPath(%q) = %q, want system-tree rejection", path, got)
			}
		}
	}
}

func TestProjectsAPIRejectsUnsafeRepoPathOnCreate(t *testing.T) {
	_, mux := newTestAPI(t)
	body, _ := json.Marshal(map[string]string{
		"id":        "unsafe-root",
		"name":      "Unsafe Root",
		"repo_path": string(filepath.Separator),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/projects", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST unsafe repo_path = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid repo_path") {
		t.Fatalf("response does not identify repo_path: %s", rec.Body.String())
	}
}

func TestProjectsAPIExistingRowsValidateOnRepoPathUpdateOnly(t *testing.T) {
	a, _ := newTestAPI(t)
	legacy := &store.Project{ID: "legacy-root", Name: "Legacy", RepoPath: string(filepath.Separator)}
	if err := a.Services.Store.CreateProject(context.Background(), legacy); err != nil {
		t.Fatalf("seed legacy project: %v", err)
	}

	nameOnly, _ := json.Marshal(map[string]string{"name": "Renamed"})
	req := httptest.NewRequest(http.MethodPut, "/api/projects/legacy-root", bytes.NewReader(nameOnly))
	req.SetPathValue("pid", "legacy-root")
	rec := httptest.NewRecorder()
	a.handleUpdateProject(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("name-only update of legacy row = %d body=%s, want 200", rec.Code, rec.Body.String())
	}

	unsafeUpdate, _ := json.Marshal(map[string]string{"repo_path": string(filepath.Separator)})
	req = httptest.NewRequest(http.MethodPut, "/api/projects/legacy-root", bytes.NewReader(unsafeUpdate))
	req.SetPathValue("pid", "legacy-root")
	rec = httptest.NewRecorder()
	a.handleUpdateProject(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("repo_path update of legacy row = %d body=%s, want 400", rec.Code, rec.Body.String())
	}

	got, err := a.Services.Store.GetProject(context.Background(), "legacy-root")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.RepoPath != string(filepath.Separator) {
		t.Fatalf("rejected update changed repo_path to %q", got.RepoPath)
	}
}
