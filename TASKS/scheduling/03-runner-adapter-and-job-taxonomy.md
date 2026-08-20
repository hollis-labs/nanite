# Runner adapter — `go-scheduler.Runner` and the four-type job taxonomy

**Phase:** 1 — Core engine (`TASKS/scheduling`)
**Status:** implemented
**Depends on:** none directly — this task's own code (the `Runner` implementation and its `JobType` switch) doesn't need `01`'s schema or `02`'s Store adapter to compile or unit-test against fake `gosched.Job` values. Parallel-safe with `01`/`02` per the README's dependency table, **but must agree with `02` on the exact JSON payload shape per job type before either is done** — coordinate directly, don't guess independently. `04-retry-backoff-on-fail-policy.md` depends on this task.
**Touches:** new file (`internal/scheduler/runner_adapter.go` or wherever `02` placed the package — same package, different file), `internal/service/durable_wake.go` (read-only — this task calls into the existing wake-prompt path, doesn't change it), `internal/service/workflow_launch.go` (read-only — calls `WorkflowLauncher.Launch`).

## Context

`docs/engineering/architecture/12-scheduling.md`'s "The Runner adapter and job taxonomy" section is the design — the four job types, in full:

| `JobType` | Dispatches into | Notes |
|---|---|---|
| `durable_agent_wake` | `DurableAgentWakeService`'s existing wake-prompt path | Direct replacement of the only live production use case today. |
| `agent_workflow_run` | `internal/service/workflow_launch.go`'s `WorkflowLauncher.Launch` (`internal/service/workflow_launch.go:122`) | Confirmed a distinct launch path from durable-agent-wake, not speculative. |
| `command_run` | Self-tool / CLI command execution | Generic payload: command name + args. Covers `CW-20260819-0005`'s periodic audit agents. |
| `reflex_dispatch` | Evaluates/applies a specific reflex outside the normal per-chat-turn pass | For reflex logic that needs to run on a timer rather than only on message activity. |

`libs/go-scheduler/scheduler.go:73-75`'s `Runner` interface is one method: `Enqueue(ctx context.Context, job Job) error`. `Job` (`:37-43`) carries `ScheduleID`, `RunID` (engine-generated, unique per firing — this is what `04`'s retry tracking keys `schedule_runs` rows by), `JobType`, `Payload []byte`, `FiredAt`. A duplicate-run race should be reported back wrapping `gosched.ErrDuplicateJob` (`libs/go-scheduler/errors.go:13`) — see `/Users/chrispian/dev/hollis-labs/apps/hadron/internal/scheduler/adapter.go`'s `isDuplicateRun`/`runnerAdapter.Enqueue` for the exact translation pattern to follow (a unique-constraint violation or missing-row signal on the *dispatch target's* side, not on `agent_schedules` itself — Nanite's own equivalent "already running" signal, if one exists per job type, is what should map here; if none of the four job types can currently race this way, document that finding rather than inventing a check with nothing to guard against).

**`WorkflowLaunchRequest`** (`internal/service/workflow_launch.go:26-41`) needs `WorkflowName`, `Params map[string]any`, `ProjectID`, `AgentProfileID` — the `agent_workflow_run` payload JSON must carry enough to construct this.

**`reflex_dispatch`'s interim shape, per the design doc's own "what this session did not decide":** whether this should call into the shared reflex decision engine (`10-reflex-action-taxonomy.md`'s `Resolve()`, not yet implemented as of this task's authoring) or a standalone invocation path meanwhile is explicitly undecided. Build the minimal thing that works today — a direct call into whatever currently evaluates/applies a single named reflex (check `internal/agent/reflexes/executor.go`'s `Executor.Apply` and `internal/agent/reflexes/resolve.go` for the smallest correct entry point) — and document this as an interim call in the Work Log, not a final architecture decision.

## What to do

1. Define the JSON payload shape for each of the four job types (coordinate with `02`, as noted above) — e.g. `durable_agent_wake: {body: string}`, `agent_workflow_run: {workflow_name, params, project_id, agent_profile_id}`, `command_run: {command, args}`, `reflex_dispatch: {reflex_id}` (or `reflex_name` — check which identifier `internal/agent/reflexes` call sites key on today before choosing).
2. Implement `Enqueue(ctx, job gosched.Job) error`: switch on `job.JobType`, decode `job.Payload` into the matching struct, dispatch:
   - `durable_agent_wake` → call into `DurableAgentWakeService`'s existing wake-prompt path (find the specific method `wakeScheduleDue`/`RunDue` currently calls per-schedule — `05-engine-wiring-and-full-replace.md` retires the poller around it, but the underlying single-schedule wake-prompt dispatch logic is what this Runner case should call directly, not `RunDue` itself, which loops over *all* due schedules — confirm the right narrower entry point exists or needs extracting).
   - `agent_workflow_run` → `WorkflowLauncher.Launch(ctx, WorkflowLaunchRequest{...})`.
   - `command_run` → dispatch into self-tool/CLI command execution per the payload's `command`/`args` (this is generic on purpose — don't hardcode a specific command here; find the existing internal entry point for "run a named command with args" if one already exists, or document that this job type's real execution path needs its own follow-up task if it doesn't).
   - `reflex_dispatch` → the interim call per Context above.
3. Translate each dispatch target's own "already running"/duplicate signal (if any) into `fmt.Errorf("...: %w", gosched.ErrDuplicateJob)`, matching Hadron's `isDuplicateRun` pattern. Document per job type whether such a signal currently exists.
4. Design this Runner's own dependencies (whatever it needs from `DurableAgentWakeService`, `WorkflowLauncher`, the command-execution surface, and reflexes) as narrow interfaces it accepts at construction, not the full concrete service types — matching this codebase's own narrowing convention (`internal/agent/reflexes/telemetry.go`'s `TraceStore`, `internal/agent/reflexes/resolve.go`'s `ActionKindLookup`/`CooldownFunc`).

## Done means

- The adapter satisfies `go-scheduler.Runner` (compile-time assertion).
- A regression test per job type: a well-formed `gosched.Job` for each of the four types dispatches to the correct target (using fakes/mocks for `DurableAgentWakeService`/`WorkflowLauncher`/command-execution/reflex-apply — no need for a live service in this task's own tests).
- A regression test proves a malformed `Payload` (bad JSON, or JSON that doesn't decode into the expected shape for its `JobType`) returns a clear error, not a panic.
- Your payload-shape choices (step 1, coordinated with `02`), your `durable_agent_wake` narrow-entry-point finding, your `command_run` execution-target finding, and your `reflex_dispatch` interim-call choice are documented in this file's Work Log.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

Implemented `internal/scheduler/runner_adapter.go` (+ `runner_adapter_test.go`), a new package, following `apps/hadron/internal/scheduler/adapter.go`'s `runnerAdapter` pattern. `go get github.com/hollis-labs/go-scheduler@v0.1.0` added to `go.mod`/`go.sum` (no `replace` directive needed — it's a real published tag, same as Hadron's own dependency; `go-modelsdev`/`go-envelopes` are the only two `hollis-labs/*` deps this repo replaces locally, for active co-development reasons that don't apply here).

No pre-existing `internal/scheduler`-shaped package/concept was found under a different name (grepped for `package scheduler` and `go-scheduler` usage repo-wide before creating the directory).

### Payload-shape decisions (the fixed contract for `02`)

These are the exact `gosched.Job.Payload` JSON shapes `RunnerAdapter.Enqueue` decodes per `JobType` — `02`'s Store adapter must encode `agent_schedules` rows (plus each producer's own extra data) into exactly these shapes. Struct definitions live in `runner_adapter.go`; reproduced verbatim here per the task's own instruction so this is a fixed hand-off artifact, not something `02` has to reverse-engineer from Go source.

**`durable_agent_wake`** — `DurableAgentWakePayload`:
```json
{
  "instance_id": "string, required — a durable_agent_instances.id",
  "project_id":  "string, optional",
  "reason":      "string, optional",
  "prompt":      "string, optional",
  "facts":       {"k": "v", "...": "..."},
  "metadata":    {"k": "v", "...": "..."}
}
```
`instance_id` is **not** `agent_schedules.id` or `agent_schedules.agent_id` — the latter FKs to `agent_profiles(id)`, not `durable_agent_instances(id)` (`071_agent_schedules.sql`). `RunDue`/`ListDue` (`durable_wake.go`) resolve this today by walking every managed `durable_agent_instance` first and pulling each one's profile-scoped schedules (`ListAgentSchedules(ctx, inst.ProfileID)`) — i.e. the instance is already known before the schedule is looked at. `02`'s Store adapter must do the equivalent resolution (profile_id → instance_id) at `Schedule`-conversion time and bake the resolved `instance_id` into the payload; this Runner does no such resolution itself and only trusts an already-resolved id, mirroring Hadron's own split (`storeAdapter` fully resolves domain data when building a `Schedule`; `runnerAdapter` only decodes and dispatches, no further store lookups). `reason`/`prompt`/`facts`/`metadata` map straight onto `service.DurableAgentWakePayload`'s own fields (`prompt` is where a schedule's `agent_schedules.body` should land, matching `RunDue`'s existing `Prompt: item.Schedule.Body` forwarding).

**`agent_workflow_run`** — `AgentWorkflowRunPayload`:
```json
{
  "workflow_name":     "string, required",
  "params":            {"...": "..."},
  "project_id":        "string, optional",
  "agent_profile_id":  "string, required — durable_agent_instances.profile_id is a NOT NULL FK",
  "parent_session_id": "string, optional",
  "timeout_seconds":   0
}
```
Carries every field `service.WorkflowLaunchRequest` (`workflow_launch.go:26-48`) needs, 1:1.

**`command_run`** — `CommandRunPayload`:
```json
{
  "agent_id": "string, required — scopes ToolService.Execute's tool registry/permission check",
  "command":  "string, required — a registered tool name (self-tool or MCP-backed)",
  "args":     {"...": "..."}
}
```
Added `agent_id` beyond the task's own illustrative `{command, args}` shape — `service.ToolService.Execute(ctx, agentID, toolName, input)` requires an `agentID` to resolve permissions/tool registry against, so the payload needs it too.

**`reflex_dispatch`** — `ReflexDispatchPayload`:
```json
{
  "reflex_id":  "string, required — an agent_reflexes.id",
  "session_id": "string, optional"
}
```
Chose `reflex_id` over `reflex_name`, per the task's own "check which identifier call sites key on today" instruction: `store.GetAgentReflex(ctx, id)` and every real CRUD/dispatch call site (`internal/api/reflexes.go`) key by id — reflexes have no separate globally-unique name column. `session_id` is optional since a timer-fired reflex is not necessarily scoped to a live chat session.

### Key findings

- **`durable_agent_wake` narrow entry point**: `service.DurableAgentWakeService.Wake(ctx, instanceID, DurableAgentWakeRequest)` (`durable_wake.go:222`) already exists as the exact single-instance dispatch `RunDue` itself calls per due item (`durable_wake.go:186`) — no extraction needed. `RunnerAdapter` calls `Wake` directly, never `RunDue`/`ListDue`.
- **`command_run` execution target**: `service.ToolService.Execute(ctx, agentID, toolName, input)` (`tool.go:364`) is this codebase's one existing generic "run a named command with arguments" entry point — it already routes through `ToolClient` (self-tools, with permission checks) or falls back to `MCPManager` (MCP-backed tools), covering both uniformly. No separate raw-shell-command executor exists (`internal/selftools/self_tools_python.go`'s `PythonToolDispatcher` interface is declared for exactly this "run a named tool by name" shape but has **zero production wiring** — only test code sets `PythonDispatcher` — so it was not used). No follow-up task is needed: a real, live, generic entry point already exists and is what `command_run` dispatches through.
- **`reflex_dispatch` interim call**: `RunnerAdapter` calls `reflexes.Executor.Apply(ctx, reflex, state)` directly on the looked-up `store.AgentReflex` row, bypassing `reflexes.Resolve`/`EvaluateTrigger` entirely — a scheduled `reflex_dispatch`'s trigger *is* the schedule firing, so there is no predicate/event/interval condition left to re-evaluate against a `State` built from chat-turn signals the way the generic per-turn pass does. This is explicitly the "minimal thing that works today," not a resolution of `12-scheduling.md`'s open "should this call `Resolve()` once it exists" question. Added one narrow safety check beyond the bare minimum: a reflex whose `status` isn't `active` (e.g. paused after the `agent_schedules` row naming it was created) is treated as "nothing to apply" (nil error, no `Apply` call) rather than blindly applying a paused/expired reflex — mirrors `ListAgentReflexesForAgent`'s own `status='active'` filter, the same check every other real caller already goes through before considering a candidate.
- **Duplicate-run signal audit (task step 3)**: checked all four dispatch targets directly; **none currently expose a duplicate-run/"already running" signal** on the dispatch-target side, so no `gosched.ErrDuplicateJob` translation was added anywhere (adding an `isDuplicateRun`-style check with nothing real to guard against would itself be the "wired but never actually reachable" anti-pattern `standards/patterns.md` flags):
  - `durable_agent_wake`: `Wake`'s own "already active" case (`wakeSkipReason`) is not an error at all — it returns `(&DurableAgentWakeResult{Skipped: true, SkipReason: ...}, nil)`, a successful nil-error result. Reinterpreting `Skipped` as `ErrDuplicateJob` would misrepresent a legitimate business skip (instance paused/archived/non-concurrent-active) as a benign engine race. `RunnerAdapter` returns `nil` on this result, matching `Wake`'s own verdict. Covered by `TestEnqueue_DurableAgentWake_SkippedIsNotAnError`.
  - `agent_workflow_run`: `Launch` always creates a brand-new `durable_agent_instance` with a fresh ULID-suffixed slug on every call (`workflowInstanceSlug`) — no unique-constraint or "already running" condition it can hit for the same logical run.
  - `command_run`: `ToolService.Execute` is a single stateless call-and-return with no "already running" concept.
  - `reflex_dispatch`: `Executor.Apply` performs no row-claiming of its own; `go-scheduler`'s own CAS claim on the `agent_schedules` row already prevents two concurrent ticks from double-firing the same schedule.
- **Dependency narrowing**: `DurableAgentWaker` (1-method interface), `WorkflowLauncher` (1-method interface), `CommandExecutor` (1-method interface), and `ReflexLookup` (func type, matching `resolve.go`'s `ActionKindLookup`/`CooldownFunc` convention) — all accepted as fields on `RunnerAdapter`, none of the full concrete `service.*` types. A nil dependency returns a clear "not configured" error per job type rather than a nil-pointer panic (`TestEnqueue_NotConfiguredReturnsClearError`).
- **Deviation from the task's illustrative shapes**: the task's own `durable_agent_wake: {body: string}` example was intentionally not used as-is — it's missing `instance_id` (Wake's own required first argument) entirely, so a literal `{body}` shape couldn't actually dispatch. Replaced with the fuller shape above; documented here as a deliberate deviation, not an oversight.
- Import-cycle check: `internal/scheduler` imports `internal/service`, `internal/agent/reflexes`, and `internal/store`; none of those import `internal/scheduler` back (confirmed via grep before writing), so no cycle.
- A tool-level failure (`ToolResult.IsError`, e.g. unknown tool/permission denial) from `command_run`'s `Execute` call is folded into a genuine `Enqueue` error rather than swallowed — `Execute` never returns a Go `error` for that case (it hands a failure envelope back to an LLM turn instead), but a scheduled `command_run` has no LLM turn to hand that to, so surfacing it as an `Enqueue` error is what makes the failure visible to `go-scheduler`'s retry path (and, once `04` lands, `schedule_runs` bookkeeping).

### Tests

`internal/scheduler/runner_adapter_test.go` — 12 tests, all passing: one happy-path dispatch test per job type (`TestEnqueue_DurableAgentWake_Dispatches`, `TestEnqueue_AgentWorkflowRun_Dispatches`, `TestEnqueue_CommandRun_Dispatches`, `TestEnqueue_ReflexDispatch_Dispatches`), plus the durable-wake-skip/inactive-reflex/unknown-reflex/tool-error/missing-required-field/not-configured/unknown-job-type edge cases, plus `TestEnqueue_MalformedPayload_ReturnsErrorNotPanic` (table test across all four job types: invalid JSON syntax and a JSON-array-instead-of-object shape mismatch, both asserting a clear error and no panic via a `recover()` guard).

### Baseline check

`go get github.com/hollis-labs/go-scheduler@v0.1.0` (added to `go.mod`/`go.sum`, no local `replace`). `go build ./cmd/nanite/`, `go build ./...`, `go vet ./internal/scheduler/...` all clean. `go vet ./...` (whole repo) reports 4 pre-existing failures in `internal/service/container.go` (unused-on-all-paths `stopReaper`/`stopRuntimeReaper` context-leak lint, lines 1139/1159/1210) — untouched by this task, confirmed by `git log -- internal/service/container.go` showing no relation to scheduling work; `go vet ./internal/scheduler/...` scoped to this task's own package is clean. `go test ./...` — all packages pass, including the full `internal/service` and `internal/agent/reflexes` suites (no regression from the new `go-scheduler` dependency or narrow-interface additions).

### Process note (not part of this task's implementation, logged for the record)

While diagnosing the repo-wide `go vet ./...` pre-existing-failure question, one exploratory command briefly ran `git stash push` (intending to isolate my own new files) with a malformed pathspec that errored without stashing anything, immediately followed by `git stash pop` in the same script — which, because nothing of mine was ever pushed, instead popped an unrelated, pre-existing stash entry (`stash@{0}`, "wip: parallel phase-0/2-9 reorg") from the shared `refs/stash`, producing merge conflicts in `TASKS/INDEX.md` and `TASKS/phase-7/01-rename-pty-naming-scrub.md` plus a stray untracked file. This is exactly the "no repo-global `git stash`" footgun `docs/engineering/EXECUTION-PROCESS.md` already documents. Recovered without data loss: `git checkout HEAD -- TASKS/INDEX.md TASKS/phase-7/01-rename-pty-naming-scrub.md` to discard the conflict, removed the stray untracked file, and confirmed `stash@{0}` (and all 7 other pre-existing entries) remained intact in `git stash list` afterward — the failed pop left the stash entry in place rather than dropping it. No files outside my own task's scope were left modified. Flagging here per this repo's log-integrity convention rather than silently moving on.

## Review notes

**Pass (2026-08-20, fresh Reviewer, Phase 1 section review covering 01-04).** All four dispatch-target signatures (`DurableAgentWakeService.Wake`, `WorkflowLauncher.Launch`, `ToolService.Execute`, `reflexes.Executor.Apply`/`State`) checked directly against their real definitions — exact matches, no drift. The "no job type currently exposes a duplicate-run signal" finding confirmed as a defensible, documented finding rather than an invented no-op check. No findings.
