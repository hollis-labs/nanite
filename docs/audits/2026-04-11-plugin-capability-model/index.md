# Audit: plugin-capability-model

## Scope

**Scope string:** `plugin-capability-model`
**Interpretation:** Map the current plugin capability surface — what plugins can touch via `internal/*` imports, what host state leaks, what's implicitly available, what a malicious plugin could do. Deliverable: capability map + gap list.

**Packages read in full:**
- `internal/plugin/host.go` — Host struct, all public methods (1189 lines)
- `internal/plugin/loader.go` — plugin discovery and loading (271 lines)
- `internal/plugin/events.go` — event catalog and Emit* methods (~590 lines)
- `internal/plugin/event_stream.go` — SSE subscriber management (47 lines)
- `internal/plugin/filter.go` — filter registry and views (~80 lines read)
- `internal/plugin/triggers.go` — trigger dispatcher (~80 lines read)
- `internal/plugin/crud.go` — CRUD route handlers (146 lines)
- `internal/plugin/subprocess/plugin.go` — subprocess bridge (200 lines)
- `framework/libs/go-plugin/plugin.go` — SDK interface definition (288 lines)

**Builtin plugins sampled:**
- `internal/plugin/builtin/adapter-claude/plugin.go` — full read
- `internal/plugin/builtin/adapter-nanite-native/plugin.go` — 120 lines
- `internal/plugin/builtin/sessionstats/plugin.go` — full read

**External plugins sampled:**
- `plugins/support-ticket/plugin.go` — full read
- `plugins/fragments-engine/plugin.go` — full read

**Also read for context:**
- `cmd/nanite/main.go` — service registration (grep for RegisterService)
- Prior audit index (`docs/audits/INDEX.md`) — cross-audit grounding
- `.nanite/agents/reviewer-backend.md` — known issues and priorities
- `.nanite/agents/backend.md` — package map and conventions

## Methodology

**Categories applied:**
- Security (trust boundary analysis, privilege boundary, capability enumeration)
- Error handling (panic propagation, cleanup failures)
- Go idioms (interface design, type assertion patterns)
- Resource management (per-plugin limits)

**Categories deferred:**
- Concurrency — covered by `concurrency-cancellation-sweep` audit
- Standards/tooling — covered by `whole-repo-tooling-and-tests-sweep` audit
- Test quality — not in scope for capability mapping

**Tools run:** None (narrow scope — this is a capability mapping, not a mechanical sweep).

**What the reviewer did NOT check:**
- Runtime behavior of subprocess plugins under load
- The complete MCP Manager API surface (only grep for service registration)
- Plugin install/uninstall API handlers (covered by `api-privilege-boundary` audit)

## Findings

### By severity

**Critical (3)**
- [01 — GetService("store") grants full database access](01-critical-getservice-store-full-db-access.md)
- [02 — Type assertion to concrete Host bypasses SDK interface boundary](02-critical-type-assertion-to-concrete-host.md)
- [03 — GetService("mcp") grants full MCP Manager access](03-critical-mcp-manager-full-access.md)

**High (3)**
- [04 — No panic recovery in event hook dispatch](04-high-no-panic-recovery-in-event-hooks.md)
- [05 — Plugin CRUD routes have no plugin-scoped authorization](05-high-crud-routes-no-plugin-scoped-auth.md)
- [06 — Event hooks cannot be cleaned up on plugin unload](06-high-event-hook-cleanup-impossible-on-unload.md)

**Medium (4)**
- [07 — GetService returns untyped interface{}](07-medium-getservice-returns-untyped-interface.md)
- [08 — Plugins can call chat.RegisterEnvelopeType — global state mutation](08-medium-chat-registerenvelopetype-global-mutation.md)
- [09 — Pre-hook cancellation allows DoS](09-medium-prehook-cancellation-dos.md)
- [10 — Subprocess entrypoint parsing allows argument injection](10-medium-subprocess-entrypoint-injection.md)

**Low (2)**
- [11 — GetPlugin allows cross-plugin state access](11-low-getplugin-cross-plugin-access.md)
- [12 — No resource limits per plugin](12-low-no-resource-limits-per-plugin.md)

**Info (1)**
- [13 — Subprocess runtime provides genuine isolation](13-info-subprocess-runtime-is-genuinely-isolated.md)

### By topic

**Privilege boundary**
- [01 — GetService("store") grants full database access](01-critical-getservice-store-full-db-access.md)
- [03 — GetService("mcp") grants full MCP Manager access](03-critical-mcp-manager-full-access.md)
- [07 — GetService returns untyped interface{}](07-medium-getservice-returns-untyped-interface.md)

**Isolation boundary**
- [02 — Type assertion to concrete Host bypasses SDK interface](02-critical-type-assertion-to-concrete-host.md)
- [08 — Direct internal import enables global state mutation](08-medium-chat-registerenvelopetype-global-mutation.md)
- [11 — GetPlugin allows cross-plugin state access](11-low-getplugin-cross-plugin-access.md)
- [13 — Subprocess runtime provides genuine isolation](13-info-subprocess-runtime-is-genuinely-isolated.md)

**Error handling / blast radius**
- [04 — No panic recovery in event hook dispatch](04-high-no-panic-recovery-in-event-hooks.md)
- [06 — Event hooks cannot be cleaned up on unload](06-high-event-hook-cleanup-impossible-on-unload.md)

**Authorization**
- [05 — CRUD routes have no plugin-scoped auth](05-high-crud-routes-no-plugin-scoped-auth.md)
- [09 — Pre-hook cancellation DoS](09-medium-prehook-cancellation-dos.md)

**Subprocess runtime**
- [10 — Subprocess entrypoint argument injection](10-medium-subprocess-entrypoint-injection.md)
- [13 — Subprocess runtime provides genuine isolation](13-info-subprocess-runtime-is-genuinely-isolated.md)

**Resource management**
- [12 — No per-plugin resource limits](12-low-no-resource-limits-per-plugin.md)

### Capability map

- [14 — Full capability map](14-capability-map.md) — complete enumeration of SDK interface, concrete Host methods, services, readable/writable state, malicious-plugin worst-case, subprocess isolation, and claimed-vs-actual verdict.

## Recommended next steps

1. **Add panic recovery to EmitEvent and EmitPreHook** (finding 04) — smallest change, largest blast-radius reduction.
2. **Remove `"container"` from service registry** (finding 07) — it grants transitive access to everything.
3. **Extend EventHook interface with PluginID** (finding 06) — enables cleanup on unload.
4. **Promote `RegisterSlot`, `RegisterKeybinding`, `RegisterHTTPHandler` to the SDK interface** — currently required by real plugins but only accessible via type assertion.
5. **Design scoped service proxies** (findings 01, 03, 07) — `PluginStore`, `PluginMCPClient` that enforce per-plugin access boundaries. This is the long-pole item and should be a separate design scope.
6. **Formalize the two-tier trust model** (finding 02, capability map section 9) — document that in-process = trusted, subprocess = untrusted-capable. Add a manifest flag and install-time gate.
7. **Follow-up scope: `plugin-sandbox-design`** — design the actual sandbox for untrusted in-process plugins, if that's a goal. This audit maps the current state; sandbox design is future work per the scope brief.

## Known issues skipped

- `Host.Shutdown()` holds mutex across `p.Unload()` — deadlock risk. Tracked in `reviewer-backend.md` and `plugin-dev.md`. Not re-flagged.
- Event hook cleanup limitation — tracked as BLG-20260410-004. Finding 06 provides additional evidence but the core issue is known.
- `http.ServeMux` doesn't support route removal — stdlib constraint, documented in `backend.md`.
- Scaffold templates have broken imports — P0-1 tracked, out of scope.
- `registers.envelopes` in plugin.yaml never read — documented dead path.

## Noticed but out of scope

- **`internal/plugin/builtin/sessionstats/handlers.go`** — the session-stats plugin creates its own SQLite schema (`InitSchema`) via the raw `*sql.DB` from `GetService("store")`. This schema is not managed by the migration system and could conflict with future migrations. Follow-up scope: `plugin-schema-management`.
- **`plugins/support-ticket/seed.go`** — the support plugin seeds agent profiles into the store during Load. If the seed data conflicts with user-created agents, behavior is undefined. Follow-up scope: `plugin-seed-isolation`.
- **`internal/plugin/subprocess/manager.go`** — the subprocess Manager has proper shutdown (cancel -> wait -> force-kill), noted as the only sound lifecycle in the `concurrency-cancellation-sweep` audit. However, subprocess crash recovery and restart policy were not audited here. Follow-up scope: `subprocess-resilience`.
- **`internal/plugin/manifest.go`** — plugin.yaml parsing was not audited. Manifest field validation, unknown-field handling, and version compatibility are all potential concerns. Follow-up scope: `plugin-manifest-validation`.
