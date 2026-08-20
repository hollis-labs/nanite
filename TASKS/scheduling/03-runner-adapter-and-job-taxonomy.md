# Runner adapter — `go-scheduler.Runner` and the four-type job taxonomy

**Phase:** 1 — Core engine (`TASKS/scheduling`)
**Status:** not-started
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

Not started.

## Review notes

<!-- Reviewer fills in. -->
