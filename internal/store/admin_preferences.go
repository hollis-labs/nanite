package store

import (
	"context"
	"database/sql"
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
	return readAdminPreferences(ctx, s.DB)
}

type preferencesReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readAdminPreferences(ctx context.Context, reader preferencesReader) (*AdminPreferences, error) {
	var p AdminPreferences
	if err := reader.QueryRowContext(ctx, `SELECT tool_stream_behavior,
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

// PreferencesTransaction holds the existing SQLite BEGIN IMMEDIATE writer lock.
// Current and Stage use this transaction, never the single-connection DB pool.
// Its methods are valid only during WithAdminPreferencesTransaction's callback.
type PreferencesTransaction struct {
	tx  *sql.Tx
	ctx context.Context
}

// WithAdminPreferencesTransaction calls fn once, synchronously. Callback errors,
// cancellation and commit failures never release a successful staged result.
// The opener's _txlock=immediate makes BeginTx acquire SQLite's writer lock.
func (s *Store) WithAdminPreferencesTransaction(ctx context.Context, fn func(*PreferencesTransaction) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin preferences transaction: %w", err)
	}
	defer rollbackUnlessCommitted(tx)
	if err := fn(&PreferencesTransaction{tx: tx, ctx: ctx}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit preferences transaction: %w", err)
	}
	return nil
}

func (t *PreferencesTransaction) Current() (*AdminPreferences, error) {
	return readAdminPreferences(t.ctx, t.tx)
}

// Stage persists only the declared preference columns. The trigger mints the
// generation in this same transaction; a true no-op does not UPDATE any column.
// Version in candidate is ignored: callers must compare preconditions against
// Current inside the callback before staging. Stage does not commit or apply.
func (t *PreferencesTransaction) Stage(candidate AdminPreferences) (*AdminPreferences, error) {
	current, err := t.Current()
	if err != nil {
		return nil, err
	}
	if current.ToolStreamBehavior == candidate.ToolStreamBehavior && current.ToolDrawerRetention == candidate.ToolDrawerRetention {
		return current, nil
	}
	if _, err := t.tx.ExecContext(t.ctx, `UPDATE user_settings
        SET tool_stream_behavior=?, tool_drawer_retention=?, updated_at=CURRENT_TIMESTAMP
        WHERE id=1`, candidate.ToolStreamBehavior, candidate.ToolDrawerRetention); err != nil {
		return nil, fmt.Errorf("stage admin preferences: %w", err)
	}
	return t.Current()
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
