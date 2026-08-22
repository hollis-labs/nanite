# Route `Approve()` through the spawn concurrency cap; make a queued-run `Cancel()` actually stop the run

**Phase:** Wave 2 — Correctness, lifecycle, concurrency (per remediation guide §4)
**Status:** not-started
**Depends on:** none within this batch (single-task folder; no sibling task in
`05-subagent-execution-ordering/` to sequence against).
**Touches:** `internal/subagent/service.go` (`Spawn`, `Approve`, `Cancel`,
`acquireSpawnSlot`, `executeWithSlot`, `execute`, `finalizeRun`, `emitStatus`),
plus new/extended regression tests in `internal/subagent/service_fanout_test.go`
and/or `internal/subagent/service_test.go`.

```yaml
requires_architect_decision: false
requires_regression_test: true
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 2 — correctness, lifecycle, concurrency · **Dispatch unit:** `W2a`
> - **Depends on:** `00/01`
> - **Blocks:** none
> - **Parallel-safe with:** all of W2a — `internal/subagent` is disjoint from every other Wave 2 task
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

### Findings addressed

- **GO-EXEC-001** (medium severity, high confidence, category: concurrency) —
  `Approve()` bypasses the fan-out concurrency semaphore entirely. `Spawn`'s
  ungated paths correctly acquire a slot from the documented cap-of-3
  semaphore (`svc.spawnSem`, sized by `spawnFanoutCap = 3` at
  `internal/subagent/service.go:179`) before dispatching a runner, but
  `Approve` launches its dispatch goroutine directly with no slot
  acquisition at all. Multiple approvals in quick succession (human or
  automated) can drive concurrently-executing subagent runners past the
  stated cap, defeating the resource-exhaustion protection the semaphore
  exists to provide.
- **GO-EXEC-002** (medium severity, medium confidence, category:
  concurrency/lifecycle) — a run cancelled while still queued for a spawn
  slot is not actually stopped, only relabeled after it finishes running
  anyway. The run row is marked `running` and broadcast via `emitStatus`
  *before* the slot semaphore is even acquired; the cancellation hook
  (`svc.cancelers[run.ID]`) is only registered *after* the slot is
  acquired. An operator calling `Cancel(runID)` on a run the UI already
  shows as "running" — but which is in fact still queued behind the cap —
  flips the DB row to `cancelled`, but the runner proceeds to fully execute
  once capacity frees up, silently wasting real compute/tool budget.
  `finalizeRun`'s re-read reconciliation logic eventually corrects the
  terminal status, so **data integrity is not at risk** — but the run
  itself is not interrupted, which is the actual bug.

Both findings are `report_section: §8.10` in
`docs/audits/2026-08-21-go-quality/findings.json` and are discussed together
in `docs/audits/2026-08-21-go-quality/REPORT.md` §8.10 (the `internal/subagent`,
`internal/worker`, `internal/workflow`, `internal/workflowrunner`,
`internal/dispatch`, `internal/dispatcher`, `internal/executor/envelope_render`
cluster review). The remediation guide's own Wave 2 grouping
(`nanite-audit-triage-remediation-planning-guide.md`, "Subagent execution
ordering" under "Wave 2 — Correctness, lifecycle, concurrency") groups these
two findings together explicitly and instructs: "Verify approval obeys the
same concurrency limit as spawn and operator cancellation can stop a run
while it is still queued. Add regression tests for both." — this task exists
to satisfy that instruction directly.

**Note on `requires_architect_decision`:** `findings.json` marks both
GO-EXEC-001 and GO-EXEC-002 as `requires_architect_decision: true`. This task
file deliberately sets the frontmatter flag above to `false` — a judgment
call made during this task-authoring pass, not a correction of the audit.
The audit's own recommendation text for GO-EXEC-001 already states the
direction plainly ("Route Approve's dispatch through `executeWithSlot`
[...], or explicitly document the exemption") and for GO-EXEC-002 equally
plainly ("Register a lightweight 'queued' cancellation hook before
`acquireSpawnSlot` is attempted, or re-check the row's current status
immediately after acquiring a slot"). Both are implementation choices
between two well-specified, non-exotic options, not open design questions
about a library, auth model, or user-facing tradeoff — the kind of thing
Wave 1/3/4/5/6's `requires_architect_decision: true` items actually are. If
a future planner disagrees, escalate before dispatch; nothing here commits
that planner to this call.

### Root cause

Both bugs trace to the same underlying gap: **`Spawn` and `Approve` are two
independent entry points into the same "run a subagent" lifecycle, and only
`Spawn` was updated to route through the fan-out semaphore
(`acquireSpawnSlot`/`executeWithSlot`) when that protection was added.**
`Approve` was written to parallel `Spawn`'s async-dispatch *shape*
(background ctx + registered `CancelFunc` + `safego.Go`) but never picked up
the semaphore acquisition step, so it calls `svc.execute` directly instead of
`svc.executeWithSlot`.

GO-EXEC-002 is a distinct but related ordering bug inside `Spawn`'s own
already-gated paths: the run's `Status` field is set to `StatusRunning`,
persisted via `insertRun`, and broadcast via `emitStatus` *before*
`acquireSpawnSlot` is called — meaning any external observer (a UI, or an
operator calling `Cancel`) sees "running" while the run may still be
purely queued, with no cancellation hook registered yet to actually stop it.

### Current behavior

**`Spawn`'s correctly-gated paths** (`internal/subagent/service.go`, the
switch on `mode` inside `Spawn`):

```go
// internal/subagent/service.go:871-879 — status set + broadcast BEFORE acquiring a slot
run.Status = StatusRunning
run.StartedAt = run.CreatedAt
if err := svc.insertRun(ctx, run); err != nil {
    return "", fmt.Errorf("insert run: %w", err)
}
// G-5: emit running event so the parent UI can render "subagent
// spawned" before the runner does any work.
svc.emitStatus(run, "")

switch mode {
case ModeSync:
    // internal/subagent/service.go:891-898
    if err := svc.acquireSpawnSlot(ctx); err != nil {
        return "", err
    }
    execCtx, execCancel := context.WithCancel(context.Background())
    svc.cancelMu.Lock()
    svc.cancelers[run.ID] = execCancel   // registered AFTER the slot is acquired
    svc.cancelMu.Unlock()
    svc.executeWithSlot(execCtx, run, req.ParentAgentID)
case ModeAsync, ModeAPI:
    // internal/subagent/service.go:909-918 — same ordering, async dispatch
    if err := svc.acquireSpawnSlot(ctx); err != nil {
        return "", err
    }
    runCtx, runCancel := context.WithCancel(context.Background())
    svc.cancelMu.Lock()
    svc.cancelers[run.ID] = runCancel    // registered AFTER the slot is acquired
    svc.cancelMu.Unlock()
    safego.Go(context.Background(), "subagent.run", func() {
        svc.executeWithSlot(runCtx, run, req.ParentAgentID)
    })
}
```

`emitStatus` (`internal/subagent/service.go:467-484`) marshals `run.ID`,
`run.Status`, etc. and hands the payload to `svc.streamSink
.SubagentStatusChanged(run.ParentSessionID, payload)` — this call is
synchronous, on the same goroutine that is about to block inside
`acquireSpawnSlot` if the cap is full. This is the mechanism GO-EXEC-002 is
about: the "running" broadcast is observable to a listener (and to
`Cancel`, since `Cancel`'s DB `UPDATE` guard set includes `StatusRunning`)
well before the run has actually started executing or has a cancellation
hook registered.

`acquireSpawnSlot` (`internal/subagent/service.go:1130-1151`) blocks on
`svc.spawnSem <- struct{}{}` until a slot frees or `ctx` is done.
`executeWithSlot` (`internal/subagent/service.go:1159-1162`) is a thin
wrapper — `defer svc.releaseSpawnSlot(); svc.execute(ctx, run,
parentAgentID)` — it does **not** itself acquire a slot; acquisition is
always the caller's explicit responsibility via `acquireSpawnSlot`.

**`Approve`'s ungated dispatch** (`internal/subagent/service.go:1087-1123`):

```go
func (svc *Service) Approve(ctx context.Context, runID string) error {
    // ... expiry check, then:
    res, err := svc.db.ExecContext(ctx,
        `UPDATE subagent_runs
           SET status=?, approved_at=?, approved_by=?, started_at=?
         WHERE id=? AND status=?`,
        StatusRunning, now, "", now, runID, StatusRequested)   // 1094-1099
    // ...
    run, err := svc.Status(ctx, runID)
    // ...
    svc.emitStatus(run, "")                                     // 1111

    // Launch runner with independent ctx + registered cancel, matching the
    // async branch pattern in Spawn.
    runCtx, runCancel := context.WithCancel(context.Background())
    svc.cancelMu.Lock()
    svc.cancelers[run.ID] = runCancel
    svc.cancelMu.Unlock()
    safego.Go(context.Background(), "subagent.run", func() {
        svc.execute(runCtx, run, run.ParentAgentID)             // 1119-1121 — NOT executeWithSlot
    })
    return nil
}
```

`Approve` never calls `acquireSpawnSlot` and never calls `executeWithSlot` —
it calls `svc.execute` directly. There is no `defer svc.releaseSpawnSlot()`
either, which is self-consistent (it never acquired a slot to release), but
means `svc.spawnSem`'s cap-of-3 guarantee simply does not apply to runs
dispatched via `Approve`.

**The single production caller of `Approve`:**
`SubagentApprovalHandler.HandleResponse`
(`internal/chat/envelope_response_subagent.go:27-55`) calls `h.svc.Approve(ctx,
p.RunID)` at line 37 when an envelope response with `Status ==
StatusSubmitted` arrives for a `subagent-spawn-approval` envelope. That
handler is registered by envelope type in
`internal/service/container.go:1207`
(`chat.RegisterResponseHandler("subagent-spawn-approval",
chat.NewSubagentApprovalHandler(subagentSvc))`) and reached via the generic,
HTTP-reachable envelope-response endpoint at
`internal/api/envelopes.go:110` (`handler.HandleResponse(ctx, *inst, resp)`).
Nothing about that path distinguishes a human clicking "Approve" in the UI
from an automated/scripted client posting the same response shape — which is
exactly the "human or automated" framing in the finding. Nothing rate-limits
or serializes concurrent HTTP requests to this endpoint.

**`Cancel`** (`internal/subagent/service.go:966-984`):

```go
func (svc *Service) Cancel(ctx context.Context, runID string) error {
    defer func() {
        svc.cancelMu.Lock()
        if c, ok := svc.cancelers[runID]; ok {
            c()
            delete(svc.cancelers, runID)
        }
        svc.cancelMu.Unlock()
    }()

    _, err := svc.db.ExecContext(ctx,
        `UPDATE subagent_runs SET status = ? WHERE id = ? AND status IN (?,?,?)`,
        StatusCancelled, runID, StatusRequested, StatusApproved, StatusRunning,
    )
    // ...
}
```

Walking the full sequence for a run that is queued behind a full cap: (1)
`Spawn` sets `run.Status = StatusRunning`, inserts, and calls `emitStatus` —
observable as "running" now; (2) `Spawn` blocks in `acquireSpawnSlot`,
**no** `cancelers[run.ID]` entry exists yet; (3) an operator, seeing
"running" in the UI, calls `Cancel(runID)`; (4) `Cancel`'s `UPDATE` succeeds
(the row's current DB status is `StatusRunning`, which is in the guard set),
flipping the row to `cancelled`; (5) `Cancel`'s deferred canceler lookup
finds nothing in `svc.cancelers` (not yet registered) and no-ops; (6) later,
a slot frees, `acquireSpawnSlot` returns nil, `cancelers[run.ID]` is
registered (too late to matter), and `executeWithSlot`/`execute` runs the
subagent to completion — despite the DB already showing `cancelled`. The
existing test suite (`TestFanout_CancelWhileQueued`,
`internal/subagent/service_fanout_test.go:250-320`) only covers the
*caller's own context* being cancelled while queued — a case that is already
handled correctly, because `acquireSpawnSlot`'s `select` on `ctx.Done()`
short-circuits cleanly (`internal/subagent/service.go:1130-1151`). It does
not cover an operator-issued `Cancel(runID)` landing on an
already-broadcast-as-running-but-still-queued run — that gap is
GO-EXEC-002, and it is the only path `finalizeRun`
(`internal/subagent/service.go:1672`) has to reconcile after the fact.

### Desired invariant

**The documented spawn concurrency cap applies uniformly regardless of entry
path (direct spawn or post-approval dispatch), and a `Cancel()` call issued
at any point in a run's lifecycle — including while queued for a slot —
actually prevents the runner from executing, not just relabels the outcome
after the fact.**

## What to do

### Scope

`internal/subagent/service.go`: `Spawn`, `Approve`, `Cancel`,
`acquireSpawnSlot`, `executeWithSlot`, `execute`, `finalizeRun`, `emitStatus`.
No other package needs a production-code change for this task — see "All
production callers" below for why `Spawn`'s other callers need no change.

### All production callers

- **`Approve`** — exactly one production caller:
  `SubagentApprovalHandler.HandleResponse`
  (`internal/chat/envelope_response_subagent.go:37`), reached via
  `internal/api/envelopes.go:110` through the response-handler registry
  wired in `internal/service/container.go:1207`. This caller needs no
  change — the fix belongs entirely inside `Approve`/`Service` so every
  caller (present or future) inherits it.
- **`Spawn`** — already correctly gated; enumerated here only to confirm
  none of its callers need touching. All four resolve to the real
  `subagent.Service.Spawn`, not a parallel implementation:
  `internal/selftools/self_tools_transport.go:1770` (`st.Subagent.Spawn`);
  `internal/dispatch/execute.go:331` (`spawner.Spawn`, against the
  `dispatch.Spawner` interface); `internal/service/hint_dispatch_adapter.go:59`
  (`a.spawner.Spawn`); and `internal/service/dispatch_wiring.go:73`
  (`d.svc.Spawn`, inside `dispatchSpawner.Spawn`, the adapter that
  `dispatch.Spawner` and `hint_dispatch_adapter`'s `spawner` field both
  resolve to — confirmed by `internal/service/dispatch_wiring.go:18-19,42,60,73`).
- **`Cancel`** — not separately re-enumerated; any caller of `Cancel` on any
  run ID benefits from the fix inside `Cancel`/`Spawn`'s ordering, no caller
  needs a signature or call-site change.

### Proposed direction

**GO-EXEC-001.** Route `Approve`'s dispatch through the same
`acquireSpawnSlot` + `executeWithSlot` machinery `Spawn` already uses,
instead of calling `svc.execute` directly. Two sub-choices exist for
*where* the slot acquisition happens, and this task treats the choice
between them as an implementation-level call, not an architect escalation
— document whichever is picked in Work log:
  - **(a) Acquire before dispatching the goroutine**, mirroring `Spawn`'s
    `ModeAsync` shape exactly: `Approve` itself blocks on
    `svc.acquireSpawnSlot(ctx)` before returning, using `ctx` (the caller's
    request context) to govern the wait — consistent with how `Spawn`
    already lets a queued caller's own cancellation unblock cleanly. This
    makes `Approve`'s HTTP response latency reflect real queueing, same as
    async `Spawn` today.
  - **(b) Acquire inside the dispatched goroutine**, before calling
    `execute`: `Approve` returns immediately after the DB transition, and
    the background goroutine calls `acquireSpawnSlot` on its own
    `context.Background()`-derived ctx before proceeding. This keeps
    `Approve`'s HTTP response fast regardless of queue depth, at the cost
    of the DB/broadcast-observable "running" state briefly preceding actual
    slot acquisition — which is exactly the ordering GO-EXEC-002 already
    identifies as the risky part, so this sub-choice should not be picked
    without also fixing GO-EXEC-002's ordering (below), since otherwise it
    reproduces the same bug inside `Approve`.

  Either way, replace the direct `svc.execute(runCtx, run,
  run.ParentAgentID)` call at `internal/subagent/service.go:1120` with a
  call that goes through `executeWithSlot` (or an equivalent that acquires
  before executing and releases on every exit path), so `spawnFanoutCap`
  is enforced identically for both entry points.

**GO-EXEC-002.** Either:
  - **(a) Register a lightweight "queued" cancellation hook before
    `acquireSpawnSlot` is attempted** — e.g. register a `cancelers[run.ID]`
    entry (or a distinct queued-cancel registry) that, when invoked, cancels
    the `ctx` passed into `acquireSpawnSlot` itself, so a `Cancel(runID)`
    landing while still queued unblocks the wait and the run never proceeds
    to `execute`; then re-register (or promote) the real execution-scoped
    `CancelFunc` once the slot is acquired, same as today; **or**
  - **(b) re-check the run's current persisted status immediately after
    `acquireSpawnSlot` returns and before invoking the runner** — if the row
    is already `cancelled` (or otherwise terminal) at that point, release the
    slot immediately (without ever calling `execute`) and return/no-op
    instead of running the subagent.

  Direction (a) is closer to the existing `Cancel`/`cancelers` architecture
  (guide's non-goal below already rules out redesigning it) and gives an
  operator-issued `Cancel` the fastest possible response even under a fully
  saturated cap; direction (b) is a smaller diff (one extra status read) but
  means a queued run that gets cancelled still occupies the queue position
  until its "turn" would have come up, then discovers it's cancelled and
  bails — acceptable given the invariant only requires the runner not
  execute, not that the queue position free up early. Either satisfies the
  desired invariant; pick one and document the choice and rationale in Work
  log.

Apply whichever GO-EXEC-002 fix is chosen to **all** of `Spawn`'s gated
paths that set `StatusRunning`/call `emitStatus` before `acquireSpawnSlot`
(`ModeSync` and `ModeAsync`/`ModeAPI`, both at
`internal/subagent/service.go:871-919`) — not just one mode — and to
`Approve`'s dispatch once GO-EXEC-001 routes it through the same
acquire-then-run shape.

### Non-goals

- **Do not redesign the semaphore or the cancellation architecture.** Both
  primitives (`svc.spawnSem` / `acquireSpawnSlot` / `releaseSpawnSlot`, and
  `svc.cancelers` / `Cancel`) exist and work correctly for the paths that
  already use them correctly (`Spawn`'s existing fan-out test suite in
  `service_fanout_test.go` passes and exercises real concurrency/queueing/
  cancellation behavior). This task closes two specific ordering/coverage
  gaps, not a rewrite.
- **Do not change `spawnFanoutCap`'s value (3)** or make it configurable —
  out of scope, unrelated to either finding.
- **Do not touch `Reject`, `expireIfStale`, or `shouldRetry`** — reviewed in
  the same audit section and not implicated in either finding.
- **Do not change the `SubagentApprovalHandler`/envelope-response wiring** —
  the fix belongs entirely inside `subagent.Service`; the caller needs no
  change (see "All production callers").

### Dependencies

- None within this batch.
- No external/architect dependency — see the `requires_architect_decision`
  note under "Findings addressed" above.

### Tests required

Per the remediation guide's explicit instruction ("Add regression tests for
both"), and per this task's own specification:

1. **GO-EXEC-001 regression — Approve obeys the cap.** A test that queues
   more than 3 gated runs and approves them concurrently, asserting
   `runner.started` (or, more precisely, the existing
   `controlledRunner.highWaterMark()` concurrent-in-flight gauge — see
   `internal/subagent/service_fanout_test.go:26-71`) never exceeds
   `spawnFanoutCap` (3) at any point. Concretely: reuse `newTestDB`,
   `controlledRunner`, `stubPoster` (or `recordingPoster`) from the existing
   fanout/service test files (same package, `internal/subagent`); construct
   the service with `stubSettings{us: store.UserSettings{
   SubagentApprovalRequired: true, SubagentApprovalTimeoutSeconds: 3600}}` —
   the exact fixture `TestApprove_TransitionsAndRunsRunner`
   (`internal/subagent/service_test.go:857-883`) already uses to force every
   `Spawn` call down the gated (`StatusRequested`) path — with a stub
   approver so the approval envelope emits without error; `Spawn` (e.g. 5×,
   `ModeSync` or `ModeAsync`) to produce 5 `StatusRequested` runs and collect
   their run IDs; then call `svc.Approve(ctx, runID)` for all 5 concurrently
   (one goroutine per call, mirroring `spawnAsync`'s shape at
   `internal/subagent/service_fanout_test.go:81-103`); assert
   `runner.highWaterMark() <= 3` after releasing the gate and draining all
   five approvals, and — mirroring `TestFanout_AtCap_QueuesExtra`
   (`internal/subagent/service_fanout_test.go:181-242`) — assert exactly 3
   runners are started (not 5) before the gate is released, proving the
   4th/5th genuinely queued rather than the assertion passing by coincidence
   on wall-clock timing alone.
2. **GO-EXEC-002 regression — queued `Cancel` actually stops the run.** A
   test mirroring `TestFanout_CancelWhileQueued`'s structure
   (`internal/subagent/service_fanout_test.go:244-320`) but driving the
   cancellation differently: fill all 3 slots with the existing
   `controlledRunner`/`spawnAsync` pattern so they block on the gate; then
   dispatch one more `Spawn` call (`ModeAsync`) that will queue behind the
   full cap; install a fake `SubagentStreamSink` (via `svc.SetStreamSink`,
   `internal/subagent/service.go:454`, satisfying the interface at
   `internal/subagent/types.go:288-292`) that, on observing a
   `SubagentStatusChanged` payload with `"status":"running"` for the queued
   run's `run_id` (unmarshal the JSON payload — same shape `emitStatus`
   marshals at `internal/subagent/service.go:471-478`), hands the run ID
   back to the test goroutine over a channel; from the test goroutine (not
   the sink callback, to avoid holding `svc.cancelMu` re-entrantly or
   racing the `Spawn` call's own goroutine), call `svc.Cancel(ctx, runID)`
   on that run **while all 3 original slots are still held (gate not yet
   released)** — i.e. before the queued `Spawn` call could possibly have
   acquired a slot; then release the gate and assert (a) `runner.started`
   never reaches 4 (the cancelled queued run's `Run` is never invoked at
   all — the queued runner never starts), matching
   `TestFanout_CancelWhileQueued`'s existing assertion shape at
   `internal/subagent/service_fanout_test.go:305-308`, and (b) the run's
   final DB status (via `svc.Status`) is `StatusCancelled`, not something
   `finalizeRun` overwrote back to a completed/failed terminal state.
3. Both new tests should run under `go test -race` as part of
   `internal/subagent`'s existing race-focused suite; per REPORT.md §8.10,
   this package's own `-race` run needs an extended timeout (the base
   report's already-confirmed finding) — use whatever timeout convention
   the existing `internal/subagent` `-race` CI/Makefile target already uses,
   not the default `go test` timeout.

### Prevention

- The two new regression tests above are themselves the direct prevention
  mechanism — any future change that reintroduces an ungated dispatch path,
  or reverts the queued-cancellation ordering, breaks them.
- Consider (not required for this task, worth flagging for
  `12-quality-ratchet-and-standards/`) whether a lint/code-review checklist
  item should exist for "every new way to reach `execute`/`executeWithSlot`
  must go through `acquireSpawnSlot`" — this is the second time a
  parallel-entry-point-missed-the-shared-gate shape has shown up in this
  cluster's review (the guide explicitly asked reviewers to hunt for
  "multiple ways to launch/stop/resume the same entity"; REPORT.md §8.10
  found the `Spawn`/`worker.SpawnFull`/`dispatch.ExecuteTask`/
  `workflowrunner.Launch` surface free of this problem, but `Approve` — a
  fifth entry point into the same `execute` — was missed by that same
  check because it's not a "launch" in the audit's original framing, it's
  a "resume a previously gated launch." A future audit pass or lint rule
  should treat approval/resume paths as first-class entry points for this
  check, not a special case.

### Verification

```bash
go build ./internal/subagent/...
go vet ./internal/subagent/...
go test ./internal/subagent/... -run 'Fanout|Approve|Cancel' -v
go test -race ./internal/subagent/...   # use the extended timeout convention noted above
go test ./...
```

Observable behavior required for PASS:

- The new GO-EXEC-001 regression test fails against current `HEAD` (proving
  it actually reproduces the bug) and passes once `Approve` routes through
  slot acquisition.
- The new GO-EXEC-002 regression test fails against current `HEAD` and
  passes once the queued-cancel ordering fix lands.
- All pre-existing `internal/subagent` tests, especially
  `service_fanout_test.go`'s four existing fan-out tests and
  `service_test.go`'s `TestApprove_*` tests, continue to pass unmodified in
  behavior (their assertions may need no changes at all if the fix is
  additive).
- `go test -race ./internal/subagent/...` clean.

### Risk / rollback

- **GO-EXEC-001 fix regression surface:** if direction (a) is chosen
  (`Approve` blocks on slot acquisition before returning), any caller of
  `Approve` that assumed a fast, non-blocking response (the HTTP handler at
  `internal/api/envelopes.go:110` has whatever timeout the API layer
  imposes on that endpoint) could now observe added latency under a
  saturated cap — check that endpoint's timeout budget is generous enough,
  or pick direction (b) instead.
- **GO-EXEC-002 fix regression surface:** direction (a) (a queued-cancel
  hook) adds a second kind of `cancelers` entry with different lifecycle
  semantics than the existing execution-scoped one — get the
  register/promote/clear ordering wrong and a queued-then-cancelled run's
  hook could double-invoke or leak. Direction (b) (re-check status
  post-acquire) is lower-risk (one extra read, no new registry lifecycle)
  but must not introduce a new race between the status read and a
  concurrent `Cancel` landing a moment later — acceptable per the existing
  `finalizeRun` reconciliation safety net, which already tolerates this
  class of race for other terminal-status paths.
- **Rollback:** both fixes are additive/reordering changes confined to
  `internal/subagent/service.go`; revert that file (and the new test files)
  to return to current (buggy but understood) behavior. No schema or
  cross-package signature change is anticipated by either proposed
  direction, so rollback should not require touching any of `Approve`'s or
  `Spawn`'s callers.

## Done means

- [ ] `Approve` dispatches through the same slot-acquisition path `Spawn`
      uses (`acquireSpawnSlot`/`executeWithSlot` or equivalent) — no code
      path reaches `execute` without having acquired a semaphore slot.
- [ ] A run cancelled via `Cancel(runID)` while still queued for a slot
      (broadcast as "running" but not yet past `acquireSpawnSlot`) never
      invokes the runner — `finalizeRun`'s reconciliation is no longer the
      only mechanism producing a correct terminal status for this case.
- [ ] New regression test proves GO-EXEC-001 is fixed: concurrently
      approving more gated runs than the cap never drives concurrent
      in-flight runners above `spawnFanoutCap` (3).
- [ ] New regression test proves GO-EXEC-002 is fixed: an operator-issued
      `Cancel` on an already-broadcast-as-running-but-still-queued run
      results in the runner never starting.
- [ ] Both new tests fail against pre-fix code and pass against fixed code
      (verified during implementation, not just asserted).
- [ ] All pre-existing `internal/subagent` tests still pass, including the
      four existing fan-out tests and the `TestApprove_*` tests.
- [ ] `go build ./...`, `go vet ./...`, `go test ./...`, and
      `go test -race ./internal/subagent/...` (extended timeout) all pass.
- [ ] The GO-EXEC-001/GO-EXEC-002 direction chosen (among the sub-options
      in "Proposed direction") is recorded in Work log with rationale.

## Work log

<Worker fills this in.>

## Review notes

<Reviewer fills this in.>
