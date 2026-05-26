package api

import (
	"net/http"
	"strings"

	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/providercatalog"
	"github.com/hollis-labs/nanite/internal/store"
)

// handleListProviders returns the provider-dropdown rows merged from
// three sources, in order:
//
//  1. Container.ProviderCatalog (CW-20260526-0001) — one row per
//     provider successfully registered in initProviders. This is the
//     authoritative source for API providers; registering a new engine
//     provider auto-surfaces it without a parallel DB seed.
//  2. Store.ListProviders() — DB-seeded rows whose name is NOT in the
//     catalog. Pre-hybrid backstop so a freshly-built DB or a future
//     operator-managed row still surfaces.
//  3. Container.BootProfiles (CW-20260514-0047) — file-backed launch
//     profiles, emitted with their stable encoded id.
//
// Boot-profile entries use the encoded id `bootprofile:<profile_id>` as
// both their row id and their provider_type so the dropdown can
// round-trip the selection back through `session.Provider` unchanged.
//
// nil-safe — when ProviderCatalog is nil (tests that don't wire it)
// the response degrades to the pre-hybrid shape (DB + boot-profile),
// keeping the test suite stable.
func (a *API) handleListProviders(w http.ResponseWriter, r *http.Request) {
	dbProviders, err := a.Services.Store.ListProviders()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	dbProviders = visibleProviderRows(dbProviders)

	providers := mergeCatalogAndDBProviders(a.Services.ProviderCatalog, dbProviders)

	if reg := a.Services.BootProfiles; reg != nil {
		for _, spec := range reg.List() {
			// PR #170 round 1: skip launchless / prompt-only profiles.
			// A LaunchSpec with empty Provider compiled without a paired
			// launch (Profile.Launch == "") and has no CLI target — the
			// runtime cannot boot it, so it must not appear as a
			// selectable dropdown row.
			if spec.Provider == "" {
				continue
			}
			providers = append(providers, bootProfileProviderRow(spec))
		}
	}

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

// handleListModels returns the DB-seeded model rows merged with one
// synthesized model row per boot-profile entry. The FE dropdown
// (ComposerToolbar.tsx) iterates models, groups them by provider_id,
// and emits one selectable item per model — so a boot profile must
// surface at least one model row for the user to see/pick it.
//
// The synthesized model uses the encoded provider id verbatim as its
// model_id, so the selection payload `{provider, model}` round-trips
// the boot-profile pointer in both fields. CW-20260514-0048 will
// consume the provider half on the chat-runtime side; either side
// is enough to recover the LaunchSpec via Registry.Lookup.
func (a *API) handleListModels(w http.ResponseWriter, r *http.Request) {
	models, err := a.Services.Store.ListModels()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	models = visibleModelRows(models)

	if reg := a.Services.BootProfiles; reg != nil {
		for _, spec := range reg.List() {
			// PR #170 round 1: see handleListProviders — launchless
			// profiles have no runtime target, so they must not
			// surface in the model dropdown either.
			if spec.Provider == "" {
				continue
			}
			models = append(models, bootProfileModelRow(spec))
		}
	}

	a.jsonResp(w, http.StatusOK, models)
}

// bootProfileProviderRow builds the synthetic provider row for one
// compiled LaunchSpec. The row shape mirrors store.ProviderConfig so
// the existing FE ProviderConfig type and the ComposerToolbar grouping
// logic accept it without changes. is_enabled defaults to true — an
// operator with a configured catalog clearly wants the entries to be
// selectable.
//
// CreatedAt / UpdatedAt are deliberately empty: the catalog is a
// filesystem-backed source, and surfacing a fake DB timestamp would
// mislead any future audit / sort path that depends on those fields.
// The settings UI currently shows them as raw strings — empty is the
// honest answer.
func bootProfileProviderRow(spec *bootprofile.LaunchSpec) store.ProviderConfig {
	id := bootprofile.EncodeProviderID(spec.ProfileID)
	return store.ProviderConfig{
		ID:           id,
		Name:         bootProfileProviderLabel(spec),
		ProviderType: id,
		IsEnabled:    true,
	}
}

// bootProfileModelRow builds the synthetic model row paired with the
// synthetic provider row. The dropdown groups models by provider_id
// so this row must point at the same id the provider row carries.
//
// ModelID echoes the encoded provider id verbatim — see the comment
// on handleListModels for the round-trip rationale.
func bootProfileModelRow(spec *bootprofile.LaunchSpec) store.Model {
	id := bootprofile.EncodeProviderID(spec.ProfileID)
	return store.Model{
		ID:            id,
		ProviderID:    id,
		ModelID:       id,
		DisplayName:   bootProfileModelLabel(spec),
		IsEnabled:     true,
		SupportsTools: true,
		ProviderType:  id,
	}
}

// bootProfileProviderLabel is the human-readable name surfaced on the
// provider row. UILabel is the canonical operator-facing label
// (launch.ui_label → profile.display_name → profile.id, decided in
// bootprofile.Compile).
func bootProfileProviderLabel(spec *bootprofile.LaunchSpec) string {
	if spec.UILabel == "" {
		return spec.ProfileID
	}
	return spec.UILabel
}

// bootProfileModelLabel is the dropdown text used inside the model
// group.
func bootProfileModelLabel(spec *bootprofile.LaunchSpec) string {
	if spec.UILabel == "" {
		return spec.ProfileID
	}
	return spec.UILabel
}

func visibleProviderRows(providers []store.ProviderConfig) []store.ProviderConfig {
	out := providers[:0]
	for _, p := range providers {
		if isHiddenPTYProviderType(p.ProviderType) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func visibleModelRows(models []store.Model) []store.Model {
	out := models[:0]
	for _, m := range models {
		if isHiddenPTYProviderType(m.ProviderType) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func isHiddenPTYProviderType(providerType string) bool {
	return providerType == "pty" || strings.HasPrefix(providerType, "pty-")
}
