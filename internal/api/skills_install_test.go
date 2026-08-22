package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// TASKS/skills/05's "Fix required" section (fresh reviewer, 2026-08-21):
// the task's original diff shipped runSkillInstall's 422-vs-500 HTTP-status
// classification and handleSyncSkill's slug-mismatch guard with zero
// automated coverage. This file closes that gap using this package's
// established httptest-based convention (newTestAPI in api_test.go).

// skillFixture resolves one of task 04's internal/skillinstall
// install-pipeline test fixtures by relative path. internal/api has no
// existing testdata/ convention of its own (checked before adding one --
// see task 05's Work Log), so these fixtures are reused directly rather
// than duplicated.
func skillFixture(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "skillinstall", "testdata", "fixtures", name))
	if err != nil {
		t.Fatalf("resolve fixture path for %q: %v", name, err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("fixture %q not found at %s: %v", name, abs, err)
	}
	return abs
}

// newSkillsTestAPI mirrors api_test.go's newTestAPI, with one addition: it
// sets AppConfig.Skills.VendorStorageDir to a path under this test's own
// t.TempDir() root. Without this, service.Container's SkillVendor
// construction falls back to the repo-root-relative default
// ("data/skills/vendor", resolved against the package's working directory
// at test time) -- the exact same pre-existing side effect
// ArtifactsConfig.StorageDir has for internal/api/data/artifacts/, and
// which artifacts_test.go's own newArtifactTestAPI already avoids the same
// way for artifacts. Harmless (the directory is .gitignore'd) but easy to
// avoid, so this test file does.
func newSkillsTestAPI(t *testing.T) (*API, *http.ServeMux) {
	t.Helper()
	root := t.TempDir()
	dbPath := filepath.Join(root, "test.db")
	prepareAPIStoreDB(t, dbPath)
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	appCfg := config.DefaultAppConfig()
	appCfg.Skills.VendorStorageDir = filepath.Join(root, "skills-vendor")

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:             s,
		Providers:         provider.NewRegistry(),
		WorkingDir:        root,
		ManagedConfigRoot: filepath.Join(root, ".nanite"),
		AppConfig:         appCfg,
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	t.Cleanup(func() { svc.Shutdown() })
	if svc.SkillVendor == nil {
		t.Fatal("expected SkillVendor to initialize against a writable temp root")
	}

	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	return a, mux
}

func postSkillJSON(t *testing.T, mux *http.ServeMux, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func decodeInstallResponse(t *testing.T, w *httptest.ResponseRecorder) InstallSkillResponse {
	t.Helper()
	var resp InstallSkillResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode install response: %v; body: %s", err, w.Body.String())
	}
	return resp
}

func decodeErrorMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v; body: %s", err, w.Body.String())
	}
	return resp["error"]
}

// writeMinimalSkillPackage writes a minimal, validly-parseable SKILL.md
// package to dir declaring the given slug/name. skill.ParsePackageDir does
// not itself confirm that declared scripts/references/assets exist on disk
// (that's task 04's Validator's job, a distinct and later concern) so this
// is enough for handleSyncSkill's pre-Install slug-match parse to succeed.
func writeMinimalSkillPackage(t *testing.T, dir, slug, name string) {
	t.Helper()
	content := "---\nname: " + name + "\nslug: " + slug + "\ndescription: minimal sync-mismatch test fixture\ncontext: inline\n---\n\nMinimal body.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

// 1. Successful install.
func TestHandleInstallSkill_Success(t *testing.T) {
	_, mux := newSkillsTestAPI(t)

	w := postSkillJSON(t, mux, "/api/skills/install", map[string]string{
		"path": skillFixture(t, "sample-skill"),
	})

	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/skills/install: expected 201, got %d; body: %s", w.Code, w.Body.String())
	}
	resp := decodeInstallResponse(t, w)
	if resp.Skill.Slug != "sample-skill" {
		t.Errorf("expected skill slug %q, got %q", "sample-skill", resp.Skill.Slug)
	}
	if resp.Skill.ID == "" {
		t.Error("expected a non-empty skill ID")
	}
	if resp.Address == "" {
		t.Error("expected a non-empty vendored address")
	}
	if resp.Reused {
		t.Error("expected reused=false for a brand-new address on first install")
	}
}

// 2. Malformed package -> 422, never 500 or a panic.
func TestHandleInstallSkill_MalformedPackage(t *testing.T) {
	for _, name := range []string{"malformed-frontmatter", "malformed-missing-script"} {
		t.Run(name, func(t *testing.T) {
			_, mux := newSkillsTestAPI(t)

			w := postSkillJSON(t, mux, "/api/skills/install", map[string]string{
				"path": skillFixture(t, name),
			})

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("expected 422 for malformed package %q, got %d; body: %s", name, w.Code, w.Body.String())
			}
			if msg := decodeErrorMessage(t, w); msg == "" {
				t.Error("expected a non-empty error message")
			}
		})
	}
}

// 3. SkillVendor == nil -> 503 from both endpoints, not a nil-pointer panic.
func TestHandleInstallSkill_VendorNil(t *testing.T) {
	a, mux := newSkillsTestAPI(t)
	a.Services.SkillVendor = nil

	w := postSkillJSON(t, mux, "/api/skills/install", map[string]string{
		"path": skillFixture(t, "sample-skill"),
	})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when SkillVendor is nil, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleSyncSkill_VendorNil(t *testing.T) {
	a, mux := newSkillsTestAPI(t)
	a.Services.SkillVendor = nil

	w := postSkillJSON(t, mux, "/api/skills/anything/sync", map[string]string{
		"path": skillFixture(t, "sample-skill"),
	})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when SkillVendor is nil, got %d; body: %s", w.Code, w.Body.String())
	}
}

// 4. Sync against an unknown slug -> 404.
func TestHandleSyncSkill_UnknownSlug(t *testing.T) {
	_, mux := newSkillsTestAPI(t)

	w := postSkillJSON(t, mux, "/api/skills/does-not-exist/sync", map[string]string{
		"path": skillFixture(t, "sample-skill"),
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for sync against an unknown slug, got %d; body: %s", w.Code, w.Body.String())
	}
}

// 5. Sync against a real, already-installed slug with a package declaring a
// different slug -> 409, and no new skill row created as a side effect of
// the rejected attempt. This is the security-relevant guard: a caller must
// never be able to mutate a different skill's row by supplying a
// mismatched path/slug pair.
func TestHandleSyncSkill_SlugMismatch(t *testing.T) {
	a, mux := newSkillsTestAPI(t)

	installed := decodeInstallResponse(t, postSkillJSON(t, mux, "/api/skills/install", map[string]string{
		"path": skillFixture(t, "sample-skill"),
	}))
	if installed.Skill.Slug != "sample-skill" {
		t.Fatalf("setup: expected sample-skill install, got slug %q", installed.Skill.Slug)
	}

	otherPkg := t.TempDir()
	writeMinimalSkillPackage(t, otherPkg, "other-skill", "Other Skill")

	w := postSkillJSON(t, mux, "/api/skills/sample-skill/sync", map[string]string{
		"path": otherPkg,
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a sync slug mismatch, got %d; body: %s", w.Code, w.Body.String())
	}

	got, err := a.Services.Store.GetSkillBySlug(context.Background(), "other-skill")
	if err != nil {
		t.Fatalf("GetSkillBySlug(other-skill): %v", err)
	}
	if got != nil {
		t.Fatalf("rejected sync must not create a row for the mismatched slug, got %+v", got)
	}

	// The target row itself must also be untouched.
	unchanged, err := a.Services.Store.GetSkillBySlug(context.Background(), "sample-skill")
	if err != nil {
		t.Fatalf("GetSkillBySlug(sample-skill): %v", err)
	}
	if unchanged == nil {
		t.Fatal("expected sample-skill row to still exist")
	}
	if unchanged.ContentHash != installed.Skill.ContentHash || unchanged.Version != installed.Skill.Version {
		t.Errorf("expected sample-skill row unchanged by the rejected sync, got hash=%q version=%d (was hash=%q version=%d)",
			unchanged.ContentHash, unchanged.Version, installed.Skill.ContentHash, installed.Skill.Version)
	}
}

// 6. Idempotent re-sync (same content) -> 200 and reused: true.
func TestHandleSyncSkill_IdempotentReuse(t *testing.T) {
	_, mux := newSkillsTestAPI(t)
	fixture := skillFixture(t, "sample-skill")

	first := decodeInstallResponse(t, postSkillJSON(t, mux, "/api/skills/install", map[string]string{
		"path": fixture,
	}))
	if first.Reused {
		t.Fatal("expected reused=false on the very first install")
	}

	w := postSkillJSON(t, mux, "/api/skills/sample-skill/sync", map[string]string{
		"path": fixture,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for an idempotent re-sync, got %d; body: %s", w.Code, w.Body.String())
	}
	second := decodeInstallResponse(t, w)
	if !second.Reused {
		t.Error("expected reused=true for a re-sync against unchanged content")
	}
	if second.Address != first.Address {
		t.Errorf("expected the same vendored address on an unchanged re-sync, got %q vs %q", second.Address, first.Address)
	}
}
