# Agent self-tool — schedule a follow-up

**Phase:** 2 — Observability & producers (`TASKS/scheduling`)
**Status:** implemented
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

Implemented by a worker session, in an isolated worktree
(`agent-a0e33d79cd4a78b0a`). Ground truth confirmed before writing any
code, per the dispatch prompt's own instruction not to just trust the
"already exists" claim.

**Step 1 — package-location resolution.** Confirmed directly (`ls
internal/selftools/`): the package already exists on `main`, populated with
~40 `self_tools_*.go` files including `self_tools_task_update_report.go`
(the `harness-reactive-self-tools/07` worked example) already registered
via `selfToolDefinitions()` / `CallTool`'s switch. **No `internal/mcp`
fallback needed** — this task's own step-1 fallback branch (for the case
the sibling batch hadn't landed) does not apply; built directly in
`internal/selftools`, following `task_update_report`'s exact registration
pattern (definition function in `self_tools.go`'s `selfToolDefinitions()`
list, `CallTool` switch case, golden example JSON under
`internal/selftools/examples/`).

**Step 2 — naming and job-type scope.**

- **Tool name: `schedule_create`**, the task's own candidate, confirmed
  against `docs/engineering/GLOSSARY.md` before locking it — no existing
  `schedule_*` tool-name term is defined there to collide with (the
  Glossary's only "schedule" mentions are prose inside the **Reflexes**
  entry, not a locked tool name).
- **Job-type scope: narrowed to `durable_agent_wake` only, exactly as the
  task's own Context suggested** — `job_type` is not an input field on this
  tool's schema at all (not merely defaulted-but-overridable). Confirmed by
  reading `internal/scheduler/store_adapter.go`'s `buildPayload` before
  designing the handler: for `job_type=durable_agent_wake`,
  `job_payload` is never read at all — the Store adapter resolves
  `instance_id` itself from the row's `agent_id` (via
  `resolveDurableAgentInstanceID`) and forwards `body` as the wake
  `Prompt`. This means the handler needs no `job_payload`/`instance_id`
  input surface whatsoever for this job type to work correctly end to end
  — confirmed live in the dogfeed (below). Letting an agent choose
  `agent_workflow_run`/`command_run`/`reflex_dispatch` as its own job_type
  would open a broader, unreviewed capability-surface question
  (`12-scheduling.md`'s "What this session did not decide" already flags
  `command_run` sandboxing as open) that this task deliberately does not
  expand into.
- **`max_retries`/`on_fail` are also not input fields** — hardcoded per-call
  by the handler (`scheduleCreateMaxRetries = 3`, `scheduleCreateOnFail =
  "disable"`), not exposed as agent-settable knobs in v1, keeping the input
  schema to exactly `kind` / `cron_expr` / `message` / `name`.
- **`max_retries`/`on_fail` default divergence, documented as asked:**
  `MaxRetries` stays at 3, matching `01`'s own column default — no reason a
  self-authored follow-up needs a smaller retry budget than an operator- or
  YAML-authored one. `OnFail` diverges to `"disable"` instead of `01`'s
  `"retry"` column default. Read `internal/scheduler/retrying_runner.go`'s
  `applyOnFail` before deciding: `on_fail="retry"` is not itself a
  meaningful terminal action once `max_retries` is exhausted — it falls
  through to the exact same non-disabling "log and keep going" behavior as
  `"notify"` (that file's own doc comment: "'retry' is not itself a
  meaningful terminal action once max_retries is exhausted... resolves to
  behave identically to 'notify' at exhaustion: log/emit, do not
  disable"). That's a reasonable default for a human-reviewed,
  operator-/YAML-authored schedule, but an agent-authored follow-up with no
  human review in the loop should not be allowed to keep silently failing
  forever on a row that stays `status=active`. `on_fail="disable"` makes an
  exhausted self-schedule visibly terminal (`status` flips to `expired`,
  discoverable via `ListAgentSchedules`) instead of indefinitely
  retryable-but-silently-broken.
- **Cron validation is a deliberate, documented addition beyond the task's
  literal ask**: the handler pre-validates `cron_expr` via
  `cron.ParseStandard` (a validity check only, immediately rejecting a
  malformed input with a clear tool error) before ever calling
  `store.ComputeAgentScheduleNextRun` — because that helper's own
  documented fallback for a malformed spec is "due now" (a silent,
  surprising immediate fire), which is the right defensive behavior for
  its existing internal callers (a migration backfill, a boot-time YAML
  sync) but the wrong UX for a front-door LLM-tool input. This does **not**
  duplicate `ComputeAgentScheduleNextRun`'s own next-occurrence math (the
  actual finding flagged on task `05`, `TASKS/ESCALATIONS.md`'s 2026-08-20
  entry) — it only calls the exact same `cron.ParseStandard` function once,
  for parseability, and delegates the real next-run computation entirely to
  `store.ComputeAgentScheduleNextRun`, never reimplementing `.Next(now)`
  itself.
- **`one_shot` semantics matched to what the table actually supports, not
  invented**: confirmed via `backfillScheduleNextRun`'s own doc comment
  (`internal/store/agent_schedules.go`) that a `one_shot` row has no
  independent target-time encoding in `schedule_spec` today — it fires on
  the engine's very next tick. The tool's schema and description reflect
  this exactly (`cron_expr` is rejected outright if supplied alongside
  `kind="one_shot"`, with a clear error explaining there is no
  delayed-target-time field), rather than silently accepting and ignoring a
  future-time input the underlying schema can't actually honor.

**Step 3 — handler and agent-scoping enforcement.** `callScheduleCreate`
(`internal/selftools/self_tools_schedule_create.go`) validates `kind`,
`message` (required), `cron_expr` (required+validated for `kind="cron"`,
rejected for `kind="one_shot"`), computes `next_run` via
`store.ComputeAgentScheduleNextRun`, and calls `InsertAgentSchedule`.

Agent-scoping is enforced with **no `agent_id` input field on the schema at
all** — there is nothing for a caller to even attempt to override. The
calling agent's own id is resolved from `ctx` by
`resolveSelfScheduleAgentID`, which is a real finding beyond mechanically
mirroring `self_tools_whoami.go`'s existing "resolve the caller" precedent:
`whoami`'s own doc comment names its identity source as
`chatServiceImpl.executeToolBatch`'s `mcp.WithCallerProfile` stamp — but
that stamp is set **only** by the in-process chat-loop tool executor.
Confirmed by reading `internal/api/tools_call.go`'s `handleSelfToolCall`
and `internal/mcpserver/self_proxy.go` directly: the real path a
CLI-launched durable agent's `nanite mcp` subprocess uses to reach a
self-tool (`POST /api/tools/call`) stamps only `mcp.WithSessionID` — its
request body has no agent-identity field at all. A durable agent asking to
schedule its own follow-up wake is exactly the primary expected caller of
this tool, so relying solely on `whoami`'s mechanism would have silently
excluded the main use case. `resolveSelfScheduleAgentID` therefore tries
two already-established identity-from-ctx mechanisms in order: (1)
`mcp.CallerProfileFromContext` (the in-process chat-loop's H1 stamp, same
as `whoami`), then (2) `mcp.SessionIDFromContext` →
`store.GetSessionPrimaryAgent` — an existing, already-widely-used accessor
(`internal/service/agent.go`'s `resolveBinding`,
`internal/chat/commands_builtin.go`, `internal/api/agents.go`,
`internal/api/frontend_readiness.go` all already read it the same way),
not a new resolution path invented for this task, and correct because every
durable-agent session already gets exactly this primary-agent binding at
session-creation time (`durable_agents.go`'s
`selectOrCreateLaunchSession` → `EnsureSessionAgent(sess.ID,
inst.ProfileID, "default", true)`). A caller with neither signal is
rejected outright with a clear error, never silently falling through to an
empty `agent_id`.

**Step 4 — defaults.** Covered under Step 2 above (`max_retries`/`on_fail`
divergence).

**Regression tests**
(`internal/selftools/self_tools_schedule_create_test.go`): well-formed cron
input via the H1 caller-profile path produces a real row scoped to that
agent with a correctly-computed (independently re-verified)
`next_run`, correct hardcoded defaults, and a `self:`-prefixed
`created_by`; a one_shot call computes "due now" and derives a default
`name` when omitted; the session-fallback path (no caller-profile stamp,
session id only — the real CLI-launch proxy shape) resolves the correct
agent via `GetSessionPrimaryAgent`; the required negative proof
(`TestCallScheduleCreate_CannotTargetAnotherAgent`) has agent B call the
tool while explicitly passing an unsolicited `agent_id` arg naming agent A
— asserts agent A gets zero rows and agent B (the real caller) gets
exactly one, scoped to itself; a caller with no identity signal at all in
`ctx` is rejected; and a table-driven validation suite covers invalid
`kind`, missing/invalid `cron_expr`, `cron_expr` supplied alongside
`one_shot`, and missing `message`, confirming none of them insert a row.

**Live dogfeed** (per Done means, against an isolated scratch DB/port, not
the deployed service): built the binary to an absolute scratch path
(never `./nanite` in the repo root), booted `serve -db <scratch>/scratch.db
-port 8099 -dev`. Created a real agent profile via `POST /api/agents`, a
real `durable_agent_instances` row bound to it via `POST
/api/durable-agents` (defaults to `status=sleeping`), then a single,
documented, minimal direct-SQL nudge (`UPDATE durable_agent_instances SET
status='paused'`) — deliberately, to keep the dogfeed side-effect-free: a
`sleeping` instance isn't covered by `wakeSkipReason`'s switch and would
have caused the fired wake to actually attempt `DurableAgentStartRequest`
(a real CLI-subprocess spawn needing real provider credentials), whereas
`paused` produces a clean, documented, graceful `wake_skipped` result. Then
created a session bound to that agent as primary via `POST /api/sessions`,
and called `schedule_create` through the **real MCP self-tool surface**
(`POST /api/tools/call` — the identical loopback proxy path a CLI-launched
agent's `nanite mcp` subprocess uses), passing only `session_id` (no
caller-profile stamp — exercising the session-fallback resolution path
specifically, since that's the path a real durable-agent caller actually
uses). Confirmed the full pipeline fired through the Phase 1 engine within
the same second (`go-scheduler`'s 1s tick), verified across three tables
directly: `agent_schedules` (row scoped to the correct `agent_id`,
`status` flipped `active`→`expired` after a successful one_shot dispatch,
`max_retries=3`/`on_fail=disable`/`job_type=durable_agent_wake` as
designed), `schedule_runs` (one row, `status=succeeded`), and
`durable_agent_events` (a real `wake_skipped` event tied to the correct
`instance_id`, message `"instance paused"` — direct proof
`RunnerAdapter.enqueueDurableAgentWake` → `DurableAgentWaker.Wake` was
genuinely invoked by the engine, not just that a row sat unclaimed).

Also live-verified the cross-agent-scoping enforcement (in addition to the
Go regression test): created a second agent + session, called
`schedule_create` through the same live `/api/tools/call` surface with an
explicit, unsolicited `"agent_id"` arg naming the first agent — the
resulting row's `agent_id` was the actual caller (second agent), not the
spoofed value, confirmed by direct SQL query.

**Cleanup note:** `POST /api/agents` / `POST /api/durable-agents` write
real managed-config files (`.nanite/agents/*.md`,
`.nanite/durable-agents/*.yaml`) resolved against the server process's CWD,
not the `-db` scratch path — the scratch server was started from within
this worktree's own directory, so these three fixture files briefly landed
as untracked files inside the worktree. Caught by the standing
post-dogfeed `git status --short` check; removed before finalizing. No
tracked file was touched. Flagged here as a real footgun for the next
scratch dogfeed that creates agent/durable-agent config via these two
endpoints specifically: start the process from the scratch directory (or
find/pass an explicit config-root override) rather than relying on the
`-db` flag alone to keep everything scratch-scoped.

**Baseline checks.** `go build ./cmd/nanite/`, `go build ./...`, `go vet
./...` (one pre-existing, unrelated `internal/service/container.go`
`stopReaper`/`stopRuntimeReaper` finding — confirmed untouched by this
task, matching the same finding already noted in `harness-reactive-self-
tools/07`'s own Work Log), and `go test ./...` (full suite, every package
`ok`, none touching `container.go` either) all pass.

## Review notes

**Pass (2026-08-20, fresh Reviewer, Phase 2 section review covering 06-09).** Confirmed no `agent_id` (or any cross-agent-targeting) field exists anywhere in the tool's `InputSchema`; `job_type` likewise never reachable as input. Traced both `resolveSelfScheduleAgentID` resolution paths against their real definitions — both are pure `context.Value` reads set exclusively by trusted call sites, never derived from the tool's `args` map, so a spoofed `agent_id` arg has structurally no path to influence the resolved identity (confirmed the shipped negative test exercises a real cross-agent attempt, not just a schema-absence assertion). `on_fail="disable"` divergence confirmed real and correctly reasoned.

**One minor, non-blocking nit**: `scheduleCreateMaxRetries = 3` is a second hardcoded literal duplicating `InsertAgentSchedule`'s own default (also 3) — `07`'s hook avoids this by leaving `MaxRetries` unset and letting the column default apply. If the store-level default ever changes, this tool's rows would silently stop tracking it. Low severity, not fixed, worth a note for a future consolidation pass alongside the `Engine` field rename / `ComputeAgentScheduleNextRun` dedup already logged in `ESCALATIONS.md`.
