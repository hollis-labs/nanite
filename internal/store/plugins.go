package store

// Phase 5 item 02 (TASKS/phase-5/02-build-plugin-installed-enabled-state-model.md)
// -- store accessors for the `plugins` table, the DB-backed installed/enabled
// state model that replaces the old plugin.yaml <-> plugin.yaml.disabled
// file-rename mechanism. See migrations/121_plugins_installed_enabled_state.sql
// for the full design rationale and internal/plugin/manage.go +
// internal/plugin/loader.go for the read paths that consume PluginState rows.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrPluginStateNotFound is returned when a plugins row cannot be located.
var ErrPluginStateNotFound = errors.New("plugin state not found")

// PluginKindBuiltin and PluginKindSubprocess are the two values the
// `plugins.kind` CHECK constraint accepts -- mirrors the same builtin/
// subprocess split used throughout internal/plugin (see GLOSSARY.md's
// "Plugin" entry).
const (
	PluginKindBuiltin    = "builtin"
	PluginKindSubprocess = "subprocess"
)

// PluginState is one row in `plugins` -- the installed/enabled state for a
// single plugin, keyed by its canonical plugin id (manifest.Identifier() /
// p.ID(), not the on-disk pluginsDir directory name, which can differ).
type PluginState struct {
	PluginID  string `json:"plugin_id"`
	Kind      string `json:"kind"` // "builtin" | "subprocess"
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const pluginStateColumns = `plugin_id, kind, installed, enabled, created_at, updated_at`

func scanPluginState(scanner interface{ Scan(...any) error }, r *PluginState) error {
	return scanner.Scan(&r.PluginID, &r.Kind, &r.Installed, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
}

// GetPluginState returns the row for pluginID, or ErrPluginStateNotFound if
// no row exists yet. A missing row is NOT an error condition for callers
// deciding enable/installed state -- see IsPluginEnabled, which fails open
// to "enabled" for exactly this case.
func (s *Store) GetPluginState(ctx context.Context, pluginID string) (*PluginState, error) {
	var out PluginState
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+pluginStateColumns+` FROM plugins WHERE plugin_id = ?`, pluginID,
	)
	if err := scanPluginState(row, &out); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPluginStateNotFound
		}
		return nil, fmt.Errorf("get plugin state: %w", err)
	}
	return &out, nil
}

// ListPluginStates returns every row in `plugins`, ordered by plugin_id.
func (s *Store) ListPluginStates(ctx context.Context) ([]PluginState, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+pluginStateColumns+` FROM plugins ORDER BY plugin_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list plugin states: %w", err)
	}
	defer rows.Close()
	out := make([]PluginState, 0)
	for rows.Next() {
		var r PluginState
		if err := scanPluginState(rows, &r); err != nil {
			return nil, fmt.Errorf("scan plugin state: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EnsurePluginSeeded inserts a default row for pluginID (installed=true,
// enabled=defaultEnabled) if -- and only if -- no row exists yet. An existing
// row is left completely untouched, so this is safe to call on every
// discovery/load pass without ever clobbering an operator's prior
// enable/disable choice. Used by internal/plugin/loader.go to populate the
// table the first time a builtin or on-disk plugin is seen.
func (s *Store) EnsurePluginSeeded(ctx context.Context, pluginID, kind string, defaultEnabled bool) error {
	if pluginID == "" {
		return fmt.Errorf("ensure plugin seeded: plugin_id is required")
	}
	if kind != PluginKindBuiltin && kind != PluginKindSubprocess {
		return fmt.Errorf("ensure plugin seeded: kind %q invalid: must be %q or %q", kind, PluginKindBuiltin, PluginKindSubprocess)
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO plugins (plugin_id, kind, installed, enabled, created_at, updated_at)
		 VALUES (?, ?, 1, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(plugin_id) DO NOTHING`,
		pluginID, kind, defaultEnabled,
	)
	if err != nil {
		return fmt.Errorf("ensure plugin seeded: %w", err)
	}
	return nil
}

// SetPluginEnabled sets the enabled flag for pluginID, creating the row
// (installed=true) if it doesn't exist yet. kind is only consulted on first
// insert -- an existing row's kind is left as-is. This is the write path
// behind internal/plugin/manage.go's DisablePlugin/EnablePlugin.
func (s *Store) SetPluginEnabled(ctx context.Context, pluginID, kind string, enabled bool) error {
	if pluginID == "" {
		return fmt.Errorf("set plugin enabled: plugin_id is required")
	}
	if kind != PluginKindBuiltin && kind != PluginKindSubprocess {
		return fmt.Errorf("set plugin enabled: kind %q invalid: must be %q or %q", kind, PluginKindBuiltin, PluginKindSubprocess)
	}
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO plugins (plugin_id, kind, installed, enabled, created_at, updated_at)
		 VALUES (?, ?, 1, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(plugin_id) DO UPDATE SET enabled = excluded.enabled, updated_at = datetime('now')`,
		pluginID, kind, enabled,
	)
	if err != nil {
		return fmt.Errorf("set plugin enabled: %w", err)
	}
	return nil
}

// IsPluginEnabled reports whether pluginID is enabled. hasRow is false when
// no row exists yet -- callers should treat that as "enabled" (fail open to
// today's implicit default-on behavior for any plugin never explicitly
// toggled), which is exactly what the bool return already defaults to
// (true) in that branch.
func (s *Store) IsPluginEnabled(ctx context.Context, pluginID string) (enabled bool, hasRow bool, err error) {
	row, err := s.GetPluginState(ctx, pluginID)
	if errors.Is(err, ErrPluginStateNotFound) {
		return true, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return row.Enabled, true, nil
}

// DeletePluginState removes pluginID's row entirely. Intended for a future
// uninstall path; not exercised by any caller in Phase 5 item 02 itself.
func (s *Store) DeletePluginState(ctx context.Context, pluginID string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM plugins WHERE plugin_id = ?`, pluginID)
	if err != nil {
		return fmt.Errorf("delete plugin state: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete plugin state rows affected: %w", err)
	}
	if n == 0 {
		return ErrPluginStateNotFound
	}
	return nil
}
