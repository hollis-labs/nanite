# Agent self-tool — schedule a follow-up

**Phase:** 2 — Observability & producers (`TASKS/scheduling`)
**Status:** not-started
**Depends on:** `02-store-adapter.md` (needs a valid, dispatchable `InsertAgentSchedule` call, same reasoning as `07`). **Cross-batch dependency, real, not just a reference read**: `docs/engineering/architecture/12-scheduling.md`'s "Producers" section places this new self-tool in `internal/selftools`, per `11-harness-reactive-self-tools.md`'s resolved package placement — but `internal/selftools` doesn't exist until `TASKS/harness-reactive-self-tools/01-move-self-tools-to-internal-selftools.md` lands, and that task is `not-started` as of this task's authoring (planning-only, same as this whole batch). See step 1 for how to handle dispatch order.
**Touches:** either `internal/selftools/` (if `harness-reactive-self-tools/01` has landed by the time this task is dispatched) or `internal/mcp/self_tools_*.go` (if it hasn't — see step 1), following whichever location's existing self-tool registration pattern is current at dispatch time.

## Context

`docs/engineering/architecture/12-scheduling.md`'s "Producers" section, item 2: a new `nanite_*`-namespaced self-tool (the namespace already reserved for first-party tools per `docs/tool-naming-convention.md:95-103`) letting an agent schedule its own follow-up work. This is a plain self-tool, not a **harness-reactive** self-tool (`11-harness-reactive-self-tools.md`'s own mechanism) — it doesn't need `internal/selftools/reactions`' DB-backed reaction config; it's a normal tool with a handler that calls `InsertAgentSchedule` directly, the same class of thing `07`'s reflex hook does, just agent-initiated instead of reflex-initiated.

**Naming**: per `docs/tool-naming-convention.md`'s `<concept>_<verb>` convention (not the bare `nanite_*` prefix, reserved for the namespace defense mechanism, not a literal naming requirement — see that doc's own `nanite_code_execute` as the one real exception, and `TASKS/harness-reactive-self-tools/07-worked-example-task-update-report.md`'s identical naming reasoning for `task_update_report`). A candidate: `schedule_create` — check this doesn't collide with anything `09-operator-http-api.md`'s own naming introduces, and check `docs/engineering/GLOSSARY.md` before locking it, per this repo's standing instruction.

## What to do

1. **Resolve the cross-batch dependency before writing code**: check whether `TASKS/harness-reactive-self-tools/01-move-self-tools-to-internal-selftools.md` has landed. If yes, add this tool in `internal/selftools` following its current registration pattern. If no, add it in `internal/mcp` following the current (pre-move) `SelfToolsTransport` pattern — **do not block this task on the other batch landing first**; the design doc's own placement preference is a should, not a hard gate, and `internal/mcp`'s self-tool registration is a real, working location today regardless of whether the move has happened. If added to `internal/mcp`, leave a clear comment noting it should move alongside the rest of `SelfToolsTransport` when/if `harness-reactive-self-tools/01` lands, so it isn't stranded as the one self-tool left behind.
2. Define the tool: name (per Context), description (When-to-use / When-NOT-to-use / Required-context / Output-shape, matching the house style of existing self-tool descriptions), input schema — at minimum a schedule kind (`cron`/`one_shot`), the cron expression or one-shot target time, the job payload (what should happen when it fires — likely constrained to `durable_agent_wake`-shaped self-follow-up for v1, since letting an agent arbitrarily schedule `command_run`/`agent_workflow_run` jobs on itself is a broader capability question this task shouldn't silently expand into; document if you scope it narrower than the full four-job-type taxonomy and why).
3. Handler: validate input, compute `next_run` (same helper `02`/`07` use), call `InsertAgentSchedule` scoped to the calling agent's own `agent_id` — an agent should not be able to schedule a job on another agent's behalf via this tool; enforce that at the handler level, don't rely on the caller not attempting it.
4. Apply sane defaults for `max_retries`/`on_fail` the same way `07` does, unless there's a reason a self-scheduled follow-up should default differently (e.g. a stricter `max_retries` for agent-initiated schedules than reflex/operator-initiated ones — your call, document it).

## Done means

- A regression test proves calling the tool with a well-formed input produces a real `agent_schedules` row scoped to the calling agent, with a correctly-computed `next_run`.
- A regression test proves the tool cannot be used to schedule a job against a different agent's `agent_id`.
- A live dogfeed: call the tool through the real MCP surface against a scratch instance, confirm the resulting row fires through the engine.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Your package-location resolution (step 1), your tool name, and your job-type-scope call (step 2) are documented in this file's Work Log.

## Work log

Not started.

## Review notes

<!-- Reviewer fills in. -->
