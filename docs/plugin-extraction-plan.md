# Plugin Extraction Plan

> Decided 2026-04-04. Phases are independent unless noted.

## Principles

1. **Migrations = DDL only. seed.go = data only.** Enforced via CI grep (no INSERT in .sql files).
2. **Connectors are standalone plugins.** A connector plugin registers infrastructure (e.g., `plugin-connector-linear`). Feature plugins consume connectors, never own them.
3. **MCP as requirement OR connector.** Some integrations (Fragments Engine) use MCP servers directly rather than connectors. Plugins declare their requirements (`mcp_servers` in plugin.yaml). At load time, if a required MCP server is unavailable, the plugin emits a warning and degrades gracefully — no silent failures.
4. **Core stays lean.** Core provides: sessions, messages, agents, providers, models, tools, context, streaming, settings. Everything else is a plugin.

## Phase 1 — Cleanup & Dead Code (quick, low risk)

**Status: DONE (2026-04-04)**

- [x] Squash 27 migrations into single `001_schema.sql` (DDL only)
- [x] Move seed data (user_settings singleton, catalog sources) into `seed.go`
- [x] Fix stale `conduit-plugins` catalog URL → `nanite-plugins`
- [x] Remove trivia dead code (`TriviaQuestionCard.tsx`, `TriviaLeaderboardCard.tsx`)
- [x] Remove stale known issues from boot prompt (connector imports, self-tools test, seed overlap)
- [x] Delete old `nanite.db`, rebuild via Cerberus
- [x] Migration purity CI hook (lefthook pre-commit: grep for INSERT in .sql files)
- [x] Remove marvel plugin entirely (builtin Go package + frontend components + plugin dir)
- [x] Remove demo-presenter plugin entirely (builtin Go package + plugin dir)
- [x] Remove dead envelope components (EmailInboxCard, EmailComposeCard, EmailPreviewCard, TeamsMessageCard)
- [x] Fix plugin YAML paths (giphy, oembed, support-ticket — component paths now correct)
- [x] Fix giphy envelope type mismatch (YAML `giphy-card` → `giphy-modal` to match backend)
- [x] Move plugin envelopes out of CORE_ENTRIES (giphy, oembed, support-ticket, sprint now source-tagged to their plugins)
- [x] Add missing core primitives to CORE_ENTRIES (approval-card, proposal-card, question-form)
- [x] Add missing types to backend registeredTypes (session-task, approval-card, proposal-card, question-form, oembed-card, sprint-planning-review)
- [x] Envelope sync test passing
- [x] Full test suite green

## Phase 2 — Fragments Engine Plugin Extraction

Extract all Volon/Engine-specific functionality into `plugin-fragments-engine`.

### What moves:
- `internal/api/volon_proxy.go` → routes become plugin API endpoints
- Sprint plugin (`plugins/sprint/`) → merged into `plugin-fragments-engine`
- Volon backlog button widget (`VolonBacklogButton.tsx`)
- Sprint planning modal + review card (already in `plugins/sprint/` frontend)
- Task envelope cards tied to Engine (`TaskDispositionCard.tsx`, `TaskCompleteNotificationCard.tsx`)
- Engine-specific tool knowledge entries in `internal/toolclient/tool_knowledge.go`

### What stays in core:
- Generic task tracking (`/api/tasks/*`, `internal/task/`) — this is the local task backend
- The `TaskBackend` interface (defined in Phase 4)

### MCP requirement pattern:
- Plugin declares `mcp_servers: [engine]` in `plugin.yaml`
- On load, checks if Engine MCP server is available
- If unavailable: plugin loads but tools/endpoints return "Engine not connected" errors
- No connector needed — Engine uses MCP directly

### Rename:
- All references to "Volon" become "Fragments Engine" or just "Engine"

## Phase 3 — Debug Widget Plugin

**Status: DONE (2026-04-04)**

Extract debug/developer widgets into `plugin-debug` (bundled, default-on when `developer_mode` is true).

- [x] Created `internal/plugin/builtin/debugwidgets/plugin.go` — registers 3 widgets
- [x] Registered in `allplugins.go`
- [x] Moved frontend widgets + panels to `ui/src/components/plugins/debug/`
- [x] Updated `plugin-widgets.ts` — 3 widgets moved from CORE_ENTRIES to PLUGIN_ENTRIES (source: "debug")
- [x] Backend API endpoints stay in core (Option B — handlers are thin DB reads)
- [x] All imports updated, no broken references
- [x] go build + go vet + go test clean, frontend builds, Cerberus deploy

### What moved:
- `BrokerDecisionsWidget.tsx` + `BrokerDecisionsPanel.tsx`
- `SlotInspectorWidget.tsx` + `SlotInspectorPanel.tsx`
- `TurnSnapshotWidget.tsx` + `TurnSnapshotPanel.tsx`
- `DebugPanel.tsx` + `DebugPanelsContainer.tsx`

### What stays in core:
- Backend: `/api/broker/decisions`, `/api/debug/slots` endpoints (Option B)
- `broker_decisions` table and store methods
- `ObservabilityWidget.tsx` — useful for all users, not just debug
- `session-info`, `context-budget`, `token-usage`, `agent-status`, `tools` widgets

## Phase 4 — Task Backend Abstraction

Define `TaskBackend` interface so multiple implementations can provide task/todo functionality.

### Interface:
```go
type TaskBackend interface {
    CreateTask(ctx context.Context, task Task) (Task, error)
    ListTasks(ctx context.Context, filter TaskFilter) ([]Task, error)
    GetTask(ctx context.Context, id string) (Task, error)
    UpdateTask(ctx context.Context, id string, update TaskUpdate) (Task, error)
    TransitionTask(ctx context.Context, id string, status string) (Task, error)
    DeleteTask(ctx context.Context, id string) error
}
```

### Implementations (each a plugin):
- `plugin-tasks-local` — SQLite (current `/api/tasks/*` endpoints, extracted from core)
- `plugin-tasks-engine` — Fragments Engine MCP (wraps `engine_task_*` tools)
- `plugin-tasks-linear` — Linear API via linear connector (future)
- `plugin-tasks-todotxt` — todo.txt file format (future)

### Core provides:
- The `TaskBackend` interface definition
- A registry for backends: `Host.RegisterTaskBackend(name, backend)`
- API routes delegate to the active backend (configured in user_settings)

## Phase 5 — Connector Plugins

Build connectors as-needed. Each connector is a standalone plugin that registers a transport.

### Transport Priority Pattern

Many services offer multiple integration paths (CLI, MCP, REST API). Connectors should support
multiple transports and auto-select the best available one, with user override.

**Default priority: CLI > MCP > API**

Rationale: CLI tools are pre-authenticated (uses existing user sessions), battle-tested for
tool calls, and avoid API key management. MCP is next because it's structured and typed.
Raw REST API is the fallback when nothing else is available.

```yaml
# plugin.yaml for plugin-connector-github
name: connector-github
config:
  transport:
    description: "Transport method for GitHub operations"
    type: string
    default: auto          # auto-detect best available
    enum: [auto, cli, mcp, api]
  github_token:
    description: "GitHub personal access token (only needed for api transport)"
    type: string
    env_var: GITHUB_TOKEN
    required: false        # not required when using CLI
```

**Auto-detection logic:**
1. Check if `gh` CLI is installed and authenticated (`gh auth status`)
2. Check if a GitHub MCP server is registered
3. Fall back to API with `GITHUB_TOKEN`
4. If none available: load plugin but surface warning, all operations return clear error

**User override:** Setting `transport: cli` forces CLI even if MCP is available.
Setting `transport: api` forces API even if CLI is installed.

### First connector: `plugin-connector-github`

Operations exposed:
- Issues: create, list, get, update, close, comment
- PRs: create, list, get, review, merge, comment
- Repos: list, get, search
- Releases: create, list

Consumed by: feature plugins that need GitHub access (e.g., code review, issue tracking).

### Future connectors (build as-needed):
- `plugin-connector-linear` — Linear API (issues, projects, teams)
- `plugin-connector-slack` — Slack API (messages, channels)
- `plugin-connector-email` — Email (IMAP/SMTP)

### Feature plugins that consume connectors:
- `plugin-notifications` — uses slack, email connectors for outbound notifications
- `plugin-tasks-linear` — uses linear connector for task backend

### Pattern:
```yaml
# plugin.yaml for plugin-tasks-linear
name: tasks-linear
requires:
  connectors: [linear]    # graceful degrade if missing
  mcp_servers: []          # none needed
```

## Phase 6 — Remaining Extractions

Lower priority, do when natural.

### Envelope plugins:
- `plugin-email` — EmailInboxCard, EmailComposeCard, EmailPreviewCard (requires email connector)
- `plugin-teams` — TeamsMessageCard (requires teams connector)
- `plugin-documents` — ProposalCard, ReportCard, DocumentViewerCard (standalone, no connector needed)

### Feature plugins:
- `plugin-bookmarks` — Bookmark CRUD + auto-title via LLM
- `plugin-actions` — Custom actions + keybindings registry
- `plugin-demo-presenter` — Demo mode (already partially a plugin)

## Phase 7 — Primitive Envelope Library

Define a set of reusable, well-designed envelope primitives that plugins can use out of the box
instead of building custom cards from scratch. These are core-provided building blocks.

### Current core primitives (Phase 1):
- `document-viewer` — render markdown/text content
- `report-card` — metrics visualization with bar charts and colored cards
- `error-report` — structured error display
- `approval-card` — yes/no/allow/deny decision card
- `proposal-card` — proposed action with accept/reject
- `question-form` — interactive form with multiple input types

### Planned additions:
- `info-card` — simple title + body + optional icon, for informational messages
- `list-card` — ordered/unordered list with optional actions per item
- `metric-card` — single KPI display (value, trend, label)
- `progress-card` — progress bar with status text
- `confirmation-card` — "are you sure?" with confirm/cancel actions
- `table-card` — tabular data display with optional sorting
- `timeline-card` — chronological event list
- `diff-card` — before/after comparison view

### Design principles:
- Each primitive accepts a typed `data` payload (documented JSON schema)
- Primitives are unstyled beyond the design system — they use shadcn components
- Plugins declare which primitive type they want in their envelope `data.type` field
- New primitives can be added without plugin changes — they're backwards compatible

### Implementation:
- Frontend: one React component per primitive in `ui/src/components/chat/envelopes/primitives/`
- Backend: add type string to `registeredTypes` in `envelope.go`
- Registry: add to CORE_ENTRIES in `plugin-envelopes.ts`
- Test: envelope sync test catches mismatches automatically

## CI Enforcement

### Migration purity check:
```bash
# Fail if any .sql migration contains INSERT/UPDATE/DELETE
if grep -qiE '^\s*(INSERT|UPDATE|DELETE)\b' internal/store/migrations/*.sql; then
  echo "ERROR: Migrations must be DDL only. Move data operations to seed.go."
  exit 1
fi
```

Add to lefthook pre-commit or CI pipeline.

## Open Questions

1. **Widget registration for core widgets** — Currently hardcoded in frontend. Should core widgets also go through the plugin widget registry? This would make the system uniform but adds complexity for things that will always exist.
2. **Plugin dependency resolution** — If `plugin-tasks-linear` requires `plugin-connector-linear`, should the plugin host auto-resolve dependencies or just warn?
3. **Hot-reload for plugins** — Current plugin system loads at startup. Should extracted features support hot-reload, or is restart acceptable?
