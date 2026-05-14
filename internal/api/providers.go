package api

import (
	"net/http"

	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/store"
)

// handleListProviders returns the DB-seeded provider rows merged with
// boot-profile-backed entries from the Container's BootProfiles registry
// (CW-20260514-0047). Boot-profile entries use the stable encoded ID
// `bootprofile:<profile_id>` as both their row id and their provider_type
// so the dropdown can round-trip the selection back through
// `session.Provider` unchanged.
//
// Coexistence rule: DB-seeded rows (Anthropic, the pty-* CLI rows, etc.)
// are emitted unchanged. Boot-profile entries are additive — even if a
// profile happens to use `provider_alias: claude`, the DB Anthropic row
// AND the boot-profile row are both surfaced with distinct IDs. The FE
// dropdown groups by provider_id, so two rows with different IDs render
// as two visually-distinct dropdown groups.
//
// Empty / nil registry leaves the response identical to the pre-feature
// shape; this satisfies the "no catalog → no behavior change" acceptance
// criterion.
func (a *API) handleListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := a.Services.Store.ListProviders()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

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
// bootprofile.Compile). We append " (boot profile)" so an operator
// scanning the providers panel can immediately distinguish boot-
// profile-backed rows from the DB-seeded ones — there's no separate
// affordance in the existing FE chrome.
func bootProfileProviderLabel(spec *bootprofile.LaunchSpec) string {
	if spec.UILabel == "" {
		return spec.ProfileID + " (boot profile)"
	}
	return spec.UILabel + " (boot profile)"
}

// bootProfileModelLabel is the dropdown text used inside the model
// group. Same suffix policy as the provider label — duplication is
// intentional because each row renders in a different chrome (provider
// settings panel vs. composer model dropdown) and we don't want either
// surface to silently drop the boot-profile disambiguator.
func bootProfileModelLabel(spec *bootprofile.LaunchSpec) string {
	if spec.UILabel == "" {
		return spec.ProfileID + " (boot profile)"
	}
	return spec.UILabel + " (boot profile)"
}
