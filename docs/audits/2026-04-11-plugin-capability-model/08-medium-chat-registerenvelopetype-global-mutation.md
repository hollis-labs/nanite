# [Medium] Plugins can call chat.RegisterEnvelopeType — global state mutation

**Scope:** Plugin capability model
**Topic:** Security — implicit capability via import
**Date:** 2026-04-11

## Problem

Because builtin and `plugins/` plugins are compiled in the same binary, they can import any `internal/*` package and call any exported function. The fragments-engine plugin demonstrates this by calling `chat.RegisterEnvelopeType()` directly, bypassing the plugin host entirely.

## Evidence

`plugins/fragments-engine/plugin.go:L50-53`:

```go
// Register envelope types so they pass backend validation.
chat.RegisterEnvelopeType("sprint-planning-review")
chat.RegisterEnvelopeType("task-disposition")
chat.RegisterEnvelopeType("task-complete-notification")
```

This is a direct import of `internal/chat` and a direct call to a package-level function that modifies global state (the envelope type registry). The plugin host has no visibility into this registration — `GetUIComponents`, `ListPlugins`, or any host API will not reflect these envelope types as plugin-contributed.

## Impact

1. **Any in-process plugin can register arbitrary envelope types** — the backend validation (`chat.IsRegisteredEnvelopeType`) will pass for any type the plugin registers, enabling new response formats the UI may not expect.
2. **Global state pollution** — envelope types registered by a plugin persist even after the plugin is unloaded (there is no `UnregisterEnvelopeType`).
3. **The `internal/` import boundary is fiction** — Go's `internal/` convention prevents external modules from importing, but in-process plugins in the same module bypass this entirely. Every exported function in every `internal/` package is accessible.

This is a specific instance of the general problem in finding 02 (type assertion bypass), but at the package-import level rather than the interface level.

## Recommendation

1. Move envelope type registration into the plugin host API (`Host.RegisterEnvelopeType`) so registrations are tracked, scoped to plugins, and cleaned up on unload.
2. Document that in-process plugins can import `internal/*` and that this is by design — but establish a convention that plugins should only use `plugin.Host` methods and `go-plugin` SDK types. Treat direct `internal/*` imports as a code-review flag.

## References

- `plugins/fragments-engine/plugin.go:L50-53` — direct internal import
- `internal/chat/envelope.go` — `RegisterEnvelopeType` (global registry)
- Known issue: `registers.envelopes` in plugin.yaml is never read — documented dead path
