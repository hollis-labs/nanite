# Planner (todos + plans)

Nanite's internal planner subsystem — `internal/store/{todos,plans}.go` — backed by SQLite tables `todos` and `plans`. Two shapes, one purpose: structured work tracking for agents and the user, scoped to workspace / project / session.

For agent conventions (when to use todos vs plans, TodoWrite vs `nanite_todo_create`, sub-agent handoff patterns), see `internal/assets/framework/docs/nanite-planner.md` — that is the canonical conventions doc. This file is the dev/implementation reference.

## Package layout

```
internal/
  store/
    todos.go          — Todo struct + TodoFilter + 6 methods
    plans.go          — Plan struct + PlanStep + PlanFilter + 7 methods
    todos_test.go
    plans_test.go
  api/
    api.go            — 13 HTTP handlers registered in Mux
    todos.go          — todo HTTP handlers
    plans.go          — plan HTTP handlers
  mcp/
    self_tools.go     — 5 MCP tools registered (nanite_todo_{create,update,list}, nanite_plan_{create,update})
    self_tools_transport.go — tool handlers dispatch into Store methods
```

## Data model

### Todo

- `id` (UUID) · `scope` · `scope_id` · `parent_id` (nesting)
- `title` · `description` · `status` · `priority` · `labels` (JSON array) · `metadata` (JSON object)
- `created_by` · `created_at` · `updated_at`

**Statuses:** `pending`, `in_progress`, `done`, `blocked`
**Priorities:** `low`, `medium`, `high`, `critical`

### Plan

- `id` (UUID) · `scope` · `scope_id`
- `title` · `description` · `status` · `steps` (JSON array of `PlanStep`) · `metadata` (JSON object)
- `created_by` · `created_at` · `updated_at`

**Statuses:** `proposed`, `approved`, `in_progress`, `complete`, `abandoned`

### PlanStep

- `id` · `title` · `status` · `todo_id` (optional link) · `depends_on` (array of step ids) · `acceptance` · `notes`

**Statuses:** `pending`, `in_progress`, `done`, `skipped`

## HTTP routes

Registered in `internal/api/api.go`:

```
GET    /api/todos
POST   /api/todos
GET    /api/todos/{id}
PUT    /api/todos/{id}
DELETE /api/todos/{id}
GET    /api/todos/{id}/children

GET    /api/plans
POST   /api/plans
GET    /api/plans/{id}
PUT    /api/plans/{id}
PUT    /api/plans/{id}/steps/{stepID}
DELETE /api/plans/{id}
POST   /api/plans/{id}/approve
```

`POST /api/plans/{id}/approve` is a dedicated transition endpoint that moves `proposed → approved`. Use for UI approve buttons; generic step/plan updates still go through `PUT`.

## MCP tools

Five tools registered as core builtins in `internal/mcp/self_tools.go`:

- `nanite_todo_create` — scope + title required; priority defaults to `medium`
- `nanite_todo_update` — id required; any subset of title/description/status/priority/labels
- `nanite_todo_list` — optional filters; tool description prompts the agent to wrap results in a `todo-list` envelope when presenting to the user (the handler itself returns plain text)
- `nanite_plan_create` — scope + title required; status defaults to `proposed`; tool description prompts the agent to wrap the returned plan in a `plan-review` envelope
- `nanite_plan_update` — id required; if `step_id` supplied, updates just that step; otherwise plan-level

All five handlers live in `internal/mcp/self_tools_transport.go`.

## Envelope types

Two S5 envelope types are registered in `config/envelopes.yaml`:

- `todo-list` — interactive list card; UI fetches live via `/api/todos` and lets the user toggle statuses
- `plan-review` — approve/reject card; user action POSTs `/api/plans/{id}/approve` or transitions to `abandoned`

## Migration

Tables live in the baseline migrations. No separate migration for the planner subsystem — it shipped with the core schema and has been stable across Phase 2/3.

## Testing

- Unit tests: `internal/store/{todos,plans}_test.go` — cover CRUD, filters, status transitions, JSON round-trips
- MCP handler coverage: `internal/mcp/self_tools_transport_test.go`
- API coverage: none currently dedicated to todo/plan endpoints. Add endpoint-level tests in `internal/api/` if/when coverage is needed — the general `internal/api/api_test.go` exercises the mux, not the individual handlers.

Run with:

```bash
go test ./internal/store/... ./internal/mcp/...
```

## Out of scope for this subsystem

- **Clockwork Manifold** — task orchestration is a separate project in the portfolio, not a nanite concern.
- **Engine / Vanta Conduit** — portfolio-wide project/sprint tracking lives in Engine; long-term memory lives in Vanta. Use those for cross-project work; use nanite's planner for in-session and per-project local tracking.
- **Workflow execution** — plans describe work, they do not execute it. Plan steps are progressed by agents (parent or sub-agent), not by a scheduler.
