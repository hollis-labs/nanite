# Fix DelegateAndAggregate's unbounded collector hang and decide a package-wide policy for untracked safego.Go spawns

**Phase:** Wave 2 — Correctness, lifecycle, concurrency (audit-remediation batch, sequenced 2026-08-21 — see the sequencing block below)
**Status:** reviewed
**Depends on:** none as a hard blocker. Cross-reference only: this task's untracked spawns (`GO-SVCCORE-002`) are a *candidate* cause for task 03's investigation (`GO-SVCCORE-006`) — task 03 does not depend on this task landing first, but if task 03's investigation lands first and confirms candidate 2 (untracked-goroutine accumulation), that strengthens the case for prioritizing the `GO-SVCCORE-002` half of this task.
**Touches:** `internal/service/delegation.go` (`DelegateAndAggregate`, for GO-SVCCORE-001), `internal/service/events_composite.go` (~18 `safego.Go` sites, for GO-SVCCORE-002), `internal/service/agent_deps.go` (see drift note below), and `internal/service/container.go:1184-1194` (the existing precedent comment to read, not necessarily to edit — **Wave 0 revalidation (2026-08-22) note:** shifted +51 lines from the audit-era `1133-1143` by unrelated additive changes earlier in the file (`TASKS/skills/` wiring a `SkillVendor` field, the loop-batch wiring a `ReflexEngine` field); the comment's own text is byte-identical, confirmed by direct diff against the audited commit).
**Requires architect decision:** **true for the GO-SVCCORE-002 half** (package-wide policy call — see below). **False for the GO-SVCCORE-001 half** (clear fix direction with a concrete sibling pattern to follow) — but see the pre-implementation check noted under that finding before starting.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 2 — correctness, lifecycle, concurrency · **Dispatch unit:** `W2a`
> - **Depends on:** `00/01`
> - **Blocks:** `12/03` — that task lints the `safego` adoption this one performs
> - **Parallel-safe with:** `04/01`, `04/02`, `04/05`, `05/01`
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

This task covers two related but independently-actionable findings from `internal/service`'s composition-root cluster review (`docs/audits/2026-08-21-go-quality/REPORT.md` §8.3, `REPORT.md:781` and `REPORT.md:783`). Both concern goroutines spawned via `internal/safego`'s fire-and-forget `Go` helper, but they are different problems with different resolution paths — treat them as two sub-tasks within this one file, not one fix.

### Findings addressed
- `GO-SVCCORE-001` (medium, concurrency) — `DelegateAndAggregate`'s collector can hang forever if a worker panics before writing its result.
- `GO-SVCCORE-002` (low, concurrency/lifecycle) — ~18 untracked `safego.Go` fire-and-forget spawns that `Container.Shutdown()` cannot drain.

---

## Part A — GO-SVCCORE-001: DelegateAndAggregate's collector has no timeout/cancel safety valve

### Root cause
`(*chatServiceImpl).DelegateAndAggregate` (`internal/service/delegation.go:304-386`, confirmed current) spawns one `safego.Go` worker per sub-task to call `s.workers.SpawnFull(...)` concurrently, writing an `indexedResult` to a shared channel `ch` when each worker finishes (`delegation.go:358-374`, confirmed current: `ch <- indexedResult{idx: idx, result: r}` inside the `safego.Go` closure). The collector loop immediately after (`delegation.go:378-381`, confirmed current) is an unconditional blocking receive:
```go
results = make([]chat.SubTaskResult, len(decomposition.SubTasks))
for range decomposition.SubTasks {
    ir := <-ch
    results[ir.idx] = ir.result
}
```
`safego.Go` recovers a panic in the worker closure and reports it (via `recoverAndReport`, `internal/safego/safego.go:80-85`) — but recovery happens *after* the closure has already exited without reaching its `ch <-` send. If that happens, the collector's `for range { <-ch }` blocks on that missing value forever; nothing else can ever write to `ch` for that index. This is directly comparable to `DelegateAndAggregate`'s sibling method `DelegateTask` (`internal/service/delegation.go:25-...`), which has a real `select` loop with both a `time.After(runReq.Timeout)` case (`delegation.go:169-238`, confirmed current: `timeout := time.After(runReq.Timeout)` ... `case <-timeout:`) and a `case <-ctx.Done()` case (`delegation.go:258`, confirmed current) — `DelegateAndAggregate`'s collector has neither.

### Desired invariant
No goroutine collector in `internal/service` should be able to hang indefinitely waiting on a channel write that a panicking worker failed to make. Every fan-out/collect pattern should have the same safety valve `DelegateTask` already demonstrates: a bounded wait via `ctx.Done()` and/or an explicit timeout.

### Proposed direction
Give `DelegateAndAggregate`'s collector the same timeout/cancel `select` `DelegateTask` already uses — either:
1. Wrap the collector's `<-ch` in a `select` with `case <-ctx.Done()` (and/or a `time.After(...)` matching whatever timeout semantics apply here — check whether `DelegateAndAggregate` has an equivalent to `DelegateTask`'s `runReq.Timeout` field, or whether the caller's `ctx` is expected to carry the deadline instead); or
2. Have each spawned closure's deferred recover write a synthetic error `indexedResult` to `ch` before the panic unwinds past it, so the collector never actually needs to wait past a bounded window — this requires wrapping each `safego.Go` closure's body in its own `defer recover()` that writes the fallback result, since `safego.Go`'s own built-in recovery happens at a scope the closure itself can't observe/react to.

Option 1 more directly mirrors `DelegateTask`'s existing pattern (stronger "prevention via consistency" argument); option 2 is more surgical (doesn't need a timeout value to be chosen/threaded through) but adds a second layer of panic handling. Not an architect-level decision — pick whichever is more consistent with a maintainer's read of `DelegateTask`'s intent, but document the choice in the Work Log.

### Non-goals
Do not change `DelegateTask` itself, `s.workers.SpawnFull`, or the worker-manager's own panic/error semantics.

### Pre-implementation check (flagged by the audit, not yet independently verified)
The audit's own false-positive-considerations note for `GO-SVCCORE-001` states: "Only reachable if `worker.Manager.SpawnFull` (or the closure) can actually panic in practice — not independently verified." Before implementing, verify whether `SpawnFull` (or anything in the closure body between spawn and the `ch <-` send) can realistically panic — if it's provably panic-free by construction, this finding's practical urgency is lower (though the fix is still correct defensive practice and cheap either way; the guide's own principle is "every started resource has an owner," which applies to logical wait-forever hangs too, not just goroutine leaks).

### Tests required
- A regression test that forces a worker closure to panic before its `ch <-` send (e.g. inject a `SpawnFull` implementation, via whatever seam exists or needs adding, that panics for one sub-task) and asserts `DelegateAndAggregate` returns within a bounded time instead of hanging — this is the direct reproduction of the original defect; without it, a future refactor could silently reintroduce the hang.
- Cover the "all workers succeed" happy path remains unaffected (existing tests, if any, should still pass).

---

## Part B — GO-SVCCORE-002: ~18 untracked safego.Go fire-and-forget spawns

### Root cause
`internal/safego.Go`'s own doc comment is explicit about its contract: *"context is used only for OTel span attachment; it is not passed to fn. Callers that need cancellation should use `internal/lifecycle.Manager.Go`"* (`internal/safego/safego.go:78-80`, confirmed current). Despite this, `internal/service` has multiple call sites using `safego.Go` for spawns that arguably *do* need to be drainable at shutdown — confirmed current counts (re-verified against `HEAD`, not just the audit's `8feeee5c` baseline, since counts have almost certainly drifted — see drift note below):
- `internal/service/events_composite.go` — **18** `safego.Go(ctx, "service.events.*", ...)` call sites (event/plugin-hook dispatch on session-start, session-end, tool-call, error, pre-compact, etc. — confirmed via `grep -c "safego.Go(" internal/service/events_composite.go` = 18, matching the audit's own "~18" figure almost exactly on its own).
- `internal/service/delegation.go` — 2 current `safego.Go` sites (one of which is `DelegateAndAggregate`'s own spawn, Part A above; treat that one as covered by Part A's fix, not this part).
- `internal/service/agent_deps.go` — **the audit's original finding cites this file, but a fresh grep against current `HEAD` (`grep -n "safego\." internal/service/agent_deps.go`) returns zero matches.** This is a real drift signal, not an invented correction — either the file was refactored since the audited commit (moved the spawns elsewhere, removed them, or renamed the pattern) or the audit's file attribution was imprecise. **Verify current state of `agent_deps.go` before starting this task** — do not assume the audit's file list is still accurate; re-grep the whole package for `safego.Go(` and reconcile against the ~18-20 total the audit implies, rather than trusting the three-file list verbatim.

None of these ~18-20 spawns are drainable by `Container.Shutdown()` — they are bare `go func(){...}()`-equivalent fire-and-forget calls, so a burst of in-flight event/plugin-hook dispatches at shutdown time can continue running past `Shutdown()` returning.

**The codebase already knows this pattern matters and has a real precedent for fixing it.** One specific wake-reactor spawn was deliberately migrated off `safego.Go` onto a tracked `*lifecycle.Manager` after a PR review flagged exactly this risk — see `internal/service/container.go:1184-1194` (confirmed current), which reads in full:
```go
// Copilot PR #258 review: wire the wake-reactor goroutine spawn onto
// a tracked *lifecycle.Manager instead of the untracked safego.Go
// default, so a burst of messages can't leave unbounded goroutines
// running past process shutdown. Reuses chatSvcImpl's own manager
// (same package, field access is intra-package) rather than
// constructing a second one — these goroutines call back into
// chatSvcImpl anyway, so draining them alongside chat's own
// generateResponse goroutines on Shutdown is the right scope, not a
// separate lifecycle. chatSvcImpl already exists by this point (same
// ordering constraint as SetWakeReactor immediately above).
messagingSvc.SetLifecycleManager(chatSvcImpl.lifecycle)
```
`chatServiceImpl` already has a live `*lifecycle.Manager` field (`lifecycle *lifecycle.Manager`, `internal/service/chat.go:279`, confirmed current) with an established `s.lifecycle.Go(name, func(bgCtx context.Context) {...})` usage pattern (`internal/service/chat.go:587`, confirmed current) and its own bounded `Shutdown(chatShutdownMaxWait)` call (`chat.go:986-987`, confirmed current) — this is the concrete, already-proven mechanism available to migrate any of the ~18-20 sites onto, not a new pattern that needs inventing.

### Desired invariant
Decide, package-wide, which `safego.Go` sites in `internal/service` are genuinely fire-and-forget-safe (best-effort telemetry/logging that's fine to abandon at shutdown) versus which need `lifecycle.Manager`-tracked cancellation (anything whose in-flight work matters for correctness, or whose unbounded continuation past `Shutdown()` could cause observable issues — e.g. writing to a store connection that's mid-close). Apply that decision consistently, not site-by-site improvisation.

### Requires architect decision — why
This is **not** a mechanical fix like Part A. It requires a judgment call about which of the ~18-20 spawns are safe to leave as pure fire-and-forget (the wake-reactor precedent implies at least some event/plugin-hook dispatches were judged risky enough to migrate) versus which aren't — that's a package-wide policy question, not a per-site technical fix. The remediation guide's own architect-decision queue (§9) and this batch's README both flag exactly this kind of "decide package-wide which sites need tracking vs. which are fine" call as requiring architect sign-off before an implementer starts migrating individual sites, to avoid 18 independent, possibly-inconsistent judgment calls.

### Proposed direction (for the architect decision, not a mandate)
- Audit each of the ~18-20 sites (re-verify the real current total first, per the drift note above) against a simple test: "if this goroutine is still running when `Shutdown()` returns, does anything break, get lost, or corrupt state?" Event/plugin-hook dispatches that only log or emit best-effort telemetry are plausibly safe to leave untracked; anything that writes to a store or triggers user-visible side effects mid-shutdown is a stronger migration candidate.
- If the decision is "migrate a subset," the mechanism is proven: route those sites through `chatServiceImpl.lifecycle.Go(...)` the same way the wake-reactor spawn does, following `container.go:1184-1194`'s own reasoning for why reusing `chatSvcImpl`'s existing manager (rather than constructing a second one) is the right scope.
- If the decision is "none of these need tracking, they're all genuinely disposable," that's also an acceptable outcome — but should be recorded explicitly (e.g. a short package-doc note near `safego.Go`'s own doc comment, or in this task's Work Log) so a future reader doesn't re-flag the same ~18 sites as an open question again.

### Non-goals
Do not migrate `safego.Go` sites in files/packages outside `internal/service`'s `events_composite.go`/`delegation.go`/`agent_deps.go` (and whatever the drift re-audit finds) as part of this task — the finding's scope is this package's specific sites, not a repo-wide `safego.Go` audit.

### Tests required
- If any sites are migrated to `lifecycle.Manager.Go`, a regression test confirming `Container.Shutdown()` (or `chatServiceImpl`'s own shutdown path) actually drains them within the bounded wait, rather than merely compiling.
- No new tests required for sites explicitly decided to remain fire-and-forget — document the decision instead.

### Prevention
Whatever the architect decides, record it as an explicit rule (a package doc comment, or an addition to this project's engineering standards doc) so future `safego.Go` call sites in `internal/service` are added with the same policy in mind, rather than requiring a fresh audit each time. Maps to the remediation guide's "Lifecycle Ownership" standard (Wave 7).

### Verification
- Part A: the regression test described above passes, and `DelegateAndAggregate` provably returns within a bounded time when a worker panics pre-send.
- Part B: whatever subset (if any) is migrated is confirmed drained by `Shutdown()` via a real test, and the package-wide policy decision is recorded somewhere durable (not just this task's Work Log, which is ephemeral per this project's dual-write conventions).

### Risk / rollback
Part A: low risk, additive safety-valve logic on an existing collector loop. Part B: risk scales with how many sites get migrated — each migration changes a spawn's cancellation semantics, so regression-test each migrated site individually rather than batch-migrating all ~18-20 at once without verification.

### Done means
- [ ] **Part A:** `DelegateAndAggregate`'s collector has a timeout/cancel `select` (or equivalent panic-safe fallback-write mechanism) matching `DelegateTask`'s existing pattern; a regression test proves a pre-send worker panic no longer hangs the collector.
- [ ] **Part A pre-check:** the "can `SpawnFull`/closure body actually panic in practice" question is answered (even if the answer is "yes, defensively assume it can") before or during implementation, not left unverified.
- [ ] **Part B:** the real current count and file locations of untracked `safego.Go` sites in `internal/service` are re-verified against `HEAD` (not assumed from the audit's `agent_deps.go` citation, which did not reproduce on a fresh grep).
- [ ] **Part B:** an architect decision is obtained and recorded for which sites need `lifecycle.Manager` tracking vs. remain fire-and-forget, before any site is migrated.
- [ ] **Part B:** any migrated sites are verified drained by a real shutdown-path test.

## Work log

- 2026-08-22 — Re-derived the package inventory from the Wave 2 base before editing: 34 `safego.Go` sites across seven production files. Applied AD-26's authoritative classification exactly: migrated 23 asynchronous jobs to a shared chat lifecycle (8 composite plugin callbacks, 3 direct `chat.go` plugin callbacks, 6 `chat_generate.go` plugin callbacks, 2 auto-title/tag jobs, 2 recovery jobs, and 2 delegation/worker jobs); retained the 10 bounded `ActivityEmitter` HTTP sends as explicit fire-and-forget work; retained the one concurrent-tool child because its caller synchronously joins it. Final `internal/service` inventory is 11 `safego.Go` sites: those 10 activity sends plus the joined tool child.
- Part A pre-check: `worker.Manager.SpawnFull` is not panic-free by construction. It calls injected interface dependencies and, when `ChatDelegator.DelegateTask` returns `(nil, nil)`, dereferences the nil result. The collector therefore needs a defensive panic path even if current production implementations normally honor the result contract. Narrowed the chat dependency to `fullWorkerSpawner`, extracted the exact fan-out/collect path for direct regression coverage, and made every child publish a synthetic indexed error result from a local recover. The collector also selects on caller cancellation and owner shutdown, so manager admission rejection during shutdown cannot recreate a missing-send hang. The all-success path continues to preserve input order.
- Part B: created the chat lifecycle at the composition root and supplied the same manager to `CompositeEmitter` and `chatServiceImpl`. Migrated APIs that accept context (auto-title/tag, worker dispatch, displaced-session stop) to the owner-cancelled context, preserving caller cancellation for worker/delegation execution. Plugin callback APIs expose no context, so they are drainable but cannot observe cancellation directly. Bare `chatServiceImpl`/`CompositeEmitter` test constructions execute inline rather than creating an unowned fallback goroutine.
- Hardened `lifecycle.Manager` by serializing the closed transition with admission and `WaitGroup.Add`; this prevents `Shutdown` from observing zero work between the old separate closed check and Add. Added a concurrent spawn-vs-shutdown race regression.
- Verified plugin-host ordering and closed the smallest in-scope gap from AD-26: `Container.Shutdown` now waits for chat lifecycle drain before calling `Plugins.Shutdown`, while both remain inside the container's bounded parallel shutdown group. Added a real host/plugin ordering regression. Partial `NewContainer` construction now shuts down the newly-created shared lifecycle on every error return. No follow-up is needed for this gap.
- Regression coverage added: panic-before-send returns a synthetic result within one second; successful concurrent worker results remain ordered; composite plugin dispatch blocks shutdown until drained; an in-flight auto-title provider call observes lifecycle cancellation and drains; plugin unload happens only after chat shutdown; lifecycle admission never starts work after shutdown returns. New regressions were not run against a reverted tree before editing, so no pre-fix failure transcript was captured; each test directly encodes the historical interleaving/behavior.
- Verification passed: `go test -race ./internal/lifecycle -run TestGoConcurrentWithShutdownNeverStartsAfterReturn -count=1`; `go test -race ./internal/service -run 'Test(DelegateSubTasks|CompositeEmitter_PluginDispatch|Container_ShutdownDrainsChat)' -count=1`; `go test ./internal/lifecycle ./internal/service -count=1`; `go build ./cmd/nanite/`; `go vet ./...`; `go test ./...`.
- 2026-08-22 review-failure correction — Replayed both blocking failures against the reviewed implementation before editing: `TestDelegateSubTasks_NilOwnerExecutesInline` panicked at `owner.Go`, and `TestContainer_ShutdownTimeoutDoesNotUnloadPluginsLater` failed after the real 10-second container deadline when releasing Chat caused plugin unload after `Container.Shutdown` had returned. Corrected the shutdown protocol by making `ChatService.Shutdown` report drain errors, permanently skipping plugin unload when Chat fails or misses the container deadline, and running any admitted plugin unload synchronously to completion. The timeout contract now states explicitly that the deadline bounds unload admission and independent waits, while a plugin unload already in progress is joined and may extend total shutdown because the host has no context-aware unload API. Added regressions for successful Chat→plugin ordering, failed-drain skip, blocked/late Chat with no post-return unload, and the joined-unload postcondition.
- The same correction gives `delegateSubTasks` the established bare-service fallback: a nil lifecycle owner executes each worker body inline, retains the local panic-to-indexed-result conversion, and disables the owner-done select through a nil channel. No fallback manager or unowned goroutine is created. Replaced the probabilistic admission stress test with a narrow test-only barrier at the historical closed-check/`WaitGroup.Add` gap; the test uses `TryLock` to prove deterministically that the admission mutex remains held across that exact gap before exercising shutdown drain.
- Correction verification passed: pre-fix reproductions above; `go test -race ./internal/lifecycle -run TestGoAdmissionGateCoversWaitGroupAdd -count=20`; `go test -race ./internal/service -run 'Test(DelegateSubTasks|ChatService_ShutdownReportsLifecycleDrainFailure|Container_(ShutdownDrainsChatBeforeUnloadingPlugins|ChatDrainFailureSkipsPluginUnload|ShutdownJoinsPluginUnloadOnceStarted|ShutdownTimeoutDoesNotUnloadPluginsLater))' -count=10`; `go test -race ./internal/service -count=1`; `go test -race ./internal/lifecycle ./internal/service -count=1`; `go build ./cmd/nanite/`; `go vet ./...`; `go test ./...`. Status remains `implemented` pending a fresh review.
- 2026-08-22 second review-failure correction — Reproduced both nil-owner cancellation gaps before editing: a pre-cancelled call invoked both workers and returned success, while cancellation in the first of three inline tasks still invoked the second cancellation-ignoring worker. The nil-owner path now collects results directly in input order and checks `ctx.Err()` before every invocation and again after each inline call returns. A pre-cancel starts no work; mid-sequence cancellation returns the caller's cancellation deterministically and prevents all later work from starting; an already-running inline worker still cannot be preempted if it ignores context, preserving the no-unowned-goroutine policy.
- Redesigned the lifecycle test seam so the test controls the actual `WaitGroup.Add`/active-count operation while `admissionMu` must remain held, then asserted both the gate and the observable shutdown-before-start postcondition. Mutation-tested the exact defect by moving `admissionMu.Unlock()` before that controlled Add: `TestGoAdmissionGateCoversWaitGroupAdd` failed immediately with `admission gate was not held across the closed-check/WaitGroup.Add gap`; restoring the production ordering passes. Added nil-owner regressions for pre-cancel, three-task mid-sequence cancellation with a cancellation-ignoring later worker, and successful input ordering. Verification passed: `go test -race ./internal/lifecycle -run TestGoAdmissionGateCoversWaitGroupAdd -count=20`; `go test -race ./internal/service -run 'TestDelegateSubTasks' -count=50`; `go test -race ./internal/lifecycle ./internal/service -count=1`; `go test ./internal/lifecycle ./internal/service -count=1`; `go build ./cmd/nanite/`; `go vet ./...`; `go test ./...`. Status remains `implemented` pending fresh review.

## Review notes

- PASS (2026-08-22, after two correction cycles): final fresh review confirmed
  AD-26's exact inventory—34 base sites, 23 migrated to lifecycle ownership,
  and 11 deliberately retained (`10` bounded ActivityEmitter sends plus one
  synchronously joined tool child).
- Independent mutation analysis confirms the admission regression fails if
  `admissionMu` unlocks before `WaitGroup.Add`; 100 race repetitions passed in
  the fixed state. Nil-owner delegation passed 100 race repetitions and now
  handles pre-cancel, mid-sequence cancel, panic conversion, and ordered success
  without creating an unowned goroutine.
- Plugin shutdown regressions passed 20 race repetitions: failed, panicking, or
  late Chat drain permanently skips unload, and an admitted unload is joined
  before `Container.Shutdown` returns. Focused lifecycle/service build, vet,
  test, and race checks passed; one redundant combined service race run was
  interrupted under host contention after all targeted gates were green.
