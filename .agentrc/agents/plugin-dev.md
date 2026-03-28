# Agent Context: Conduit Plugin Developer

## Purpose

Develop, audit, and maintain plugins for the Conduit chat interface. This includes the plugin contract in `libs/plugin/`, the plugin scaffold, individual plugins under `conduit/plugins/`, and associated documentation.

## Key Paths

| Path | Purpose |
|------|---------|
| `libs/plugin/` | Plugin contract — `Plugin`, `Host`, `CRUDHandler`, `EventHook`, `UIComponent` interfaces |
| `libs/plugin/plugin.go` | Core interfaces: `Plugin`, `Host`, `Connector`, `Installable`, `Uninstallable` |
| `libs/plugin/registry.go` | Plugin registry |
| `libs/plugin/uiregistry.go` | UI component registry (widget, envelope, action, workflow, view) |
| `libs/plugin/example.go` | Reference implementation — shows Load/Unload, CRUD, events, UI |
| `conduit/internal/plugin/` | Host implementation, plugin loading, hot-swap |
| `conduit/internal/plugin/builtin/` | Compiled-in plugin Go code |
| `conduit/plugins/` | Plugin activation directory (yaml manifests, `repos.yaml`) |
| `conduit/docs/architecture/plugin-system.md` | Architecture doc |
| `conduit/docs/plugin-install-guide.md` | Install/manage guide (GUI, CLI, REST API) |

## Plugin Contract Summary

A plugin implements `plugin.Plugin`:
- `ID()`, `Name()`, `Version()`, `Description()`, `Dependencies()`
- `Load(host Host) error` — registers CRUD handlers, event hooks, UI components, connectors
- `Unload() error` — cleanup
- `Status() PluginStatus`

Optional: `Installable` (one-time setup), `Uninstallable` (cleanup on removal).

Host services available: `store` (SQLite), `engine` (chat), `mcp` (MCP manager), `toolclient` (tool broker).

CRUD auto-wires 5 HTTP routes per resource type. Event hooks run concurrently with 5s timeout.

## UI Component Types

`widget`, `envelope`, `action`, `workflow`, `view` — registered via `host.RegisterUIComponent()`. Frontend rendering is currently hardcoded in `EnvelopeRenderer.tsx` (static if/else chains).

## Existing Plugin Inventory

| Plugin | State | Notes |
|--------|-------|-------|
| `support-ticket` | **Complete** | Full CRUD, events, MCP, agent seeding, React UI, download handler |
| `session-stats` | **Partial** | Framework + JS hooks, no Go backend |
| `example` | **Stub** | plugin.yaml only, reference structure |
| `demo-presenter` | **Stub** | Agent profile only |
| `email` | **Stub** | Gmail OAuth2 framework, no Go code |
| `giphy` | **Stub** | Giphy API framework, no Go code |
| `marvel` | **Stub** | Marvel/TMDB framework, no Go code |
| `oembed` | **Stub** | oEmbed framework, no Go code |
| `teams` | **Stub** | Teams webhook framework, no Go code |
| `trivia` | **Stub** | Trivia game framework, no Go code |

## Known Limitations

1. Frontend component loading hardcoded (no dynamic registry)
2. `plugin.yaml` is documentation only — Go code is source of truth
3. No plugin auto-discovery (manual loading in `main.go`)
4. No plugin isolation (same process)
5. No widget/slot system implemented
6. No quick action framework implemented
7. CRUD error mapping uses fragile string matching
8. `http.ServeMux` doesn't support route removal on unload

## Conventions

- Plugin Go code lives in `conduit/internal/plugin/builtin/<name>/`
- Plugin manifest + UI + agent profiles live in `conduit/plugins/<name>/`
- Registration via `init()` calling `hostplugin.RegisterPlugin()`
- Blank import in `internal/plugin/allplugins/allplugins.go`
- Agent seeding is idempotent (check-before-create)
- Config resolution: env var → DB → config file → default
