# Decide and fix `cmdServe`'s `slogx.Fatal` bypassing deferred startup cleanup

**Phase:** Wave 2 — Correctness, lifecycle, concurrency
**Status:** not-started
**Depends on:** none
**Touches:** `cmd/nanite/main.go` (`cmdServe`'s `slogx.Fatal` call sites, and possibly `cmdServe`'s own signature plus `main()`'s `case "serve":` branch, depending on which direction is chosen — see below). Possibly `internal/slogx/slogx.go` (`Fatal`, `FatalContext`) if the cleanup-hook direction is chosen. **This task requires an architect decision before implementation** — see Context and What to do.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 2 — correctness, lifecycle, concurrency · **Dispatch unit:** `W2b`
> - **Depends on:** `00/01`
> - **Blocks:** `07/05`, `08/07`, `11/10` — all three edit `cmd/nanite/main.go` after this
> - **Parallel-safe with:** `06/01`, `07/01`, `07/03`
> - **Gated on:** AD-17 (signature change vs. slogx cleanup hook)
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

`requires_architect_decision: true` — the remediation guide's own recommendation offers two directions (replace `slogx.Fatal` with return/structured-error propagation at these sites, **or** make `slogx.Fatal` itself accept a cleanup-hook slice), and this task's own investigation found `slogx.Fatal` has real call sites well beyond `cmdServe` — a scope question the audit did not resolve and that materially affects which direction is cheaper/safer. Per this project's guardrails, an implementation agent must not silently pick one direction; an architect (or the operator) must decide first.

### Findings addressed
- `GO-RUNTIME-001` — severity **low** (`findings.json`; described as "low-medium" in `REPORT.md` §8.13's prose), confidence **high**. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.13; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-RUNTIME-001`.

### Root cause

`slogx.Fatal` (`internal/slogx/slogx.go:138-141`) is:

```go
func Fatal(msg string, args ...any) {
    slog.Error(msg, args...)
    os.Exit(1)
}
```

`os.Exit(1)` terminates the process immediately and — per Go's own runtime semantics — never runs any `defer` registered anywhere on the calling goroutine's stack. `cmdServe` (`cmd/nanite/main.go:94-764`) registers several defers over the course of its ~670-line body: `defer logCloser.Close()` (line 140, only if the structured-logging handler initialized successfully), `defer otelShutdown(otelCtx)` (line 171, only if OTel init succeeded), `defer s.Close()` (line 179, the SQLite store), and `defer coordStore.Close()` (line 328, the Badger coordination store, only if it opened successfully). Every `slogx.Fatal` call that fires after one of these defers is registered silently skips it — the code visually promises cleanup via `defer` that never actually executes on these paths.

### Current behavior

Grepping `cmd/nanite/main.go` for `slogx.Fatal(` finds **7** call sites, all within `cmdServe`'s body (function spans lines 94-764): lines 177, 183, 186, 196, 403, 430, 762. `REPORT.md` §8.13 and `findings.json`'s summary for `GO-RUNTIME-001` both describe this as "**5** real startup-failure call sites" without enumerating which five. **This is a real discrepancy this task's authoring pass could not resolve** — treat "5" as the audit's own approximate count and "7" as this task's own directly-observed one; whoever implements this must re-derive the current, real list of call sites from source (`grep -n "slogx.Fatal(" cmd/nanite/main.go`) rather than trusting either number blindly, since unrelated churn may also have changed the count since the audit commit (`8feeee5c`).

Per-site blast radius, traced by defer-registration position (verified by direct reading during this task's authoring pass):

| Line | Failure | Defers already registered at this point (bypassed) |
|---|---|---|
| 177 | `store.New` fails | log-handler close (140), OTel flush (171) — no store to close yet |
| 183 | `s.Seed()` fails | + SQLite store close (179) |
| 186 | `s.SeedProviders()` fails | + SQLite store close (179) |
| 196 | `envelopes.LoadCore` fails | + SQLite store close (179) |
| 403 | `service.NewContainer` fails | + Badger coordination-store close (328) — **all four** named cleanups |
| 430 | `agentworkflow.LoadRegistryDir` fails | all four |
| 762 | `srv.ListenAndServe()` returns an error | all four (last statement in `cmdServe`) |

Bounded blast radius, as `findings.json`'s `false_positive_considerations` states: "process always terminates immediately after, no accumulating leak" — `os.Exit(1)` tears down the entire process, so nothing survives past that instant (no goroutine keeps running, no descriptor persists beyond process lifetime). The defect is a gap between the code's own apparent promise (`defer`) and what actually executes — unflushed OTel spans/logs at the moment of the fatal error, and a SQLite/Badger store possibly left without a clean-close marker on disk — not a leak that compounds over time.

`main()` calls `cmdServe` at `cmd/nanite/main.go:71` (`case "serve": cmdServe(os.Args[2:])`), inside a `switch` with no further handling after the call — `cmdServe` currently has no return value.

### Desired invariant

When `cmdServe` hits a fatal startup condition, every defer registered earlier in the function's execution runs before the process exits.

## What to do

**Architect must choose between the two directions below before an implementer proceeds.** Both are described concretely enough to decide from; do not treat either as pre-selected.

### Direction A — replace `slogx.Fatal` with return/structured error propagation, scoped to `cmdServe`

Give `cmdServe` an error return (or an explicit exit-code return) and replace each of the (re-verified, current) `slogx.Fatal(...)` calls with `slog.Error(...); return fmt.Errorf(...)` (or an equivalent sentinel/wrapped error). Move the actual `os.Exit(1)` to `main()`'s `case "serve":` branch: `if err := cmdServe(os.Args[2:]); err != nil { os.Exit(1) }`. Because the exit now happens in `main()`'s stack frame, after `cmdServe` has already returned (running all its own defers on the way out), every cleanup the code currently promises actually executes.

This is a small, mechanical, same-shape edit — `REPORT.md` §8.13 already judged `cmdServe`'s existing linear if-err-fatal shape as **essential complexity, correctly sequenced** ("a single linear composition-root sequence where nearly every step genuinely depends on the prior one's output... matches the guide's own explicit allowance for this shape"), so converting each `slogx.Fatal(...)` call to `slog.Error(...); return ...` does not change that structure, only the exit mechanism. Scope is narrow: `cmdServe`'s own call sites plus `main()`'s one call site.

### Direction B — make `slogx.Fatal` itself accept a cleanup-hook slice

Extend `slogx` with something like `FatalWithCleanup(msg string, cleanup []func(), args ...any)`, or a package-level registerable hook list that `Fatal` drains before `os.Exit(1)`. This is a shared-primitive change with a real, larger blast radius than `cmdServe` alone: `slogx.Fatal` has **22** total call sites repo-wide — 7 in `cmd/nanite/main.go` (all `cmdServe`'s, per the table above), 14 in `cmd/nanite/message_cmd.go`, and 1 in `cmd/nanite/plugin_cmd.go`. This task's authoring pass did **not** trace the `message_cmd.go`/`plugin_cmd.go` call sites' own defer contexts — per the guide's "fix every sibling path" principle for a shared primitive, choosing Direction B means every one of those 15 other call sites needs to be individually assessed for whether it has cleanup to bypass and whether it should adopt the new cleanup-hook form, or the change only benefits `cmdServe` while leaving the other 15 exactly as unprotected as before (in which case Direction B buys nothing over Direction A for this specific finding, at higher implementation cost).

### Recommendation for the architect to weigh (not a decision made here)

Direction A is narrower, lower-risk, and fully resolves `GO-RUNTIME-001` on its own terms without requiring any judgment about 15 unrelated call sites this audit didn't examine. Direction B is only worth its larger cost if there's an independent reason to want a reusable cleanup-hook primitive on `slogx.Fatal` for future call sites beyond this finding.

## Non-goals

- Do not touch `message_cmd.go`'s or `plugin_cmd.go`'s `slogx.Fatal` call sites unless Direction B is explicitly chosen — those are CLI one-shot commands with their own, unaudited defer contexts, out of scope for `GO-RUNTIME-001` under Direction A.
- Do not restructure `cmdServe`'s overall sequencing beyond the mechanical `Fatal`→`return` substitution — §8.13 already judged the linear shape essential; this is not a refactor task.

## Tests required

- A test (or tests) exercising `cmdServe`'s startup-failure paths that were previously untestable because they called `os.Exit(1)` directly — once Direction A makes these `return`-based, add coverage asserting that on a simulated failure (e.g. an unopenable DB path forcing `store.New` to fail), `cmdServe` returns a non-nil error and does **not** call `os.Exit` itself (verify via the function returning normally in the test, not via a subprocess-exit-code check). If full end-to-end `cmdServe` testing is impractical given its composition-root scope, at minimum test that logCloser/otelShutdown/store-close/coordStore-close are invoked (e.g. via injectable close-tracking fakes, if such seams exist or can be added) when an error return path is taken.
- If Direction B is chosen instead: a test on `slogx.FatalWithCleanup` (or equivalent) confirming registered cleanup hooks run before `os.Exit` — note `os.Exit` itself is not mockable/testable in-process in the usual case, so this likely needs a subprocess-based test pattern (check whether one already exists elsewhere in this repo's `cmd/nanite` tests before inventing one).

## Prevention

A regression test on the chosen direction's failure-path behavior is the direct prevention mechanism. More broadly, this is an instance of the guide's "Lifecycle Ownership" standard (Wave 7): a resource opened with an explicit cleanup path (`defer`) should not have that path defeated by the same function's own error-handling convention — worth naming as a pattern if a future audit finds `slogx.Fatal` (or a similarly hard-exiting helper) used again inside a function with earlier-registered defers.

## Verification

```bash
go build ./cmd/nanite/...
go vet ./cmd/nanite/...
go test ./cmd/nanite/... -v
```

Observable behavior required for PASS: whichever direction is chosen, a simulated startup failure at one of the (re-verified, current) former `slogx.Fatal` sites results in `logCloser.Close()`/`otelShutdown(...)`/`s.Close()`/`coordStore.Close()` (whichever were registered by that point) actually executing before process exit — demonstrated by a test, not just code inspection.

## Risk / rollback

Direction A: low-medium risk — changing `cmdServe`'s signature from `func(args []string)` to something returning an error touches its one caller (`main()`) and needs careful handling of the "success" case where `cmdServe` currently blocks forever on `srv.ListenAndServe()` and only returns on error (the happy path never returns a nil-error case today; decide explicitly whether that stays true or whether a clean-shutdown path should also return nil through the same mechanism). Direction B: higher risk due to the 15-site blast radius outside `cmdServe`, per the "fix every sibling path" concern above. Rollback for either: revert to `slogx.Fatal` at the affected sites; the current behavior (silent cleanup bypass, bounded blast radius) resumes, which is a known-safe fallback state, not a regression beyond today.

## Done means

- [ ] Architect decision recorded: Direction A or Direction B, with rationale, before implementation starts.
- [ ] All of `cmdServe`'s (re-verified) `slogx.Fatal` call sites are converted to the chosen mechanism; deferred cleanup (log-handler close, OTel flush, SQLite store close, Badger coordination-store close) runs before process exit on every one of them.
- [ ] If Direction B: all 15 non-`cmdServe` `slogx.Fatal` call sites (`message_cmd.go`, `plugin_cmd.go`) have an explicit, recorded disposition (migrated to the new form, or explicitly left as bare `slogx.Fatal` with rationale) — not silently unaddressed.
- [ ] New test(s) demonstrate cleanup actually executes on a simulated startup failure.
- [ ] `go build`, `go vet`, `go test ./cmd/nanite/...` all pass.

## Work log

<!-- Worker fills in: what was actually done, any deviation from plan and why, which direction was chosen and by whom. -->

## Review notes

<!-- Reviewer fills in: pass/fail, what was independently re-verified. -->
