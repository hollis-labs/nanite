package store

import (
	"context"
	"fmt"
)

// AdminPreferences is a consistent read of the two persisted preferences and
// their host-issued opaque version. It contains no application/secret settings.
// The version is private metadata, not a user setting or a value digest.
type AdminPreferences struct {
	ToolStreamBehavior  string
	ToolDrawerRetention int
	Version             string `json:"-"`
}

// initializeAdminPreferencesVersion fills only the existing singleton's empty
// upgrade token. Fresh/reinserted rows get a token from the INSERT trigger.
// This bootstrap operation is idempotent; reads never initialize or persist.
func (s *Store) initializeAdminPreferencesVersion(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE user_settings
        SET admin_preferences_version = lower(hex(randomblob(16)))
        WHERE id = 1 AND admin_preferences_version = ''`)
	if err != nil {
		return fmt.Errorf("initialize admin preferences version: %w", err)
	}
	return nil
}

// GetAdminPreferences reads values and version in one SQLite snapshot. All
// writers to either preference invalidate the version through DB triggers in
// their own transaction. This does not supply CAS or repair legacy stale writes.
func (s *Store) GetAdminPreferences(ctx context.Context) (*AdminPreferences, error) {
	var p AdminPreferences
	if err := s.DB.QueryRowContext(ctx, `SELECT tool_stream_behavior,
        tool_drawer_retention, admin_preferences_version
        FROM user_settings WHERE id = 1`).Scan(
		&p.ToolStreamBehavior, &p.ToolDrawerRetention, &p.Version); err != nil {
		return nil, fmt.Errorf("read admin preferences: %w", err)
	}
	if !validAdminPreferencesVersion(p.Version) {
		return nil, fmt.Errorf("admin preferences version unavailable")
	}
	return &p, nil
}

func validAdminPreferencesVersion(version string) bool {
	if len(version) != 32 {
		return false
	}
	for _, c := range version {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
