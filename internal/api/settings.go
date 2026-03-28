package api

import (
	"encoding/json"
	"net/http"
)

func (a *API) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := a.Store.GetUserSettings()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	a.jsonResp(w, http.StatusOK, settings)
}

func (a *API) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	// Read existing settings first for partial merge.
	existing, err := a.Store.GetUserSettings()
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

	if err := a.Store.UpdateUserSettings(existing); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to update settings")
		return
	}

	// Sync utility provider/model to the running engine so changes take effect
	// immediately without a restart.
	if a.Engine != nil {
		a.Engine.RefreshUtilitySettings(existing.UtilityProvider, existing.UtilityModel)
	}

	a.jsonResp(w, http.StatusOK, existing)
}
