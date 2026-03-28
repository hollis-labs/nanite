package store

import (
	"encoding/json"
	"fmt"
	"time"
)

// UserSettings holds global user-level configuration.
type UserSettings struct {
	ProviderFallbackChain []string       `json:"provider_fallback_chain"`
	DefaultProvider       string         `json:"default_provider"`
	DefaultModel          string         `json:"default_model"`
	DefaultAdapter        string         `json:"default_adapter"`
	DefaultAgent          string         `json:"default_agent"`
	UtilityProvider       string         `json:"utility_provider"`
	UtilityModel          string         `json:"utility_model"`
	ToolCallDisplayMode   string         `json:"tool_call_display_mode"`
	ExtSettings           map[string]any `json:"ext_settings,omitempty"`
}

// GetUserSettings returns the singleton user settings row.
func (s *Store) GetUserSettings() (*UserSettings, error) {
	var chainJSON, provider, model, adapter, agent string
	var utilProvider, utilModel, toolMode, settingsJSON string
	err := s.DB.QueryRow(
		`SELECT provider_fallback_chain, default_provider, default_model, default_adapter,
		        default_agent, utility_provider, utility_model, tool_call_display_mode, settings
		 FROM user_settings WHERE id = 1`,
	).Scan(&chainJSON, &provider, &model, &adapter,
		&agent, &utilProvider, &utilModel, &toolMode, &settingsJSON)
	if err != nil {
		return nil, fmt.Errorf("get user settings: %w", err)
	}

	us := &UserSettings{
		DefaultProvider:     provider,
		DefaultModel:        model,
		DefaultAdapter:      adapter,
		DefaultAgent:        agent,
		UtilityProvider:     utilProvider,
		UtilityModel:        utilModel,
		ToolCallDisplayMode: toolMode,
	}
	if chainJSON != "" && chainJSON != "[]" {
		if err := json.Unmarshal([]byte(chainJSON), &us.ProviderFallbackChain); err != nil {
			return nil, fmt.Errorf("parse fallback chain: %w", err)
		}
	}
	if settingsJSON != "" && settingsJSON != "{}" {
		us.ExtSettings = make(map[string]any)
		if err := json.Unmarshal([]byte(settingsJSON), &us.ExtSettings); err != nil {
			return nil, fmt.Errorf("parse ext settings: %w", err)
		}
	}
	return us, nil
}

// UpdateUserSettings updates the singleton user settings row.
func (s *Store) UpdateUserSettings(us *UserSettings) error {
	chainJSON, err := json.Marshal(us.ProviderFallbackChain)
	if err != nil {
		return fmt.Errorf("marshal fallback chain: %w", err)
	}
	extJSON := []byte("{}")
	if us.ExtSettings != nil {
		extJSON, err = json.Marshal(us.ExtSettings)
		if err != nil {
			return fmt.Errorf("marshal ext settings: %w", err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.DB.Exec(
		`UPDATE user_settings SET
			provider_fallback_chain = ?,
			default_provider = ?,
			default_model = ?,
			default_adapter = ?,
			default_agent = ?,
			utility_provider = ?,
			utility_model = ?,
			tool_call_display_mode = ?,
			settings = ?,
			updated_at = ?
		 WHERE id = 1`,
		string(chainJSON), us.DefaultProvider, us.DefaultModel, us.DefaultAdapter,
		us.DefaultAgent, us.UtilityProvider, us.UtilityModel, us.ToolCallDisplayMode,
		string(extJSON), now,
	)
	if err != nil {
		return fmt.Errorf("update user settings: %w", err)
	}
	return nil
}
