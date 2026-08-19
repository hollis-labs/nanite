# Prompt Router Catalog — RETIRED

**Status:** retired. `internal/promptrouter` (the package this doc described) no longer exists — deleted in full by `TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md`. This page is kept as a short pointer for anyone who lands here from an old link, commit message, or CW ticket; it is not maintained content.

## What happened

`internal/promptrouter` was a deterministic, in-memory phrase-match dispatch router (`BuiltinReflexes()`, a Go-literal list of 8 entries) that fed `dispatch.AssignRole` via `ReflexHints`, plus a `~/.nanite/reflexes/*.yaml` user-override loader. Per architecture doc `docs/engineering/architecture/03-steering.md` ("Reflexes are the single steering primitive"), its phrase-matching job was migrated onto `internal/agent/reflexes`'s DB-backed `agent_reflexes` table as a new `dispatch_to_agent` action kind, rather than staying a separate system.

- **6 of the 8 catalog entries** (`background-long-task`, `planner-mention`, `planner-large-task`, `researcher-mention`, `reviewer-mention`, `worker-execute`) migrated 1:1 into `dispatch_to_agent` reflex rows — see `internal/agent/reflexes/seeds.go`'s "migrated from internal/promptrouter" section for the exact trigger/action shape and per-entry design rationale (tier-hint translation, priority ordering, phrase-regex construction).
- **2 catalog entries** (`documentor-mention`, `strategist-mention`) were dropped, not migrated — both were already phantom (empty `resolves_to.profile`, no matching agent profile exists) in the retired catalog. See the migration task's Work Log for the full accounting.
- **The `~/.nanite/reflexes/*.yaml` user-override convention** is retired. Reflexes are DB-authoritative now: operators use the same `agent_reflexes` CRUD path (`internal/api/reflexes.go`, `GET/POST/PATCH/DELETE /api/agents/{id}/reflexes`) every other reflex uses, not a parallel YAML-file override system.
- **`playbook_match_log`** (the raw-vs-sent audit table, `internal/store/reflex_log.go`) is still live — only its Go-side write-shape moved from `promptrouter.ReflexMatchEntry` to `store.ReflexMatchLogEntry`.

## Where the content lives now

- Trigger/action definitions: `internal/agent/reflexes/seeds.go`
- Evaluator (regex/predicate engine): `internal/agent/reflexes/evaluator.go`
- Upstream (pre-dispatch) evaluation call site: `internal/service/chat_reflex_dispatch.go`
- Downstream (inside `task_execute`) evaluation call site: `internal/mcp/self_tools_dispatch.go`'s `matchDispatchToAgentReflex`
- Reflex authoring generally: `docs/engineering/architecture/03-steering.md`, `internal/api/reflexes.go`

See `docs/agent-pattern-catalog.md` for the still-current agent pattern/role catalog (Chat/Strategist/Planner/Researcher/Documentor/Worker/Reviewer) — that content is independent of promptrouter's retirement.
