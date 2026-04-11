# Nanite Plugin System — Consolidated Execution Plan (2026-04-10)

> **Status:** architectural design + execution plan, ready for execution sessions.
>
> **Supersedes:** `plugin-audit-2026-04-10.md` (still useful for the raw audit findings but its fix-plan section is out of date — this document is the plan we're executing).
>
> **Scope:** everything needed to turn the Nanite plugin system into its intended shape: subprocess plugins distributed as prebuilt binaries, yaml-authoritative registrations, plugins truly separable from the nanite binary, hot install/uninstall, and a working plugin author SDK.
>
> **Session background:** this plan is the output of the 2026-04-10 discovery + design iteration that produced findings #1–#6. Read those findings in the conversation history for the reasoning behind each decision; this document is the execution artifact, not the reasoning trail.

---

## 0. Before anything else — read this

**The single biggest reason prior sessions failed is file-drift between parallel copies of the same plugin code.** There are currently multiple locations for plugin code and agents reliably grep the wrong one:

| Path | What it is | Status |
|---|---|---|
| `nanite/internal/plugin/builtin/giphy/` | Old in-tree giphy. Actually compiles into the running binary. Has the `giphy-card` envelope-type bug. | **Stale** — to delete |
| `nanite/plugins/giphy/` (directory, yaml-only) | Orphaned yaml shell — no Go code, the `plugin.yaml` doesn't match what the builtin registers | **Stale** — to delete |
| `framework/plugins/nanite/giphy/` | Extracted copy, own git repo (`hollis-labs/nanite-plugin-giphy`), has the fix applied, but does not compile — imports `github.com/hollis-labs/plugin` which is a different module, AND imports `nanite/internal/plugin` which is unreachable from an external module | **Aspirational** — to rebuild as a real subprocess plugin |
| `nanite/plugins/support-ticket/` | Git subdirectory checked out from `hollis-labs/support-ticket`. **This is the actually-running version.** Has real active history. | **Active** — migrate in place |
| `framework/plugins/nanite/support-ticket/` | Extracted snapshot in its own git repo (`hollis-labs/nanite-plugin-support-ticket`), single commit | **Snapshot** — delete after migration |

**Critical rule for the execution session: before touching any plugin code, delete the stale copies so grep returns exactly one result per plugin name.** The drift has wasted hours in every prior session because agents find the wrong file first. Phase 0 below codifies this.

**Also before anything else:** read `docs/architecture/plugin-envelope-emission-findings-2026-04-10.md` and the discussion in the 2026-04-10 session. This plan assumes you understand:

- `plugin.yaml` becomes the authoritative registration source; `LoadResult` degenerates to an ack-only response carrying only exception/skip entries.
- Subprocess plugins use JSON-RPC 2.0 over stdin/stdout (the existing `internal/plugin/subprocess/` infrastructure, extended).
- The plugin SDK lives outside nanite at `github.com/hollis-labs/plugin-sdk` (new module) with Nanite-specific extensions inside nanite at `github.com/hollis-labs/nanite/pkg/plugin` (new public package).
- Plugins ship as pre-built per-platform archives; users never compile anything.
- Core plugins are still compiled into the nanite binary via `go.work` or replace directives, but go through the same yaml-authoritative loader as subprocess plugins.
- Frontend dynamically imports plugin bundles via an importmap-based React sharing model.
- Hosting: Cloudflare R2 + Pages + custom domain.

If any of that is news, stop and read the conversation from 2026-04-10 before executing.

---

## 1. Repo / module layout (the target)

After execution, this is where things live:

```
github.com/hollis-labs/plugin-sdk                     (NEW repo, NEW go.mod)
├── plugin.go                                          Plugin interface, Logger, Error, base types
├── error.go                                           Error codes, sentinels, constructors
├── envelope.go                                        EnvelopeOut, MessageOut (wire types)
├── go.mod
└── subprocess/                                        subpackage
    ├── server.go                                      Serve() entry point
    ├── protocol.go                                    JSON-RPC wire format (shared source of truth)
    ├── types.go                                       InitParams, CommandRequest, etc.
    ├── config.go                                      ConfigReader
    ├── data.go                                        DataHelper, CacheHelper
    ├── log.go                                         stderr JSON-lines logger
    └── subprocesstest/                                testing harness subpackage
        └── harness.go

github.com/hollis-labs/nanite/pkg/plugin              (NEW public package inside nanite)
├── slot.go                                            UISlotName, UISlotEntry, slot constants
├── command.go                                         SlashCommandDef, CommandArg
├── keybinding.go                                      KeybindingDef
├── envelope.go                                        envelope registration type
├── mcp.go                                             MCP server registration type
├── http.go                                            HTTP route registration type
├── agent.go                                           agent profile registration type
└── reserved.go                                        list of reserved command names

github.com/hollis-labs/nanite/internal/plugin         (UNCHANGED location, REWORKED contents)
├── host.go                                            concrete *Host type
├── loader.go                                          discovery, yaml parsing, LoadDiscovered
├── registrations.go                                   NEW: yaml-driven Register* translation for all types
├── config.go                                          PluginManifest struct + ParseManifest (extended)
├── schemas/                                           NEW: embedded JSON Schemas
│   └── plugin.schema.v1.json
└── subprocess/                                        host-side subprocess runner
    └── runner.go                                      imports plugin-sdk/subprocess/protocol for wire types

github.com/hollis-labs/go-plugin                      DELETED (framework/libs/go-plugin/)
github.com/hollis-labs/plugin                         DELETED or absorbed into plugin-sdk (see §3.2)

github.com/hollis-labs/nanite-plugin-<name>           (one repo per plugin — already exists)
github.com/hollis-labs/nanite-plugins-catalog         (NEW repo for the catalog build pipeline)
```

**Hosting infrastructure** (to be provisioned in Phase 6):
- `plugins.nanite.hollis-labs.dev` → Cloudflare Pages, hosts `catalog.yaml` + `catalog.yaml.sig`
- `archives.nanite.hollis-labs.dev` → Cloudflare R2 via custom domain, hosts per-plugin release archives

---

## 2. Track structure

Work decomposes into ten tracks. Track A is the blocker for everything; B/C/F can run in parallel after A; D/E depend on B+C; G depends on E; H depends on E; I/J are final cleanup.

```
        ┌────── Track A ──────┐
        │  (pre-execution)    │
        └──────────┬──────────┘
                   │
   ┌───────────────┼───────────────┬──────────────┐
   ▼               ▼               ▼              ▼
Track B        Track C        Track F        (can start any time)
(host core)    (SDK repo)     (catalog infra)
   │               │               │
   └───────┬───────┘               │
           │                       │
           ▼                       │
       Track D                     │
   (frontend loader)               │
           │                       │
           ▼                       │
       Track E                     │
   (giphy integration)             │
           │                       │
           ▼                       │
       Track G ◀──────────────────┘
   (install flow)
           │
           ▼
       Track H
   (remaining plugin migrations)
           │
           ▼
       Track I
   (cleanup + deletions)
           │
           ▼
       Track J
   (polish + docs + OQ9)
```

Each track has a set of work items. Each work item is small enough that a single execution agent session can complete it. Items within a track must run sequentially; tracks in parallel can interleave.

---

## 3. Track A — Pre-execution cleanup (BLOCKER for everything else)

**Must complete in full before any other track starts.** Failure to clean up the drift before execution WILL result in duplicated or wrong-file edits.

### A.1 Delete drift copies

- **Delete** `nanite/internal/plugin/builtin/giphy/` entirely. Stale copy with the `giphy-card` bug. Real giphy becomes a subprocess plugin (Track E).
- **Delete** `nanite/internal/plugin/builtin/oembed/` entirely. Non-functional stub (event hook writes to dead `event.Data["envelope"]`). Real oembed becomes a subprocess plugin (Track H).
- **Delete** `nanite/plugins/giphy/` (yaml-only orphan dir).
- **Delete** `nanite/plugins/oembed/` (yaml-only orphan dir).
- **Delete** `nanite/plugins/session-stats/` (yaml-only orphan dir; real code lives at `internal/plugin/builtin/sessionstats/` for now — migrates later in Track H).
- **Delete** `framework/plugins/nanite/fragments-engine/` (aspirational extracted copy; fragments-engine is being removed entirely per user decision).
- **Delete** `nanite/plugins/fragments-engine/` (the in-tree fragments-engine; see §A.3 below for the full removal).

After these deletions, grep for `giphy`, `oembed`, `session-stats`, `fragments-engine` in the plugin tree should return one canonical location per plugin.

### A.2 Fix known P0 blockers that would bite the first subprocess plugin

- **Fix `Host.Shutdown()` deadlock trap.** `internal/plugin/host.go:1170` holds `h.mu` across the entire `p.Unload()` loop. Release the lock across `Unload()` calls the same way `LoadPlugin` does at `host.go:896`. Otherwise the first plugin whose Unload re-enters the host during shutdown will silently hang the process on exit.
- **Enforce protocol version handshake.** `internal/plugin/subprocess/plugin.go:133-138` reads `initResult` without comparing `initResult.Protocol` against `ProtocolVersion` from `internal/plugin/subprocess/protocol.go:232`. Add: `if initResult.Protocol != ProtocolVersion { return error }`. Four lines. Critical before any real subprocess plugin ships.
- **Fix scaffold template imports.** `internal/plugin/scaffold/templates/plugin.go.tmpl` currently imports `github.com/hollis-labs/conduit/internal/plugin` and `github.com/hollis-labs/fragments-engine/plugin`. Both are wrong module paths — any scaffolded plugin will fail to compile. Full scaffold replacement happens in Track J; fix these specific imports now so `nanite plugin new` doesn't emit broken code during execution.

### A.3 Remove fragments-engine entirely

User decision (2026-04-10): fragments-engine is being removed for now. Do not try to migrate it.

- **Delete** `nanite/plugins/fragments-engine/` directory
- **Delete** the blank import from `internal/plugin/allplugins/allplugins.go`
- **Delete** all `chat.RegisterEnvelopeType("sprint-planning-review"|"task-disposition"|"task-complete-notification")` references (`plugins/fragments-engine/plugin.go:50-52` — those lines go away with the directory)
- **Sweep core code** for references to the fragments-engine envelope types:
  - `sprint-planning-review`, `task-disposition`, `task-complete-notification`
  - Grep: `Grep pattern="sprint-planning-review|task-disposition|task-complete-notification"`
  - Known emission sites to remove: `internal/mcp/self_tools_transport.go:575-586` (task-complete-notification emission), `:698-707` (task-disposition emission)
  - These are the "giphy is faking it" pattern — core code emits envelope types that fragments-engine claims to own. Delete the emission sites.
- **Delete** the corresponding envelope components from `nanite/ui/src/components/chat/envelopes/`:
  - `SprintPlanningReviewCard.tsx`, `TaskDispositionCard.tsx`, `TaskCompleteNotificationCard.tsx`
- **Remove** these envelope type names from `scripts/generate-plugin-imports.mjs` core or plugin manifests if they appear there
- **Remove** the `sprint-planning-review`, `task-disposition`, `task-complete-notification` entries from `config/envelopes.yaml` if present
- **Verify** by running the full test suite and `go vet ./...` after removal. Anything still referencing these types needs to be updated or deleted.

### A.4 Decision: in-tree git subdirs (support-ticket) vs. extracted versions

Currently `nanite/plugins/support-ticket/` has its own `.git/` pointing at `hollis-labs/support-ticket` and `framework/plugins/nanite/support-ticket/` has its own `.git/` pointing at `hollis-labs/nanite-plugin-support-ticket`. Two separate GitHub repos for the same plugin.

**Execution decision:** keep `hollis-labs/support-ticket` as the canonical repo (it has the real commit history including the `TICKET_DATA marker fix` commit). Abandon `hollis-labs/nanite-plugin-support-ticket` (single-commit snapshot). Rename the canonical repo to `nanite-plugin-support-ticket` on GitHub, update the local git remote to match, delete the `framework/plugins/nanite/support-ticket/` snapshot directory, delete the abandoned GitHub repo.

Apply the same pattern to giphy and oembed when they're fully migrated in Tracks E and H: the "extracted" copies in `framework/plugins/nanite/*/` get deleted; real work happens in the canonical `hollis-labs/nanite-plugin-<name>` repos.

### A.5 Gate for leaving Track A

- `go build ./...` clean at repo root
- `go vet ./...` clean
- `go test ./...` clean (race tests OK to defer until Track B)
- Grep for `giphy`, `oembed`, `fragments-engine`, `sprint-planning-review`, `task-disposition`, `task-complete-notification` returns either no matches or only expected references (tests that should be deleted, or the execution plan itself)
- `Host.Shutdown()` no longer holds the mutex across `Unload()` calls
- `internal/plugin/subprocess/plugin.go` refuses a plugin whose protocol version doesn't match
- `nanite plugin new test-plugin` produces code that compiles (using the temporarily-fixed imports; full scaffold rewrite happens in Track J)

---

## 4. Track B — Host core changes (yaml-authoritative + registrations from yaml + registry endpoint)

This is the biggest track. It turns `plugin.yaml` from an advisory manifest into the authoritative source of truth and makes the host honor it.

### B.1 Extend `PluginManifest` to the full v1 schema

File: `internal/plugin/config.go:39-68`

Currently `PluginManifest` parses only: `Name`, `Version`, `Description`, `Author`, `URL`, `ShortDesc`, `Config`, `Dependencies`, `Runtime`, `Entrypoint`, `LoadType`, `ToolOverrides`.

Add all of the following as new struct fields matching the schema in finding #2:

- `SchemaVersion int`
- `License string`
- `Homepage string`
- `Repository string`
- `Protocol int`
- `NaniteCompat struct{ Min, Max string }`
- `Registers struct{ Envelopes, Commands, Slots, Components, Keybindings, Events, Crud, HttpRoutes, McpServers, AgentProfiles []... }`
- `Requires struct{ McpServers, Plugins, Features []string }`
- `UI struct{ BundleDir, Entry, Stylesheet, AssetsDir, ReactVersion string }`
- `Release struct{ ArchiveURL, ChecksumURL, SignatureURL string; Platforms []string }`

Each sub-struct has its own full type definition matching §finding #2 §plugin.yaml sketch.

### B.2 Write the JSON Schema

Create: `internal/plugin/schemas/plugin.schema.v1.json`

Full JSON Schema draft-2020-12 for the plugin.yaml structure. See finding #2 §E (validation rules matrix) for the constraints to encode. Schema must include:

- Top-level required fields: `schema_version`, `id`, `name`, `version`, `description`, `author`, `license`, `runtime`, `protocol`, `nanite_compat`
- Regex constraints: `id` matches `^[a-z][a-z0-9-]{1,62}$`; `version` is semver
- Enum constraints: `runtime` in `["subprocess", "builtin"]`; `config.*.type` in `["string", "secret", "bool", "int", "select", "multiline"]`; etc.
- Conditional constraints: `entrypoint` required when `runtime: subprocess`

Embed via `//go:embed schemas/plugin.schema.v1.json` in a new `internal/plugin/schemas.go` file.

### B.3 Install-time validator

New file: `internal/plugin/install/validate.go` (new sub-package)

Function signature:
```go
type InstallError struct {
    Failures []InstallFailure
}
type InstallFailure struct {
    Kind    string // "schema" | "cross-ref" | "bundle" | "platform" | ...
    Field   string
    Message string
}

func ValidateManifest(manifestPath, pluginDir string, opts ValidationOptions) *InstallError
```

Implement all 22 validation rules from finding #2 §E plus the additional bundle-export and asset checks. `ValidationOptions` includes a `DeveloperMode bool` field that downgrades most "refuse" outcomes to warnings per the design.

Library: `github.com/santhosh-tekuri/jsonschema/v6` already in `go.mod:29`; use for JSON Schema validation.

### B.4 Rewrite `DiscoverPlugins` and `LoadDiscovered` to be yaml-authoritative

Files: `internal/plugin/loader.go` (entire file gets rewritten)

Current behavior at `loader.go:32-86`: parses manifest, looks up constructor, returns discovered plugin. Load at `loader.go:91-134` calls the constructor (or creates a SubprocessPlugin) and then `host.LoadPlugin(p)`.

New behavior: after `LoadPlugin` returns successfully, call a new function `applyManifestRegistrations(host, manifest, plugin)` that iterates `manifest.Registers.*` and makes the corresponding host calls:

- `registers.envelopes[]` → `chat.RegisterEnvelopeType(type)` for each, AND store the component name + schema path in a new host-side map so the registry endpoint can serve them
- `registers.commands[]` → `host.RegisterCommand(def)` for each (with a handler proxying to the plugin's `command/execute` RPC for subprocess plugins, or to the plugin's in-process command handler for builtins)
- `registers.slots[]` → `host.RegisterSlot(entry)` for each
- `registers.components[]` → `host.RegisterUIComponent(comp)` for each
- `registers.keybindings[]` → `host.RegisterKeybinding(def)` for each
- `registers.events[]` → `host.RegisterEventHook(types, proxy)` with a proxy that forwards to `event/handle` RPC for subprocess plugins or to the plugin's in-process hook for builtins
- `registers.crud[]` → `host.RegisterCRUDHandler(resource, proxy)` similarly
- `registers.http_routes[]` → new `host.RegisterHTTPRoute(plugin_id, pattern, method, handler)` similarly
- `registers.mcp_servers[]` → new `mcp.Manager.AddPluginServer(name, proxy)` similarly (see B.5)
- `registers.agent_profiles[]` → load the referenced yaml file, parse as `store.AgentProfile`, upsert via `store.CreateAgent` with a `source_plugin:<id>` marker

For subprocess plugins, the "proxy" is the existing pattern from `internal/plugin/subprocess/plugin.go` — a Go handler that forwards the call over JSON-RPC to the plugin subprocess and unmarshals the response. For builtin plugins, the proxy is a direct in-process call to whatever the builtin registered.

**Key architectural move:** compiled-in plugins stop calling `chat.RegisterEnvelopeType` / `mgr.AddServer` / etc. directly from their `Load()` method. They declare in yaml; the loader does the registration on their behalf. This is the unification that finding #2 demands.

### B.5 Implement `PluginMCPTransport` (new MCP server bridge)

Files:
- New `internal/mcp/plugin_transport.go` — implements `mcp.MCPTransport` interface, wraps a reference to a subprocess plugin and forwards `ListTools` and `CallTool` over RPC via a new method constant `mcp/list_tools` and the existing `mcp/call_tool` idea from finding #2.
- New `internal/mcp/manager.go` method `AddPluginServer(name string, plugin *subprocess.SubprocessPlugin, mcpSpec MCPServerRegistration) error`

For builtin plugins (compiled in), they continue to provide their own `MCPTransport` implementations directly — yaml-authoritative loader calls `mgr.AddServer(name, transport)` on their behalf via a different code path.

### B.6 Unregister paths for all registrations

The lifecycle gap from finding #1 §3.4. Cannot ship the install/uninstall flow without this.

Each of the following needs a matching `Unregister*` implementation that removes the registration when `UnloadPlugin` is called:

- `host.go` — commands, slots, keybindings, CRUD handlers, HTTP routes, MCP servers, event hooks (see next bullet for event hooks specifically), envelope type registrations, config schemas, UI components
- `internal/chat/commands.go` — command registry needs an `Unregister(name string)` method
- `internal/chat/envelope.go` — `registeredTypes` map needs an `UnregisterEnvelopeType(type string)` function
- `internal/mcp/manager.go` — needs `RemoveServer(name string)` (currently only `AddServer`)

**Sharp edge: event hook unregistration.** Current `EventHook` interface has no PluginID accessor (see `host.go:1043-1052` comment). Add `PluginID() string` to the interface (breaking change to the interface, but subprocess plugins use an SDK-provided `pluginEventHook` type that implements it automatically; compiled-in plugins need to be updated to return the plugin's ID). Then `UnloadPlugin` can iterate `h.eventHooks` and remove entries matching the plugin ID.

**Sharp edge: HTTP route unregistration.** `http.ServeMux` is stdlib and does not support route removal. Options:
1. **Replace `http.ServeMux` with a custom router** that supports route removal (e.g., `httprouter`, `chi`, or a minimal custom trie). Big blast radius — affects every route in `internal/api/`.
2. **Keep `http.ServeMux` and accept "plugin routes leak until process restart"**. Plugins can be uninstalled, but their HTTP routes stay registered (returning 404 because the plugin's handler is gone). Acceptable if we nil-guard the handler and return 404 with a clean error.
3. **Wrap `http.ServeMux` in a mutable adapter** that uses a per-route indirection. `adapter.HandleFunc(pattern, handler)` stores the handler in a map and registers a stable wrapper function with ServeMux that dispatches via the map. On unregister, delete from the map — the ServeMux entry is now a 404 producer.

**Recommendation:** option 3. Small scope, no disruption to existing routes, gets us functional route unregistration. Implement as `internal/server/mutable_mux.go` (new file) wrapping the existing mux.

### B.7 New consolidated registry API endpoint

New file: `internal/api/plugins_registry.go`

Endpoint: `GET /api/plugins/registry`

Response shape (see finding #6 §"API surface the host must expose"):
```json
{
  "envelopes": {
    "giphy-modal": {
      "plugin_id": "giphy",
      "component": "GiphyModalCard",
      "version": 1,
      "schema_url": "/api/plugins/giphy/ui/../envelopes/giphy-modal.schema.json"
    }
  },
  "widgets": { ... },
  "slots": { "composer-toolbar": [{ "id": "giphy-quick-action", ... }], ... },
  "plugins": {
    "giphy": {
      "bundle_url": "/api/plugins/giphy/ui/index.js",
      "stylesheet_url": "/api/plugins/giphy/ui/style.css",
      "bundle_hash": "sha256:abc...",
      "react_version": "^19.0.0"
    }
  }
}
```

Server-side: computed on demand from the in-memory registries populated by the loader (B.4). Cache until a `plugin.installed/uninstalled/updated/enabled/disabled` event fires.

### B.8 Plugin lifecycle event stream

New file: `internal/api/plugins_events.go`

Endpoint: `GET /api/plugins/events` (SSE)

Emits events:
- `plugin.installed` — `{ plugin_id, version }`
- `plugin.uninstalled` — `{ plugin_id }`
- `plugin.updated` — `{ plugin_id, from_version, to_version }`
- `plugin.enabled` — `{ plugin_id }`
- `plugin.disabled` — `{ plugin_id }`
- `plugin.load_failed` — `{ plugin_id, reason }`

**Delete** the old dead `GET /api/plugins/events/stream` endpoint (`internal/api/event_stream.go`, registered at `internal/api/api.go:227`) since it has zero frontend consumers per finding #1 §3.3. The new `/api/plugins/events` endpoint replaces it for lifecycle events; the old one was a misdirected experiment at broadcasting plugin-emitted events to subscribers.

### B.9 Extend `InitParams` with `DataDir` / `CacheDir` / `LogLevel`

Files: `internal/plugin/subprocess/protocol.go:79-89` (`InitParams` struct)

Add fields per finding #2 §InitParams:
```go
type InitParams struct {
    PluginDir string
    DataDir   string   // NEW
    CacheDir  string   // NEW
    Config    map[string]string
    LogLevel  string   // NEW
    HostInfo  HostInfo
}
```

Host populates `DataDir = ~/.nanite/plugin-data/<id>/` and `CacheDir = ~/.nanite/plugin-cache/<id>/`, creating the directories if they don't exist, passing resolved absolute paths.

### B.10 Extend `LoadResult` to ack-only + add new methods/fields

Files: `internal/plugin/subprocess/protocol.go:102-116` (`LoadResult` struct)

Remove `Commands`, `Slots`, `Components`, `Keybindings`, `ConfigSchema`, `EventSubscriptions`, `CRUDResources` fields (they're now yaml-authoritative and the plugin doesn't return them). Keep `Dependencies` if runtime-dynamic dependency resolution is needed, otherwise remove (yaml has them).

Add: `SkippedRegistrations []SkippedRegistration` with `Kind`, `ID`, `Reason` fields.

Add new method constants in `protocol.go:44-59`:
```go
const (
    // Existing...
    MethodInit   = "plugin/init"
    MethodLoad   = "plugin/load"
    MethodUnload = "plugin/unload"
    MethodHealth = "plugin/health"
    MethodCommandExecute = "command/execute"
    MethodEventHandle    = "event/handle"
    MethodCRUDCreate     = "crud/create"
    // ... etc

    // NEW
    MethodMCPCallTool = "mcp/call_tool"
    MethodHTTPHandle  = "http/handle"
    MethodMigrate     = "plugin/migrate"
)
```

Add corresponding request/response types for `MCPCallRequest`, `MCPCallResult`, `HTTPRequest`, `HTTPResponse` per finding #2 §Response return types and finding #5 §Wire types.

Extend `EventHandleResult` and `CommandExecResult` with `Envelopes []EnvelopeOut` fields per Design Point 1.

### B.11 Host-side envelope validation at emission time

Wherever envelopes flow from a plugin response into a chat stream (via `EventHandleResult.Envelopes`, `CommandExecResult.Envelopes`, `MCPCallResult.Envelopes`), the host must:

1. Look up each `EnvelopeOut.Type` in the plugin's declared envelope types
2. Validate `Data` against the compiled JSON Schema loaded from `envelopes/<type>.schema.json` at install time
3. If either check fails, drop the envelope and log an error — do NOT pass through (developer mode: log and pass with a warning marker)

This is the "strict instead of advisory" piece — replace the advisory behavior at `internal/chat/envelope.go:163-201` (`ParseEnvelopes`) for plugin-sourced envelopes. Core-generated envelopes (from `chat.BuildKBEnvelope` etc.) can keep the advisory path since they're host-owned.

### B.12 Chat engine propagates envelopes from command results

Find the code that handles slash command execution. `host.RegisterCommand` is at `internal/plugin/host.go:718`; its handler signature returns `map[string]interface{}`. Trace the call site and update the chat engine to:

1. Check for an `envelopes` key in the command result map
2. If present, validate each envelope and inject into the assistant message being created

Exact file/line unknown without running grep; find via `Grep pattern="RegisterCommand|CommandHandler"` in `internal/chat/` and `internal/api/`.

### B.13 Gate for leaving Track B

- `go build ./...` clean
- `go test ./internal/plugin/... ./internal/chat/...` clean
- `go test -race ./internal/plugin/...` clean (race conditions matter for the unregister paths)
- A compiled-in builtin plugin (e.g., bookmarks) loads and registers its entries via the yaml-authoritative path, not via direct `host.Register*` calls in its Load function
- Unload the same plugin and verify all registrations are removed (grep the host's internal maps)
- `GET /api/plugins/registry` returns a response shaped per §B.7
- `GET /api/plugins/events` streams events when a plugin is load/unload-cycled

---

## 5. Track C — plugin-sdk module

New repo, new Go module, independent release cycle.

### C.1 Create the repo and bootstrap the module

- Create `github.com/hollis-labs/plugin-sdk` repo
- `go mod init github.com/hollis-labs/plugin-sdk`
- Set up `go.mod` with `go 1.26.1` (matching nanite)
- Add to framework monorepo if that's the pattern, or stand-alone — user's call

### C.2 Move the base plugin types into the SDK

Currently some of these live in `framework/libs/go-plugin/plugin.go` (the to-be-deleted module) and some in nanite's `internal/plugin/types.go`. Consolidate into the SDK:

- `Plugin` interface — from `framework/libs/go-plugin/plugin.go:12-43`
- `PluginStatus` struct — from `framework/libs/go-plugin/plugin.go:39-45`
- `Host` interface (base — Nanite extensions move to `nanite/pkg/plugin`) — new, simpler version
- `CRUDHandler` interface — from `framework/libs/go-plugin/plugin.go:107-123`
- `EventHook` interface — from `framework/libs/go-plugin/plugin.go:125-132`, PLUS the new `PluginID() string` method from B.6
- `Event` struct — from `framework/libs/go-plugin/plugin.go:134-140`
- `UIComponent` struct, `UIComponentType` constants — from `framework/libs/go-plugin/plugin.go:143-162`
- `Installable`, `Uninstallable` interfaces — from `framework/libs/go-plugin/plugin.go:193-204`
- `Connector` interface — from `framework/libs/go-plugin/plugin.go:205-214`
- `ConfigFieldDef` struct — from `framework/libs/go-plugin/plugin.go:215-226`
- `Error`, error codes, sentinels, constructors — from `framework/libs/go-plugin/plugin.go:263-282` and nanite's `internal/plugin/subprocess/protocol.go:62-74`
- `Logger` interface — from `framework/libs/go-plugin/plugin.go:282-290`

**New types** (from finding #2 and #5):
- `EnvelopeOut` struct
- `MessageOut` struct

### C.3 Move wire protocol types into the SDK

Files to create: `subprocess/protocol.go`, `subprocess/types.go`

Source: `nanite/internal/plugin/subprocess/protocol.go` gets its types split:
- The pure wire format types (`RPCRequest`, `RPCResponse`, `RPCError`, method constants, error codes) → `plugin-sdk/subprocess/protocol.go`
- The request/response types (`InitParams`, `InitResult`, `LoadResult`, `CommandExecParams`, etc.) → `plugin-sdk/subprocess/types.go`
- Nanite-specific wire types that leaked in (`UISlotEntry`, `CommandArg`, `KeybindingDef`, `CommandRegistration`, `ComponentRegistration` at lines 146-175) → move to `nanite/pkg/plugin/` (see Track B)

**After the move, nanite's `internal/plugin/subprocess/` imports from `plugin-sdk/subprocess/protocol` for the wire types.** Host-side code that was importing local definitions now imports from the SDK. The types file in nanite `internal/plugin/subprocess/protocol.go` becomes empty or deleted.

**Sharp edge: this is a coordinated flip.** Both sides (nanite host and plugin-sdk) must use the same types. Bootstrap order:
1. Create plugin-sdk repo with a placeholder `protocol` package
2. Copy the existing wire types from nanite's internal/plugin/subprocess/protocol.go into plugin-sdk/subprocess/protocol.go
3. Tag plugin-sdk v0.1.0
4. Add `github.com/hollis-labs/plugin-sdk v0.1.0` to nanite's go.mod with a replace directive pointing at the local checkout during development
5. Update nanite's `internal/plugin/subprocess/plugin.go` to import types from `github.com/hollis-labs/plugin-sdk/subprocess` instead of local definitions
6. Delete the local type definitions in nanite
7. Verify `go build ./...` clean

### C.4 Implement the plugin-side SDK

New files in `plugin-sdk/subprocess/`:

- **`server.go`** — `Serve(p Plugin) error` entry point. Internal state machine: stdin reader loop, method dispatcher, goroutine-per-request, panic recovery, shutdown handling (SIGTERM/SIGINT/plugin/unload). Uses type assertion on the Plugin parameter to detect which optional capability interfaces it implements (`CommandHandler`, `MCPHandler`, `EventHandler`, `HTTPHandler`, `CRUDHandler`, `HealthChecker`, `Migrator`).

- **`config.go`** — `ConfigReader` interface with `String`, `Bool`, `Int`, `Secret`, `Required`, `Has` methods. Secret tracking integrates with the logger.

- **`data.go`** — `DataHelper` and `CacheHelper` interfaces with path helpers only. No SQLite, no KV store, no HTTP client — keep minimal.

- **`log.go`** — stderr JSON-lines logger. Package-level `Log()` returns the initialized logger (nil-safe pre-init via a no-op logger).

- **`types.go`** — SDK-level Go types (`CommandRequest`, `CommandResult`, etc.) that wrap the wire types in `protocol.go`. Plugins see these friendly types; Serve translates between wire and SDK types.

See finding #5 for the full API sketch.

### C.5 Implement the testing harness

New package: `plugin-sdk/subprocess/subprocesstest/`

File: `harness.go`

See finding #5 §"Testing harness" for the API. `Harness` wraps a plugin and provides direct method dispatch (no subprocess, no RPC) with mocked InitParams and temp dirs.

Include the JSON roundtrip feature per OQ3: `WithJSONRoundtrip(true)` option or `NANITE_PLUGIN_SDK_JSON_ROUNDTRIP=1` env var forces every request/response through a JSON marshal+unmarshal cycle to catch wire-format issues. Off by default for speed.

### C.6 Publish SDK v0.1.0

- Tag the repo
- Set up a lefthook pre-commit hook on the SDK repo that runs the test suite with `NANITE_PLUGIN_SDK_JSON_ROUNDTRIP=1` when `*_test.go` files change (per OQ3 agreement)
- CI: GitHub Actions running full tests, always with `NANITE_PLUGIN_SDK_JSON_ROUNDTRIP=1`

### C.7 Gate for leaving Track C

- `go test ./...` clean in the SDK repo
- `go test ./...` with `NANITE_PLUGIN_SDK_JSON_ROUNDTRIP=1` clean
- Nanite's `go build ./...` clean with the SDK imported via replace directive
- A trivial "hello world" subprocess plugin can be written in <50 lines using only SDK imports, spawned, and receive an init/load/unload cycle

---

## 6. Track D — Frontend loader rewrite

Can start after Track B ships the `/api/plugins/registry` and `/api/plugins/events` endpoints.

### D.1 Rewrite `ui/src/lib/plugin-loader.ts` to Model A

Replace the current file (references finding #6 §"What needs to change in plugin-loader.ts"). Key changes:

- **Delete** `createRegistryAPI` and `PluginRegistryAPI` interface (Model B, unused)
- **Delete** `markPluginLoaded` / `isPluginLoaded` exports
- **Add** `syncPluginRegistry(data: PluginRegistryResponse): Promise<void>` as the main entry point
- **Add** private `loadPluginBundle(pluginId, data)` helper that does dynamic import + named export extraction + CSS injection
- **Add** private `unloadPlugin(pluginId, data)` helper that removes registry entries and the stylesheet link
- **Add** private `injectStylesheet(pluginId, url)` helper
- **Keep** the three dynamic registry Maps, subscription pattern, `wrapComponent`, `clearDynamicRegistry`
- **Upgrade** the subscription pattern to `useSyncExternalStore` where consumers are hooks

See finding #6 §"Plugin loader flow (detailed)" for the full implementation sketch.

### D.2 React Query integration

New hook: `ui/src/hooks/usePluginRegistry.ts`

```ts
export function usePluginRegistry() {
  const { data } = useQuery({
    queryKey: ['plugins', 'registry'],
    queryFn: () => api.fetchPluginRegistry(),
    staleTime: Infinity,
  })
  useEffect(() => { if (data) syncPluginRegistry(data) }, [data])
  const version = useSyncExternalStore(subscribeRegistry, getRegistryVersion, getRegistryVersion)
  return { version, ready: !!data }
}
```

New hook: `ui/src/hooks/usePluginEventStream.ts` — subscribes to `/api/plugins/events` SSE, invalidates React Query caches on lifecycle events.

Both hooks called from `ui/src/components/AppShell.tsx` at app startup.

### D.3 Build-time codegen scope reduction

File: `scripts/generate-plugin-imports.mjs`

Current behavior: reads every `plugins/*/plugin.yaml` at nanite build time and generates `ui/src/generated/plugin-envelopes.ts` with hardcoded imports.

New behavior: only reads **core** plugins (the ones compiled into the nanite binary), not subprocess plugins. Core plugin list comes from a new `plugins.yaml` declaration (see Track F for the catalog structure) or a new `core_plugins.yaml` file in the nanite repo.

Non-core plugins populate the registry at runtime via `plugin-loader.ts`, not at build time.

### D.4 Importmap + es-module-shims setup

Files:
- `ui/index.html` — add importmap `<script>` and `es-module-shims` loader before the main bundle script
- `ui/src/_host/react.ts` — new file that re-exports React for bundling as a stable chunk
- `ui/src/_host/react-dom.ts`, `ui/src/_host/react-dom-client.ts`, `ui/src/_host/react-jsx-runtime.ts` — same
- `ui/vite.config.ts` — add `rollupOptions.output.manualChunks` splitting React-related modules into stable chunks with known output names; add `_host/*.ts` as entries

Self-host `es-module-shims` (per N27) by adding it as an npm dependency and serving from `/assets/_host/es-module-shims.js` instead of referencing jspm.io's CDN.

**Sharp edge: Vite SPA template ordering.** The current `index.html` is probably auto-processed by Vite's HTML plugin which injects the main bundle script. The importmap must appear BEFORE that main script for the native importmap API to work. Verify execution order by inspecting the built `index.html`. If Vite's injection happens in the wrong place, use a Vite HTML transform plugin to insert the importmap at the right position.

### D.5 `PluginLoadErrorCard` component

New file: `ui/src/components/chat/envelopes/PluginLoadErrorCard.tsx`

See finding #6 §"Error handling and fallbacks" for the implementation. Not a plugin envelope — it's nanite's built-in fallback for when a plugin's component fails to load or render.

### D.6 Gate for leaving Track D

- Frontend builds clean (`npm run build` in `ui/`)
- `PluginLoadErrorCard` renders for intentionally-broken dynamic imports in dev
- `usePluginRegistry` hook fetches and `syncPluginRegistry` populates the dynamic registry
- Core plugin envelopes still render (regression test — core path must not break during the refactor)
- Unit test: `syncPluginRegistry` called with a mocked response containing a fake plugin → verifies that `loadPluginBundle` is called with the right URL

---

## 7. Track E — Giphy as the first subprocess plugin (integration test)

Depends on B, C, D. This is the vertical slice that proves the full stack works.

### E.1 Create/update the giphy plugin repo

- Canonical repo: `github.com/hollis-labs/nanite-plugin-giphy` (the one at `framework/plugins/nanite/giphy/` with the fresh extraction commit)
- Delete or ignore the broken imports in the current state — we're rewriting the Go side to use the new SDK
- Structure the repo as finding #3 §"Archive layout":
  ```
  ├── plugin.yaml
  ├── go.mod
  ├── main.go
  ├── search.go
  ├── envelopes/giphy-modal.schema.json
  ├── ui/
  │   ├── package.json
  │   ├── vite.config.ts
  │   └── src/
  │       ├── index.tsx
  │       └── components/GiphyModalCard.tsx
  ├── Makefile               (or .goreleaser.yml)
  ├── README.md
  ├── LICENSE
  └── CHANGELOG.md
  ```

### E.2 Write giphy.plugin.yaml per the v1 schema

Use the full example from finding #3. Include envelope schema reference, command, MCP server with `search` tool, config fields.

### E.3 Write the Go side using the plugin-sdk

Implement `giphyPlugin` struct with `Plugin` interface + `CommandHandler` + `MCPHandler` capabilities. See finding #5 §"Giphy revisited with the real SDK". ~150 LOC of Go across `main.go` and `search.go`.

`go.mod` declares `github.com/hollis-labs/plugin-sdk v0.1.0` as its only direct dependency (plus `golang.org/x/...` as needed for HTTP).

### E.4 Write the React component

- Move `GiphyModalCard.tsx` from nanite's `ui/src/components/chat/envelopes/GiphyModalCard.tsx` into the plugin repo at `ui/src/components/GiphyModalCard.tsx`
- Delete from nanite (it's plugin-owned now)
- `ui/src/index.tsx` re-exports the component as a named export

### E.5 Plugin build tooling

- `ui/package.json` with Vite and React as peer/external dependencies
- `ui/vite.config.ts` with `rollupOptions.external: ['react', 'react-dom', 'react-dom/client', 'react/jsx-runtime']` and `cssCodeSplit: false`
- `Makefile` target `make release` that:
  1. Runs `cd ui && npm install && npm run build` → produces `ui/dist/index.js` and `ui/dist/style.css`
  2. Cross-compiles `go build` for 5 platforms (darwin-arm64, darwin-amd64, linux-amd64, linux-arm64, windows-amd64)
  3. Assembles per-platform archives with the layout from finding #4 §B
  4. Computes SHA256 checksums
  5. Signs with Ed25519 (using a dev key for now; real signing in Track F)

### E.6 Local install end-to-end test

- `nanite plugin install ./framework/plugins/nanite/giphy` (local dev install path)
- Verify: plugin loads, registers envelope/command/MCP server, bundle downloads to frontend, `/giphy cats` works end-to-end with a rendered GIF card
- Verify: agent can call `giphy.search` via MCP and see the GIF card
- Verify: `nanite plugin uninstall giphy` cleanly removes everything and the frontend falls back correctly

### E.7 Gate for leaving Track E

- `/giphy balloons` in chat renders a GIF card with no `unregistered_type` warnings in the logs
- Agent tool call for `giphy.search` returns output + envelope that renders
- `nanite plugin uninstall giphy` cleanly unloads (no leaked goroutines, no leaked registry entries, no leaked HTTP handlers, frontend registry updates)
- Reinstall works
- Plugin Manager UI shows giphy with correct metadata

**This track is the critical integration test for everything in B+C+D.** Expect to find bugs here that weren't visible in isolated work.

---

## 8. Track F — Catalog infrastructure (can run in parallel with B/C/D)

Independent of everything else except Track A.

### F.1 Provision Cloudflare R2 and Pages

- Create Cloudflare account / project (if not already)
- Create R2 bucket: `nanite-plugin-archives`
- Create Pages project: `nanite-plugins-catalog`
- Set up custom domains: `plugins.nanite.hollis-labs.dev` (Pages) and `archives.nanite.hollis-labs.dev` (R2)
- Configure R2 CORS if the browser ever needs to fetch directly (probably not — nanite host proxies)

### F.2 Generate the catalog root key

- Generate an Ed25519 keypair for the catalog root
- Store private key in a password manager / YubiKey (offline backup)
- Store a copy in GitHub Actions secrets for CI signing
- Embed the public key in nanite's binary at a new `internal/plugin/catalog/trustedkeys.go` file

### F.3 Create the catalog repo and build pipeline

New repo: `github.com/hollis-labs/nanite-plugins-catalog`

Contents:
- `catalog.schema.v1.json` — JSON Schema for the catalog file format (finding #4 §D)
- `plugins.yaml` — seed list (core vs default vs available tiers)
- `Makefile` or `.github/workflows/build-catalog.yml`
- `scripts/build-catalog.go` — the builder

Catalog build logic:
1. Read `plugins.yaml` for the list of plugin repos to include
2. For each repo, fetch the latest tag
3. Download the release artifacts (or clone + build locally)
4. Read each plugin's `plugin.yaml` to extract metadata
5. Compute the per-platform SHA256 of each archive (or read from the `.sha256` file in the release)
6. Read the per-platform `.sig` files
7. Assemble `catalog.yaml` per the schema in finding #4 §D
8. Sign `catalog.yaml` with the catalog root key → `catalog.yaml.sig`
9. Push both to Cloudflare Pages via the Wrangler CLI or GitHub Actions Cloudflare deploy action

### F.4 Gate for leaving Track F

- `catalog.yaml` exists at `https://plugins.nanite.hollis-labs.dev/catalog.yaml`
- `catalog.yaml.sig` verifies against the embedded root key
- At least one plugin archive is hosted at `https://archives.nanite.hollis-labs.dev/...` (giphy from Track E once it's ready, or a stub placeholder)
- Nanite can fetch the catalog, verify the signature, and parse the content

---

## 9. Track G — Install flow implementation

Depends on E (proven architecture) + F (real catalog infrastructure).

### G.1 Install state machine

New file: `internal/plugin/install/install.go` (new sub-package started in Track B)

Implement the state machine from finding #4 §E. States: `NotInstalled → Downloading → Verifying → Extracting → Validating → Loading → Ready`, with clean rollback at every failure point.

### G.2 Download, verify, extract helpers

- `internal/plugin/install/download.go` — HTTP download with progress, timeout, size limit
- `internal/plugin/install/verify.go` — SHA256 check, Ed25519 signature check
- `internal/plugin/install/extract.go` — tar.gz extraction with tarslip/symlink/path/size guards

### G.3 Staging and atomic swap

- `internal/plugin/install/staging.go` — manage `~/.nanite/plugin-staging/` temp dirs, lock files, atomic rename via `os.Rename`
- **Sharp edge: `os.Rename` is only atomic within the same filesystem.** Staging dir must be on the same FS as `~/.nanite/plugins/`. Normally both are under `~/.nanite/` so this is fine, but document it and fail early with a clear error if they're on different FSes.
- **Sharp edge: Windows behavior.** On Windows, `os.Rename` doesn't work if the destination exists. Implement a prepare-delete-rename sequence for update flows.

### G.4 Catalog fetch and trust verification

- `internal/plugin/catalog/fetch.go` — fetch `catalog.yaml` + `.sig`, verify signature, cache in `~/.nanite/plugin-catalog/`
- `internal/plugin/catalog/trust.go` — trusted key lookup, expiration + revocation checks

### G.5 Plugin Manager UI updates

Files under `ui/src/components/settings/PluginManager.tsx` + related:
- Catalog browse view with tier filters
- Install flow progress through the state machine
- Error display with actionable messages
- Uninstall confirm modal
- Update notifications
- Skipped registration display (from `LoadResult.SkippedRegistrations`)

This is substantial UI work. Split into its own session if needed — the backend install flow is independent and can be driven via CLI first.

### G.6 CLI commands

File: `cmd/nanite/plugin_cmd.go`

Implement:
- `nanite plugin install <id>` — from catalog
- `nanite plugin install ./path` — local dev install
- `nanite plugin uninstall <id>`
- `nanite plugin update <id>`
- `nanite plugin reload <id>` — dev mode only
- `nanite plugin watch ./path` — dev mode only
- `nanite plugin logs <id>` — stderr tail
- `nanite plugin release` — invoke within a plugin dir to build + sign archives

### G.7 Gate for leaving Track G

- Install flow state machine has unit tests for every transition and failure mode
- End-to-end: `nanite plugin install giphy` from the real catalog downloads, verifies, installs, loads, renders. `uninstall` reverses cleanly. `update` swaps versions atomically with rollback on failure.

---

## 10. Track H — Remaining plugin migrations

Depends on E (pattern proven).

### H.1 Oembed

- Canonical repo: `github.com/hollis-labs/nanite-plugin-oembed`
- Start from the extracted copy at `framework/plugins/nanite/oembed/` which already has the right structure (README, LICENSE, tests, `cache.go`, `fetch.go`, `providers.go`)
- Rewrite `plugin.go` to use plugin-sdk
- **Design decision:** oembed uses the event hook path (`EventHandler`) with URL detection in `message.sent` events, returning envelopes via `EventHandleResult.Envelopes`. This is the pattern for "plugin reacts to messages and injects cards" that was broken before.
- Build React component (`OembedCard.tsx` — move from nanite if it exists, otherwise write new)
- Full archive + install + integration test

### H.2 Support-ticket

- Canonical repo: `github.com/hollis-labs/support-ticket` (existing, rename to `nanite-plugin-support-ticket` on GitHub)
- The most complex plugin — CRUD handler + MCP tool server + HTTP routes + agent profile + 4 envelope types + React components
- Migration is a real chunk of work, probably 2-3 execution sessions by itself
- **Sharp edge: KB Postgres dependency.** Support-ticket's KB transport currently requires Postgres via `SUPPORT_DATABASE_URL`. Decide whether to (a) keep this as a required env var in the plugin's config, (b) ship a SQLite-based default KB implementation, or (c) make the KB optional and have the plugin degrade gracefully. Recommendation: (c) with a `SkippedRegistration` for the `support-kb` MCP server if the DB is unavailable. Plugin still loads, ticket CRUD still works, KB search returns a "database not configured" message.
- Move the UI components from nanite into the plugin's `ui/src/components/`:
  - `KBResultCard.tsx`, `TicketFormCard.tsx`, `TicketConfirmationCard.tsx`, `ResolutionCaptureCard.tsx`, `TicketInitFlow.tsx`, `ticket-utils.ts`
- Agent profile at `plugins/support-ticket/agents/it-support.yaml` moves to `nanite-plugin-support-ticket/agents/it-support.yaml`
- Full archive + install + integration test with real KB search

### H.3 Core plugins continue as compiled-in, using the new loader

These stay in the nanite binary but migrate to the yaml-authoritative loader:

- `agentrc-sync` (the `adapter-nanite-native` + related logic; the adapter-* builtins)
- `agent-widgets`, `context-widgets`, `debug-widgets`, `observability-widgets`, `bookmarks-widget`
- `session-stats` (previously at `internal/plugin/builtin/sessionstats/`)

Process per core plugin:
1. Verify `plugin.yaml` exists and matches the v1 schema (write one if missing)
2. Remove direct `host.Register*` calls from the plugin's `Load` method — the loader does it from yaml now
3. Plugin becomes a shell with `Load`/`Unload` and capability methods (`Command`, `EventHandle`, etc.) that the loader calls via its in-process proxy
4. Verify everything still works

**Sharp edge: core plugins cannot fully eliminate `internal/*` imports** because they ARE compiled into nanite. That's the fundamental distinction between core and subprocess plugins. Core plugins can still type-assert the Host and reach into `*Host` methods if needed. The goal is to remove UNNECESSARY `internal/*` imports (i.e., those that exist only because the Host surface didn't provide a public method), not to pretend core plugins are external.

### H.4 Gate for leaving Track H

- All plugins (core + subprocess) load cleanly via the unified yaml-authoritative path
- `go build ./plugins/...` (if any plugins remain in-tree for vendoring) has zero `internal/*` imports except in core plugins
- Every plugin ships with tests
- Every plugin has a README, LICENSE, CHANGELOG
- Manual QA: install, use, uninstall each plugin successfully

---

## 11. Track I — Cleanup and deletions

After H ships, the old dead code can finally go.

### I.1 Delete the `go-plugin` module

- Remove `replace github.com/hollis-labs/go-plugin => ../framework/libs/go-plugin` from `nanite/go.mod:24`
- Remove `github.com/hollis-labs/go-plugin v0.0.0` from `nanite/go.mod:9`
- Delete the `framework/libs/go-plugin/` directory
- All imports of this module should have been migrated to `plugin-sdk` during Track C

### I.2 Delete the universal `github.com/hollis-labs/plugin` module

At `/Users/chrispian/Projects-apps/plugin/`. Its contents were consolidated into `plugin-sdk` during Track C.

If other apps in the framework still import this, keep it alive but mark deprecated. Otherwise delete.

### I.3 Delete nanite/pkg/plugin shims that were temporary

Any bridge files created during the migration to keep old code paths working — remove once all callers are migrated.

### I.4 Delete dead code from finding #1

- `Host.RegisterProvider` (`internal/plugin/host.go:673`) — zero real callers. Delete.
- `Host.RegisterCLIAdapter` (`internal/plugin/host.go:698`) — zero real callers. Delete. The adapter-* plugins wire via direct import from `internal/service/install/adapters.go:13-17` which continues to work.
- `Host.SetStore(*store.Store)` public method signature leak (`internal/plugin/host.go:438`) — move to an unexported initializer.
- Old `/api/plugins/events/stream` endpoint (`internal/api/event_stream.go`) — already deleted in Track B.8.
- Old `event.Data["envelope"]` pattern — removed along with the dead giphy/oembed code in Track A.
- `internal/plugin/types.go:25-30` Nanite-only slot constants — moved to `nanite/pkg/plugin/slot.go` during Track B.

### I.5 Delete dead frontend code

- Old `createRegistryAPI` / `PluginRegistryAPI` in `ui/src/lib/plugin-loader.ts` — deleted in Track D.
- Any React components that are no longer referenced after plugin migrations (Giphy/Oembed/Support-ticket components that moved to plugin repos)

### I.6 Delete orphaned plugin directories

- `framework/plugins/nanite/fragments-engine/` — already deleted in Track A.3
- `framework/plugins/nanite/giphy/`, `oembed/`, `support-ticket/` — delete after their respective plugins are published via the canonical `hollis-labs/nanite-plugin-*` repos. These were transitional.

### I.7 Gate for leaving Track I

- `git grep -l "go-plugin\|internal/plugin/builtin/giphy\|framework/plugins/nanite/fragments-engine"` returns nothing
- `go build ./...` clean
- `go test ./... -race` clean

---

## 12. Track J — Polish and docs

Final track. Ships the plugin system publicly.

### J.1 Scaffold template rewrite

File: `internal/plugin/scaffold/templates/`

Full rewrite for subprocess plugins per finding #4 §N11. Generate:
- Go `main.go` using plugin-sdk (with real imports this time)
- `plugin.yaml` matching schema v1
- Minimal `ui/src/index.tsx` + `vite.config.ts` + `package.json`
- Working `Makefile` (or `.goreleaser.yml`) for cross-platform release
- Example `envelopes/*.schema.json`
- `README.md`, `LICENSE`, `CHANGELOG.md`, `.gitignore` stubs
- `.github/workflows/release.yml` for automated releases

`nanite plugin new --subprocess <name>` becomes the default path. `nanite plugin new --builtin <name>` generates a core plugin scaffold (for internal use).

### J.2 Signing enforcement

- Catalog signatures always required in production mode (dev mode bypasses)
- Per-plugin signatures required in production mode unless user enables "allow unsigned plugins" in settings
- Plugin Manager UI displays trust tier (signed ✓ / unsigned ⚠️ / untrusted 🚫)

### J.3 Developer mode affordances

Complete the list from finding #4 §H and finding #6 §"Developer mode affordances":

- `nanite plugin reload <id>` — hot reload for dev
- `nanite plugin watch ./path` — file watcher
- `window.__nanite_reloadPlugin(id)` browser console helper
- `window.__nanite_pluginRegistry` inspector
- Verbose dev-mode logging in the frontend loader

### J.4 Documentation

New docs under `docs/`:

- **`plugin-authoring-guide.md`** — how to write a plugin end-to-end. Targets plugin authors. Walks through the giphy example. Links to plugin-sdk godoc, plugin.yaml reference, design token list, React version compatibility rules.
- **`plugin-sdk-reference.md`** — API reference for the SDK (can be auto-generated from godoc).
- **`plugin-yaml-reference.md`** — full plugin.yaml schema reference with field-by-field descriptions, examples, validation rules.
- **`plugin-packaging-guide.md`** — how to build, sign, and publish a plugin archive.
- **`plugin-catalog-guide.md`** — how to submit a plugin to the official catalog.

Update `.nanite/agents/plugin-dev.md` to reflect the new reality (final version, replacing the one I wrote in the earlier session that was partially wrong).

### J.5 OQ9: Shared shadcn/Radix primitives via importmap

**User decision:** do this "very soon, it'll be useful fast." Scheduling into Track J as a concrete item rather than deferring to v2.

Work items:
1. Decide which primitives to share. Recommendation: Button, Card, Dialog, DropdownMenu, Popover, Select, Tooltip, Toast, Input, Textarea, Label, ScrollArea, Separator. These are the high-value, API-stable ones. Skip complex ones (Command, DataTable, Form) that have edge cases plugins rarely need.
2. Create `ui/src/_host/shadcn/*.ts` re-export files for each primitive.
3. Add each to the importmap at `ui/index.html` as `@nanite/ui/<primitive>` (namespaced so it's clear these are host-provided).
4. Add each as an external in plugin scaffold's `vite.config.ts` template.
5. Version the shared primitives — `ui.shadcn_version` field in plugin.yaml enforces compatibility. Breaking changes to the shared primitive API bump this version.
6. Document the shared primitive list + version policy in the authoring guide.
7. Update giphy's `GiphyModalCard.tsx` to use `@nanite/ui/card` instead of bundling its own Card — prove the pattern works.

**Sharp edge: shared primitives couple plugin version compatibility to nanite's shadcn version.** If nanite updates Card's API, every plugin using Card is potentially broken. Enforce via `ui.shadcn_version: "^1.0.0"` semver constraint in plugin.yaml, checked at install time.

### J.6 Gate for leaving Track J

- Docs published
- Scaffold produces a working plugin from scratch (`nanite plugin new foo --subprocess` → `make release` → install → use)
- Signing enforced in production mode
- Shared primitives working across at least two plugins
- Plugin ecosystem is ready for external developers

---

## 13. Sharp edges — consolidated list

Referenced throughout but collected here for quick access during execution:

1. **Drift between `nanite/plugins/*`, `nanite/internal/plugin/builtin/*`, and `framework/plugins/nanite/*`** — resolve in Track A before anything else.

2. **`Host.Shutdown()` deadlock** (`host.go:1170`) — holds mutex across `p.Unload()` loop. Fix in Track A.

3. **Protocol version check missing** (`internal/plugin/subprocess/plugin.go:133-138`) — fix in Track A.

4. **Scaffold template has wrong module imports** — fix in Track A as a quick patch, full rewrite in Track J.

5. **`http.ServeMux` can't unregister routes** — use a mutable adapter (option 3 in §B.6) to get functional unregistration without replacing the whole router.

6. **`os.Rename` is only atomic within the same filesystem** — install staging dir must share FS with `plugins/` dir. Document and fail fast if not.

7. **Windows `os.Rename` requires destination to not exist** — update flow needs prepare-delete-rename sequence on Windows.

8. **Windows process groups** — `cmd.SysProcAttr.Setpgid` in `internal/plugin/subprocess/manager.go:150` is POSIX-only. Add a `//go:build` guarded Windows-specific SysProcAttr setup.

9. **Deterministic archive hashing** — tar must use zero mtimes, sorted file order, consistent uid/gid. Otherwise catalog hashes won't match what users compute.

10. **`github.com/hashicorp/go-plugin` naming collision with `github.com/hollis-labs/go-plugin`** — technical coexistence works, but delete the hollis-labs version in Track I to avoid documentation confusion.

11. **SDK wire protocol type move is a coordinated flip** — nanite's host and plugin-sdk must agree on the wire types. Execute in the exact order in §C.3 or compilation breaks.

12. **`EventHook` interface breaking change** — adding `PluginID() string` method (B.6) breaks any existing in-process `EventHook` implementers. Update every one during the refactor.

13. **Importmap script ordering** — must appear before the main bundle script in `index.html`. Verify with the Vite-generated output; use a Vite HTML transform if needed.

14. **Self-host `es-module-shims`** — don't depend on jspm.io CDN; bundle into nanite's own assets per N27.

15. **React version compatibility** — if nanite bumps React, every plugin is potentially broken. Enforce via `ui.react_version` constraint (N28) checked at install time.

16. **fragments-engine removal sweep** — need to remove not just the plugin directory but also core code that references its envelope types (emission sites in `self_tools_transport.go:575-707`), frontend components, and envelope manifest entries.

17. **Core plugins can still import `internal/*`** — that's the definition of "core." The goal is to remove UNNECESSARY imports, not eliminate them. Subprocess plugins must have zero `internal/*` imports.

18. **Support-ticket KB Postgres dependency** — decide the graceful-degrade strategy in Track H.2 before the migration.

19. **OQ9 shared primitives version coupling** — plugin version compatibility ties to nanite's shadcn version. Enforce via semver constraint.

20. **The chat engine command result propagation** (B.12) requires finding the exact call site in the chat engine; not located during the audit. Expect a grep + trace during execution.

21. **Plugin bundle browser cache** — content-addressed URLs via `?v=<hash>` query string. If nanite's HTTP handler ignores query strings on static files (which is the default for some routers), the cache-busting won't work. Verify the handler respects the query string as part of the URL identity.

22. **Dev mode affordances cannot be enabled in production builds** — if the developer mode flag is runtime-only, a malicious user could flip it to bypass signing. Make sure dev mode requires both the runtime flag AND a build-time tag or environment check that production binaries can't satisfy.

---

## 14. What lives where (execution-time cheat sheet)

| Thing | Repo | Module path | Notes |
|---|---|---|---|
| Plugin SDK (base + subprocess) | `hollis-labs/plugin-sdk` | `github.com/hollis-labs/plugin-sdk` | NEW, standalone |
| Nanite host | `hollis-labs/nanite` | `github.com/hollis-labs/nanite` | existing |
| Nanite public plugin types | `hollis-labs/nanite` | `github.com/hollis-labs/nanite/pkg/plugin` | NEW, importable by plugins |
| Nanite plugin host implementation | `hollis-labs/nanite` | `github.com/hollis-labs/nanite/internal/plugin` | existing, reworked |
| Giphy plugin | `hollis-labs/nanite-plugin-giphy` | plugin repo, has own `go.mod` | existing repo, rewritten in Track E |
| Oembed plugin | `hollis-labs/nanite-plugin-oembed` | plugin repo | rewritten in Track H |
| Support-ticket plugin | `hollis-labs/nanite-plugin-support-ticket` (renamed from `support-ticket`) | plugin repo | rewritten in Track H |
| Plugin catalog | `hollis-labs/nanite-plugins-catalog` | catalog source + build pipeline | NEW, Track F |
| Catalog hosting | Cloudflare Pages at `plugins.nanite.hollis-labs.dev` | N/A | NEW, Track F |
| Archive hosting | Cloudflare R2 at `archives.nanite.hollis-labs.dev` | N/A | NEW, Track F |
| Plugin working copies | `~/.nanite/plugins/<id>/` | N/A | user filesystem |
| Plugin data | `~/.nanite/plugin-data/<id>/` | N/A | user filesystem |
| Plugin cache | `~/.nanite/plugin-cache/<id>/` | N/A | user filesystem |
| Plugin staging | `~/.nanite/plugin-staging/` | N/A | user filesystem |
| Downloaded catalog | `~/.nanite/plugin-catalog/catalog.yaml` | N/A | refreshed periodically |

Modules to delete:
- `github.com/hollis-labs/go-plugin` (at `framework/libs/go-plugin/`) — contents split between plugin-sdk and nanite/pkg/plugin
- `github.com/hollis-labs/plugin` (at `/Users/chrispian/Projects-apps/plugin/`) — absorbed into plugin-sdk

---

## 15. Deferred to v2 (explicitly out of scope for this plan)

Capture only, no execution in this plan:

- **Bidirectional RPC** — plugin-initiated calls back to the host. Enables async event-driven plugins and MCP sampling/roots. Protocol version bump required.
- **Bidirectional MCP** — corollary: plugins can use MCP-spec sampling (asking host to invoke LLMs) and roots (asking host for workspace resource lists) once bidirectional RPC exists.
- **`MockHost` for testing bidirectional plugins** — only needed once v2 lands.
- **WASM runtime for plugins** — alternative to subprocess. Revisit when Go-to-WASM toolchain improves.
- **Cosign / sigstore keyless signing** — upgrade path from the Ed25519 root-key model. Compatible with our current catalog format (just a new `signature_type: cosign` variant).
- **Multiple installed versions of the same plugin** — out of scope.
- **Community catalogs** — v1 ships with the single default catalog. Multi-catalog support is a later addition.
- **Plugin marketplace ratings/reviews** — requires hosted service.
- **Remote plugin catalog API (as opposed to static file)** — v1 is a static signed file. Dynamic catalog API is later.
- **Plugin-to-plugin dependencies beyond declaration** — plugins can `requires.plugins: [other-plugin]` today, which gates load order. Richer dep resolution (version ranges, conflict resolution) is v2.
- **Bundled SDK helpers** — optional `plugin-sdk/httpclient`, `plugin-sdk/sqlite`, etc. Start minimal in v1; add subpackages as demand emerges.
- **`window.__nanite_host__.reactQuery` shared React Query client** — decided in OQ8 to include in importmap, may defer to post-v1 if React Query shared primitives are complex.
- **Plugin hot-reload at the UI level** (HMR for plugin React components during dev) — Track J dev mode affordances cover basic reload; full HMR is deeper work.
- **Plugin telemetry and remote crash reporting** — local stderr ring buffer only in v1.

---

## 16. Validation gates — overall done-ness checklist

The plugin system is "done" when all of the following are true:

- [ ] `nanite plugin new giphy-clone --subprocess` scaffolds a plugin that builds and runs without manual fixes
- [ ] All plugins in the default catalog can be installed and uninstalled cleanly via the Plugin Manager UI
- [ ] `/giphy balloons` renders a GIF card with no `unregistered_type` warnings
- [ ] Agent tool call for `giphy.search` returns both output and envelope
- [ ] Reinstalling a plugin at a different version cleanly updates the frontend registry without page reload
- [ ] `Host.Shutdown()` does not deadlock even if a plugin's `Unload` re-enters the host
- [ ] `go build ./...` in nanite has zero references to `github.com/hollis-labs/go-plugin` and zero to `github.com/hollis-labs/plugin`
- [ ] `go test -race ./internal/plugin/... ./internal/chat/...` is clean
- [ ] A fresh install of giphy on darwin-arm64, linux-amd64, and windows-amd64 works end-to-end from the catalog
- [ ] `catalog.yaml` and `catalog.yaml.sig` verify against the embedded root key on fresh install
- [ ] Unsigned plugin install is refused in production mode and warned-and-confirmed in dev mode
- [ ] Envelope data payloads are validated against plugin-shipped JSON Schemas at emission time
- [ ] Plugin Manager UI shows load failures with actionable error messages
- [ ] `ui/src/lib/plugin-loader.ts` is Model A (named exports) and all its functions are in use
- [ ] Importmap is set up in production build and native-loads React from the shared chunk
- [ ] Shared shadcn primitives (OQ9) work across at least two plugins
- [ ] `.nanite/agents/plugin-dev.md` reflects the new reality accurately
- [ ] A new developer can read the docs and ship a working plugin in under two hours

---

## 17. Suggested execution session structure

The user already said tokens and agents are plentiful. Suggested splits:

- **Session 1:** Track A (cleanup) + Track C (SDK bootstrap). Single agent, full focus.
- **Session 2 (parallel with 3):** Track B (host core). Backend agent.
- **Session 3 (parallel with 2):** Track F (catalog infra). Separate agent, mostly ops + schema work.
- **Session 4:** Track D (frontend loader rewrite). Frontend agent.
- **Session 5:** Track E (giphy integration). Full-stack agent — this is the proof-of-architecture.
- **Session 6:** Track G (install flow + Plugin Manager UI). Full-stack.
- **Session 7:** Track H (oembed + support-ticket + core plugin migrations). Can split into sub-sessions per plugin.
- **Session 8:** Track I (cleanup and deletions). Mechanical, low-risk.
- **Session 9:** Track J (polish, docs, OQ9). Multiple sub-sessions.

Total: ~8–10 focused sessions, some in parallel.

---

## 18. Document hygiene

This document is the execution plan; it is **not** the place to track execution progress. Use TaskCreate or a separate progress tracker during execution sessions.

When execution completes a track, the corresponding section can be annotated inline (e.g., `**Status: completed 2026-04-XX**`) or moved to a "Completed" section at the bottom. Don't delete sections — they're load-bearing context for agents joining mid-stream.

Update `.nanite/agents/plugin-dev.md` at the end of Track J to reflect the final state, and mark this execution plan as "archive" when all tracks are done.
