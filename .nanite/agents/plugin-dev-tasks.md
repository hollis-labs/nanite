# Nanite Plugin Dev — Task Context

Boot `nanite-plugin-dev` and load this file for task context.

---

## Task 1: Audit Plugin Docs for Currency

**Status:** Audited 2026-03-28 — significant drift found. Update deferred until after plugin inventory stabilizes.

**Key findings:**
- Architecture doc says 2 plugins, actually 10+ (after cleanup)
- Wrong paths (`plugins/support/` → `plugins/support-ticket/`)
- 3 "known limitations" are now resolved (auto-discovery, config, plugin.yaml parsing)
- Host interface docs show 7 methods, actual has 22+
- Missing 7 Emit* methods and 6 event types from docs

**Action:** Rewrite both docs now that Tasks 3-5 are complete.

---

## Task 2: Create/Update Plugin Scaffold

**Status:** Audited 2026-03-28 — scaffold is current.

- `internal/plugin/builtin/example/` was the reference but has been deleted (Task 3 cleanup)
- `internal/plugin/scaffold/` templates are correct and current
- `libs/plugin/example.go` still exists as library-level reference

**Action:** Decide if a new reference plugin is needed or if scaffold + support-ticket are sufficient.

---

## Task 3: Audit Existing Plugins — Keep/Delete

**Status:** Complete 2026-03-28.

### Deleted (4):
- `example` — YAML-only stub; builtin Go code + allplugins import removed
- `email` — Gmail OAuth2 stub; plugins/ dir removed
- `teams` — Webhook stub; plugins/ dir removed
- `trivia` — Game framework; builtin Go code + allplugins import removed

### Kept — Next Actions:

| Plugin | Status | Next Action |
|--------|--------|-------------|
| support-ticket | Complete | None |
| session-stats | Has Go backend + 3 widgets registered | None urgent |
| demo-presenter | Working | Move 8 global self-tools into plugin `Load()` via `host.GetService("mcp")` |
| giphy | Working, config schema added | Fix `giphy-card` envelope missing from frontend registry |
| marvel | Working | Add `RegisterConfigSchema` (3 API keys); add reciprocal movie→character search action on MarvelMovieCard |
| oembed | Backend complete, frontend broken | Fix manifest component path (`ui/OEmbedCard` → `ui/components/chat/envelopes/OEmbedCard`); regenerate plugin-envelopes.ts |
| context-widgets | Complete | Core plugin — session info, context budget, token usage |
| agent-widgets | Complete | Core plugin — agent status, tools |
| observability-widgets | Complete | Core plugin — observability metrics |
| bookmarks-widget | Complete | Core plugin — bookmarks |

---

## Task 4: Move Workflow Menu to Plugin

**Status:** Complete 2026-03-28 — removed entirely.

Workflow feature was ~70% complete but had zero actual workflows defined, no CRUD UI, synchronous-only execution, and overlapped with Hadron blueprints. Removed all frontend (components, NavRail item, layout store, API client, types) and backend (internal/workflow/, API handlers, store methods, chat engine trigger, MCP self-tools, main.go wiring). DB table `workflows` remains but is unused.

---

## Task 5: Move Widgets to Plugins

**Status:** Complete 2026-03-28 — full dynamic widget system implemented.

**What was built:**
- **Widget registry** (`ui/src/generated/plugin-widgets.ts`) — lazy-import registry mapping widget IDs to React components, same pattern as envelopes. All 7 core widgets registered.
- **Backend validation** (`internal/plugin/host.go`) — RegisterUIComponent validates ID format (alphanumeric+hyphens, 2-64 chars), rejects cross-plugin ID collisions, validates type/name/description lengths. Tracks plugin ownership via `uiOwners` map.
- **WidgetRenderer** (`ui/src/components/widgets/WidgetRenderer.tsx`) — Suspense + per-widget error boundary. Unknown widgets fall back to metadata card.
- **API-driven RightRail** (`ui/src/components/RightRail.tsx`) — fetches widget list from API, reads `widget_visibility`/`widget_order` from user settings, renders via registry.
- **4 standalone widget plugins:**
  - `context-widgets` — Session Info, Context Budget, Token Usage
  - `agent-widgets` — Agent Status, Tools
  - `observability-widgets` — Observability
  - `bookmarks-widget` — Bookmarks
- **LoadRegisteredBuiltins** — ensures compiled-in plugins without a `plugins/` directory are loaded and visible in the plugin manager.
- **Widget preferences** — `widget_visibility` and `widget_order` stored in `ext_settings` (no migration needed).
- **Widget Admin panel** (`ui/src/components/settings/WidgetManager.tsx`) — new "Widgets" tab in Settings. Toggle visibility, drag-to-reorder, gear icon for plugin config, source plugin badge.
- **Plugin config secrets** — `secret` type fields routed to OS keychain via `secrets.Set()`/`secrets.Get()`, never stored in DB. API masks secrets as `********`.
- **Giphy config** — `RegisterConfigSchema` added for API key (secret/keychain) and content rating (select).
- **Builtin plugins in managed API** — compiled-in plugins now appear in `/api/plugins/managed` with type `"core"`.
- **PluginWidgets.tsx deleted** — replaced by WidgetRenderer.
- **Agent Status widget** — removed mode switcher, replaced with status indicator (idle/streaming/tool pending).

**Also fixed:**
- TipTap duplicate `suggestion$` plugin key — `SlashCommandExtension` and `FileMentionExtension` now use unique `PluginKey` instances.
