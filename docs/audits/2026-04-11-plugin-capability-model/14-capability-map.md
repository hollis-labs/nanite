# Plugin Capability Map

**Date:** 2026-04-11
**Scope:** Complete enumeration of what a plugin can touch via the Host interface, services, type assertions, and internal imports.

## 1. What a plugin receives at load time

A plugin's `Load(host plugin.Host)` method receives a single argument: the `plugin.Host` interface. This is the intended API surface. However, the actual accessible surface depends on the runtime:

| Runtime | Receives | Can escape to |
|---|---|---|
| **Builtin (in-process)** | `plugin.Host` interface (13 methods) | Full `*Host` struct via type assertion; all `internal/*` via import |
| **Subprocess (JSON-RPC)** | `InitParams` + `LoadParams` over JSON-RPC | Nothing beyond the protocol |

## 2. SDK interface methods (plugin.Host) — 13 methods

These are on the official interface at `framework/libs/go-plugin/plugin.go:L47-104`:

| Method | Category | What it grants |
|---|---|---|
| `GetPlugin(id)` | Discovery | Read metadata of any other loaded plugin; type-assert to concrete |
| `RegisterCRUDHandler(type, handler)` | HTTP | Mount 5 HTTP routes for a resource type |
| `RegisterEventHook(types, hook)` | Events | Execute code on any system event |
| `RegisterUIComponent(comp)` | UI | Register frontend widget/envelope with optional server handler |
| `GetService(name)` | Services | Retrieve ANY registered service as `interface{}` |
| `GetConfig(key)` | Config | Read plugin's own config (keychain, env, DB, file) |
| `SetConfig(key, value)` | Config | Write plugin's own config to DB |
| `RegisterConfigSchema(fields)` | Config | Define config fields for frontend rendering |
| `RegisterConnector(name, conn)` | Integrations | Register outbound connector (webhook, email, etc.) |
| `RegisterProvider(name, prov)` | LLM | Register an LLM provider into the provider registry |
| `RegisterCLIAdapter(name, adapter)` | LLM | Register a CLI adapter for PTY bridges |
| `RegisterCommand(cmd)` | Commands | Register a slash command with handler |
| `RegisterSlot(entry)` | UI | Register UI slot entry (nav, toolbar, etc.) |
| `RegisterKeybinding(kb)` | UI | Register keyboard shortcut |
| `Logger()` | Observability | Get a logger instance |
| `Context()` | Lifecycle | Get the host's context (cancel = host shutdown) |

## 3. Concrete Host methods NOT on the SDK interface

These are only accessible via type assertion `host.(*hostplugin.Host)`:

| Method | What it grants |
|---|---|
| `RegisterHTTPHandler(pattern, handler)` | Mount arbitrary HTTP route |
| `RegisterFilter(name, priority, fn)` | Insert into sync filter chains (message content, tool output) |
| `RegisterFilterWithView(name, pri, view, fn)` | Filter with explicit data view (reasoning-blind, etc.) |
| `ApplyFilter(name, data, ctx)` | Execute a filter chain (normally host-only) |
| `FilterChainLen(name)` | Query filter chain depth |
| `RegisterService(name, svc)` | Register a service accessible by ALL plugins |
| `SetStore(s)` | Replace the database store |
| `SetRouter(router)` | Replace the HTTP router |
| `SetCommandRegistry(reg)` | Replace the command registry |
| `RegisterTaskBackend(name, backend)` | Register a task execution backend |
| `PlaceArtifact(...)` | Create artifact records in the DB |
| `EmitEvent(event)` | Emit arbitrary events to all hooks |
| `EmitPreHook(type, session, data)` | Emit pre-hooks and check cancellation |
| `SubscribeEvents()` | Subscribe to ALL system events via channel |
| `UnsubscribeEvents(ch)` | Unsubscribe from events |
| `LoadPlugin(p)` | Load another plugin into the host |
| `UnloadPlugin(id)` | Unload a plugin |
| `Shutdown()` | Shut down the entire plugin host |
| `GetPlugin(id)` | (also on SDK — listed for completeness) |
| `ListPlugins()` | List all loaded plugins |
| `GetUIComponents()` | List all UI components |
| `GetUIComponentsWithOwners()` | UI components with plugin ownership |
| `GetCRUDHandlers()` | All CRUD handlers |
| `ListConnectors()` | All connector names |
| `GetConnector(name)` | Get any connector |
| `GetConnectorStatuses()` | Health of all connectors |
| `CheckConnectorHealth(name)` | Probe a connector's health |
| `CheckAllConnectorHealth()` | Probe all connectors |
| `GetSlotEntries(slot)` | Read UI slot entries |
| `GetAllSlots()` | Read all UI slot data |
| `GetKeybindings()` | Read all keybindings |
| `SetPluginConfig(id, cfg)` | Set config for any plugin |

## 4. Services accessible via GetService

| Service name | Registered at | Concrete type | What it grants |
|---|---|---|---|
| `"store"` | `main.go:175` | `*store.Store` | **Full SQLite database access** — all tables, raw `*sql.DB` |
| `"mcp"` | `main.go:176` | `*mcp.Manager` | **Full MCP access** — tool execution, server add/remove |
| `"toolclient"` | `main.go:177` | `*toolclient.ToolClient` | Tool broker — selection, permissions, knowledge |
| `"container"` | `main.go:249` | Container struct | **Service container** — references ALL core services |
| `"tasks"` | `main.go:251` | Task service | Task backend registration and management |
| `"cli-adapter:{name}"` | `host.go:702` | Per-adapter | Individual CLI adapter instances (set by RegisterCLIAdapter) |
| `"provider-registry"` | main wiring | Provider registry | LLM provider registration |

## 5. Host state accessible to plugins

### Readable

| State | How accessed | Isolation |
|---|---|---|
| All loaded plugins | `GetPlugin(id)`, `ListPlugins()` | None — full list |
| All UI components | `GetUIComponents()` | None — all plugins |
| All connectors | `ListConnectors()`, `GetConnector()` | None — all plugins |
| All event hooks (indirectly) | Register hook for `*` events | None — see all events |
| All slot entries | `GetSlotEntries()`, `GetAllSlots()` | None |
| All keybindings | `GetKeybindings()` | None |
| Any plugin's config | `SetPluginConfig()` via type assertion | None |
| Full database | `GetService("store")` | None |
| Full MCP state | `GetService("mcp")` | None |
| All system events | `SubscribeEvents()` via type assertion | None |

### Writable / Modifiable

| State | How modified | Risk |
|---|---|---|
| Database (all tables) | `GetService("store")` | Data corruption, data theft |
| MCP server registry | `GetService("mcp").AddServer/RemoveServer` | Tool poisoning |
| Event stream | `EmitEvent()` via type assertion | Trigger other plugins' hooks |
| HTTP routes | `RegisterHTTPHandler()`, `RegisterCRUDHandler()` | Route shadowing |
| Filter chains | `RegisterFilter()` via type assertion | Modify message/tool output |
| Provider registry | `RegisterProvider()` | Replace LLM providers |
| Plugin lifecycle | `LoadPlugin()`, `UnloadPlugin()`, `Shutdown()` | DoS |
| Other plugins' config | `SetPluginConfig()` via type assertion | Config tampering |

## 6. What a malicious in-process plugin can do

Worst-case scenario for a hostile plugin compiled into the binary:

1. **Read all database data** — sessions, messages, API keys, user settings, artifacts.
2. **Execute arbitrary commands** — via MCP tool execution (`nanite_code_execute`).
3. **Write arbitrary files** — via MCP dev tools.
4. **Make outbound HTTP requests** — via `web_fetch` tool or direct `net/http`.
5. **Crash the host** — panic in an event hook (no recovery), or call `Shutdown()`.
6. **Block all operations** — pre-hook cancellation on `message.sending` and `tool.executing`.
7. **Poison other plugins** — emit crafted events, replace services, modify filter chains.
8. **Exfiltrate data** — read DB, subscribe to events, make outbound HTTP calls.
9. **Persist across restarts** — write to the database, modify plugin.yaml files on disk.
10. **Shadow core API routes** — register handlers at `/api/sessions` etc.

## 7. What a subprocess plugin can do

Constrained to the JSON-RPC protocol:

1. Declare UI components, config schemas, event hooks (proxied), commands, slots, keybindings.
2. Handle proxied command invocations.
3. Receive proxied event notifications.
4. **Cannot** access the database, MCP manager, tool broker, or any service.
5. **Cannot** register HTTP routes directly (only via manifest declarations).
6. **Cannot** type-assert the host.
7. **Cannot** import internal packages.
8. **Can** run arbitrary code in its own process (filesystem, network, etc.) — but this is contained to the subprocess's own capabilities and could be further sandboxed via OS-level controls.

## 8. Gap summary

| Gap | Severity | Finding |
|---|---|---|
| `GetService("store")` = full DB | Critical | 01 |
| Type assertion defeats SDK boundary | Critical | 02 |
| `GetService("mcp")` = full tool exec | Critical | 03 |
| No panic recovery in event hooks | High | 04 |
| No plugin-scoped auth on CRUD routes | High | 05 |
| Event hooks can't be cleaned up on unload | High | 06 |
| `GetService` returns untyped interface{} | Medium | 07 |
| Direct `internal/*` import = global mutation | Medium | 08 |
| Pre-hook cancellation = DoS vector | Medium | 09 |
| Subprocess entrypoint argument injection | Medium | 10 |
| `GetPlugin` returns concrete plugin object | Low | 11 |
| No per-plugin resource limits | Low | 12 |

## 9. Claimed-vs-actual verdict

**Claim:** "Plugins run in-process with the host (no isolation — explicit design choice)."

**Verdict: Confirmed — for in-process plugins, there is truly zero isolation.** The SDK interface (`plugin.Host`) is an advisory API surface, not a security boundary. In-process plugins have full access to the host's memory space, all internal packages, the raw database, the MCP manager, the HTTP router, and every other host resource. The only constraint is what the plugin author chooses to use.

**For subprocess plugins:** genuine isolation exists. The subprocess runtime enforces the SDK boundary at the process level via JSON-RPC. A subprocess plugin cannot access host internals, the database, or other services. This is the correct model for untrusted plugins.

The architecture has a clear two-tier trust model:
- **Tier 1 (trusted):** In-process / builtin plugins — full access, same trust as core code.
- **Tier 2 (untrusted-capable):** Subprocess plugins — isolated via process boundary.

This is a reasonable architecture if documented and enforced. The gap is that the boundary between tiers is not formalized — there is no manifest flag or install-time gate that distinguishes trusted from untrusted plugins.
