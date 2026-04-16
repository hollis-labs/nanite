# Nanite Planner — Agent Conventions

Nanite ships two internal stores — `todos` and `plans` — plus MCP tools that let agents track structured work inside a chat session. This document is the **conventions** layer: when to reach for these tools, how to scope work, and how agents hand off between each other via plan steps.

This is agent-facing. For implementation details (package map, HTTP routes, schema), see `docs/planner.md` in the nanite source tree.

## Tool surface

Five MCP tools are exposed to the LLM (always-on, no plugin required):

| Tool | Purpose |
|---|---|
| `nanite_todo_create` | Create a todo item (workspace/project/session scope, optional parent, priority, labels) |
| `nanite_todo_update` | Transition status, change priority, edit title/description/labels |
| `nanite_todo_list` | List/filter todos (by scope, status, priority). The tool description prompts the agent to wrap results in a `todo-list` envelope when presenting to the user. |
| `nanite_plan_create` | Create a plan with ordered steps. The tool description prompts the agent to wrap the returned plan in a `plan-review` envelope when status is `proposed`. |
| `nanite_plan_update` | Update plan fields, or transition a single step (`step_id=...`) |

Full HTTP is also available under `/api/todos/*` and `/api/plans/*` (13 routes total) for non-LLM callers (UI, CLI, sub-agent runners). See `docs/planner.md`.

## Scope — pick one

All todos and plans carry a **scope** (`workspace | project | session`) and a **scope_id**:

| Scope | When to use | `scope_id` |
|---|---|---|
| `session` | Ephemeral work within a single chat session. Disappears with session cleanup. | session UUID |
| `project` | Work that outlives the session but is local to one project. Visible to every session in that project. | `project_id` |
| `workspace` | Cross-project work. Rare — prefer Engine for portfolio-wide tracking. | `""` (empty) |

**Default to `session` scope.** Reach for `project` only when a human/other agent will need the item in a future session. Reach for `workspace` only when Engine isn't fit for the task — typically local-only drafts before promotion.

## TodoWrite vs `nanite_todo_create` — the decision rule

Claude Code's built-in `TodoWrite` tool and nanite's `nanite_todo_create` serve overlapping-but-distinct purposes. Do not pipe one through the other.

| If the todo is… | Use |
|---|---|
| Internal agent bookkeeping for the current turn / next few steps. Not meaningful to the user or future sessions. | **`TodoWrite`** (CC built-in) |
| Work the user will want to see, approve, revisit in a later session, or act on via the UI. | **`nanite_todo_create`** |
| A checklist inside a plan you're executing. | **Plan step** (see below) |

Rule of thumb: if the item would survive `/clear`, persist it via `nanite_todo_*`. Otherwise use `TodoWrite`.

Agents may use both in the same turn — `TodoWrite` to drive their own procedural state, `nanite_todo_create` to surface committed work to the user. Don't duplicate the same item into both stores.

## Todo vs Plan — when to reach for each

| | Todo | Plan |
|---|---|---|
| **Shape** | Flat or nested (`parent_id`), independent items | Ordered steps with dependencies and acceptance criteria |
| **Approval** | None — created in `pending`, work proceeds freely | Typically `proposed` → user approves via `plan-review` envelope → `approved` |
| **Lifecycle** | `pending → in_progress → done` (or `blocked`) | `proposed → approved → in_progress → complete` (or `abandoned`) |
| **Use when** | Items are independent, or approval isn't needed | Multi-step work where sequence, dependencies, or user approval matters |

**Bias toward todos for small work.** A plan is worth the ceremony when:

- The work spans more than ~3 steps
- Steps have dependencies the user should see before approving
- You want the user to approve the approach before execution
- Sub-agents will claim and execute steps independently

If in doubt, start with todos. Promote to a plan later if the shape grows.

## Status lifecycles

### Todo

```
pending → in_progress → done
       ↓
    blocked (transient — unblock by transitioning back to pending or in_progress)
```

### Plan

```
proposed → approved → in_progress → complete
        ↓                         ↓
     abandoned                 abandoned
```

### Plan step

```
pending → in_progress → done
       ↓
     skipped
```

Plan steps and todos can be coupled: a step carries an optional `todo_id` linking it to a concrete todo. Use this when a plan step is the same work as an existing todo — one transition updates both in meaning (though the stores are separate; update each explicitly).

## Envelope integration

Two S5 envelope types pair with these tools. Note: the tool handlers themselves return plain text; the *agent* wraps the results in a `nanite-envelope` block. The tool descriptions prompt the agent to do this when presenting to the user.

- **`todo-list`** — paired with `nanite_todo_list`. The UI renders an interactive card; users can toggle status directly.
- **`plan-review`** — paired with `nanite_plan_create` when status is `proposed`. The UI renders approve/reject affordances; user interaction transitions the plan to `approved` or `abandoned`.

Wrap results in these envelopes when you want the user to see or act on them in the chat UI. Skip the envelope for pure agent-internal reads.

## Sub-agent handoff pattern

Plans are the default substrate for parent → sub-agent handoff.

**Parent agent:**

1. `nanite_plan_create scope=session scope_id=<sid> title=... steps=[...]` — status defaults to `proposed`; wrap the returned plan in a `plan-review` envelope.
2. User approves → plan transitions to `approved`.
3. Parent spawns sub-agent via the inline subagent runner (Phase 3 S7 groundwork) or external mechanism, **passing the `plan_id` and the target `step_id`** as part of the spawn context.

**Sub-agent:**

1. On boot, receive `plan_id` and `step_id` from the parent's spawn context. If the sub-agent needs the plan body, fetch via HTTP: `GET /api/plans/{plan_id}` (MCP tools don't currently include a plan reader — it's a parent-passes-id handoff).
2. Claim the step: `nanite_plan_update id=<plan_id> step_id=<step_id> status=in_progress`.
3. Do the work.
4. Mark done: `nanite_plan_update id=<plan_id> step_id=<step_id> status=done notes="..."`.
5. Return control. The parent decides whether to spawn a new sub-agent for the next step.

**Invariants:**

- Only one agent should hold a step in `in_progress` at a time. There is no hard lock — discipline is on the agent.
- A sub-agent that can't complete a step transitions it to `skipped` with notes explaining why, not `done`.
- If the parent needs a step redone, it transitions the step back to `pending` and respawns or continues.
- Plan-level status (`in_progress` → `complete`) is the parent's responsibility, not the sub-agent's. Sub-agents only touch step status.

For work too small for a plan, parent can create a `project`-scoped todo with `description` carrying the brief, and hand the `todo_id` to the sub-agent as its starting context. The sub-agent updates the todo as it works.

## Worked examples

### Ad-hoc session work

User: "fix the three failing tests in internal/chat."

Agent (no ceremony needed):

```
TodoWrite [
  { content: "read test output", status: in_progress }
  { content: "find root cause",  status: pending }
  { content: "apply fix",        status: pending }
]
```

Nothing goes to `nanite_todo_*` — it's all procedural and ends with the turn.

### Tracked work the user will revisit

User: "we should rewrite the retry logic, but not today."

Agent:

```
nanite_todo_create
  scope=project scope_id=<pid>
  title="rewrite chat retry logic"
  priority=medium
  description="current impl at internal/chat/retry.go mixes backoff with dedup. Split concerns."
```

This outlives the session and shows up when the user opens the project's todo list next week.

### Multi-step work with approval

User: "migrate the envelope registry to YAML."

Agent:

```
nanite_plan_create
  scope=project scope_id=<pid>
  title="migrate envelope registry to YAML"
  steps=[
    { id: "1", title: "read current Go-registered envelopes" }
    { id: "2", title: "draft config/envelopes.yaml", depends_on: ["1"] }
    { id: "3", title: "add loader + validation", depends_on: ["2"],
      acceptance: "go test ./internal/envelope passes" }
    { id: "4", title: "migrate register sites to YAML entries", depends_on: ["3"] }
  ]
  // status defaults to "proposed" — plan-review envelope rendered
```

User clicks Approve → plan is `approved`. Agent (or sub-agent) claims steps in order, transitioning each to `in_progress` → `done`.

## Anti-patterns

- **Don't duplicate `TodoWrite` items into `nanite_todo_*`.** Pick one layer per item.
- **Don't create `workspace`-scoped todos as a reflex.** Default to `session` or `project`. Workspace scope is for a handful of truly global items.
- **Don't skip the plan-review envelope for multi-step work.** If you're creating a plan you intend the user to see, let the UI render the approval step — don't create as `approved` directly.
- **Don't mark plan-level `complete` before all steps are `done` or `skipped`.** Transition steps first, then plan.
- **Don't use plans for single-step work.** A plan with one step is a todo wearing a suit.
