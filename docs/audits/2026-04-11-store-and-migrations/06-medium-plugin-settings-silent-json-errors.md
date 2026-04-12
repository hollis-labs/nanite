# [Medium] ListPluginSettings silently ignores JSON unmarshal errors

**Scope:** internal/store/plugin_settings.go
**Topic:** Error handling
**Date:** 2026-04-11

## Problem

`ListPluginSettings` discards errors from `json.Unmarshal` when parsing the `settings` and `schema` JSON columns. If a plugin has corrupted JSON in its settings row, the function returns a `PluginSettings` struct with nil `Settings` and nil `Schema` maps -- no error, no log, no indication of corruption.

## Evidence

```go
// plugin_settings.go:L116-117
json.Unmarshal([]byte(settingsJSON), &ps.Settings)
json.Unmarshal([]byte(schemaJSON), &ps.Schema)
```

Compare with `GetPluginSettings` (same file, L46-53) which correctly checks and returns unmarshal errors:

```go
// plugin_settings.go:L46-53
if err := json.Unmarshal([]byte(settingsJSON), &ps.Settings); err != nil {
    return nil, fmt.Errorf("parse plugin settings: %w", err)
}
if err := json.Unmarshal([]byte(schemaJSON), &ps.Schema); err != nil {
    return nil, fmt.Errorf("parse plugin schema: %w", err)
}
```

## Impact

The `ListPluginSettings` function is called from the plugin settings API endpoint (`GET /api/plugins/settings`). If a plugin's settings are corrupted (e.g., by a bug in a prior write, or manual DB edit), the list endpoint silently returns that plugin with empty settings. The user sees a plugin with no config where there should be one, with no error message to diagnose.

This is the difference between "plugin X has no settings" and "plugin X has corrupted settings" -- the former is normal, the latter needs attention.

## Recommendation

Check the unmarshal errors and either skip the row with a log, or return the error:

```go
if err := json.Unmarshal([]byte(settingsJSON), &ps.Settings); err != nil {
    log.Printf("warn: corrupt plugin settings for %s: %v", pid, err)
    ps.Settings = map[string]any{}
}
if err := json.Unmarshal([]byte(schemaJSON), &ps.Schema); err != nil {
    log.Printf("warn: corrupt plugin schema for %s: %v", pid, err)
    ps.Schema = []ConfigField{}
}
```

The log-and-continue approach is preferred for a list endpoint (one corrupt row shouldn't block listing all plugins), but the error must be visible.

## References

- `GetPluginSettings` (plugin_settings.go:L46-53) -- the same file's single-read function correctly checks these errors.
