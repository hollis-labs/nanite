# Nanite Plugin System — Audit & Fix Plan (2026-04-10)

> # ⚠ SUPERSEDED
>
> **The fix plan in §4 and later sections of this document is OUT OF DATE.**
>
> This audit was written early in the 2026-04-10 design session, before the investigation discovered the parallel plugin extraction work at `framework/plugins/nanite/*`, the existing (but unused) subprocess RPC infrastructure at `internal/plugin/subprocess/`, and the deliberate `plugin` vs `go-plugin` SDK split. Subsequent iteration produced a significantly different target architecture:
>
> - Subprocess plugins distributed as prebuilt binaries (not compile-in)
> - `plugin.yaml` authoritative, `LoadResult` degenerates to ack-only
> - Fragments-engine removed entirely
> - Plugin SDK at `github.com/hollis-labs/plugin-sdk` (new module, NOT the existing `go-plugin`)
> - Catalog + archives hosted on Cloudflare R2 + Pages
>
> **Authoritative artifact:** `docs/architecture/plugin-execution-plan-2026-04-10.md` — ten-track execution plan with file:line pointers, sharp edges, and validation gates. Start there.
>
> **What IS still useful in this document:**
> - §1 (Executive Summary) — still an accurate characterization of the "plumbing without adoption" state
> - §2 (Ground Truth vs Documented State) — docs-vs-reality drift notes
> - §3 (Gap Inventory) — the raw gaps are still the gaps; only the fix strategy changed
> - §3.5 (Plugin inventory issues) — still accurate per-plugin problem list
>
> **What to IGNORE in this document:**
> - §4 (Beta Blockers vs Post-Beta) — built on the assumption that plugins stay in-tree
> - §5 (Execution Plan) — replaced entirely by the execution plan linked above
> - §6 (Decisions Needed) — all resolved in the 2026-04-10 session
> - §7 (Success Criteria for Beta) — replaced by §16 of the execution plan
>
> **Session context:** the full architectural reasoning is in the 2026-04-10 conversation that produced findings #1–#7. Read the execution plan's §0 for the TL;DR of what changed.

---

> **Status:** discovery + design only. Execution happens in dedicated backend and frontend sessions.
>
> **Scope:** The Nanite plugin contract and every shipped plugin. No other app (Fragments Engine tooling, Cortex, Volon) is in scope. No backwards-compat concerns — no external users yet.
>
> **Companion docs:**
> - `docs/architecture/plugin-execution-plan-2026-04-10.md` — **authoritative execution plan (supersedes §4–§7 of this document)**
> - `docs/architecture/plugin-envelope-emission-findings-2026-04-10.md` — raw envelope emission gap findings
> - `docs/architecture/plugin-system.md` — original architecture (partly stale)
> - `docs/architecture/plugin-evolution-plan.md` — Phase 1–8 plan (status claims partly stale)
> - `docs/plugin-extraction-plan.md` — extraction plan (phases 1,3,6,7 done)

---

## 1. Executive Summary

The plugin system is **mostly plumbing, barely used, and quietly broken in places**. Core extension features (slots, widgets, commands, keybindings, config, connectors, event streaming, providers, envelopes) each have one of three failure modes:

1. **Plumbing exists but zero plugin adoption** (slots, commands, keybindings, connectors, providers, CLI-adapters-via-host)
2. **Half-wired across backend and frontend** (event SSE stream has no frontend consumer; widget registry is not API-driven despite claims; `/api/plugin-config` path differs from docs)
3. **Plugin contract is a leaky abstraction** — every plugin under `plugins/*` imports `internal/chat`, `internal/mcp`, or `internal/store`, meaning no plugin can live as an external Go module. The "plugin SDK at `github.com/hollis-labs/go-plugin`" is real but insufficient: plugins must reach past it into Nanite internals for envelope registration, MCP server registration, DB access, MCP tool execution, and agent seeding.

The envelope-emission findings doc (2026-04-10) is confirmed in full, plus one previously-unverified gap now also confirmed: **nothing in the frontend consumes `/api/plugins/events/stream`**, so the `event.Data["envelope"]` paths in the giphy and oembed built-in plugins are dead end-to-end.

**Beta-release status:** Of the 15 plugins that ship, all compile cleanly and most render correctly in the UI, but several embarrassments remain for a developer-friend audience (see §4).

---

## 2. Ground Truth vs Documented State

| Claim in docs | Reality |
|---|---|
| "Plugin contract lives in `libs/plugin/`" (`plugin-dev.md`) | **Wrong.** Contract lives in external module `github.com/hollis-labs/go-plugin` at sibling path `../framework/libs/go-plugin` (replace directive in `go.mod:24`). |
| "`plugin.yaml` is documentation only — Go code is source of truth" (`plugin-dev.md`, `plugin-system.md §13`) | **Partly wrong.** The backend *does* parse `plugin.yaml` for `Name`, `Version`, `Description`, `Config` schema, `Dependencies`, `Runtime`, `Entrypoint`, `LoadType`, `ToolOverrides`. It does **not** parse `registers.envelopes`, `registers.quick_actions`, `registers.hooks`, `api_endpoints`, `agents`, or `requires`. (`internal/plugin/config.go:134`, `loader.go:54`) |
| "Phase 5b frontend TODO: slash command arg hints + plugin badge" (`plugin-evolution-plan.md`) | **Partial.** Arg hints still TODO. Plugin badge is rendered as a field in the API DTO and declared in the TS type, but `SlashCommandMenu.tsx` never reads `source` — no badge visible. |
| "Phase 8a frontend TODO: `useKeyboardShortcuts` merge" (`plugin-evolution-plan.md`) | **Stale.** Frontend merge is done: `ui/src/hooks/useKeyboardShortcuts.ts:96,221-232` calls `usePluginKeybindings()` and dispatches plugin command actions. Mark 8a-fe as DONE. |
| "Widget Plugin Migration — fully API-driven" (2026-03-28) | **Misleading.** `ui/src/generated/plugin-widgets.ts` is a static codegen manifest with hardcoded `import()` statements. A dynamic ESM fallback exists but no plugin uses it. |
| "No plugin auto-discovery (manual loading in `main.go`)" (`plugin-dev.md` Limitation #3) | **Wrong.** `plugin.DiscoverPlugins(pluginsDir)` at `internal/plugin/loader.go:32` scans the plugins directory and `cmd/nanite/main.go:480` runs discovery at startup. `LoadRegisteredBuiltins` is a second pass for compiled-in plugins without yaml on disk. |
| "Frontend component loading hardcoded" (`plugin-dev.md` Limitation #1) | **Partly resolved.** Envelopes and widgets are codegenned into registry files at build time — not a runtime registry, but also not hardcoded if/else chains. A dynamic ESM loader exists (`ui/src/lib/plugin-loader.ts`) but is unused. |
| "No plugin isolation" (`plugin-dev.md` Limitation #4) | **Still true.** By design — acceptable for beta. |
| "CRUD error mapping uses fragile string matching" (`plugin-dev.md` Limitation #7) | **Resolved by Phase 1d.** `crud.go` uses `errors.As` against `PluginError` now. |
| "`http.ServeMux` doesn't support route removal" (`plugin-dev.md` Limitation #8) | **Still true**, and **shared by many registries** (see §3.4). |

---

## 3. Gap Inventory

### 3.1 Envelope system (from findings doc — all still true)

| Gap | Evidence | Severity |
|---|---|---|
| G1 — No `Host.RegisterEnvelopeType` method. Only `chat.RegisterEnvelopeType` in `internal/chat/envelope.go:27`. | grep confirms 3 non-test call sites, all in `plugins/fragments-engine/plugin.go:50-52`. | **High** (forces `internal/chat` import) |
| G2 — Loader ignores `plugin.yaml` `registers.envelopes`. | `internal/plugin/config.go:39-68` `PluginManifest` struct has no envelopes field. Only consumer is `scripts/generate-plugin-imports.mjs:129-184`. | **High** (manifest is misleading) |
| G3 — `ValidateEnvelope` is advisory. Unregistered types flow through with only a log warning. | `internal/chat/envelope.go:163-201` `ParseEnvelopes` appends despite `unregistered_type`. `internal/service/chat_generate.go:612-617` logs and moves on. | **Medium** (no teeth, noisy logs) |
| G4 — `event.Data["envelope"]` is dead code system-wide. | Writers at `builtin/giphy/plugin.go:161` and `builtin/oembed/plugin.go:138`. Zero readers in Go. Zero frontend SSE consumers of `/api/plugins/events/stream` — the unverified item from findings §2.3 is now **confirmed dead**. | **Low** (just delete) |
| G5 — `giphy-card` vs `giphy-modal` type-name mismatch. | Plugin yaml declares `giphy-modal`, plugin Go emits `giphy-card`, but the `!giphy` self-tool (`internal/mcp/self_tools_transport.go:444-456`) emits `giphy-modal` which is what actually reaches users. The plugin path is vestigial. | **Low** (delete vestigial code) |

### 3.2 Plugin contract surface (from contract audit)

| Gap | Evidence | Severity |
|---|---|---|
| G6 — No `Host.DB()` / `Host.SQL(pluginID)` for plugin-owned tables. Plugins access `*store.Store.DB` via service + type assertion. | `plugins/support-ticket/plugin.go:46-58` shows the `hasDB` workaround. | **High** |
| G7 — No `Host.ExecuteMCPTool(...)`. Plugins cast to `*mcp.Manager` and call `ExecuteTool` directly. | `plugins/fragments-engine/plugin.go` lines 178, 198, 238, 258, 284, 324, 349 (7 call sites). | **High** |
| G8 — No `Host.RegisterMCPServer(name, transport)`. `mcp.MCPTransport` is in `internal/mcp/`. | `plugins/fragments-engine/plugin.go:68`, `plugins/support-ticket/plugin.go:107`. | **High** |
| G9 — No `Host.RegisterAgentProfile(profile)`. Plugins import `internal/store` and call `store.CreateAgent`. | `plugins/support-ticket/seed.go:14-56`. Plugin-shipped yaml agents (e.g., `plugins/support-ticket/agents/it-support.yaml`) are never loaded. | **Medium** |
| G10 — `Host.SetStore(s *store.Store)` leaks `*store.Store` in a public method signature. | `internal/plugin/host.go:438`. | **Low** (host-only, but breaks the public surface promise) |
| G11 — Nanite-only slot constants (`SlotComposerAbove`, `SlotComposerBelow`, `SlotMessageActions`, `SlotMessageHeader`, `SlotSessionSidebar`, `SlotModal`) live in `internal/plugin/types.go:25-30`, not in the SDK. | Plugins using these must import `hostplugin "github.com/hollis-labs/nanite/internal/plugin"`. | **Medium** |
| G12 — Event-name constants, `EventData`, `NewEvent`, `NormalizeEventType` all live in `internal/plugin/events.go`, not the SDK. | Plugins subscribing to typed events re-invent string constants. | **Medium** |
| G13 — 35+ `Emit*` helpers on `*Host` are not on the SDK `Host` interface — plugins cannot emit typed events without a type assertion. | `internal/plugin/events.go:261-548`. | **Low** |
| G14 — `RegisterHTTPHandler` is a `*Host` method, not on the SDK. Fragments-engine type-asserts to use it. | `internal/plugin/host.go:172`, `plugins/fragments-engine/plugin.go:76,127-133` (7 routes). | **Medium** |
| G15 — SDK `Registry` stub returns `"not supported by shared Registry; use Conduit Host"` for slot/command/keybinding/connector/provider/adapter/config/filter. | `../framework/libs/go-plugin/registry.go:243-284`. Dead code inside the shared library. | **Low** (delete or make it real) |

### 3.3 Feature coverage gaps (from feature audit)

| Feature | Backend | Frontend | Plugin adoption | Status |
|---|---|---|---|---|
| UI slots | ✅ | ✅ (12 mounts) | **1 of 12** (fragments-engine) | **Plumbing** — looks impressive, mostly empty |
| Widgets | ✅ (metadata) | Static codegen | 4 IDs, all in-tree | **Half-wired** — not API-driven; codegen-only |
| Slash commands | ✅ | No `source` badge | **0 plugins** | **Unused** + missing badge UI |
| Keybindings | ✅ | ✅ (merged at `useKeyboardShortcuts.ts:96`) | **0 plugins** | **Unused** |
| Plugin config | ✅ | ✅ | 1 plugin (giphy) | **Working** |
| Connectors | ✅ | n/a | **0 concrete connectors** | **Parked** (intentional, per extraction plan Phase 5) |
| Event SSE (`/api/plugins/events/stream`) | ✅ | **No consumer** | 0 | **Dead endpoint** |
| `Host.RegisterProvider` / `RegisterCLIAdapter` | ✅ (dead methods) | n/a | **0 real plugin uses** — adapter-* are wired via direct import in `internal/service/install/adapters.go:13-17` | **Bypassed** |
| Envelope registration | Only via `internal/chat` import | n/a | 1 plugin (fragments-engine) | **See G1–G5** |

### 3.4 Lifecycle gaps

Every "Register*" path is one-way. The only registration types that get properly unregistered on `UnloadPlugin` are **filters**, **UI components** (by `uiOwners` map), and **keybindings** (by `kbOwners` map). Everything else leaks on unload:

| Registration | Unregister path? | Evidence |
|---|---|---|
| Event hooks | ❌ | `internal/plugin/host.go:1043-1052` comment: "EventHook does not expose plugin ID" |
| HTTP routes (CRUD + custom) | ❌ | `http.ServeMux` stdlib limitation; `host.go:193-197` acknowledges it |
| `h.crudHandlers` map | ❌ | Never cleared |
| Slash commands | ❌ | `CommandRegistrar` has no unregister method |
| UI slots (`h.slots`) | ❌ | No removal in `UnloadPlugin` |
| Connectors (`h.connectors`, `h.connectorOwners`, `h.connectorHealth`) | ❌ | No removal anywhere |
| Providers | ❌ | Forwarded to provider-registry service; no unregister |
| CLI adapters | ❌ | Stored under `services["cli-adapter:"+name]`; never deleted |
| Task backends | ❌ | Forwarded to `task.Service`; no unregister |
| MCP servers | ❌ | Registered through `*mcp.Manager.AddServer`, no per-plugin cleanup |

**`Host.Shutdown()` deadlock risk** — `host.go:1170` holds `h.mu` across the entire loop calling `p.Unload()` for every plugin. Any plugin `Unload` that re-enters the host to deregister anything will deadlock. Works today only because no plugin does that.

### 3.5 Plugin inventory issues

All 15 plugins compile. Issues worth calling out:

| Plugin | Issue | Severity |
|---|---|---|
| `giphy` (builtin) | Event-hook envelope emission is dead code. The working `!giphy` path is in core self-tools, not the plugin. The plugin's `Load()` registers a config schema and a dead hook. Delete or re-purpose. | Low (cleanup) |
| `oembed` (builtin) | Same as giphy — event-hook envelope write is dead code. No core-side equivalent; oEmbed cards currently never render. | **Medium** (advertised but broken) |
| `support` | Declares 4 envelope types in yaml, registers none via Go. Envelopes still render (frontend codegen picked them up) but backend logs `unregistered_type` for every one, plus `kb-result` is always emitted by core `chat.BuildKBEnvelope` so it works accidentally. KB transport requires Postgres at `SUPPORT_DATABASE_URL` and silently disables otherwise. | Medium (noisy logs + demo-fragile) |
| `fragments-engine` | Hard-depends on Engine MCP server at runtime. When Engine is absent, chat-header sprint button returns 503 on click. | Medium (bad UX) |
| `adapter-opencode` | Header comment admits format is unverified. | Medium (shipping unfinished) |
| `plugins/giphy/` and `plugins/oembed/` yaml-only dirs | Orphaned. Real Go code lives in `internal/plugin/builtin/{giphy,oembed}/`. Confuses anyone exploring the plugins directory. | Low (delete or add README) |
| `plugins/session-stats/` | Yaml exists, no Go — real code is at `internal/plugin/builtin/sessionstats/`. Same orphan pattern. | Low |
| `nanite plugin new` scaffold | Template imports `github.com/hollis-labs/conduit/internal/plugin` and `github.com/hollis-labs/fragments-engine/plugin`. Both are wrong. Scaffolded plugins won't compile. | **High** (onboarding-breaking) |
| Test coverage | Only adapter-* plugins have tests. giphy, oembed, support, fragments-engine, session-stats, bookmarks, debug-widgets, and the four widget-shell plugins have zero tests. | Medium |

---

## 4. Beta Blockers vs Post-Beta

Given the target audience (developer friends, first beta), severity is judged by "will this make Nanite look broken / unprofessional on first use."

### 4.1 P0 — ship blockers (must fix before beta)

1. **`nanite plugin new` scaffold is broken.** Any developer friend who tries to build a plugin will hit "cannot find module" on first `go build`. Fix the template imports in `internal/plugin/scaffold/templates/plugin.go.tmpl` and `plugin.yaml.tmpl`.
2. **`adapter-opencode` ships with "format is unverified" header.** Either verify against the real Opencode CLI or remove from the shipped adapter list until it's ready.
3. **`oembed` doesn't actually render oEmbed cards.** The event-hook path is dead. Either wire a working emission path (tool handler + `ENVELOPE_DATA` marker, matching the `!giphy` self-tool pattern) or mark the plugin as disabled and remove the oembed-card advertising.
4. **`fragments-engine` 503-on-click when Engine MCP is absent.** Hide the sprint-planning chat-header action when no Engine connection exists. This is a UX polish blocker — the plugin itself is fine.
5. **`Host.Shutdown()` deadlock trap.** Release the lock across `p.Unload()` calls the same way `LoadPlugin` does. Currently works only because no plugin re-enters the host during unload; adding one later will silently break shutdown.
6. **Fix `plugin-dev.md` stale Key Paths.** The agent context file points to a nonexistent `libs/plugin/` directory.

### 4.2 P1 — visible polish (should fix before beta)

7. **Slash command plugin badge in `SlashCommandMenu.tsx`.** API already returns `source`. Add a small badge next to command name.
8. **`support` envelope log noise.** Either call `chat.RegisterEnvelopeType` from `plugins/support-ticket/plugin.go:Load()` for all 4 declared types, OR make the loader read `registers.envelopes` (solves G1+G2 at once — see P2 item 14).
9. **Delete the dead `/api/plugins/events/stream` endpoint** or wire a frontend consumer. Currently just noise in the API surface.
10. **`giphy` plugin cleanup.** Delete the dead event-hook envelope write. Decide whether the plugin still has a reason to exist (config schema for API key + rating) or if it should be folded into the core self-tool.
11. **Orphaned `plugins/{giphy,oembed,session-stats}` yaml-only directories.** Delete them. Real code lives under `internal/plugin/builtin/`.
12. **Update `docs/architecture/plugin-evolution-plan.md` status claims** — Phase 8a frontend is actually DONE; Phase 5b slash command badge still needs the UI work; widget migration is not fully API-driven; etc.

### 4.3 P2 — plugin contract hardening (right architectural work)

This is the bulk of the design work. Goal: make the contract coherent enough that a developer can write a real out-of-tree plugin using only `github.com/hollis-labs/go-plugin`.

13. **Add `Host.RegisterEnvelopeType(envelopeType string) error` to the SDK interface.** Implement on `*Host`; delegates to `chat.RegisterEnvelopeType`. Removes the last `internal/chat` import from plugins.
14. **Make the loader parse `plugin.yaml` `registers.envelopes:`** and call `Host.RegisterEnvelopeType` for each entry during `LoadPlugin`. This is where yaml declarations become real runtime state. Eliminates the silent type-name mismatch class of bugs.
15. **Make envelope registration enforcing, not advisory.** In `ParseEnvelopes`, drop unregistered envelopes and log at `error` level. Add a migration path: run with a "soft" mode flag in dev that warns but doesn't drop, so we can catch existing plugins that need updates.
16. **Add `Host.RegisterMCPServer(name string, transport MCPTransport) error`** with a public `MCPTransport` interface in the SDK. The SDK interface methods mirror `internal/mcp.MCPTransport` (`ListTools`, `CallTool`). An adapter in `internal/plugin/host.go` bridges SDK transport to `*mcp.Manager.AddServer`.
17. **Add `Host.ExecuteMCPTool(ctx context.Context, toolName string, args map[string]any) (string, error)`.** Internal impl forwards to `*mcp.Manager.ExecuteTool`.
18. **Add `Host.DB(pluginID string) (*sql.DB, error)` OR `Host.SQL() SQL`** with a scoped SQL interface (`Exec`, `Query`, `QueryRow`, `Begin`). Preference: the latter — gives us a clean place to later add per-plugin namespacing / quota. Adopters: support-ticket. Simpler alternative: ship `Host.DB()` returning `*sql.DB` directly and document "plugin-owned tables must be prefixed with `plugin_<id>_`".
19. **Add `Host.RegisterAgentProfile(profile AgentProfile) error`** with a public SDK `AgentProfile` type. Alternative: add a loader pass that reads `plugins/<id>/agents/*.yaml` during plugin discovery and materializes them to the store. Preference: loader pass — keeps agent files file-based per the architecture rule.
20. **Add `Host.RegisterHTTPHandler(pattern string, handler http.Handler) error`** to the SDK interface (already exists on `*Host`). Fragments-engine no longer needs the type assertion.
21. **Move `EventData`, event-name constants, `NewEvent`, `NormalizeEventType`, and Nanite-specific slot constants** into the SDK. Bulk move from `internal/plugin/events.go` and `internal/plugin/types.go:25-30` to `github.com/hollis-labs/go-plugin`.
22. **Promote typed event emitters to the SDK interface.** Add at least `Host.EmitEvent(event Event) error` so plugins can emit without a `*Host` type assertion. Typed helpers (`EmitSessionStart` etc.) can stay on the concrete host if we want to keep the interface small.
23. **Remove `Host.SetStore(*store.Store)` from the public method set.** Move it to an unexported initializer or a separate `HostInitializer` interface only `main.go` uses.
24. **Fix event-hook unregister.** Add `PluginID() string` to `EventHook` interface (or to a new `EventHookWithOwner` interface, falling back to "no cleanup" for legacy hooks). Clean up on `UnloadPlugin`.
25. **Add unregister paths** for slash commands, slots, connectors, providers, CLI adapters, task backends, and MCP servers. Each needs its owner map entry populated during Register and swept during `UnloadPlugin`.
26. **Delete the dead `RegisterProvider` and `RegisterCLIAdapter` Host methods** — they're zero-caller. Adapter-* plugins can keep their current direct-import wiring, or we rebuild both method paths to actually mediate adapter registration through the plugin host. Preference: delete the methods to reduce API surface.
27. **Delete the `../framework/libs/go-plugin` `Registry` stub** (`registry.go:243-284`). It's a zero-caller shadow of the real `*Host`, returning "not supported" for every method.
28. **Normalize `/api/plugin-config/{id}`** — docs say `/api/plugins/{id}/config`. Pick one and make code, docs, and frontend agree.

### 4.4 P3 — cleanup

29. Add minimal smoke tests for giphy, oembed, support-ticket, session-stats, and the widget-shell plugins.
30. Rewrite `docs/architecture/plugin-system.md` §13 (Known Limitations) from scratch to reflect current state.
31. Create a `docs/plugin-dev-handbook.md` that walks a new developer from `nanite plugin new` to a working plugin using only the SDK.
32. Add a CI check that greps every file under `plugins/*` for `github.com/hollis-labs/nanite/internal/` imports and fails if any are found. This enforces the "no internal imports from plugins" rule once the contract work is done.
33. Add a CI check that validates every `plugin.yaml` `registers.envelopes:` entry matches a backend-registered type after loader changes.

---

## 5. Execution Plan — How to Split the Work

### 5.1 Backend session (primary work)

**Goal:** land P0 items 1, 2, 5; P1 items 8, 9, 10, 11; all of P2; all of P3 except 29 (testing is its own thing).

Suggested sequencing:
1. P0 item 5 — `Shutdown` deadlock (5-minute fix, unblocks nothing but removes a trap).
2. P0 item 1 — scaffold template imports (one-file change).
3. P0 item 2 — either verify `adapter-opencode` or remove it from the shipped list.
4. P2 items 13, 14, 15 as a bundle — adds `Host.RegisterEnvelopeType`, teaches the loader to parse `registers.envelopes`, flips validation to enforcing. Concurrently migrate `plugins/fragments-engine/plugin.go` to use the new method and drop its `internal/chat` import. P1 item 8 (support envelope registration) falls out for free — the loader registers them from yaml.
5. P2 item 20 + G14 — promote `RegisterHTTPHandler` to the SDK. Drop the fragments-engine type assertion.
6. P2 items 16, 17 — public MCP server / tool execution. Migrate `plugins/fragments-engine/tools.go` and `plugins/support-ticket/{plugin.go,kb.go}` to drop `internal/mcp` imports.
7. P2 item 18 — public DB access. Migrate `plugins/support-ticket/plugin.go` to drop `internal/store` import.
8. P2 item 19 — plugin agent loader pass. Migrate `plugins/support-ticket/seed.go` to use it. Drop last `internal/store` import from support-ticket.
9. P2 item 21 — SDK move of events + slot constants. Update all in-tree builtins to import from the SDK.
10. P2 items 22, 23, 24, 25 — the lifecycle/unregister cleanup pass.
11. P1 item 9 — delete `/api/plugins/events/stream` (or wire something).
12. P1 items 10, 11 — giphy cleanup + orphan dir deletion.
13. P2 items 26, 27 — delete dead `RegisterProvider` / `RegisterCLIAdapter` / `Registry` stub.
14. P2 item 28 — normalize `/api/plugin-config` path.
15. P3 items 30, 31, 32, 33 — docs + CI checks.

The P2 bundle (items 13–21) is the core architectural work. Once it lands, every plugin in `plugins/*` should compile with zero `internal/*` imports. That's the quantitative success criterion.

### 5.2 Frontend session

**Goal:** land P0 item 4, P1 item 7, and whatever frontend changes fall out of backend P2 work.

1. P1 item 7 — slash command badge in `SlashCommandMenu.tsx`. Read `source` field, render small badge.
2. P0 item 4 — hide `fragments-engine` chat-header action when Engine MCP is absent. This may need a new backend signal (e.g., plugin reports a "degraded" status) or it can poll the plugin's own health via an API endpoint — TBD during execution.
3. If backend P2 item 15 ships — ensure the frontend gracefully handles the case where an envelope stream contains a now-rejected unregistered type (show a generic error card instead of a blank).
4. Frontend P3 item — check `GiphyCard.tsx` is removed if the giphy plugin cleanup deletes the `giphy-card` type.

### 5.3 Testing session (P3 item 29)

Separate from execution. Can run in parallel once backend contract work lands.

---

## 6. Decisions Needed Before Execution

Items the executor will need direction on. None block this session since this is discovery + design only.

1. **P2-14 loader behavior:** When `plugin.yaml` declares an envelope type that isn't declared anywhere else, should the loader validate that the frontend has a component for it? Currently `scripts/generate-plugin-imports.mjs` silently drops plugin envelopes whose component files don't exist. We could fail-loud at backend load time instead.
2. **P2-15 enforcing mode rollout:** Hard-drop unregistered envelopes immediately, or ship a "soft" mode for one release cycle? Given no users exist yet, hard-drop is probably fine.
3. **P2-18 DB API shape:** Return `*sql.DB` (simple, exposes raw stdlib) or a scoped `Host.SQL()` interface (opinionated, wraps Exec/Query/QueryRow/Begin)? Preference noted above.
4. **P2-19 agent seeding approach:** Host method or loader pass? Preference noted above (loader pass).
5. **P2-26 adapter host methods:** Delete the dead methods and leave adapter wiring as direct-import, or rebuild both sides to mediate through the host? Preference noted above (delete).
6. **`giphy` plugin fate:** Delete entirely (core self-tool is sufficient) or keep as a config-schema-only plugin? If kept, there's nothing for it to actually do.
7. **`oembed` plugin fate:** Delete (P0 item 3 option A) or rewrite to use the marker-based emission path (P0 item 3 option B)? Option B is the right call if oEmbed cards are valued; otherwise option A is faster.
8. **Fragments-engine plugin extraction:** The findings doc §4.2 and §5 notes that `plugins/fragments-engine` lives inside the Nanite Go module only because it imports `internal/*`. After P2 items 13–19 land, it should be possible to move fragments-engine to a real external Go module. Do we want to do that before beta (proves the contract works) or after (keeps the beta change surface small)? Preference: after beta. Proves the contract first via the support-ticket migration.

---

## 7. Success Criteria for Beta

This is the minimum that must be true before the beta ships:

- [ ] `nanite plugin new support-test` scaffolds a plugin that compiles and loads without modification.
- [ ] All 15 shipped plugins compile, load, and function end-to-end in a fresh install.
- [ ] `go build ./plugins/...` produces zero `internal/*` import errors (i.e., after the P2 bundle, plugins should only need `github.com/hollis-labs/go-plugin`).
- [ ] `go test -race ./internal/plugin/... ./plugins/...` is clean.
- [ ] Fresh `!giphy balloons` renders a GIF card without any `unregistered_type` warnings in the logs.
- [ ] Fresh `search_kb` (support-ticket) renders a KB card without any `unregistered_type` warnings in the logs.
- [ ] `Host.Shutdown()` does not deadlock under a plugin whose `Unload` re-enters the host.
- [ ] `PluginManager.tsx` can install, enable, disable, configure, and uninstall a test plugin end-to-end.
- [ ] `plugin-dev.md` agent context matches reality (Key Paths, limitations, inventory).
- [ ] `docs/architecture/plugin-system.md` is accurate or marked stale.
