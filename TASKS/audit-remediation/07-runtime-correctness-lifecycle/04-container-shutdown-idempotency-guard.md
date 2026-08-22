# Add an idempotency guard to `service.Container.Shutdown`

**Phase:** Wave 2 — Correctness, lifecycle, concurrency
**Status:** not-started
**Depends on:** none
**Touches:** `internal/service/container.go` (`Container` struct fields, `Container.Shutdown`). No other package needs changes.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 2 — correctness, lifecycle, concurrency · **Dispatch unit:** `W2b`
> - **Depends on:** `04/01` (same file, `internal/service/container.go`)
> - **Blocks:** `09/01`, `09/02`
> - **Parallel-safe with:** none
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

`requires_architect_decision: false` — a small, low-risk, defense-in-depth addition that mirrors a pattern already correctly implemented elsewhere in this codebase (`internal/lifecycle.Manager.Shutdown`). No design ambiguity.

### Findings addressed
- `GO-RUNTIME-005` — severity **informational**, confidence **high**. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.13; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-RUNTIME-005`.

### Root cause

`Container.Shutdown` (`internal/service/container.go:1424` onward) has **no idempotency guard at all** — no `sync.Once`, no atomic/boolean "already shut down" flag, no early-return check. It fans out to multiple subsystem-`Shutdown`/`Stop` calls in parallel goroutines (the `run` helper, `container.go:1427-1439`, with a per-subsystem panic-recovery wrapper) plus several synchronous stop calls before that (`c.stopModelCatalog()`, `c.Engine.Stop()`, `c.stopSubagentReaper()`/`c.subagentReaper.Stop()`, `c.stopRuntimeReaper()`/`c.runtimeReaper.Stop()` — `container.go:1441-1475`). Nothing prevents `Shutdown` from being invoked a second time, which would re-trigger every one of those calls again. Whether a second invocation is actually safe depends entirely on each individual subsystem's own idempotency — not exhaustively audited here — which is exactly why the guide's own lifecycle-invariant question ("can Stop be called twice?") has a genuinely undefended answer for `Container` today.

This is in direct contrast to its sibling, `internal/lifecycle.Manager.Shutdown` (`internal/lifecycle/lifecycle.go:97-140`), which `REPORT.md` §8.13 names explicitly as **"the reference implementation the rest of the codebase should be measured against (correct, idempotent, well-documented shutdown)."** `lifecycle.Manager.Shutdown`'s own doc comment states: "Shutdown is idempotent; the second call waits on the same WaitGroup and returns immediately once all goroutines have completed" (`lifecycle.go:95-96`), backed by `m.closed.Store(true)` recorded at entry (`lifecycle.go:98`) plus reliance on `context.CancelFunc` and `sync.WaitGroup.Wait()`'s own natural safety on repeated calls.

### Currently dormant, not an active bug

`Container.Shutdown` has exactly **one** production call site, `cmd/nanite/main.go:747`, inside the SIGINT/SIGTERM signal handler:

```go
safego.Go(context.Background(), "cmd.nanite.signal-handler", func() {
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    <-sigCh
    slog.Info("shutting down")
    if err := daemonLifecycle.Shutdown(10 * time.Second); err != nil {
        slog.Error("daemon lifecycle shutdown", "err", err)
    }
    container.Shutdown()
    os.Exit(0)
})
```

(`main.go:739-749`). This goroutine body runs at most once by construction — a single blocking `<-sigCh` receive on a channel registered once via `signal.Notify` — so `Container.Shutdown()` fires exactly once in the current call graph. This finding is flagged because the guardrail question is legitimate and currently undefended, not because there is an observed double-shutdown bug today.

### Related, smaller observation (context only — not part of this task's scope)

`internal/runtime/agent.Session.Stop` (`internal/runtime/agent/manager.go:62`) has the same structural gap — no internal idempotency guard on `Stop` itself. But its one real caller, `chatServiceImpl.CloseAgentSession` (`internal/service/chat.go:1006-1029`), gates access via an atomic `s.activeSessions.LoadAndDelete(sessionID)` (`chat.go:1010`) **before** ever reaching `sess.Stop(ctx)` (`chat.go:1027`): a second `CloseAgentSession` call for the same session ID finds nothing at that key (already deleted by the first call) and returns early at `chat.go:1011-1012`. So `Session.Stop` cannot actually be double-invoked through its real production path today. `REPORT.md` §8.13 judges this "healthy-with-a-caveat, not a separate finding" — **this task does not touch `internal/runtime/agent/manager.go`**; it is noted here purely to explain why this task's scope is `Container` only, not both call sites the audit observed.

### Desired invariant

`Container.Shutdown` can be called any number of times without re-running the subsystem shutdown fan-out more than once; a second (or Nth) call is a safe no-op with respect to the subsystems it stops.

## What to do

1. Add a guard field to the `Container` struct — a `sync.Once` is the simplest, most idiomatic fit: wrap the entire existing `Shutdown` body in `c.shutdownOnce.Do(func() { ... })`. Unlike `lifecycle.Manager.Shutdown`, `Container.Shutdown` has no legitimate reason for a second caller to block until the first call's fan-out completes and then re-run any of it — `sync.Once` already gives any second caller safe blocking-until-first-call-completes semantics for free, with less code than replicating `lifecycle.Manager`'s closed-flag-plus-naturally-idempotent-primitives approach.
2. Locate the `Container` struct's field declarations (earlier in `container.go`) and add the new field in a sensible location near other lifecycle-related state, if any exists there already.
3. Re-verify `container.go:1424` is still `Shutdown`'s current start line before editing — cited from direct reading during this task's authoring pass; confirm no drift since.
4. Do not modify `internal/lifecycle/lifecycle.go` — `Manager.Shutdown` is already correct and is the reference this task is matching, not changing.
5. Do not modify `internal/runtime/agent/manager.go`'s `Session.Stop` — see the "related, smaller observation" above; explicitly out of scope for this task.

## Non-goals

- Do not add idempotency guards to the individual subsystems `Shutdown` fans out to (`c.Workers.Shutdown`, `c.Chat.Shutdown`, `c.Tasks.Snapshot`, etc.) — this task guards `Container.Shutdown` itself from being re-entered; it does not audit or fix every subsystem's own double-call safety, which is a larger, separate scope.

## Tests required

- A new test in `internal/service` (co-located with `Container`'s existing tests, using whatever test-construction helper already exists for building a `Container` in tests) that calls `Shutdown()` twice — sequentially, and ideally also concurrently via two goroutines — and asserts: no panic; the call returns/completes within a bounded time on both invocations; and (if the underlying subsystems expose an easy way to observe it, e.g. a call counter on a test double) that subsystem `Shutdown`/`Stop` calls are only invoked once. If no such call-count hook is readily available, at minimum confirm the second call doesn't block forever or panic on a double-close of an already-closed resource (a real risk without the guard — e.g. a channel closed twice panics in Go).

## Prevention

The new double-`Shutdown` test is the direct regression check for this specific case. More broadly this is a concrete instance of the guide's own "Lifecycle Ownership" standard (Wave 7, §4: "Every goroutine/background worker/resource has an explicit owner and shutdown path..."), which this task advances by bringing `Container` in line with the `lifecycle.Manager` reference pattern the audit already holds up as the standard to match.

## Verification

```bash
go build ./internal/service/...
go vet ./internal/service/...
go test ./internal/service/... -race -run TestContainer -v
```

(Adjust the `-run` pattern to whatever the new test is actually named once written.) PASS: the double-`Shutdown` test passes with no panic; existing `Container`-related tests are unaffected.

## Risk / rollback

Very low risk — wrapping an existing function body in `sync.Once` does not change single-call behavior at all; it only makes a second call safe instead of undefined. Rollback is a single-commit revert.

## Done means

- [ ] `Container.Shutdown` is guarded (`sync.Once` or equivalent) so a second call does not re-run the subsystem shutdown fan-out.
- [ ] New test demonstrates calling `Shutdown()` twice is safe (no panic, bounded completion).
- [ ] `internal/lifecycle/lifecycle.go` and `internal/runtime/agent/manager.go` are unmodified.
- [ ] `go build`, `go vet`, `go test ./internal/service/... -race` all pass.

## Work log

<!-- Worker fills in: what was actually done, any deviation from plan and why. -->

## Review notes

<!-- Reviewer fills in: pass/fail, what was independently re-verified. -->
