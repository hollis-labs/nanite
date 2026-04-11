# Frontend Context — Nanite (React SPA)

> Project-specific frontend conventions. Loaded by the frontend agent role when working in this project.
> Lives at `nanite/.nanite/agents/frontend.md`.

## Stack

- **Framework:** React 19.2.0 + TypeScript ~5.9.3
- **Build:** Vite 7.3.1 (dev port 5176, proxies `/api` to `localhost:8090`)
- **Styling:** Tailwind CSS 4.2.1 (dark zinc palette)
- **State:** Zustand 5.0.11 (4 stores)
- **Data Fetching:** TanStack React Query 5.90.21 (staleTime 1min, 1 retry)
- **Editor:** TipTap 3.20.1 (rich text + slash commands)
- **Icons:** Lucide React
- **Linter:** Biome 2.4.7 (primary), ESLint (secondary)
- **Language:** Go backend embeds built UI via `//go:embed` in `internal/server/ui_dist/`

## Project Structure

```
ui/src/
├── main.tsx                    # ReactDOM entry → App
├── App.tsx                     # QueryClient + ErrorBoundary + AppShell
├── index.css                   # Tailwind theme, highlight.js dark, custom scrollbar
├── components/
│   ├── AppShell.tsx            # Layout orchestrator (NavRail, Sidebar, Chat, RightRail, modals)
│   ├── ErrorBoundary.tsx       # Class-based error boundary
│   ├── NavRail.tsx             # Vertical nav: workspace selector, nav items, inbox badge
│   ├── RightRail.tsx           # Collapsible widgets panel
│   ├── chat/
│   │   ├── ChatMain.tsx        # Chat area + circuit breaker + session takeover alerts
│   │   ├── ChatHeader.tsx      # Session title, agent mode, model selector
│   │   ├── ChatTranscript.tsx  # Scrollable message list with streaming
│   │   ├── ChatComposer.tsx    # TipTap editor with slash commands
│   │   ├── ChatMessage.tsx     # Single message rendering
│   │   ├── MessageContent.tsx  # Markdown + envelope dispatch
│   │   ├── ToolCallDisplay.tsx # Tool execution status cards
│   │   ├── AgentPicker.tsx     # Agent selection UI
│   │   ├── AgentRoster.tsx     # Multi-agent roster
│   │   ├── extensions/         # TipTap slash command extension
│   │   └── envelopes/         # 25+ rich response card components
│   ├── sidebar/
│   │   └── LeftSidebar.tsx     # Sessions list (pinned, conversations, tasks)
│   ├── settings/
│   │   ├── SettingsPage.tsx    # Tab router (preferences, providers, shortcuts, agents, etc.)
│   │   ├── PreferencesPanel.tsx # Session defaults, utility model, fallback chain
│   │   ├── ProviderManager.tsx # Provider cards: enable/disable, API keys, CLI paths
│   │   ├── ShortcutsPanel.tsx  # Keyboard shortcut editor
│   │   ├── AgentProfileManager.tsx
│   │   ├── SkillsBrowser.tsx
│   │   ├── PromptTemplateEditor.tsx
│   │   ├── ToolDashboard.tsx   # MCP server management + tool discovery
│   │   ├── PluginManager.tsx   # Plugin lifecycle + config gear icon
│   │   ├── PluginConfigPanel.tsx # Dynamic config form (string/bool/int/select/secret)
│   │   └── observability/      # Execution stats, utility call comparison, process health
│   ├── workflows/              # Workflow list, run modal, result cards
│   ├── a2a/                    # A2A inbox panel, task thread panel
│   ├── widgets/                # Right rail widgets (tokens, context, bookmarks, tools, agent)
│   ├── modals/                 # SprintPlanningModal
│   ├── drawers/                # ArtifactsDrawer
│   └── ui/                     # Primitives (Button, Tooltip, ScrollArea)
├── stores/
│   ├── useAppStore.ts          # activeWorkspaceId, activeProjectId, activeSessionId
│   ├── useChatStore.ts         # streaming, toolCalls, errors, mode, model, presence
│   ├── useLayoutStore.ts       # Panel open/close state (persisted to localStorage)
│   └── useSprintPlanningStore.ts
├── hooks/
│   ├── useChat.ts              # SSE stream handling, message CRUD, circuit breaker
│   ├── useKeyboardShortcuts.ts # Cmd+B/L/N/K/D/./]/[ bindings
│   ├── usePresence.ts          # /api/presence SSE for active streams + pending tools
│   ├── useTaskContext.ts       # Detect task-scoped sessions (context_type === 'task')
│   └── useToolRefresh.ts       # Auto-refresh tool discovery on session switch
├── lib/
│   ├── api.ts                  # Fetch-based REST client (50+ endpoints)
│   ├── types.ts                # All TypeScript interfaces (500+ lines)
│   └── utils.ts                # cn() helper (clsx + tailwind-merge)
└── generated/
    └── plugin-envelopes.ts     # Auto-generated lazy plugin envelope registry
```

## Component Inventory

| Component | Location | Purpose |
|-----------|----------|---------|
| `AppShell` | `components/AppShell.tsx` | Root layout — NavRail, LeftSidebar, ChatMain, RightRail, modals |
| `NavRail` | `components/NavRail.tsx` | Vertical nav with workspace selector, page links, inbox badge |
| `ChatMain` | `chat/ChatMain.tsx` | Chat viewport with circuit breaker + takeover alerts |
| `ChatTranscript` | `chat/ChatTranscript.tsx` | Scrollable message list with streaming content |
| `ChatComposer` | `chat/ChatComposer.tsx` | TipTap editor with slash commands and toolbar |
| `ChatMessage` | `chat/ChatMessage.tsx` | Individual message with envelope rendering |
| `MessageContent` | `chat/MessageContent.tsx` | Markdown + structured envelope dispatch |
| `ToolCallDisplay` | `chat/ToolCallDisplay.tsx` | Tool call status indicators (running/done/error) |
| `EnvelopeRenderer` | `chat/envelopes/EnvelopeRenderer.tsx` | Dispatches to typed envelope cards + plugin registry |
| `LeftSidebar` | `sidebar/LeftSidebar.tsx` | Sessions grouped by pinned/conversations/tasks |
| `RightRail` | `components/RightRail.tsx` | Collapsible widgets panel (6 widgets) |
| `SettingsPage` | `settings/SettingsPage.tsx` | Tab-based settings (agents, skills, prompts, tools, plugins) |
| `ToolDashboard` | `settings/ToolDashboard.tsx` | MCP server management and tool discovery UI |
| `PluginConfigPanel` | `settings/PluginConfigPanel.tsx` | Dynamic plugin config form (5 field types) |
| `ProcessHealthPanel` | `settings/observability/ProcessHealthPanel.tsx` | Active CLI processes with kill-stale |
| `UtilityLogTable` | `settings/observability/UtilityLogTable.tsx` | Individual utility call log |
| `ArtifactChip` | `chat/ArtifactChip.tsx` | Inline artifact link chip in messages |
| `InboxPanel` | `a2a/InboxPanel.tsx` | A2A message inbox with user/agent tabs |
| `TaskThreadPanel` | `a2a/TaskThreadPanel.tsx` | Task-scoped A2A thread sidebar |
| `WorkflowPanel` | `workflows/WorkflowPanel.tsx` | Workflow list and run modal |

## Zustand Stores

### useAppStore
- `activeWorkspaceId`, `activeProjectId`, `activeSessionId` — current navigation state
- `configVersion` — bumped to invalidate settings caches

### useChatStore
- `isStreaming`, `streamingContent`, `streamingSessionId` — SSE stream state
- `toolCalls: ToolCall[]` — active tool executions
- `toolWarnings: ToolWarning[]` — MCP tool issues
- `chatErrors: ChatError[]` — dismissible error list
- `circuitOpen` — rate limit circuit breaker
- `sessionTakeover` — another tab stole the SSE connection
- `textOnlyMode` — fallback when agent has no MCP tools
- `activeMode: AgentMode`, `activeModel: string` — current agent config
- `activeStreams`, `pendingTools` — Maps for presence tracking
- `toolCallDisplayMode` — persisted to localStorage

### useLayoutStore (persisted as `nanite-layout`)
- `leftSidebarOpen`, `rightRailOpen`, `artifactsDrawerOpen`, `workflowPanelOpen`, `taskThreadOpen`, `inboxPanelOpen`
- `currentPage: 'chat' | 'settings'`

### useSprintPlanningStore
- `isOpen`, `projectId` — modal state for sprint planning

## Patterns to Follow

### Data Fetching
```typescript
const { data, isLoading } = useQuery({
  queryKey: ['resource', id],
  queryFn: () => api.fetchResource(id),
  enabled: !!id,
})
```
All data fetching uses React Query with conditional `enabled`. Mutations use `useMutation` + `queryClient.invalidateQueries`.

### SSE Streaming
```typescript
const es = new EventSource(url)
es.addEventListener('delta', (e) => { /* append content */ })
es.addEventListener('tool_call', (e) => { /* add to store */ })
es.addEventListener('stream_end', () => { /* finalize */ })
```
SSE connects to `/api/stream/{message_id}`. Events: `delta`, `tool_call`, `tool_result`, `tool_warning`, `status`, `circuit_open`, `session_takeover`, `stream_end`, `error`.

### State Flow
User interaction → Zustand action → API mutation → React Query invalidation → re-render.

### Component Visibility
Drawers, modals, and panels check layout store state: `if (!open) return null`.

### Error Handling in API
```typescript
if (!res.ok) {
  const err = await res.json().catch(() => ({ error: `Request failed: ${res.status}` }))
  throw new Error(err.error || message)
}
```

### Button Component (CVA)
Uses `class-variance-authority` with variants: `default`, `ghost`; sizes: `sm`, `md`, `lg`, `icon`.

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Cmd+B` | Toggle left sidebar |
| `Cmd+/` | Toggle right rail |
| `Cmd+L` | Focus composer |
| `Cmd+N` | New session |
| `Cmd+K` | Open sidebar |
| `Cmd+]` | Next session |
| `Cmd+[` | Previous session |
| `Cmd+D` | Bookmark last assistant message |
| `Cmd+.` | Toggle artifacts drawer |

## Envelope System

The envelope system renders structured agent responses as interactive cards. `EnvelopeRenderer` dispatches by `envelope.type` to lazy-loaded components.

**Core types:** ProposalCard, QuestionForm, ApprovalCard, TaskDispositionCard, TaskCompleteNotificationCard, SprintPlanningReviewCard, DocumentViewerCard, ReportCard, KBResultCard, ResolutionCaptureCard, ErrorCard.

**Plugin types:** GiphyCard, OEmbedCard, TicketFormCard, etc.

Plugin envelopes are auto-generated via `scripts/generate-plugin-imports.mjs` (runs as prebuild/predev).

## Styling Convention

- **Dark theme:** zinc palette (bg-zinc-950, text-zinc-100, border-zinc-800)
- **Accent:** indigo (indigo-400, indigo-600)
- **Semantic colors:** emerald (success), amber (warning), red (error)
- **Muted text:** text-zinc-400
- **Scrollbar:** custom styled via CSS (WebKit + Firefox), `scrollbar-gutter: stable`
- **Syntax highlighting:** highlight.js github-dark theme
- **Transitions:** `transition-all duration-200 ease-in-out` on panels and drawers
- New styles should use Tailwind utility classes. No inline CSS.

## Build & Dev

| Command | Purpose |
|---------|---------|
| `npm run dev` | Vite dev server on port 5176 (proxies `/api` → `localhost:8090`) |
| `npm run build` | `tsc -b && vite build` → `ui/dist/` |
| `npm run lint` | Biome check |
| `npm run lint:fix` | Biome check --write |
| `npm run generate:plugins` | Regenerate plugin envelope registry |

**Production embedding:** Go binary embeds `ui/dist/` via `//go:embed` into `internal/server/ui_dist/`. Dockerfile runs multi-stage build (npm build → Go embed → single binary on port 8090).

**Dev mode:** Backend `-dev` flag skips embedded UI, allowing separate Vite dev server with HMR.

## Anti-Patterns Found

1. ~~**Loose TypeScript** — `(a as any).is_primary` casts in AppShell~~ — **FIXED.** No `as any` casts remain in `AppShell.tsx`.

2. ~~**Duplicate API definitions** — `listAgentProfiles()` and `listAgents()`~~ — **FIXED (2026-04-10).** Dead `listAgentProfiles` alias removed from `lib/api.ts`; `listAgents()` is canonical.

3. ~~**Long hook dependency arrays** — `useChat.sendMessage` 17 deps~~ — **FIXED.** `sendMessage` dep array now `[sessionId, queryClient]` (2 deps).

4. ~~**Manual Map mutations in store**~~ — **FIXED.** Store now creates immutable `new Map()` copies for all Map mutations (`activeStreams`, `pendingTools`, `cliActiveSessions`).

5. ~~**Magic event strings** — SSE event types~~ — **FIXED.** Centralized as `SSE.DELTA`, `SSE.TOOL_CALL`, etc. (see `lib/sse-events.ts`), used throughout `useChat.ts`.

6. ~~**Global error counter** — module-scoped `errorCounter` in `useChat`~~ — **FIXED.** Symbol no longer present.

---

## Beta Known Issues (canonical list)

**Primary tracking:** [`docs/beta-known-issues.md`](../../docs/beta-known-issues.md). Check this document before starting frontend work. P0 issues there are currently backend-only, but that can change — always check first.

**Post-beta items filed to Engine backlog:** query `engine_backlog_list --project-id nanite` for deferred items.

---

## Frontend Quick Wins (pre-beta polish)

Small items the frontend agent can knock out. These are not high-severity, but they're quick enough that clearing them improves the user experience.

### Code quality — from §Anti-Patterns Found above

All code-quality items (1, 3, 5, 6) have been resolved in flight since this list was written. Item (2) closed 2026-04-10. See the strike-throughs in §Anti-Patterns Found above for specifics. **No open code-quality items remain.**

### Wiring — from inline TODOs

- [x] **`SearchModal.tsx`** — `jumpToMessage` wired via `pendingJump` state bus in `useChatStore` (2026-04-10). `useChat` has a dedicated reactive `pendingJump` effect (separate from the load-messages effect) that fetches the messages-around window on trigger and signals `ChatTranscript` via `scrollToMessageId`. `ChatTranscript`'s scroll effect uses a `requestAnimationFrame` retry loop (max 1s deadline) because the signal can race ahead of the DOM commit for fetched messages. Works for both same-session and cross-session jumps.

---

## Known Gaps

> Architectural or design gaps that affect how the frontend should evolve. Not bugs, not backlog polish — these are decisions that were deferred or started but not finished. Read this before doing related work so you don't rebuild a half-built abstraction on top of another one.

### Plugin slot component resolution — incomplete

The backend slot system (`internal/plugin/host.go` `RegisterSlot`/`GetAllSlots`, `/api/plugins/ui-slots`) is live, and plugins register slot entries with a `component` string field (e.g. fragments-engine's `sprint-planning`). The frontend fetches them via `usePluginSlots()` and 3 call sites (`AppShell.tsx`, `RightRail.tsx`, `SettingsPage.tsx`) look up the component via `getSlotComponent()` in `ui/src/lib/plugin-slot-lookup.ts`.

**The gap:** there is no mechanism for a plugin to ship a React component that `getSlotComponent()` can return. Today it only delegates to the runtime `getDynamicSlotComponent()` in `plugin-loader.ts`, which would return a hit if a plugin called `registerSlotComponent()` at runtime — but **no plugin does**. Real plugins that need UI (like fragments-engine's sprint-planning modal) bypass the slot system entirely: they register an action name in Go, and `AppShell.tsx` has a hardcoded `useEffect` that catches a `plugin-modal` event and renders a hardcoded `<SprintPlanningModal>`. All 3 `getSlotComponent()` call sites handle `undefined` gracefully (silent no-render), so the gap is invisible at runtime.

**Earlier state (now cleaned up):** there used to be a `ui/src/generated/plugin-slot-components.ts` file with empty `CORE_ENTRIES`/`PLUGIN_ENTRIES` registries and `@PLUGIN_SLOT_ENTRIES_START`/`END` marker comments for a never-written generator. It was gitignored because it lived under `ui/src/generated/`, which broke fresh checkouts. Removed 2026-04-10 and replaced with the 4-line `plugin-slot-lookup.ts` shim.

**What needs deciding before any work here:** how should plugins ship React components to the frontend? Options roughly: (a) convention-based lazy import from a plugin path, (b) plugin JS bundles loaded over HTTP at runtime, (c) a build-time codegen step that scans `plugin.yaml` manifests for component declarations and writes a registry like `plugin-envelopes.ts`. Each has real design trade-offs — don't pick one in a hurry. See `docs/architecture/plugin-system.md` §13 ("No Widget/Slot System") for the documented acknowledgment of this gap.

### Chat window-mode pagination after jump-to-message — unresolved

After a search → jump-to-message lands, `useChat` fetches a centered window via `api.getMessagesAround` and sets `paginationState` to `null`. The null state hides the "load older" button and makes `hasOlderMessages` return false — because the `messages-around` endpoint doesn't currently return the window's `oldest_offset` relative to the full session, so we genuinely don't know where in the full history we are. Users who land on a jumped-to message therefore cannot scroll further back within that session until they re-enter it normally.

**Options for fixing:** extend the backend `messages?around=...` response to include `oldest_offset`/`total` for the around-window, then `useChat` can reconstruct a real pagination state; OR add an explicit "window mode" flag to `paginationState` that shows a "Load full history" button instead of "Load older"; OR keep current behavior and require a session re-entry. Post-beta decision.

---

## Frontend Polish Backlog (post-beta)

**Source:** [`docs/frontend-punchlist.md`](../../docs/frontend-punchlist.md) — ~60 UI polish items (command palette, infinite scroll, drawers redesign, widget controls, CRUD-in-modals, search, context menus, skeletons, optimistic UI, etc.).

All polish-tier. Not high-severity. Address in a dedicated post-beta sprint or opportunistically when touching the relevant files.

---

## Reference Implementations

| Pattern | File | Why it's good |
|---------|------|---------------|
| SSE streaming with recovery | `hooks/useChat.ts` | Circuit breaker, session takeover, clean cleanup |
| Zustand store with persistence | `stores/useLayoutStore.ts` | Clean persist middleware usage with localStorage |
| Lazy envelope dispatch | `generated/plugin-envelopes.ts` | Code-split plugin cards, registry pattern |
| CVA component variants | `components/ui/Button.tsx` | Clean variant definition with tailwind-merge |
| TipTap slash commands | `chat/extensions/SlashCommandExtension.ts` | Suggestion API integration for editor commands |

## Notes

- Path alias: `@/` maps to `src/` (configured in vite.config.ts and tsconfig)
- React Query `staleTime`: 1 minute default, 1 retry
- Layout state persisted to localStorage as `nanite-layout`
- Tool call display mode persisted separately as `nanite:toolCallDisplayMode`
- No env vars — all config hardcoded; production uses embedded defaults
- Plugin system: plugins provide envelope components registered at build time
- A2A messaging: agent-to-agent collaboration via inbox + task threads
- Total UI code: ~80 files across components, stores, hooks, and lib

---

## Beta Release TODO (Frontend)

### 1. Plugin Config UI
- [x] **Plugin settings panel** — Render plugin config schemas from `GET /api/plugin-config/{id}`. Dynamic form renderer for all 5 field types (string, bool, int, select, secret). Gear icon on active/disabled plugins opens config panel.
- [x] **Config override components** — Plugins can set `component` on ConfigFieldDef to name a custom React component. Registry at `generated/plugin-config-components.ts`. Gated behind `developer_mode` toggle in Preferences > Advanced.
- [x] **Recover mode** — Toggle in Preferences > Advanced. When on: PluginConfigPanel uses default primitives (skips component overrides), EnvelopeRenderer uses core-only registry (skips plugin envelopes), PluginWidgets hidden.
- [x] **Plugin widget mount points** — `GET /api/plugins/ui-components` → `PluginWidgets` component in RightRail. Shows widget-type components with name, description, props. Gated behind developer_mode, hidden in recover_mode.

### 2. Slash Commands & Fragments v1 UX
- [x] Port relevant UI patterns from Fragments v1 (user will specify which).
- [x] Ensure TipTap slash command extension picks up plugin-registered commands (backend: `Host.RegisterCommand`).

### 3. Artifacts Drawer
- [x] **Artifacts panel** — List session artifacts with preview (images, code, markdown), download, and back navigation. Eye icon for previewable types.
- [x] **Inline artifact links** — `[name](artifact:name)` markdown links render as clickable ArtifactChip components that open the drawer.
- [x] Wire artifact upload into the composer (Paperclip button + drag-and-drop with visual feedback).

### 4. Observability Dashboard
- [x] **Execution stats widget** — `GET /api/metrics/executions` → table/chart of recent calls (Chart.js + shadcn).
- [x] **Utility call comparison** — `GET /api/metrics/utility` → side-by-side provider comparison.
- [x] **Observability right-rail widget** — at-a-glance stats.
- [x] **Process health panel** — `GET /api/processes/health` → active CLI processes with uptime, idle time, stale badges. "Kill Stale" button via `POST /api/processes/kill-stale`. Also wired `useUtilityCallLog` into new UtilityLogTable.

### 5. Session Creation UX
- [x] Creation-time overrides (adapter, provider, model, agent) — inline form in sidebar.
- [x] Wire `default_agent` from database settings (removed localStorage hack).
- [x] Improve clone to carry title + agent from source session.
- [x] Fork session: clone with full message history (`POST /api/sessions/{id}/fork`).
- [x] NavRail "New Chat" passes defaults from userSettings.

### 6. Multi-Session Presence
- [x] Presence indicators work with 3+ concurrent streaming sessions (Map-based, no single-session assumptions).
- [x] `cli_active` presence event → cyan pulsing dot in sidebar for PTY activity between messages.
- [x] `session_archived` presence event → immediate sidebar update via query invalidation.
- [x] Priority order: tool-pending (amber) > streaming (green) > cli-active (cyan).

### 7. Provider/Model Management
- [x] All 11 providers seeded on every boot (Anthropic, OpenAI, Ollama, Gemini, Mistral, Azure, 5 CLIs).
- [x] Provider icons for all providers in model picker and provider manager.
- [x] ProviderManager UI: compact flex-wrap cards with enable/disable, API keys (OS keychain), CLI paths, base URLs.
- [x] CLI auto-detection via adapter.Detect() with manual path override.
- [x] Toggle locked until requirements met (API key or CLI detected).
- [x] Model picker filters out disabled providers.
- [x] Provider startup: keychain → env var → skip.

### 8. Widget Plugin Migration
> **Complete 2026-03-28.** Chose component registry approach (same pattern as envelopes). Widgets are lazy-loaded via `plugin-widgets.ts` registry, rendered through `WidgetRenderer.tsx` with per-widget Suspense + error boundary. RightRail is fully API-driven.

- [x] **Design widget rendering system** — Component registry pattern (lazy imports, same as envelopes). No ADR needed — pattern is proven.
- [x] **Migrate SessionInfoWidget** — registered by `context-widgets` plugin.
- [x] **Migrate BookmarksWidget** — registered by `bookmarks-widget` plugin.
- [x] **Migrate ContextBudgetWidget** — registered by `context-widgets` plugin.
- [x] **Migrate TokenUsageWidget** — registered by `context-widgets` plugin.
- [x] **Migrate ObservabilityWidget** — registered by `observability-widgets` plugin.
- [x] **Migrate ToolsWidget** — registered by `agent-widgets` plugin.
- [x] **Migrate AgentStatusWidget** — registered by `agent-widgets` plugin. Mode switcher removed, replaced with status indicator.
- [x] **Update PluginWidgets renderer** — Replaced entirely with `WidgetRenderer.tsx`. `PluginWidgets.tsx` deleted. Dev-mode gate removed.

### 9. Widget Admin Panel
> **Complete 2026-03-28.** New "Widgets" tab in Settings with drag-to-reorder and per-widget visibility toggles.

- [x] **Widget manager page** — `WidgetManager.tsx` in Settings. Fetches registered widgets from API, shows source plugin badge, gear icon for plugin config.
- [x] **Widget enable/disable** — Per-widget eye toggle. Persists to `widget_visibility` in `ext_settings`.
- [x] **Widget sort order (drag-drop)** — Drag-and-drop reordering. Persists to `widget_order` in `ext_settings`.
- [x] **Plugin settings access from widget cards** — Gear icon opens `PluginConfigPanel` for the widget's source plugin.
- [x] **Backend: widget preferences** — Stored in `ext_settings` JSON (no migration needed). `widget_visibility` (map) and `widget_order` (array) merged via existing partial update.

---

## Deferred from Phase 1 audit campaign (2026-04-11) — user-facing polish follow-ups

Pulled out of the Phase 1 audit queue during the 2026-04-11 audit-orchestrator brainstorm. These are follow-up work items, not audits. Land in regular frontend work queue when the user picks them up.

- **i18n readiness.** Hardcoded strings (frontend side), number/date formatting, RTL implications. Pairs with the DE/ES translation-file feature (see tracking.md delegated items). Go-side string sweep is a separate concern owned by backend.
- **a11y — keyboard navigation, screen-reader labels, color contrast, focus management in shadcn components.** Covers the expansion scope proposed as `* a11y` plus the existing frontend queue item `keyboard-shortcuts-and-a11y`. Both moved here as one combined follow-up item per user direction.

**Recommended shape:** these are normal frontend workstream items, not audits. When the user is ready to work on them, they belong in whatever the frontend-side task backlog is (Engine backlog `#nanite` project tag, or wherever frontend tasks currently live). They do not need a scoping session — the work is well-understood, it just needs to happen.
