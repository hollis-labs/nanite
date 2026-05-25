package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// CW-20260514-0047: tests covering the dropdown surface for both the
// "no catalog configured" branch (DB-seeded providers only) and the
// "catalog configured" branch (DB rows + boot-profile entries
// coexisting). Both /api/providers and /api/models are exercised
// because the FE composer dropdown groups by provider_id and iterates
// models — we need both shapes correct.

func TestListProviders_NoBootCatalog_OnlyDBRows(t *testing.T) {
	_, mux := newTestAPIWithSeededProviders(t)

	req := httptest.NewRequest("GET", "/api/providers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got []store.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Seeded API rows populated via SeedProviders.
	// Confirm none of them carry a boot-profile-namespaced provider
	// type — that's the "no behavior change when no catalog
	// configured" acceptance criterion. Coexistence is covered in
	// TestListProviders_BootCatalogConfigured_CoexistsWithDBRows.
	if len(got) == 0 {
		t.Fatal("expected seeded providers, got none")
	}
	for _, p := range got {
		if bootprofile.IsProviderID(p.ProviderType) {
			t.Errorf("provider %q unexpectedly bootprofile-namespaced", p.ID)
		}
		if bootprofile.IsProviderID(p.ID) {
			t.Errorf("provider id %q unexpectedly bootprofile-namespaced", p.ID)
		}
	}
}

func TestListProviders_BootCatalogConfigured_CoexistsWithDBRows(t *testing.T) {
	mux := newTestAPIWithBootCatalog(t)

	req := httptest.NewRequest("GET", "/api/providers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got []store.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Coexistence: DB-seeded API providers and boot-profile providers
	// must appear together. PTY/CLI DB providers are intentionally
	// hidden from user-facing provider lists; CLI launches are exposed
	// via boot profiles instead.
	var sawHiddenPTY, sawBootBackend, sawAnthropic bool
	for _, p := range got {
		if isHiddenPTYProviderType(p.ProviderType) {
			sawHiddenPTY = true
		}
		switch p.ProviderType {
		case "anthropic":
			sawAnthropic = true
		case bootprofile.EncodeProviderID("nanite.backend.main"):
			sawBootBackend = true
		}
	}
	if sawHiddenPTY {
		t.Error("hidden PTY provider leaked into /api/providers")
	}
	if !sawAnthropic {
		t.Error("seeded anthropic row missing")
	}
	if !sawBootBackend {
		t.Error("boot-profile provider for nanite.backend.main missing")
	}
}

func TestListModels_BootCatalogConfigured_SurfacesProfileModelRow(t *testing.T) {
	mux := newTestAPIWithBootCatalog(t)

	req := httptest.NewRequest("GET", "/api/models", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var got []store.Model
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	encoded := bootprofile.EncodeProviderID("nanite.backend.main")
	var sawBootModel, sawHiddenPTYModel bool
	for _, m := range got {
		if m.ModelID == encoded && m.ProviderType == encoded {
			sawBootModel = true
		}
		if isHiddenPTYProviderType(m.ProviderType) || isHiddenPTYProviderType(m.ProviderID) {
			sawHiddenPTYModel = true
		}
	}
	if !sawBootModel {
		t.Error("boot-profile model row missing for nanite.backend.main")
	}
	if sawHiddenPTYModel {
		t.Error("hidden PTY model leaked into /api/models")
	}
}

// TestListProviders_DecodeRoundTrip — the dropdown payload sends
// provider_type back to the server unchanged. Confirm the value the
// API emits round-trips through DecodeProviderID, so the runtime
// (CW-20260514-0048) can pull the ProfileID back out without any
// extra plumbing.
func TestListProviders_DecodeRoundTrip(t *testing.T) {
	mux := newTestAPIWithBootCatalog(t)

	req := httptest.NewRequest("GET", "/api/providers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var got []store.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var found bool
	for _, p := range got {
		if !bootprofile.IsProviderID(p.ProviderType) {
			continue
		}
		found = true
		profileID, ok := bootprofile.DecodeProviderID(p.ProviderType)
		if !ok || profileID == "" {
			t.Errorf("provider %q failed decode round-trip", p.ProviderType)
		}
	}
	if !found {
		t.Fatal("expected at least one boot-profile-namespaced provider in fixture")
	}
}

func TestListProviders_BootProfileLabelIncludesDisambiguator(t *testing.T) {
	mux := newTestAPIWithBootCatalog(t)
	req := httptest.NewRequest("GET", "/api/providers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var got []store.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	encoded := bootprofile.EncodeProviderID("nanite.backend.main")
	for _, p := range got {
		if p.ProviderType != encoded {
			continue
		}
		// The fixture's profile carries display_name="Nanite —
		// Backend" and launch.ui_label="Nanite (Claude PTY)".
		// bootprofile.Compile prefers ui_label, so the dropdown
		// row should derive from that directly. CLI launches are now
		// user-facing concepts, so the label should not carry an
		// implementation suffix.
		if p.Name == "" {
			t.Error("boot-profile provider name is empty")
		}
		if want := "Nanite (Claude PTY)"; p.Name != want {
			t.Errorf("provider name = %q, want %q", p.Name, want)
		}
		return
	}
	t.Fatal("boot-profile provider not found")
}

// newTestAPIWithBootCatalog spins up a Container backed by a one-
// profile boot catalog so the providers/models tests can exercise the
// "catalog configured" branch without dragging in real
// ~/.config/nanite/config.yaml state. Seeded DB providers are also
// populated so coexistence assertions can fire.
func newTestAPIWithBootCatalog(t *testing.T) *http.ServeMux {
	t.Helper()
	s := newSeededStore(t)
	catalogRoot := t.TempDir()
	writeFixtureCatalog(t, catalogRoot)

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:                  s,
		Providers:              provider.NewRegistry(),
		BootProfileCatalogPath: catalogRoot,
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	if svc.BootProfiles == nil || svc.BootProfiles.IsEmpty() {
		t.Fatalf("expected container to wire a non-empty boot-profile registry from %s", catalogRoot)
	}

	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	return mux
}

// newTestAPIWithSeededProviders is the "no catalog configured" twin
// of newTestAPIWithBootCatalog: same seeded DB, no BootProfileCatalogPath.
// Used to pin the "no behavior change when no catalog" assertion.
func newTestAPIWithSeededProviders(t *testing.T) (*API, *http.ServeMux) {
	t.Helper()
	s := newSeededStore(t)

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:     s,
		Providers: provider.NewRegistry(),
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)
	return a, mux
}

// newSeededStore opens a temp-dir store and runs the same Seed +
// SeedProviders calls that cmd/nanite/main.go runs at boot. Without
// these, the providers/models tables are empty and the coexistence
// assertions cannot fire against the canonical DB rows.
func newSeededStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Seed(); err != nil {
		t.Fatalf("store.Seed: %v", err)
	}
	if err := s.SeedProviders(); err != nil {
		t.Fatalf("store.SeedProviders: %v", err)
	}
	return s
}

// TestListProviders_ReloadPicksUpNewProfile mirrors the "catalog
// refresh path" acceptance criterion. We mount a one-profile catalog,
// stamp a second profile to disk, call Registry.Reload, and confirm
// the new profile shows up in the next /api/providers response. The
// plugin lifecycle hookup is out of scope for 0047 — what matters
// here is that the entry point exists and behaves.
func TestListProviders_ReloadPicksUpNewProfile(t *testing.T) {
	s := newSeededStore(t)

	catalogRoot := t.TempDir()
	writeFixtureCatalog(t, catalogRoot)

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:                  s,
		Providers:              provider.NewRegistry(),
		BootProfileCatalogPath: catalogRoot,
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	// Baseline: only the backend profile is loaded.
	encodedFrontend := bootprofile.EncodeProviderID("nanite.frontend.main")
	if _, ok := svc.BootProfiles.Lookup(encodedFrontend); ok {
		t.Fatal("precondition: frontend profile should not be loaded yet")
	}

	// Stamp a second profile + reload.
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(catalogRoot, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("boot-profiles/nanite.frontend.main.yaml",
		`id: nanite.frontend.main
display_name: "Nanite — Frontend"
launch: nanite-claude
identity:
  lineage_alias: nanite.frontend.main
  role: frontend
slots:
  agent:
    type: text
    content: "you paint"
`)
	if err := svc.BootProfiles.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/providers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var got []store.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var sawFrontend bool
	for _, p := range got {
		if p.ProviderType == encodedFrontend {
			sawFrontend = true
		}
	}
	if !sawFrontend {
		t.Fatal("Reload did not surface the new boot-profile in /api/providers")
	}
}

// TestListProviders_LaunchlessProfileIsHidden pins the PR #170
// round 1 fix: a profile with no `launch:` field compiles to a
// LaunchSpec with Provider == "" and has no runtime target. It must
// be invisible to the dropdown (both /api/providers and /api/models)
// because the user cannot meaningfully boot it. The profile is still
// present in Registry.Lookup so prompt-preview / non-dropdown callers
// can compile it; only the dropdown surfaces filter it out.
func TestListProviders_LaunchlessProfileIsHidden(t *testing.T) {
	s := newSeededStore(t)

	catalogRoot := t.TempDir()
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(catalogRoot, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Launchless profile: no `launch:` key. Compiles cleanly, but
	// the resulting LaunchSpec.Provider is empty.
	mustWrite("boot-profiles/nanite.preview-only.yaml",
		`id: nanite.preview-only
display_name: "Preview-Only Profile"
identity:
  lineage_alias: nanite.preview-only
  role: preview
slots:
  agent:
    type: text
    content: "prompt-only profile"
`)

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:                  s,
		Providers:              provider.NewRegistry(),
		BootProfileCatalogPath: catalogRoot,
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	// /api/providers must not include the launchless profile.
	{
		req := httptest.NewRequest("GET", "/api/providers", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var got []store.ProviderConfig
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode providers: %v", err)
		}
		encoded := bootprofile.EncodeProviderID("nanite.preview-only")
		for _, p := range got {
			if p.ProviderType == encoded {
				t.Fatalf("launchless profile leaked into /api/providers as %+v", p)
			}
		}
	}

	// /api/models must not include the launchless profile.
	{
		req := httptest.NewRequest("GET", "/api/models", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		var got []store.Model
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatalf("decode models: %v", err)
		}
		encoded := bootprofile.EncodeProviderID("nanite.preview-only")
		for _, m := range got {
			if m.ProviderID == encoded {
				t.Fatalf("launchless profile leaked into /api/models as %+v", m)
			}
		}
	}

	// Registry still knows about it — preview callers can compile.
	encoded := bootprofile.EncodeProviderID("nanite.preview-only")
	if _, ok := svc.BootProfiles.Lookup(encoded); !ok {
		t.Fatal("Registry.Lookup should still find launchless profile (only the dropdown surfaces filter)")
	}
}

func writeFixtureCatalog(t *testing.T, root string) {
	t.Helper()
	mustWrite := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("boot-profiles/nanite.backend.main.yaml",
		`id: nanite.backend.main
display_name: "Nanite — Backend"
launch: nanite-claude
identity:
  lineage_alias: nanite.backend.main
  role: backend
slots:
  agent:
    type: text
    content: "you build"
`)
	mustWrite("launches/nanite-claude.yaml",
		`id: nanite-claude
provider: pty-claude
workdir: /tmp/nanite
ui_label: "Nanite (Claude PTY)"
`)
}
