# [Low] ext_settings JSON column has no schema validation or key namespace protection

**Scope:** internal/store/user_settings.go
**Topic:** ext_settings JSON column
**Date:** 2026-04-11

## Problem

The `ext_settings` column in `user_settings` (mapped to the `settings` TEXT column in the schema) is a `map[string]any` that is read and written as raw JSON with no key validation, no namespace enforcement, and no protection against key collision.

## Evidence

```go
// user_settings.go:L22
ExtSettings map[string]any `json:"ext_settings,omitempty"`
```

```go
// user_settings.go:L68-73
if settingsJSON != "" && settingsJSON != "{}" {
    us.ExtSettings = make(map[string]any)
    if err := json.Unmarshal([]byte(settingsJSON), &us.ExtSettings); err != nil {
        return nil, fmt.Errorf("parse ext settings: %w", err)
    }
}
```

The `settings` column is used for extension/plugin settings that don't have their own columns. There is also a separate `plugin_settings` table for per-plugin settings, but the `user_settings.settings` column can store arbitrary keys.

The scope definition mentions "key collision with `widget_visibility`, `widget_order`" as a concern. Currently, any API caller can write any key to `ext_settings` via `UpdateUserSettings`. If a plugin writes `widget_visibility` and the frontend writes `widget_visibility`, the last write wins with no merge logic.

## Impact

- **Key collision.** Two independent features using the same key in `ext_settings` silently overwrite each other.
- **Type instability.** A key that was `string` can be overwritten with `int` or `[]string` because `map[string]any` accepts anything. Downstream consumers that type-assert will panic or return incorrect values.
- **No cleanup.** When a plugin is uninstalled, its keys in `ext_settings` are never removed.

## Recommendation

1. Consider prefixing plugin keys with a namespace: `plugin:<plugin_id>:<key>`.
2. Add a helper that does key-level merge rather than wholesale replacement in `UpdateUserSettings`.
3. For known keys like `widget_visibility` and `widget_order`, define typed accessors rather than relying on the bag-of-any pattern.

This is Low because the current usage is limited and the system is single-user, so collision is unlikely in practice.

## References

- `plugin_settings.go` -- the per-plugin settings table, which is the correct place for plugin-scoped settings.
- `user_settings` schema (`001_schema.sql:L315-332`) -- the `settings` column is `TEXT NOT NULL DEFAULT '{}'`.
