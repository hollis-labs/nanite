package api

import (
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// The provider and model wire types are API-owned: their keys match what
// the store rows emitted when handlers returned them directly.
// providers_types_test.go pins them. None carries a credential — API keys
// live in the OS keychain and only has_api_key/has_key reach the wire.

// ProviderConfigView is a provider row. Settings is the row's free-form
// JSON, returned as stored.
type ProviderConfigView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ProviderType string `json:"provider_type"`
	IsEnabled    bool   `json:"is_enabled"`
	Settings     string `json:"settings"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func providerConfigToView(p *store.ProviderConfig) ProviderConfigView {
	return ProviderConfigView{
		ID:           p.ID,
		Name:         p.Name,
		ProviderType: p.ProviderType,
		IsEnabled:    p.IsEnabled,
		Settings:     p.Settings,
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
	}
}

// providerConfigsToView translates a provider list. A nil input stays nil.
func providerConfigsToView(rows []store.ProviderConfig) []ProviderConfigView {
	if rows == nil {
		return nil
	}
	out := make([]ProviderConfigView, 0, len(rows))
	for i := range rows {
		out = append(out, providerConfigToView(&rows[i]))
	}
	return out
}

// ProviderStatusView is a provider row plus whether it has an API key in
// the keychain and whether it is registered with the provider registry.
type ProviderStatusView struct {
	ProviderConfigView
	HasAPIKey  bool `json:"has_api_key"`
	Registered bool `json:"registered"`
}

// ProviderStatusDetailView is GET /api/providers/{id}/status. Its fields are
// in alphabetical key order so it encodes byte-for-byte like the
// map[string]any it replaced.
type ProviderStatusDetailView struct {
	HasAPIKey  bool               `json:"has_api_key"`
	Provider   ProviderConfigView `json:"provider"`
	Registered bool               `json:"registered"`
}

// ProviderAPIKeyResponse is POST /api/providers/{id}/api-key. Its fields are
// in alphabetical key order so it encodes byte-for-byte like the
// map[string]any it replaced. KeySource (CW-20260930-0101) is where the key
// the provider now runs on comes from — "keychain", "environment", or "" —
// so clearing a keychain key while an env key is set is not reported as the
// provider having no key.
type ProviderAPIKeyResponse struct {
	HasKey     bool   `json:"has_key"`
	KeySource  string `json:"key_source"`
	ProviderID string `json:"provider_id"`
}

// UpdateProviderRequest is PUT /api/providers/{id}. A nil field leaves that
// column untouched.
type UpdateProviderRequest struct {
	IsEnabled *bool   `json:"is_enabled,omitempty"`
	Settings  *string `json:"settings,omitempty"`
}

func (r UpdateProviderRequest) toStore() store.ProviderUpdate {
	return store.ProviderUpdate{IsEnabled: r.IsEnabled, Settings: r.Settings}
}

// ModelView is a model row, joined with its provider's type.
type ModelView struct {
	ID             string `json:"id"`
	ProviderID     string `json:"provider_id"`
	ModelID        string `json:"model_id"`
	DisplayName    string `json:"display_name"`
	ContextWindow  int    `json:"context_window"`
	MaxOutput      int    `json:"max_output"`
	SupportsTools  bool   `json:"supports_tools"`
	SupportsVision bool   `json:"supports_vision"`
	IsEnabled      bool   `json:"is_enabled"`
	Pricing        string `json:"pricing"`
	SortOrder      int    `json:"sort_order"`
	ProviderType   string `json:"provider_type"`
}

// modelsToView translates a model list. A nil input stays nil.
func modelsToView(rows []store.Model) []ModelView {
	if rows == nil {
		return nil
	}
	out := make([]ModelView, 0, len(rows))
	for _, m := range rows {
		out = append(out, ModelView{
			ID:             m.ID,
			ProviderID:     m.ProviderID,
			ModelID:        m.ModelID,
			DisplayName:    m.DisplayName,
			ContextWindow:  m.ContextWindow,
			MaxOutput:      m.MaxOutput,
			SupportsTools:  m.SupportsTools,
			SupportsVision: m.SupportsVision,
			IsEnabled:      m.IsEnabled,
			Pricing:        m.Pricing,
			SortOrder:      m.SortOrder,
			ProviderType:   m.ProviderType,
		})
	}
	return out
}

// ProviderTestResponse is POST /api/providers/{id}/test. Status is one of
// the service.ProviderCheck* values; OK is true only for "accepted" and
// "cli_found". Path is set for a CLI provider row only.
type ProviderTestResponse struct {
	Message string `json:"message"`
	OK      bool   `json:"ok"`
	Path    string `json:"path,omitempty"`
	Status  string `json:"status"`
}

func providerCheckToView(r service.ProviderCheckResult) ProviderTestResponse {
	return ProviderTestResponse{Message: r.Message, OK: r.OK, Path: r.Path, Status: r.Status}
}

func cliDetectionToView(d service.CLIDetection) CLIDetectionResult {
	return CLIDetectionResult{
		Name:         d.Name,
		ProviderType: d.ProviderType,
		Detected:     d.Detected,
		Path:         d.Path,
		EnvVar:       d.EnvVar,
	}
}
