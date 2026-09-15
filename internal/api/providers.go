package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/nanite/internal/providercatalog"
	"github.com/hollis-labs/nanite/internal/store"
)

// handleListProviders returns the provider-dropdown rows merged from two
// sources, in order:
//
//  1. Container.ProviderCatalog (CW-20260526-0001) — one row per
//     provider successfully registered in initProviders. This is the
//     authoritative source for API providers; registering a new engine
//     provider auto-surfaces it without a parallel DB seed.
//  2. Store.ListProviders() — DB-seeded rows whose name is NOT in the
//     catalog. Pre-hybrid backstop so a freshly-built DB or a future
//     operator-managed row still surfaces.
//
// TASKS/phase-2/04-retire-boot-profile-catalog.md: a third source used to
// live here — Container.BootProfiles, file-backed launch profiles
// emitted with an encoded `bootprofile:<id>` row id/provider_type. That
// catalog is retired in full; the dropdown surfaces only catalog + DB
// rows now.
//
// nil-safe — when ProviderCatalog is nil (tests that don't wire it)
// the response degrades to the DB-only shape, keeping the test suite
// stable.
func (a *API) handleListProviders(w http.ResponseWriter, r *http.Request) {
	dbProviders, err := a.Services.Store.ListProviders(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	dbProviders = visibleProviderRows(dbProviders)

	providers := mergeCatalogAndDBProviders(a.Services.ProviderCatalog, dbProviders)

	a.jsonResp(w, http.StatusOK, providers)
}

// mergeCatalogAndDBProviders is the hybrid merge: catalog wins over DB
// when both carry the same provider_type, DB rows for un-cataloged
// names fall through unchanged. catalog=nil collapses to the pre-hybrid
// pass-through. Order: catalog rows first (registration order),
// then DB rows with provider_types not present in the catalog.
//
// Exposed (lowercase, package-internal) so the per-source tests can
// exercise the merge without an HTTP roundtrip.
func mergeCatalogAndDBProviders(catalog *providercatalog.Catalog, dbProviders []store.ProviderConfig) []store.ProviderConfig {
	if catalog == nil {
		return dbProviders
	}
	entries := catalog.List()
	if len(entries) == 0 {
		return dbProviders
	}

	covered := make(map[string]struct{}, len(entries))
	out := make([]store.ProviderConfig, 0, len(entries)+len(dbProviders))
	for _, e := range entries {
		out = append(out, catalogProviderRow(e))
		covered[e.Name] = struct{}{}
	}
	for _, p := range dbProviders {
		if _, ok := covered[p.ProviderType]; ok {
			continue
		}
		out = append(out, p)
	}
	return out
}

// catalogProviderRow synthesizes a store.ProviderConfig from a catalog
// Entry. The shape mirrors what SeedProviders persists for the same
// provider so the FE consumer (and any code that joined on the DB row)
// sees the same fields. is_enabled defaults to true — the catalog
// records "this provider is wired and reachable"; an operator-driven
// disable knob would need a different layer.
func catalogProviderRow(e providercatalog.Entry) store.ProviderConfig {
	return store.ProviderConfig{
		ID:           e.RowID,
		Name:         e.DisplayName,
		ProviderType: e.Name,
		IsEnabled:    true,
	}
}

// handleListModels returns the DB-seeded model rows. TASKS/phase-2/04-
// retire-boot-profile-catalog.md removed a second source that used to
// live here — one synthesized model row per boot-profile catalog entry.
func (a *API) handleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := a.Services.Store.ListModels(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	models = visibleModelRows(models)

	a.jsonResp(w, http.StatusOK, models)
}

// allowedFromEnv reads a comma-separated allowlist. Unset or empty means "no
// restriction", so the default behaviour is unchanged and a typo that empties
// the list shows everything rather than nothing — the safer direction for a
// list someone picks from.
func allowedFromEnv(name string) map[string]bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	out := map[string]bool{}
	for _, s := range strings.Split(raw, ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out[s] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func visibleProviderRows(providers []store.ProviderConfig) []store.ProviderConfig {
	// NANITE_VISIBLE_PROVIDERS narrows what a person can pick — by provider
	// type, e.g. "openai". Nanite seeds providers it has no key for, and a
	// picker offering a provider that cannot answer is a demo failure waiting
	// for someone to choose it.
	allowed := allowedFromEnv("NANITE_VISIBLE_PROVIDERS")
	out := providers[:0]
	for _, p := range providers {
		if isHiddenPTYProviderType(p.ProviderType) {
			continue
		}
		if allowed != nil && !allowed[strings.ToLower(p.ProviderType)] {
			continue
		}
		out = append(out, p)
	}
	return out
}

func visibleModelRows(models []store.Model) []store.Model {
	// NANITE_VISIBLE_MODELS narrows the model list by model id. The catalogue
	// is compiled into the binary while a gateway serves whatever it serves,
	// so the two disagree by default and the picker offers models that 404.
	allowed := allowedFromEnv("NANITE_VISIBLE_MODELS")
	out := models[:0]
	for _, m := range models {
		if isHiddenPTYProviderType(m.ProviderType) {
			continue
		}
		if allowed != nil && !allowed[strings.ToLower(m.ModelID)] {
			continue
		}
		out = append(out, m)
	}
	return out
}

func isHiddenPTYProviderType(providerType string) bool {
	return providerType == "pty" || strings.HasPrefix(providerType, "pty-")
}
