package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/hollis-labs/nanite/internal/service"

	"github.com/hollis-labs/nanite/internal/providerkeys"
)

func (a *API) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req UpdateProviderRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if err := a.Services.ProviderConfig.Update(r.Context(), id, req.toStore()); err != nil {
		if errors.Is(err, providerkeys.ErrInvalidKeyRef) {
			a.errorResp(w, http.StatusBadRequest, err.Error())
			return
		}
		a.errorResp(w, http.StatusInternalServerError, "failed to update provider: "+err.Error())
		return
	}

	p, err := a.Services.ProviderConfig.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "provider not found")
		return
	}
	a.jsonResp(w, http.StatusOK, providerConfigToView(p))
}

// handleSetProviderAPIKey accepts {"api_key": "sk-..."} and stores it in the OS keychain.
func (a *API) handleSetProviderAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body SetProviderAPIKeyRequest
	if err := a.decode(r, &body); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	result, err := a.Services.ProviderConfig.SetAPIKey(r.Context(), id, body.APIKey)
	if err != nil {
		var unavailable *service.ProviderCredentialStoreError
		if errors.As(err, &unavailable) {
			a.jsonResp(w, http.StatusServiceUnavailable, map[string]any{
				"error": unavailable.Error(), "code": "credential_store_unavailable",
				"environment_variable": unavailable.EnvironmentVariable, "restart_required": true,
			})
		} else {
			a.errorResp(w, http.StatusInternalServerError, "could not update provider credential")
		}
		return
	}

	a.jsonResp(w, http.StatusOK, ProviderAPIKeyResponse{
		ProviderID: id,
		HasKey:     result.HasKey,
		KeySource:  result.KeySource,
	})
}

func (a *API) handleGetProviderStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	p, err := a.Services.ProviderConfig.Get(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "provider not found")
		return
	}

	key := a.providerKeyStatus(r.Context(), id)
	registered := a.Services.Providers != nil && a.Services.Providers.Has(p.ProviderType)

	a.jsonResp(w, http.StatusOK, ProviderStatusDetailView{
		Provider:   providerConfigToView(p),
		HasAPIKey:  key.Usable,
		KeyCode:    key.Code,
		KeySource:  key.Source,
		Registered: registered,
	})
}

// CLIDetectionResult describes the auto-detection status for one CLI adapter.
type CLIDetectionResult struct {
	Name         string `json:"name"`          // adapter name (claude, codex, opencode)
	ProviderType string `json:"provider_type"` // pty-claude, pty-codex, etc.
	Detected     bool   `json:"detected"`
	Path         string `json:"path"`    // resolved path if found
	EnvVar       string `json:"env_var"` // env var for override
}

// handleDetectCLI runs auto-detection for all CLI adapters using the same
// Detect() logic that the runtime uses at startup.
func (a *API) handleDetectCLI(w http.ResponseWriter, r *http.Request) {
	detected := a.Services.ProviderConfig.DetectCLIs(r.Context())
	results := make([]CLIDetectionResult, 0, len(detected))
	for _, d := range detected {
		results = append(results, cliDetectionToView(d))
	}
	a.jsonResp(w, http.StatusOK, results)
}

// handleTestProviderConnection checks whether a provider row is usable right
// now — for an API provider, whether its key is accepted (CW-20260930-0101).
// Every check outcome is a 200 with ok/status; an unknown id is a 404.
func (a *API) handleTestProviderConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	result, err := a.Services.ProviderConfig.TestConnection(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "provider not found")
		return
	}
	a.jsonResp(w, http.StatusOK, providerCheckToView(result))
}

func (a *API) handleGetAllProviderStatuses(w http.ResponseWriter, r *http.Request) {
	providers, err := a.Services.ProviderConfig.List(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	providers = visibleProviderRows(providers)

	out := make([]ProviderStatusView, 0, len(providers))
	for _, p := range providers {
		key := a.providerKeyStatus(r.Context(), p.ID)
		registered := a.Services.Providers != nil && a.Services.Providers.Has(p.ProviderType)

		// For CLI providers, also check pty- and sub- variants.
		if !registered && strings.HasPrefix(p.ProviderType, "pty-") {
			registered = a.Services.Providers != nil && (a.Services.Providers.Has(p.ProviderType) || a.Services.Providers.Has("sub-"+strings.TrimPrefix(p.ProviderType, "pty-")))
		}

		out = append(out, ProviderStatusView{
			ProviderConfigView: providerConfigToView(&p),
			HasAPIKey:          key.Usable,
			KeySource:          key.Source,
			KeyCode:            key.Code,
			Registered:         registered,
		})
	}

	a.jsonResp(w, http.StatusOK, out)
}

// providerKeyStatus reports whether a provider row has a usable key and where
// it resolves from. Only rows Nanite has an API adapter for have a key to
// report; the rest (CLI providers) are the zero Status.
func (a *API) providerKeyStatus(ctx context.Context, id string) providerkeys.Status {
	if a.Services.ProviderKeys == nil {
		return providerkeys.Status{ProviderID: id}
	}
	if _, ok := service.APIProviderSpecByID(id); !ok {
		return providerkeys.Status{ProviderID: id}
	}
	return a.Services.ProviderKeys.Status(ctx, id)
}
