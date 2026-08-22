# Decide and fix `internal/background` job-registry unbounded growth (+ fix the false "reaped" claim)

**Phase:** Wave 2 — Correctness, lifecycle, concurrency
**Status:** implemented
**Depends on:** none
**Touches:** `internal/background/service.go` (`Service`, `jobRecord`, `Submit`, `onBackendComplete`, `Status`, `Result`); `internal/background/pty.go` (`PTYBackend`, `ptyJob`, `Status`'s doc comment). **This task requires an architect decision before the retention-policy half is implemented** — see Context and What to do. The doc-comment fix is required regardless of that decision.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 2 — correctness, lifecycle, concurrency · **Dispatch unit:** `W2b`
> - **Depends on:** `00/01`
> - **Blocks:** none
> - **Parallel-safe with:** `06/01`, `07/01`, `07/02`
> - **Gated on:** AD-18 — **retention half only.** The doc-comment fix is required regardless and is not gated.
> - **requires_security_review:** false · **requires_regression_test:** true

> ## ✅ AD-18 DECIDED (2026-08-22) — TTL plus count cap; evicted must not read as unknown
>
> Evict completed jobs on a TTL with a hard ceiling on retained records.
>
> **`Status`/`Result` must return a distinct "expired" state, not not-found.**
> Silently turning "job succeeded" into "job unknown" is a different bug, not a
> fix — this is the part most likely to be got wrong, so make it explicit in
> the type, not just the docs.
>
> Rejected: count-only LRU (a burst can evict a result before its owner reads
> it, with no time-based guarantee) and persist-to-store (needs a schema
> migration this batch otherwise claims none of, and turns a correctness fix
> into a storage feature).
>
> **The leak has a concrete rate.** The only `delete(svc.jobs, jobID)` in
> `internal/background/service.go` is on the immediate `backend.Start` failure
> path (`:126`); every job that actually *starts* is retained for the process
> lifetime holding up to `DefaultMaxOutputBytes` (1 MiB) of output. Roughly
> 1 MiB leaked per background job, indefinitely.
>
> **The doc-comment fix is no longer gated** — `PTYBackend.Status` falsely
> claims completed jobs are "reaped". Required regardless of the retention work.

## Context

`requires_architect_decision: true` — either add a TTL/LRU eviction policy, or explicitly accept unbounded retention as intentional for this package's current MVP scope. Either way, `PTYBackend.Status`'s doc comment must be corrected — it currently states something the code does not do.

### Findings addressed
- `GO-RUNTIME-004` — severity **medium**, confidence **medium**. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.13; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-RUNTIME-004`.

### Root cause

Both of `internal/background`'s job registries — `Service.jobs` (`internal/background/service.go:55`, `jobs map[string]*jobRecord`) and `PTYBackend.jobs` (`internal/background/pty.go:72`, `jobs map[string]*ptyJob`) — are populated on every job submission and have no eviction path for completed jobs.

In `Service`, the **only** deletion anywhere in `service.go` is at `Submit`'s own line 126, `delete(svc.jobs, jobID)`, which fires exclusively when `svc.backend.Start` returns an error **synchronously** — i.e., the job never actually started running. Every job that reaches `StatusRunning` and later finishes (success, failure, or cancellation) goes through `onBackendComplete` (`service.go:203-243`), which mutates the existing `jobRecord` in place (`status`, `output`, `err`, `completedAt`) but never removes it from `svc.jobs`. That job's record — including its captured output — stays in memory for the rest of the process's lifetime.

In `PTYBackend`, there is **no deletion of any kind** anywhere in `pty.go` — confirmed by grepping the file for `delete(b.jobs` (zero matches). Jobs submitted through `PTYBackend` never leave its map at all, regardless of terminal state.

### Retained memory per job

`jobRecord`/`ptyJob` retain the job's full captured output, capped per-job at `DefaultMaxOutputBytes = 1 << 20` (1 MiB) — `internal/background/types.go:89-92`. So each individual job's footprint is bounded, but total registry size is `(number of jobs ever submitted) × (up to 1 MiB each)`, unbounded across the process's uptime.

### The false "reaped" claim

`PTYBackend.Status`'s doc comment (`internal/background/pty.go:219-222`) reads:

```go
// Status returns the lifecycle state of jobID. ErrUnknownJob when
// the backend has no record (job either never existed or was reaped
// out of the map after completion — the Service maintains its own
// authoritative record either way).
func (b *PTYBackend) Status(jobID string) (JobStatus, error) {
```

No reaping logic exists anywhere in `pty.go` — this claim is false today, independent of whichever direction is chosen below for the retention-policy question. This is worth fixing on its own even if the architect decides unbounded retention is acceptable for now: a stale comment claiming a safety mechanism exists that doesn't is itself misleading to a future reader debugging memory growth.

### Production reachability

The single production entry point into `Service.Submit` is the `background_job` self-tool: `internal/selftools/self_tools_transport.go:2257`, `id, err := st.Background.Submit(ctx, classify.PatternBackground, req)`. `Service`/`PTYBackend` are constructed once, at daemon boot: `internal/service/container.go:1216`, `backgroundSvc := background.NewService(background.NewPTYBackend(), messagingSvc)`. Registry growth therefore tracks cumulative `background_job` tool-call volume across the daemon's uptime — a figure `findings.json`'s `false_positive_considerations` notes was **not measured** in the audit.

### Desired invariant (contingent on the architect decision)

Either: registry size is bounded by an explicit TTL/LRU/count policy applied consistently to both `Service.jobs` and `PTYBackend.jobs`; or: unbounded retention is an explicit, documented design choice for this MVP scope, with `PTYBackend.Status`'s comment corrected to match reality either way.

## What to do

**Architect must choose between the two directions below.**

### Direction A — bound retention

Add a TTL and/or LRU/count-based eviction policy. Concretely: evict `jobRecord`s some fixed duration after `CompletedAt`, or cap the total number of tracked jobs with LRU eviction of the oldest-completed entries. This needs either a periodic background sweep — the existing in-repo pattern to follow is the reaper goroutines already wired in `internal/service/container.go` (`stopSubagentReaper`/`subagentReaper.Stop()` and `stopRuntimeReaper`/`runtimeReaper.Stop()`, both stopped during `Container.Shutdown`) — or an inline eviction check on `Submit`/`Status`/`Result`.

**Before implementing, verify precisely how `Service` and `PTYBackend`'s two separate job maps relate** — `PTYBackend.Status`'s own (currently false) doc comment claims "the Service maintains its own authoritative record either way," implying `Service.jobs` is meant to be the source of truth callers actually query, with `PTYBackend.jobs` as internal backend-side bookkeeping. If that's accurate, an eviction policy may only need to target one of the two maps (or the two need independent, deliberately-different retention windows) — trace both `Status`/`Result` call paths (do callers ever query `PTYBackend.Status` directly, or always through `Service`?) before assuming symmetric eviction is required.

### Direction B — explicitly accept unbounded retention as intentional MVP scope

`internal/background/pty.go`'s own package doc comment already frames this package as an MVP shape ("Despite the name, this MVP does NOT allocate a real PTY..." `pty.go:61-66`). If the architect judges realistic `background_job` volume makes this a non-issue for now, record that decision explicitly — a code comment on the `jobs` map field(s) stating retention is unbounded-by-design pending measured production volume, not a silent status quo.

### Either direction

Fix `PTYBackend.Status`'s doc comment (`pty.go:219-222`) to stop claiming reaping happens: describe either the real (unbounded, Direction B) behavior, or the new eviction policy (Direction A), accurately.

## Non-goals

- Do not build a general-purpose eviction/cache abstraction reusable elsewhere in the codebase — scope this to `internal/background`'s two job maps specifically.
- Do not change `DefaultMaxOutputBytes` (`types.go:92`) or the per-job 1 MiB output cap — that's a separate, already-correct bound, not part of this finding.
- Do not change the terminal-state duplicate-completion guard in `onBackendComplete` (`service.go:198-216`) except as strictly required to make it interact correctly with a new eviction path (see Tests required).

## Tests required

- If Direction A: a test that submits N jobs, lets them reach a terminal state, advances past the TTL (or triggers the eviction condition directly, without relying on real wall-clock sleep in the test if the implementation supports fake-time injection), and asserts the evicted job IDs return `ErrUnknownJob` from `Status`/`Result` while jobs within the retention window still resolve correctly.
- Regardless of direction: add a test for the specific edge case where an eviction (if implemented) could race a **late or duplicate** completion callback — `onBackendComplete`'s existing terminal-state guard (`service.go:211-216`) already defends against a backend firing the completion callback twice; confirm eviction doesn't reintroduce a crash/incorrect-state bug if a completion callback fires for a job ID that's already been evicted (e.g., `onBackendComplete` should treat "job ID not found" the same safe way it already treats "unknown job" — verify current behavior at `service.go:205-209` covers this, since eviction would produce the same `!ok` state that path already handles).

## Prevention

If Direction A: the new eviction test is the regression check for the bound itself. If Direction B: the corrected doc comment is the prevention — it stops a future reader from believing reaping already happens and re-investigating (or worse, building on top of) a false assumption.

## Verification

```bash
go build ./internal/background/...
go vet ./internal/background/...
go test ./internal/background/... -race -v
```

PASS: existing test suite (`service_test.go`, `pty_test.go`) continues to pass unchanged; if Direction A, the new eviction test passes; `PTYBackend.Status`'s doc comment no longer claims reaping unless Direction A actually implements it (in which case the comment should describe the real policy, not the old false one).

## Risk / rollback

Direction A carries real risk of evicting a job result before a caller has polled it — `background_job` is a poll-based tool per its self-tool shape, so the TTL/eviction window needs to be conservative (and ideally configurable) relative to realistic polling latency; get this reviewed rather than picking an arbitrary number. Direction B is zero functional risk (comment-only change). Rollback for Direction A: revert the eviction-sweep addition; `Service`/`PTYBackend` resume their current (unbounded but otherwise stable) behavior.

## Done means

- [ ] Architect decision recorded: bounded retention (Direction A) or explicit accepted-unbounded-for-MVP (Direction B), with rationale.
- [ ] `PTYBackend.Status`'s doc comment (`pty.go:219-222`) no longer falsely claims jobs are reaped; it accurately describes current behavior post-decision.
- [ ] If Direction A: eviction policy implemented consistently across whichever of `Service.jobs`/`PTYBackend.jobs` actually need it (per the authoritative-record investigation above), with a passing regression test.
- [ ] If Direction B: an explicit in-code comment records the accepted-risk decision on the relevant map field(s).
- [ ] `go build`, `go vet`, `go test ./internal/background/... -race` all pass.

## Work log

- 2026-08-22: Implemented the operator-approved AD-18 Direction A: `Service`
  retains completed results for 24 hours with a hard ceiling of 100 completed
  records. Retention runs inline on submit, completion, status, and result
  access; it never selects pending/running jobs. The clock and limits are
  injectable through an unexported test constructor, so TTL and count behavior
  are deterministic without sleeps.
- `Status` and `Result` now distinguish eviction from a never-issued id with
  `StatusExpired` plus `ErrExpiredJob`. Issued ids carry a random per-service
  prefix, which lets missing ids be classified without replacing the bounded
  result registry with an unbounded tombstone map. The `background_status`
  tool description includes the new state/error contract.
- Caller trace: production constructs exactly one `Service`/`PTYBackend` pair
  in `internal/service/container.go`; the `background_job` and
  `background_status` self-tools call `Service.Submit` and `Service.Result`.
  No production caller invokes `PTYBackend.Status` directly (its only direct
  callers are package tests), so `Service` is the retained-result authority.
  `PTYBackend` now deletes its process-only entry as completion is delivered.
  Its status comment now describes that real behavior instead of claiming a
  nonexistent post-completion retention/reaping policy.
- Correction to the task's audit-era rationale: `ptyJob` does not retain the
  captured output; the output is passed to the completion callback and retained
  by `Service.jobRecord`. `PTYBackend.jobs` was still independently unbounded,
  but only by process metadata, so immediate completion cleanup is the smallest
  consistent policy for that map.
- Added regressions for TTL eviction, within-window availability, the hard
  completed-count cap, explicit expired-vs-unknown semantics, protection of
  active jobs, late/duplicate completion after eviction, and backend process
  record cleanup. Pre-fix proof: the new backend cleanup regression failed with
  `completed backend job remains retained` before the production change.
- Verification: `go build ./internal/background/...`,
  `go vet ./internal/background/...`, and
  `go test -race ./internal/background/... -count=1 -v` pass; focused
  `internal/selftools` compilation passes; full `go build ./cmd/nanite/`,
  `go vet ./...`, and `go test ./...` pass.

## Review notes

<!-- Reviewer fills in: pass/fail, what was independently re-verified. -->
