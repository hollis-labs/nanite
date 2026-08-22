# [INVESTIGATION, NOT A KNOWN FIX] Determine why internal/service's own `go test -race` times out

**Phase:** Wave 2 — Correctness, lifecycle, concurrency (audit-remediation batch, unsequenced — see folder README)
**Status:** not-started
**Depends on:** none as a hard blocker, but land after task 02 (`02-fix-api-test-container-shutdown-leak.md`) if convenient — not because this task needs task 02's code, but because task 02's fix removes one theoretical confound from `internal/api` + `internal/service`'s *combined* 25-minute timeout dump (see Context) before this investigation draws conclusions. Sequencing-only.
**Touches:** No source changes expected as the primary deliverable of this task — see "What to do." If root-causing points to a concrete fix, that fix's scope depends entirely on what's found and is not knowable in advance.
**Requires architect decision:** false — but this is flagged explicitly as an **investigation task, not an implementation task**. Do not estimate or dispatch this like a task with a known fix; the deliverable is a root-cause determination backed by real evidence, and a *possible* follow-up fix task, not a guaranteed patch in this task itself.

## Context

This task implements **`GO-SVCCORE-006`** (medium severity, high confidence, category testing — `docs/audits/2026-08-21-go-quality/REPORT.md:791`, `report_section: "§8.3, corrects GO-TEST-001's scope"`).

**This is explicitly a DIFFERENT, unsolved problem from task 02 in this folder.** The remediation guide is explicit about this (guide "Container/reaper lifecycle" subsection, Wave 2: keep distinct "(3) `internal/service`'s own race timeout, whose cause was explicitly found to be different" from (2), the API test leak). `GO-SVCCORE-006` itself is framed by the audit as a correction to a *plausible-but-wrong* assumption in the base report (`GO-TEST-001`), which originally recommended checking `internal/service`'s own test suite "for the same gap" as `internal/api`'s. A later, more precise pass falsified that assumption directly:

### Findings addressed
- `GO-SVCCORE-006` — `internal/service`'s own `-race` timeout is not the Container-reaper-leak mechanism `GO-TEST-001` describes.

### Root cause — unknown; this is the thing to determine
**What's ruled out:** `internal/service`'s own `-race` timeout is confirmed **not** caused by the same mechanism as `internal/api`'s (task 02's target). Verified via exhaustive grep across all of `internal/service`'s test files: **zero calls to `NewContainer`** anywhere in the package's own test suite. Independently re-confirmed against current `HEAD` while authoring this task: `grep -rl "NewContainer(" internal/service/*_test.go` returns no matches, against `find internal/service -maxdepth 1 -name "*_test.go" | wc -l` = **126** test files and `grep -rc "^func Test" internal/service/*_test.go` totaling **776** test functions — both counts match the audit's own cited numbers exactly, so this part of the finding is current, not stale. Task 02's fix (`t.Cleanup(func(){ container.Shutdown() })` in `internal/api`'s test files) will do **nothing** for `internal/service`'s own timeout, because `internal/service`'s tests never construct a `Container` in the first place.

**What's suspected but not yet confirmed** (the audit names two candidates, not a determination):
1. Sheer suite size (776 test functions across 126 files) × `-race`'s per-goroutine instrumentation overhead against Go's default 10-minute per-package timeout — i.e., possibly a pure timeout artifact like `internal/messaging`/`internal/selftools`/`internal/subagent` turned out to be (those 3 packages, per `REPORT.md:530-533`, timed out at the default 10 minutes but **passed cleanly, zero `DATA RACE` reports**, once re-run with `-timeout 25m` — confirming they were timeout artifacts, not hangs). `internal/service` and `internal/api` did **not** clear even at 25 minutes, which is why the audit treats this "just a big suite" explanation as weaker for `internal/service` specifically ("no longer explainable as 'just a big suite'" — `REPORT.md:535`, said in the context of `internal/api`, but the same 25-minute non-completion applies to `internal/service` too per `REPORT.md:534`).
2. Accumulation from the untracked `safego.Go` spawns documented in this folder's task 04 (`GO-SVCCORE-002`) — ~18 fire-and-forget goroutine spawns in `events_composite.go` (and originally reported also in `delegation.go`/`agent_deps.go`, though re-verify current counts, see task 04's own note about drift there) with no owner able to drain them. If `internal/service`'s own test suite exercises code paths that trigger these spawns repeatedly across 776 tests, the same "goroutines accumulate across a `-race`-instrumented run" mechanism that made `internal/api`'s reaper leak so costly could apply here via a different source.

Neither candidate is confirmed. This task's job is to actually determine which (if either, or if it's something else entirely) is the real cause, with evidence.

## What to do

### Proposed direction
Run `go test -race -timeout 25m ./internal/service` (matching the audit's own extended-timeout methodology, `raw/test-race-extended-timeout.log`) with instrumentation to actually see what's running at timeout, rather than just observing pass/fail:
- A goroutine-dump-on-timeout harness — Go's `-race` timeout panic already includes a full goroutine dump (this is exactly how the audit found the 288 reaper-traced goroutines for `internal/api`+`internal/service` combined, `REPORT.md:558-561`); the next step is doing the same line-range-bucketing analysis the audit did for `internal/api`, but specifically isolating what fraction (if any) of `internal/service`'s own timeout dump traces back to `safego.Go` call sites in `events_composite.go`/`delegation.go`/`agent_deps.go` versus generic test-goroutine noise from a large suite.
- `GODEBUG=asyncpreemptoff=1` (or other `-race`-diagnostic `GODEBUG` flags) as a secondary tool if the goroutine dump alone doesn't distinguish "large suite is just slow" from "something is actually accumulating/hanging."
- Consider bisecting: does `-race` on a representative *subset* of `internal/service`'s 126 test files (e.g. excluding files that exercise `events_composite.go`/`delegation.go`'s event-emission paths) complete faster or within the default timeout? This would be direct evidence for or against candidate 2 without needing task 04's fix to land first.
- If task 02 has already landed by the time this runs, note that `internal/api`'s contribution to any *combined* dump is no longer relevant — this task should run `internal/service` in isolation (`./internal/service`, not `./internal/api/... ./internal/service/...` together) to avoid re-introducing that confound.

### Non-goals
- Do not attempt to fix whatever is found without first reporting the root cause — if the fix turns out to be small and obviously safe (e.g. it *is* candidate 1, a pure timeout artifact, and the fix is "bump the timeout" or "nothing to fix"), that's an acceptable outcome for this task to state, but do not silently expand into task 04's scope (fixing the untracked `safego.Go` spawns) as part of this investigation — if the evidence points to candidate 2, hand that off as a confirmed dependency/motivation for task 04 rather than implementing it here.
- Do not re-litigate whether `internal/api`'s timeout is the same or different — that's already settled (`GO-TEST-001`, task 02).
- Do not treat "the suite eventually completes at 25 minutes" as sufficient if it does — the audit's own bar was "obtain a real `-race` verdict," meaning zero `DATA RACE` reports observed once the run actually completes, not merely "didn't panic."

### Dependencies
Soft sequencing note only (see header) — not a hard code dependency on task 02 or task 04.

### Tests required
Not applicable in the usual sense (this is an investigation, not a code change) — but the investigation itself should produce a reproducible command/harness that a future task (or re-run of this one) can use to re-verify the conclusion, and should be logged in the Work Log below with actual output/evidence, not just a narrative conclusion.

### Prevention
Once root-caused, the actual prevention mechanism depends on what's found:
- If candidate 1 (suite size × `-race` overhead): the prevention is process/tooling — e.g. a documented `-timeout` override for this package in CI/local dev instructions, similar to how the audit already had to re-run `internal/messaging`/`internal/selftools`/`internal/subagent` at 25 minutes to get real verdicts.
- If candidate 2 (untracked goroutine accumulation): the prevention is task 04's fix (bringing `safego.Go` sites under `lifecycle.Manager` tracking where appropriate) plus, likely, the same "Lifecycle Ownership" standard cited in tasks 01/02/04.
- If neither: this task's own findings become the seed for a new, more targeted investigation or finding.

### Verification
Done means for this task is a **determination**, not a fix:
- A concrete, evidenced statement of which candidate (or other cause) is responsible, backed by an actual goroutine dump, bisection result, or equivalent artifact — not speculation.
- If a follow-up fix is warranted and not already covered by an existing task (i.e., not already covered by task 04's `GO-SVCCORE-002`/`GO-SVCCORE-001` scope), this task should name that follow-up explicitly (e.g. as a new task-file candidate for a planner to create) rather than leaving it as a loose end in the Work Log only.

### Risk / rollback
None — this is a read-only investigation. If it experiments with `GODEBUG` flags or subset bisection, those are local/CI-run-only and touch no committed code unless a fix is separately proposed and reviewed.

### Done means
- [ ] `go test -race -timeout 25m ./internal/service` run in isolation (not combined with `internal/api`), with goroutine-dump-on-timeout evidence captured.
- [ ] The dump (or a completed run's absence of `DATA RACE` reports) is analyzed and bucketed by call-site origin, the same methodology the audit used for `internal/api`'s reaper goroutines.
- [ ] A concrete conclusion is recorded: which candidate (suite size, `GO-SVCCORE-002` accumulation, or something else) is the real cause, with the supporting evidence cited inline (not just asserted).
- [ ] If the conclusion implies a fix beyond what task 04 already covers, that follow-up is named explicitly (new task-file candidate, or an addendum to task 04's scope) for planner attention.

## Work log

<!-- Worker fills this in as it goes. -->

## Review notes

<!-- Reviewer fills this in. -->
