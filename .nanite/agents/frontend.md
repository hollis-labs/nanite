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

1. **Loose TypeScript** — `(a as any).is_primary` casts in AppShell to check session agent role. Should use proper type narrowing or extend the `SessionAgent` type.

2. **Duplicate API definitions** — `listAgentProfiles()` and `listAgents()` both hit `/api/agents`. One should delegate to the other.

3. **Long hook dependency arrays** — `useChat.sendMessage` has 17 dependencies, risking stale closures. Consider `useRef` for stable callback references.

4. ~~**Manual Map mutations in store**~~ — **FIXED.** Store now creates immutable `new Map()` copies for all Map mutations (`activeStreams`, `pendingTools`, `cliActiveSessions`).

5. **Magic event strings** — SSE event types (`'delta'`, `'tool_call'`, etc.) are hardcoded strings. Define as constants or an enum.

6. **Global error counter** — `errorCounter` variable in useChat is module-scoped outside the component, persisting across re-renders in unexpected ways.

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
