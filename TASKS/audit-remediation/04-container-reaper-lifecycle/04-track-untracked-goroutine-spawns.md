# Fix DelegateAndAggregate's unbounded collector hang and decide a package-wide policy for untracked safego.Go spawns

**Phase:** Wave 2 — Correctness, lifecycle, concurrency (audit-remediation batch, unsequenced — see folder README)
**Status:** not-started
**Depends on:** none as a hard blocker. Cross-reference only: this task's untracked spawns (`GO-SVCCORE-002`) are a *candidate* cause for task 03's investigation (`GO-SVCCORE-006`) — task 03 does not depend on this task landing first, but if task 03's investigation lands first and confirms candidate 2 (untracked-goroutine accumulation), that strengthens the case for prioritizing the `GO-SVCCORE-002` half of this task.
**Touches:** `internal/service/delegation.go` (`DelegateAndAggregate`, for GO-SVCCORE-001), `internal/service/events_composite.go` (~18 `safego.Go` sites, for GO-SVCCORE-002), `internal/service/agent_deps.go` (see drift note below), and `internal/service/container.go:1133-1143` (the existing precedent comment to read, not necessarily to edit).
**Requires architect decision:** **true for the GO-SVCCORE-002 half** (package-wide policy call — see below). **False for the GO-SVCCORE-001 half** (clear fix direction with a concrete sibling pattern to follow) — but see the pre-implementation check noted under that finding before starting.

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

**The codebase already knows this pattern matters and has a real precedent for fixing it.** One specific wake-reactor spawn was deliberately migrated off `safego.Go` onto a tracked `*lifecycle.Manager` after a PR review flagged exactly this risk — see `internal/service/container.go:1133-1143` (confirmed current), which reads in full:
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
- If the decision is "migrate a subset," the mechanism is proven: route those sites through `chatServiceImpl.lifecycle.Go(...)` the same way the wake-reactor spawn does, following `container.go:1133-1143`'s own reasoning for why reusing `chatSvcImpl`'s existing manager (rather than constructing a second one) is the right scope.
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

<!-- Worker fills this in as it goes. -->

## Review notes

<!-- Reviewer fills this in. -->
