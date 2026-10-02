package service

import (
	"context"
	"errors"
	"os"
	"strings"

	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/store"
)

// userSettingsStore is the slice of the store UserSettingsService reads and
// writes: the singleton user_settings row.
type userSettingsStore interface {
	GetUserSettings(ctx context.Context) (*store.UserSettings, error)
	GetAdminPreferences(ctx context.Context) (*store.AdminPreferences, error)
	UpdateUserSettings(ctx context.Context, us *store.UserSettings) error
}

// UserSettingsService is the transport-facing home for the singleton
// user_settings row. Get and Update are pass-throughs; the store still
// applies column defaults and rejects unknown compaction_strategy,
// tool_classifier_mode, auto_repair_pref and subagent_runtime values on
// write.
//
// The rules transports apply to individual fields live here as well: the
// enum validators for a settings update, tool load-preference validation
// and merge, and the dev-mode check. Store errors are returned unwrapped,
// because the API echoes some of them verbatim.
type UserSettingsService struct {
	store userSettingsStore
}

func NewUserSettingsService(st userSettingsStore) *UserSettingsService {
	return &UserSettingsService{store: st}
}

// Get returns the singleton settings row.
func (s *UserSettingsService) Get(ctx context.Context) (*store.UserSettings, error) {
	return s.store.GetUserSettings(ctx)
}

// AdminPreferences reads the two public preferences and their opaque version together.
func (s *UserSettingsService) AdminPreferences(ctx context.Context) (*store.AdminPreferences, error) {
	return s.store.GetAdminPreferences(ctx)
}

// Update writes the whole settings row.
func (s *UserSettingsService) Update(ctx context.Context, us *store.UserSettings) error {
	return s.store.UpdateUserSettings(ctx, us)
}

// SettingsValidationError reports a caller-supplied value the settings rules
// reject. Its message is meant for the caller.
type SettingsValidationError struct {
	Msg string
}

func (e *SettingsValidationError) Error() string { return e.Msg }

func settingsInvalid(msg string) error { return &SettingsValidationError{Msg: msg} }

// ToolStreamBehaviorValues is shared by legacy validation and admin discovery.
func ToolStreamBehaviorValues() []string { return []string{"streaming", "persist", "hidden"} }

// ToolDrawerRetentionValues is shared by legacy validation and admin discovery.
func ToolDrawerRetentionValues() []int { return []int{-1, 5, 15, 30, 60} }

// ValidateToolStreamBehavior accepts streaming, persist or hidden.
func ValidateToolStreamBehavior(v string) error {
	for _, allowed := range ToolStreamBehaviorValues() {
		if v == allowed {
			return nil
		}
	}
	return settingsInvalid("tool_stream_behavior must be one of: streaming, persist, hidden")
}

// ValidateToolDrawerRetention accepts -1 (keep) or 5, 15, 30 or 60.
func ValidateToolDrawerRetention(v int) error {
	for _, allowed := range ToolDrawerRetentionValues() {
		if v == allowed {
			return nil
		}
	}
	return settingsInvalid("tool_drawer_retention must be one of: -1, 5, 15, 30, 60")
}

// ValidateEmbeddingProvider accepts "" (unset) or a supported provider.
func ValidateEmbeddingProvider(v string) error {
	if v != "" && !IsSupportedEmbeddingProvider(v) {
		return settingsInvalid("embedding_provider must be one of: openai")
	}
	return nil
}

// ValidateEmbeddingMode accepts "", disabled or explicit.
func ValidateEmbeddingMode(v string) error {
	switch v {
	case "", "disabled", "explicit":
		return nil
	}
	return settingsInvalid("embedding_mode must be 'disabled' or 'explicit'")
}

// ToolLoadPreferences returns the user's per-tool load-type overrides. The
// map is nil when none are set.
func (s *UserSettingsService) ToolLoadPreferences(ctx context.Context) (map[string]string, error) {
	us, err := s.store.GetUserSettings(ctx)
	if err != nil {
		return nil, err
	}
	return us.ToolLoadPreferences, nil
}

// UpdateToolLoadPreferences merges updates into the stored overrides and
// returns the result. Each value must be "auto", "opt-in", or "" to remove
// that tool's override; the values are checked before the row is read, and
// an invalid one is a *SettingsValidationError. Read-then-write, not one
// transaction.
func (s *UserSettingsService) UpdateToolLoadPreferences(ctx context.Context, updates map[string]string) (map[string]string, error) {
	for tool, lt := range updates {
		if lt != "" && lt != string(pluginpkg.LoadTypeAuto) && lt != string(pluginpkg.LoadTypeOptIn) {
			return nil, settingsInvalid("invalid load_type for tool " + tool + ": must be \"auto\", \"opt-in\", or \"\" (remove)")
		}
	}

	us, err := s.store.GetUserSettings(ctx)
	if err != nil {
		return nil, err
	}
	if us.ToolLoadPreferences == nil {
		us.ToolLoadPreferences = make(map[string]string)
	}
	for tool, lt := range updates {
		if lt == "" {
			delete(us.ToolLoadPreferences, tool)
		} else {
			us.ToolLoadPreferences[tool] = lt
		}
	}
	if err := s.store.UpdateUserSettings(ctx, us); err != nil {
		return nil, err
	}
	return us.ToolLoadPreferences, nil
}

// DevModeEnabled reports whether the dev-mode editor affordances are on:
// NANITE_DEVMODE is truthy, or user_settings.developer_mode is set. A
// settings read that fails counts as off.
func (s *UserSettingsService) DevModeEnabled(ctx context.Context) bool {
	if EnvDevModeOn() {
		return true
	}
	us, err := s.store.GetUserSettings(ctx)
	if err != nil || us == nil {
		return false
	}
	return us.DeveloperMode
}

// EnvDevModeOn reports whether NANITE_DEVMODE is set to 1, true, yes or on
// (case-insensitive, surrounding space ignored).
func EnvDevModeOn() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("NANITE_DEVMODE")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// IsSettingsValidation reports whether err is a *SettingsValidationError.
func IsSettingsValidation(err error) bool {
	var ve *SettingsValidationError
	return errors.As(err, &ve)
}
