package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/providercatalog"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// TASKS/phase-2/04-retire-boot-profile-catalog.md: this file used to
// also cover the "catalog configured" boot-profile dropdown branch
// (CW-20260514-0047) — a synthesized provider/model row per compiled
// boot-profile LaunchSpec, coexisting with DB-seeded rows. That whole
// mechanism (internal/bootprofile, Container.BootProfiles,
// ContainerConfig.BootProfileCatalogPath) is retired in full; the
// dropdown surfaces only the registry-backed ProviderCatalog + DB rows
// now (CW-20260526-0001, covered below).

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
	if len(got) == 0 {
		t.Fatal("expected seeded providers, got none")
	}
}

// newTestAPIWithSeededProviders spins up a Container backed by a seeded
// DB (no boot-profile catalog, no ProviderCatalog) so DB-only-shape
// assertions can fire.
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
	t.Cleanup(func() { svc.Shutdown() })
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
	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	if err := s.Seed(context.Background()); err != nil {
		t.Fatalf("store.Seed: %v", err)
	}
	if err := s.SeedProviders(context.Background()); err != nil {
		t.Fatalf("store.SeedProviders: %v", err)
	}
	return s
}

// CW-20260526-0001 — registry-backed catalog merge. The hybrid layer
// must surface catalog entries (one per registered engine provider) as
// dropdown rows, must hide the DB rows whose provider_type a catalog
// entry already covers, and must leave the response unchanged when no
// catalog is wired (degraded mode for tests + forward-compat with any
// future operator-managed DB rows).

func TestListProviders_CatalogConfigured_SurfacesAllEntries(t *testing.T) {
	s := newSeededStore(t)
	cat := providercatalog.New()
	cat.Add(providercatalog.Entry{Name: "anthropic", DisplayName: "Anthropic", RowID: "anthropic-001"})
	cat.Add(providercatalog.Entry{Name: "openai", DisplayName: "OpenAI", RowID: "openai-001"})

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:           s,
		Providers:       provider.NewRegistry(),
		ProviderCatalog: cat,
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	t.Cleanup(func() { svc.Shutdown() })
	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

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

	want := map[string]string{"anthropic": "Anthropic", "openai": "OpenAI"}
	seen := map[string]string{}
	for _, p := range got {
		if name, ok := want[p.ProviderType]; ok {
			seen[p.ProviderType] = p.Name
			if p.Name != name {
				t.Errorf("provider %q name = %q, want %q (catalog DisplayName takes precedence)", p.ProviderType, p.Name, name)
			}
			if !p.IsEnabled {
				t.Errorf("provider %q IsEnabled = false; want true (catalog default)", p.ProviderType)
			}
		}
	}
	if len(seen) != len(want) {
		t.Errorf("catalog providers seen: %v; want all of %v", seen, want)
	}
}

// TestListProviders_CatalogHidesDuplicateDBRow — when both DB seed and
// catalog name a provider, catalog wins (DB row is omitted). Pins the
// "no duplicate dropdown entry" invariant — the FE groups by id, so a
// DB row alongside a catalog row would render two separate groups for
// the same engine provider.
func TestListProviders_CatalogHidesDuplicateDBRow(t *testing.T) {
	s := newSeededStore(t)
	// SeedProviders already inserted anthropic-001 with ProviderType="anthropic".
	// Wire a catalog entry for the same provider_type with a DIFFERENT
	// display name so we can prove which source wins.
	cat := providercatalog.New()
	cat.Add(providercatalog.Entry{Name: "anthropic", DisplayName: "Anthropic-from-catalog", RowID: "anthropic-001"})

	svc, err := service.NewContainer(service.ContainerConfig{
		Store:           s,
		Providers:       provider.NewRegistry(),
		ProviderCatalog: cat,
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	t.Cleanup(func() { svc.Shutdown() })
	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/providers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var got []store.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	count := 0
	var seen store.ProviderConfig
	for _, p := range got {
		if p.ProviderType == "anthropic" {
			count++
			seen = p
		}
	}
	if count != 1 {
		t.Fatalf("anthropic providers in response: got %d, want 1 (catalog dedupes DB row)", count)
	}
	if seen.Name != "Anthropic-from-catalog" {
		t.Errorf("anthropic Name = %q, want Anthropic-from-catalog (catalog wins over DB)", seen.Name)
	}
}

// TestListProviders_NilCatalog_FallsBackToDB — without a catalog the
// handler must keep emitting DB rows. Regression coverage for the test
// suite (existing fixtures use ContainerConfig{Providers, Store} only).
func TestListProviders_NilCatalog_FallsBackToDB(t *testing.T) {
	s := newSeededStore(t)
	svc, err := service.NewContainer(service.ContainerConfig{
		Store:     s,
		Providers: provider.NewRegistry(),
		// ProviderCatalog intentionally omitted.
	})
	if err != nil {
		t.Fatalf("service.NewContainer: %v", err)
	}
	t.Cleanup(func() { svc.Shutdown() })
	a := New(svc)
	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/api/providers", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	var got []store.ProviderConfig
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var sawAnthropic bool
	for _, p := range got {
		if p.ProviderType == "anthropic" {
			sawAnthropic = true
		}
	}
	if !sawAnthropic {
		t.Error("nil catalog: anthropic DB row should still surface (degraded mode)")
	}
}
