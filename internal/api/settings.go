package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/service"
)

func (a *API) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.Services.Store.GetUserSettings(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	// Marshal into a generic map so we can append the computed embedding_status
	// without duplicating every persisted field in a wrapper struct.
	raw, err := json.Marshal(settings)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to encode settings")
		return
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to decode settings")
		return
	}
	out["embedding_status"] = a.computeEmbeddingStatus(r.Context(), settings.EmbeddingMode, settings.EmbeddingProvider, settings.EmbeddingModel)
	a.jsonResp(w, http.StatusOK, out)
}

// computeEmbeddingStatus re-runs the selection helper at response time so the
// returned status reflects the current secret state, not a value frozen at
// container build time. Uses the injected embedderSelectDeps (production:
// keychain + os.Getenv). SelectEmbedder no longer does any network
// reachability probe — that was Ollama-era behavior retired alongside the
// Ollama embedder (Step 6.5, SP-20260508-0001; see embedder_select.go).
func (a *API) computeEmbeddingStatus(ctx context.Context, mode, provider, model string) string {
	_, _, status := service.SelectEmbedder(ctx, service.EmbedderSettings{
		Mode:     mode,
		Provider: provider,
		Model:    model,
	}, a.embedderSelectDeps)
	return status
}

// handleEmbeddingProviders returns the ordered list of providers that the
// Settings UI dropdown should surface.
func (a *API) handleEmbeddingProviders(w http.ResponseWriter, r *http.Request) {
	type providerInfo struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		DefaultModels []string `json:"default_models"`
	}
	catalog := []providerInfo{
		{"openai", "OpenAI", []string{"text-embedding-3-large", "text-embedding-3-small"}},
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"providers": catalog})
}

func (a *API) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	// Read existing settings first for partial merge.
	existing, err := a.Services.Store.GetUserSettings(r.Context())
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load current settings")
		return
	}

	// Decode the partial update into a raw map to detect which fields were sent.
	var raw map[string]json.RawMessage
	if err := a.decode(r, &raw); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Merge each provided field into existing settings.
	if v, ok := raw["default_provider"]; ok {
		if err := json.Unmarshal(v, &existing.DefaultProvider); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'default_provider'")
			return
		}
	}
	if v, ok := raw["default_model"]; ok {
		if err := json.Unmarshal(v, &existing.DefaultModel); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'default_model'")
			return
		}
	}
	if v, ok := raw["default_agent"]; ok {
		if err := json.Unmarshal(v, &existing.DefaultAgent); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'default_agent'")
			return
		}
	}
	if v, ok := raw["utility_provider"]; ok {
		if err := json.Unmarshal(v, &existing.UtilityProvider); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'utility_provider'")
			return
		}
	}
	if v, ok := raw["utility_model"]; ok {
		if err := json.Unmarshal(v, &existing.UtilityModel); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'utility_model'")
			return
		}
	}
	if v, ok := raw["tool_call_display_mode"]; ok {
		if err := json.Unmarshal(v, &existing.ToolCallDisplayMode); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'tool_call_display_mode'")
			return
		}
	}
	if v, ok := raw["provider_fallback_chain"]; ok {
		if err := json.Unmarshal(v, &existing.ProviderFallbackChain); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'provider_fallback_chain'")
			return
		}
	}
	if v, ok := raw["developer_mode"]; ok {
		if err := json.Unmarshal(v, &existing.DeveloperMode); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'developer_mode'")
			return
		}
	}
	if v, ok := raw["recover_mode"]; ok {
		if err := json.Unmarshal(v, &existing.RecoverMode); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'recover_mode'")
			return
		}
	}
	if v, ok := raw["tool_stream_behavior"]; ok {
		if err := json.Unmarshal(v, &existing.ToolStreamBehavior); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'tool_stream_behavior'")
			return
		}
		switch existing.ToolStreamBehavior {
		case "streaming", "persist", "hidden":
			// valid
		default:
			a.errorResp(w, http.StatusBadRequest, "tool_stream_behavior must be one of: streaming, persist, hidden")
			return
		}
	}
	if v, ok := raw["tool_drawer_retention"]; ok {
		if err := json.Unmarshal(v, &existing.ToolDrawerRetention); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'tool_drawer_retention'")
			return
		}
		switch existing.ToolDrawerRetention {
		case -1, 5, 15, 30, 60:
			// valid
		default:
			a.errorResp(w, http.StatusBadRequest, "tool_drawer_retention must be one of: -1, 5, 15, 30, 60")
			return
		}
	}
	if v, ok := raw["embedding_provider"]; ok {
		if err := json.Unmarshal(v, &existing.EmbeddingProvider); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'embedding_provider'")
			return
		}
		if existing.EmbeddingProvider != "" && !service.IsSupportedEmbeddingProvider(existing.EmbeddingProvider) {
			a.errorResp(w, http.StatusBadRequest, "embedding_provider must be one of: openai")
			return
		}
	}
	if v, ok := raw["embedding_model"]; ok {
		if err := json.Unmarshal(v, &existing.EmbeddingModel); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'embedding_model'")
			return
		}
	}
	if v, ok := raw["embedding_mode"]; ok {
		if err := json.Unmarshal(v, &existing.EmbeddingMode); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'embedding_mode'")
			return
		}
		switch existing.EmbeddingMode {
		case "", "disabled", "explicit":
			// valid
		default:
			a.errorResp(w, http.StatusBadRequest, "embedding_mode must be 'disabled' or 'explicit'")
			return
		}
	}
	if v, ok := raw["allow_unsigned_plugins"]; ok {
		// J.2: raw field pass-through. The setting is honoured only in
		// devmode builds of the host (compile-time gate in
		// internal/plugin/devmode). In production the field is stored
		// but never consulted by the signature verifier.
		if err := json.Unmarshal(v, &existing.AllowUnsignedPlugins); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'allow_unsigned_plugins'")
			return
		}
	}
	if v, ok := raw["ext_settings"]; ok {
		var ext map[string]any
		if err := json.Unmarshal(v, &ext); err != nil {
			a.errorResp(w, http.StatusBadRequest, "invalid value for field 'ext_settings'")
			return
		}
		if existing.ExtSettings == nil {
			existing.ExtSettings = make(map[string]any)
		}
		// Shallow merge: update/add keys from request, don't delete missing keys.
		for k, val := range ext {
			existing.ExtSettings[k] = val
		}
	}

	if err := a.Services.Store.UpdateUserSettings(r.Context(), existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to update settings")
		return
	}

	// Sync utility provider/model to the running engine so changes take effect
	// immediately without a restart.
	a.Services.RefreshUtilitySettings(existing.UtilityProvider, existing.UtilityModel)

	// Emit plugin event: user config changed.
	if a.Services.Plugins != nil {
		for key := range raw {
			k := key
			safego.Go(r.Context(), "api.settings.emit.config-changed", func() {
				a.Services.Plugins.EmitConfigChanged("user", k, "")
			})
		}
	}

	// Return the updated settings with the computed embedding_status attached,
	// mirroring the GET shape so the UI can refresh from the PUT response.
	raw2, err := json.Marshal(existing)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to encode settings")
		return
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw2, &out); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to decode settings")
		return
	}
	out["embedding_status"] = a.computeEmbeddingStatus(r.Context(), existing.EmbeddingMode, existing.EmbeddingProvider, existing.EmbeddingModel)
	a.jsonResp(w, http.StatusOK, out)
}

// Phase 0 item 21 ("Cut Modes, in full") removed handleGetModeAutoSwitch and
// handleSetModeAutoSwitch (B3, CW-20260428-0011) — the classifier mode
// suggestion this preference gated is gone (classify.ClassifyMode was
// deleted, along with the mode_suggestion SSE event it fed).
