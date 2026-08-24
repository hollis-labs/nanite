# Plugin System Evolution Plan

**Created:** 2026-03-29
**Status:** Phase 1 + 2a + 3a + 5a + 5b(backend) + 6 + 8a + 8b complete (2026-03-29)
**Goal:** Make Nanite an agnostic chat client where all non-core functionality lives in plugins.

---

## Current State Summary

The plugin system has a solid foundation: clean SDK interfaces, a working host runtime, config persistence, CRUD auto-wiring, event catalog, scaffold tooling, and basic lifecycle management. But plugins can't meaningfully extend the UI beyond envelopes and widgets, all plugins must be compiled into the binary, and several subsystems have dual-registry patterns that need unification.

### What Works
- Plugin SDK: 7 interfaces, clean contract
- Host runtime: load/unload, services, config, artifacts
- Config system: keychain -> DB -> env -> file -> default
- CRUD auto-wiring: 5 REST routes per resource type
- Slash commands: fully dynamic on frontend (fetched from API, plugin badge, POST execute)
- Envelope + widget registries: codegen pattern works, just empty of plugin entries
- Plugin lifecycle UI: install, uninstall, enable, disable in Settings
- Scaffold tool: `nanite plugin new` generates boilerplate

### What's Broken
- **Pre-hook cancel bug** — `EmitPreHook` passes event by value; hooks can't set cancel flag
- **Nil router window** — Host created with nil router before server starts
- **~14 events declared but never emitted** — subscribers get nothing
- **CRUD error mapping** — string-contains "not found" for 404, no typed errors
- **Dependency sort** — shallow count sort, not topological

### What's Missing
- UI slot system (plugins can't inject nav items, settings tabs, toolbar buttons, etc.)
- Dynamic component loading (envelopes/widgets require codegen + rebuild)
- Unified registries (dual systems for commands, envelopes, widgets)
- Plugin view pages (only chat + settings exist as page slots)
- Keybinding registration
- Connector dispatch (registered but never called)
- Remote plugin catalog, versioning, updates
- Dynamic plugin loading (all plugins compiled into binary today)

---

## Planned Plugins (Context for Requirements)

| Plugin | Key Platform Needs |
|---|---|
| **Fragments Engine** | Sprint modal migration, core skills, settings tab, envelope types, slash commands |
| **Git Integration** | Nav item, chat header status, composer button, envelope types (diff, PR), slash commands |
| **Agentic Developer** | Orchestration events, envelope types (plan, approval), settings tab, slash commands |
| **Nanite** | Right rail tab or widget, slash commands (/note, /todo), context menu "save to Nanite" |
| **Custom Actions** | Settings tab (action editor), keybinding registration, slash command binding, project/session hooks |

---

## Phase 1: Fix Foundations ✅

**Scope:** Bug fixes and unification. No new features — make what exists reliable.
**Agents:** Backend
**Completed:** 2026-03-29

### 1a. Fix pre-hook cancellation ✅
- Added `plugin.ErrCanceled` sentinel error to plugin SDK
- `EmitPreHook` now checks for `errors.Is(err, plugin.ErrCanceled)` — hooks return the error to signal cancellation
- Legacy map-based `cancel` flag still supported for backward compatibility

### 1b. Fix nil router window ✅
- Added `pendingRoutes` queue to Host — routes registered before `SetRouter()` are queued and replayed
- `RegisterCRUDHandler` and `RegisterUIComponent` now use `registerRoute()` helper
- All HTTP route registration is nil-router-safe

### 1c. Wire missing event emitters ✅
- Added `PluginEventEmitter` interface to chat package (avoids circular import)
- Wired `PluginHost` into `Engine` struct via `main.go`
- Added plugin event emissions for: `session.start`, `session.end`, `message.received`, `tool.called`, `tool.failed`
- Added API-layer emissions for: `session.archived`, `mode.changed`, `config.changed` (user + plugin)

### 1d. Fix CRUD error handling ✅
- Added `PluginError` typed error with `Code` field to plugin SDK
- Added `ErrNotFound()`, `ErrConflict()`, `ErrValidation()` constructors
- Replaced all `strings.Contains(err.Error(), "not found")` in `crud.go` with `errors.As` type check via `crudErrorResp()` helper

### 1e. Fix dependency sort ✅
- Replaced shallow count sort with Kahn's algorithm topological sort
- Cycle detection with error message listing participating plugins

---

## Phase 2: Unified Registries

**Scope:** Implement ADR-002 (single registry pattern) across all existing extension points.
**Agents:** Backend + Frontend
**Depends on:** Phase 1 (pre-hook fix, error handling)

### 2a. Backend — Unify slash command registry ✅
- Removed `host.slashCmds` map, replaced with `CommandRegistrar` interface
- `Host.RegisterCommand()` delegates to `CommandRegistry.RegisterPlugin()` with `Source: pluginID`
- `SetCommandRegistry()` wired from main.go after Engine + Host creation
- Single `Execute()` path in `handleExecuteCommand` — no more fallback scan
- `handleListCommands` reads from single unified registry

### 2b. Frontend — Unify envelope registry
- Single `ENVELOPE_REGISTRY` in `plugin-envelopes.ts`
- Each entry: `{ component: LazyComponent, source: "core" | pluginId }`
- Codegen script writes all entries into one map
- Recover mode: filter by `source === "core"` instead of swapping registries
- `EnvelopeRenderer` reads from the single registry

### 2c. Frontend — Unify widget registry
- Same pattern as 2b applied to `plugin-widgets.ts`
- `DEFAULT_WIDGET_ORDER` stays as a core-only default, extended by plugin entries appended at the end

### 2d. Frontend — Unify config component registry
- `plugin-config-components.ts` follows the same single-registry pattern
- Currently empty; standardize the shape before plugins start using it

---

## Phase 3: UI Slot System

**Scope:** The core new capability — let plugins inject UI into defined mount points.
**Agents:** Backend + Frontend
**Depends on:** Phase 2 (unified registry pattern established)

### 3a. Backend — Slot registration API ✅
- Added `UISlotName` constants and `UISlotEntry` struct to plugin SDK
- 8 slot types: `nav-rail`, `settings-tab`, `right-rail-tab`, `composer-toolbar`, `chat-header-action`, `context-menu:message`, `context-menu:session`, `command-palette`
- `Host.RegisterSlot(entry)` with validation, priority sorting, dedup by ID
- `Host.RegisterSlot()` added to `plugin.Host` interface (SDK)
- `GET /api/plugins/ui-slots` returns `{ [slot]: UISlotEntry[] }` sorted by priority
- Core items register with `PluginID: "core"`, plugins auto-tagged from `activePlugin`

### 3b. Frontend — Slot renderer
- `usePluginSlots(slotName)` hook: fetches from API, caches with React Query, returns ordered components
- Each slot location (NavRail, SettingsPage, RightRail, etc.) calls the hook and renders plugin items alongside core items
- Components referenced by name resolve against a component registry (similar to envelopes)
- Core items continue to be defined inline but go through the same rendering path
- Priority field controls sort order within a slot

### 3c. Frontend — Convert hardcoded arrays to slot consumers
- `NavRail.tsx`: replace `navItems` const with core items + `usePluginSlots("nav-rail")`
- `SettingsPage.tsx`: replace `sections` array with core tabs + `usePluginSlots("settings-tab")`
- `RightRail.tsx`: replace `TABS` array with core tabs + `usePluginSlots("right-rail-tab")`
- `ComposerToolbar.tsx`: add slot insertion point for plugin buttons
- `ChatHeader.tsx`: add slot insertion point for plugin actions

### 3d. Plugin view pages
- Extend `AppShell.tsx` page router beyond `chat | settings`
- Plugins registering a `slot:nav-rail` item can specify a `view` component that renders as a full page
- Route: `currentPage === pluginId` renders the plugin's view component
- This enables Git Integration's branch view, Nanite's note view, etc.

---

## Phase 4: Sprint Plugin Migration (Proof of Concept)

**Scope:** Move sprint planning from hardcoded feature to Fragments Engine plugin. Validates Phase 2-3 work.
**Agents:** Backend + Frontend
**Depends on:** Phase 3 (slot system for settings tab + nav)

### 4a. Create Fragments Engine plugin
- New plugin: `fragments-engine` in `internal/plugin/builtin/`
- Owns: sprint planning, core Fragments Engine skills, Engine-related slash commands
- Registers:
  - Envelope type: `sprint-planning-review` (move from core registry)
  - Settings tab via `slot:settings-tab` (sprint config)
  - Slash command: `/sprint`
  - Event hooks: `session.start` (check for active sprint context)

### 4b. Migrate sprint UI
- Move `ui/src/components/plugins/sprint/` components into the plugin's UI scope
- Move `useSprintPlanningStore` into the plugin scope
- Remove hardcoded `SprintPlanningModal` import from `AppShell.tsx`
- Plugin registers its modal trigger via `slot:chat-header-action` or `slot:command-palette`

### 4c. Validate the pattern
- Verify: enable/disable Fragments Engine plugin toggles all sprint UI on/off
- Verify: recover mode hides sprint envelopes and widgets
- Verify: unload removes all registered slots, commands, events
- Document the pattern as the reference for all future plugins

---

## Phase 5: Slash Commands Expansion

**Scope:** Complete the slash command story from the Beta TODO (Section 3).
**Agents:** Backend + Frontend
**Depends on:** Phase 2a (unified command registry)

### 5a. Port Fragments v1 commands ✅
- `/status` — session info (agent, model, tokens, cost, messages)
- `/providers` — runtime + DB providers, capabilities display
- `/export` — full session export as markdown (title, metadata, all messages)
- `/search <query>` — workspace-scoped message search with snippets
- All registered via `RegisterServerCommands()` in `commands_builtin.go` with `Source: "builtin"`

### 5b. Plugin command enhancements ✅ (backend)
- Added `CommandArg` struct with `Name`, `Description`, `Required`, `Type`, `Options` fields
- Added `Args []CommandArg` and `Permission string` fields to both `SlashCommandDef` (SDK) and `SlashCommand` (chat)
- `RegisterPluginCommand` propagates args and permission from plugin SDK to unified registry
- Frontend: `SlashCommandMenu` renders argument hints from schema — **frontend TODO**

### 5c. Frontend slash command UX
- Improve TipTap extension to show argument hints after command name
- Tab-completion for command arguments
- Command history (last N commands, up-arrow in composer)

---

## Phase 6: Connector Dispatch & Events ✅

**Scope:** Make connectors and the event system actually useful for workflow automation.
**Agents:** Backend
**Depends on:** Phase 1c (event emitters wired)
**Completed:** 2026-03-29

### 6a. Connector trigger bindings ✅
- `trigger_rules` DB table (migration 019) with event_type, connector_name, payload_template, filter_expr, enabled
- Store CRUD: `CreateTriggerRule`, `GetTriggerRule`, `UpdateTriggerRule`, `DeleteTriggerRule`, `ListTriggerRules`, `ListTriggerRulesByEvent`, `DeleteTriggerRulesByPlugin`
- `TriggerDispatcher` in `internal/plugin/triggers.go`: loads rules from DB on event, renders payload templates (Go text/template), evaluates filter expressions (field=value,field2=value2), dispatches to connectors
- Wired into `EmitEvent`: after hook dispatch, trigger dispatcher runs asynchronously
- API endpoints: `GET/POST /api/plugins/triggers`, `GET/PUT/DELETE /api/plugins/triggers/{id}`
- Connector validation on rule create/update (rejects unknown connector names)

### 6b. Connector health and retry ✅
- Added `Health(ctx) error` method to `Connector` interface in plugin SDK
- `TriggerDispatcher.sendWithRetry`: exponential backoff (3 retries, 1s initial, 2x factor, 30s cap)
- `ConnectorStatus` struct: Name, PluginID, Healthy, LastCheckAt, LastError, LastErrorAt, ConsecutiveFailures
- `Host.CheckConnectorHealth(name)` and `Host.CheckAllConnectorHealth()` probe Health() on connectors
- `Host.recordConnectorFailure/recordConnectorSuccess` called by dispatcher
- API: `GET /api/plugins/connectors` (cached status or `?check=true` for live probe), `GET /api/plugins/connectors/{name}/health`

### 6c. Event streaming for plugins ✅
- `Host.SubscribeEvents()` / `UnsubscribeEvents()` — buffered channel subscriber model (cap 64)
- `Host.broadcastEvent()` — non-blocking fan-out to all subscribers (drops on full buffer)
- Wired into `EmitEvent`: every event broadcasts to SSE subscribers after hooks + triggers
- API: `GET /api/plugins/events/stream` — SSE endpoint with optional `?events=type1,type2` filter

---

## Phase 7: Dynamic Loading & Marketplace (P2)

**Scope:** Remove the "must compile into binary" constraint. Enable plugin discovery and distribution.
**Agents:** Backend + Frontend
**Depends on:** Phases 1-4 complete and stable

### 7a. Plugin loading model (decision needed)
Options under consideration:
1. **Subprocess model** — Plugin runs as a separate process, communicates via JSON-RPC/gRPC. Similar to MCP servers. Maximum isolation, language-agnostic.
2. **WASM sandbox** — Plugin compiled to WASM, runs in-process with capability restrictions. Good isolation, Go-only initially.
3. **Go plugin `.so`** — Native Go plugin loading. Fragile (exact Go version match required), Linux-only. Not recommended.
4. **MCP-native plugins** — Plugins ARE MCP servers. Already have the transport layer. Limited to tool-call semantics.

Recommendation: **Subprocess + JSON-RPC** for backend logic, **URL-loaded ESM modules** for frontend components. This gives language independence, process isolation, and dynamic frontend UI without rebuilds.

### 7b. Remote plugin catalog
- Replace static `repos.yaml` with a remote catalog API
- Catalog entries: name, description, version, author, download URL, signature, compatibility range
- `GET /api/plugins/catalog` proxies to the remote catalog (or reads local cache)
- Version resolution: semver matching against Nanite version

### 7c. Plugin marketplace UI
- Browse/search available plugins in PluginManager
- Install from catalog with one click
- Version display, update available indicator
- User ratings/reviews (future, requires a hosted service)

### 7d. User-uploaded plugins
- "Install from directory" — point to a local path containing plugin.yaml + code
- "Install from archive" — upload a .tar.gz/.zip
- Frontend: file picker or directory input in PluginManager
- No signature requirement for user-uploaded (trust model is local)

### 7e. Signature verification
- Plugin archives include a signature file
- Catalog-distributed plugins verified against a known public key
- User-uploaded plugins skip verification (warning shown)

---

## Phase 8: Keybinding & Custom Actions Framework

**Scope:** Enable the Custom Actions plugin and keybinding extensibility.
**Agents:** Backend + Frontend
**Depends on:** Phase 3 (slot system), Phase 5 (slash commands)

### 8a. Keybinding registration ✅ (backend)
- Added `KeybindingDef` struct to plugin SDK with ID, Key, Action, ActionValue, Label, Description
- Added `RegisterKeybinding(kb KeybindingDef) error` to `plugin.Host` interface
- Host implementation with conflict detection: core bindings (10 reserved keys) always win, plugin-to-plugin collisions rejected, same-ID re-registration replaces
- `Host.GetKeybindings()` returns all plugin-registered keybindings
- `GET /api/plugins/keybindings` endpoint returns keybindings for frontend to merge with core
- Frontend: extend `useKeyboardShortcuts` to fetch and merge plugin bindings — **frontend TODO**
- User override: keybinding editor in Settings can reassign — **frontend TODO**

### 8b. Custom Actions object model ✅ (backend)
- `custom_actions` DB table (migration 020) with name, description, keybinding, command, slash_command, auto_triggers (JSON array), enabled
- Full store CRUD: `CreateCustomAction`, `GetCustomAction`, `UpdateCustomAction`, `DeleteCustomAction`, `ListCustomActions`, `ListCustomActionsByTrigger`
- `ListCustomActionsByTrigger` uses SQLite `json_each` to query auto-trigger arrays
- API endpoints: `GET/POST /api/actions`, `GET/PUT/DELETE /api/actions/{id}`, `POST /api/actions/{id}/execute`
- Actions with `slash_command` field auto-register as slash commands (source: `custom-action:{id}`)
- Auto-trigger dispatch: `AutoTriggerHandler` listens for `session.start`, `agent.switched`, `mode.changed` events and fires matching custom actions via `action.triggered` events
- Auto-triggers wired at startup in `main.go`
- Existing custom actions with slash commands re-registered at startup

### 8c. Custom Actions plugin — FRONTEND
- Settings tab: action editor (CRUD for custom actions)
- Integrates with slash commands: each action optionally registers as `/<name>`
- Frontend merge of plugin keybindings into `useKeyboardShortcuts`

---

## Dependency Graph

```
Phase 1 (Foundations) ✅
  |
  v
Phase 2 (Unified Registries) — 2a ✅, 2b-2d FRONTEND
  |
  v
Phase 3 (UI Slot System) — 3a ✅, 3b-3d FRONTEND
  |
  +---> Phase 4 (Sprint Migration — proof of concept) — BLOCKED on 3b-3d frontend
  |
  +---> Phase 5 (Slash Commands) — 5a ✅, 5b backend ✅, 5b-5c FRONTEND
  |
  +---> Phase 6 (Connector Dispatch) ✅
  |
  v
Phase 7 (Dynamic Loading & Marketplace) — can start after Phase 4 validates the pattern
  |
  v
Phase 8 (Keybindings & Custom Actions) — 8a-8b backend ✅, 8c FRONTEND
```

Phases 4, 5, 6, and 8 can run in parallel once their dependencies are met. Phase 7 is the largest effort and benefits from having multiple plugins built first to validate the interface.

---

## Remaining Work Summary (2026-03-29)

### Backend — All planned backend work complete ✅
No remaining backend items.

### Frontend (next session)
| Phase | Items | Status | Notes |
|-------|-------|--------|-------|
| 2b | Unify envelope registry | ✅ Done | Single ENVELOPE_REGISTRY with source field |
| 2c | Unify widget registry | ✅ Done | Same pattern as 2b |
| 2d | Unify config component registry | ✅ Done | Standardized shape |
| 3b | Slot renderer hook | ✅ Done | `usePluginSlots(slotName)` + React Query |
| 3c | Convert hardcoded arrays | ✅ Done | NavRail, SettingsPage, RightRail, ComposerToolbar, ChatHeader |
| 3d | Plugin view pages | ✅ Done | AppShell page router extended |
| 5b-fe | Slash command arg hints | TODO | Render ArgSchema in TipTap extension |
| 5c | Command history + tab-complete | TODO | UX polish |
| 8c | Custom Actions UI | TODO | Action editor in Settings, keybinding merge in useKeyboardShortcuts |
| 4a-4c | Sprint plugin migration | TODO | Proof of concept — validates Phase 2-3 |

---

## Notes

- **ADR-002** (single registry pattern) accepted — applies to all registries in Phase 2 and all future extension points.
- **Design system conventions** from `memory:feedback_ui_design_patterns.md` apply to all new plugin UI.
- **Cerberus** is required for all backend deployments — no direct `go build`.
