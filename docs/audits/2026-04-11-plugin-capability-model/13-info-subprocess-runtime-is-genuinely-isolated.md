# [Info] Subprocess runtime provides genuine isolation — recommended for untrusted plugins

**Scope:** Plugin capability model
**Topic:** Architecture — positive observation
**Date:** 2026-04-11

## Problem

This is a positive finding. The subprocess plugin runtime (`internal/plugin/subprocess/`) provides genuine capability isolation that the in-process runtime does not.

## Evidence

`internal/plugin/subprocess/plugin.go:L106-166` — subprocess plugins communicate exclusively via JSON-RPC:

- The subprocess receives `InitParams` (plugin dir, config map, host version info) and returns identity.
- The subprocess receives `LoadParams` and returns a `LoadResult` manifest declaring its registrations.
- The host translates the manifest into `Register*` calls — the subprocess never touches the host directly.
- `MakeCommandHandler` creates a handler that proxies command execution over JSON-RPC.

The subprocess cannot:
- Type-assert the host to access concrete methods.
- Import `internal/*` packages (it's a separate binary).
- Access `GetService` (not exposed over JSON-RPC).
- Access the raw database, MCP manager, or any host state.
- Register HTTP routes directly (the host registers declared routes from the manifest).

The subprocess CAN only:
- Declare UI components, config schemas, event hooks, commands, slots, and keybindings in its load manifest.
- Handle command invocations proxied over JSON-RPC.
- Receive event notifications proxied over JSON-RPC.

## Impact

The subprocess runtime is the only plugin runtime that provides meaningful isolation. It enforces the SDK boundary at the process level rather than relying on Go interface conventions.

## Recommendation

1. Document the subprocess runtime as the recommended path for third-party and untrusted plugins.
2. Consider making subprocess the default runtime and requiring an explicit `runtime: builtin` manifest flag (plus a signed/trusted marker) for in-process plugins.
3. The current builtin plugins (adapters, widgets, giphy, oembed, session-stats) could potentially be migrated to subprocess, though performance overhead should be measured first.

## References

- `internal/plugin/subprocess/plugin.go` — subprocess plugin bridge
- `internal/plugin/subprocess/transport.go` — JSON-RPC transport
- `internal/plugin/subprocess/protocol.go` — protocol definition
- `internal/plugin/subprocess/manager.go` — process lifecycle with proper shutdown
