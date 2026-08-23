# Decide the fate of team semantic routing (`TeamRoutingService`) — a self-flagged, unresolved risk

**Phase:** Wave 4 — Production islands (per remediation guide §4)
**Status:** implemented
**Depends on:** none within this batch.
**Touches:** `internal/service/team_routing.go`,
`internal/service/container.go`, `cmd/nanite/main.go`,
`internal/api/team_runs.go` (`handleLaunchTeam`) and its test,
`TASKS/teams/HANDOFF.md`, this task file, and the mutable audit-remediation
`findings.json`. The composition-root additions were discovered by the required
current-source re-verification; see Work log.

```yaml
requires_architect_decision: true
requires_security_review: false
requires_regression_test: true
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 4 — production islands · **Dispatch unit:** `W4`
> - **Depends on:** `00/01`'s reachability report
> - **Blocks:** none
> - **Parallel-safe with:** `09/01`, `09/02`, `09/04`–`09/06`
> - **Gated on:** AD-08 (wire / defer / retire)
> - **requires_security_review:** false · **requires_regression_test:** true

> ## ✅ AD-08 DECIDED (2026-08-22) — WIRE IT. The only island of six that is kept.
>
> Call `InstallTeamRunRouting` from `handleLaunchTeam`
> (`internal/api/team_runs.go`), which today calls only `LaunchTeamRun`.
>
> **Unit tests are not sufficient to close this.** The guide's four-step
> reachability proof is the acceptance bar: production entry point →
> construction/registration/wiring → feature invocation → observable behaviour.
> Being unwired-but-well-tested is precisely how this island came to exist.
>
> Why this one and not the other five: **927 lines of tests against 771 of
> implementation**, and `TASKS/teams/HANDOFF.md` named the missing wiring as a
> known risk rather than leaving it accidental. This is a feature that ran out
> of runway one call short.

## Context

### Findings addressed

- **GO-SVCEXEC-003** (medium, confidence high) — Team semantic routing
  (`TeamRoutingService`'s entire production surface — `SendToSlot`,
  `InstallTeamRunRouting`, `resolveAgentSlugForSlot`, etc.) has zero
  production callers. The intended wiring point (a future HTTP launch
  handler) has since shipped as `internal/api/team_runs.go`'s
  `handleLaunchTeam`, but it calls only `LaunchTeamRun`, never
  `InstallTeamRunRouting`. A fully-implemented, extensively-tested feature
  (`@slot` explicit addressing, priority-ranked reflex-based routing)
  currently cannot fire.

Source: `docs/audits/2026-08-21-go-quality/REPORT.md` §8.4 and
`docs/audits/2026-08-21-go-quality/findings.json`.

### This is not a fresh discovery — it's a known, self-flagged risk

`TASKS/teams/HANDOFF.md:30` **already names this exact risk explicitly** and
asks a future reader to verify it, in the Teams batch's own handoff document,
written before this audit ran:

> `team_routing.go` — `TeamRoutingService`, `SendToSlot` (explicit `@slot`
> addressing, broadcast-to-all-active default), `InstallTeamRunRouting(ctx,
> runID, teamID)` (semantic + coordinator-fallback routing, installs
> run-scoped `agent_reflexes` rows). **Not called from inside
> `LaunchTeamRun`** — must be called after it, by whatever launches a
> TeamRun (currently only task `11`'s HTTP handler does this composition;
> check whether it still does before assuming any other caller wires it).

The audit did exactly what this line asks — checked whether task 11's HTTP
handler (`handleLaunchTeam`) still does the two-step composition — and
confirmed it does not: the gap the Teams batch's own authors anticipated as a
risk when they wrote the handoff document was never closed. This task
inherits that self-flagged risk rather than surfacing a new one. Treat
`TASKS/teams/HANDOFF.md` in full (not just line 30) as required background —
it documents several other deliberate, related design choices (routing
failure modes, lazy-slot resolution timing) that any "wire" implementation
needs to respect.

### Root cause

`team_routing.go`'s own package-level comment (lines 1-27) documents the
intended composition explicitly:

```go
// internal/service/team_routing.go:14-24
// This task's own item 2 leaves the exact install-time wiring boundary to
// the worker: "called from task 08's launcher, or from this task's own
// function that 08 would call." Task 08 (team_run_launcher.go) is already
// merged and reviewed — this file does NOT retrofit a call into
// LaunchTeamRun itself. Instead, InstallTeamRunRouting (below) is a
// standalone, independently-callable function, intended to be invoked by
// whatever caller assembles a full "launch a TeamRun" operation AFTER
// TeamRunLauncher.LaunchTeamRun returns a real workflow_runs.id — most
// concretely, TASKS/teams/11-team-run-launch-api.md's future HTTP launch
// handler (not yet built as of this task), which calls LaunchTeamRun then
// InstallTeamRunRouting in sequence, the same two-step composition this
// file's own tests use.
```

Task 09 (`team_routing.go`) deliberately did not call `InstallTeamRunRouting`
from inside `LaunchTeamRun` (task 08), by design — the composition was
explicitly left for whichever caller assembles the full "launch a TeamRun"
operation. Task 11 (`internal/api/team_runs.go`'s `handleLaunchTeam`) was
that intended caller. It shipped, but the second half of the composition —
the `InstallTeamRunRouting` call — was never added.

### Current behavior

`handleLaunchTeam` (`internal/api/team_runs.go:130-187`) in full — the entire
function only calls `LaunchTeamRun`:

```go
// internal/api/team_runs.go:130-187
func (a *API) handleLaunchTeam(w http.ResponseWriter, r *http.Request) {
    ...
    result, err := a.Services.TeamRunLauncher.LaunchTeamRun(r.Context(), id, overrides)
    // internal/api/team_runs.go:152
    if err != nil {
        ...
    }
    // Best-effort: the launch itself already fully succeeded ...
    var members []store.TeamRunMember
    if a.Services.Store != nil {
        if ms, memberErr := a.Services.Store.ListTeamRunMembersByRun(r.Context(), result.RunID); memberErr == nil {
            members = ms
        }
    }
    a.jsonResp(w, http.StatusOK, teamLaunchResponse{...})
}
```

No call to `svc.InstallTeamRunRouting(ctx, result.RunID, teamID)` exists
anywhere in this function, or (confirmed by grep) anywhere else in
production code. `TeamRoutingService.InstallTeamRunRouting`
(`internal/service/team_routing.go:477`) has zero non-test callers.
`TeamRoutingService.SendToSlot` (`team_routing.go:267`) — the routing
mechanism `InstallTeamRunRouting` sets up reflex rows to eventually
trigger — is consequently also unreachable transitively, since nothing
installs the reflex rows that would cause it to be invoked. Because
`TeamRoutingService`'s own *type* is constructed and live elsewhere
(`NewTeamRoutingService`, `team_routing.go:206`), `deadcode` doesn't flag
`SendToSlot`/`resolveAgentSlugForSlot` individually — its heuristic doesn't
drill into per-method reachability once the enclosing type has any live
caller — which is why this audit's confirmation required the direct grep in
addition to the `deadcode` run, not `deadcode` alone.

Practical consequence: every TeamRun launched via `POST /api/teams/{id}/launch`
today runs with **no semantic or coordinator-fallback routing installed at
all** — `@slot`-addressed messages and priority-ranked routing rules, which
this feature was built specifically to provide, have no reflex rows backing
them for any run launched through the one production entry point that
exists.

### Desired invariant

Same disposition-clarity invariant framing as the other islands (see
`01-grounding-memory-recall.md`), with one addition specific to this island:
whatever is decided, `TASKS/teams/HANDOFF.md` (or a superseding doc) should
be updated to reflect the resolved state, so a *future* reader hitting line
30's open question gets a real answer instead of the same open question this
audit had to re-verify from scratch.

### Scope

- `internal/service/team_routing.go` — `TeamRoutingService`
  (`team_routing.go:181`), `NewTeamRoutingService` (line 206), `SendToSlot`
  (line 267), `InstallTeamRunRouting` (line 477),
  `insertTeamRoutingReflex` (line 641), `resolveAgentSlugForSlot` (line 687).
- `internal/api/team_runs.go` — `handleLaunchTeam` (lines 130-187), if
  "wire" is chosen.
- `TASKS/teams/HANDOFF.md` — read for full context on the routing-failure
  design decisions already locked by task 09 (fail-loudly on unavailable
  target, single lazy-resolution attempt, no automatic coordinator
  reroute — see the file's own "Routing-target resolution failure" section)
  that any "wire" implementation must preserve, not re-litigate.
- Also transitively in scope if "wire" is chosen: `resolveAgentSlugForSlot`
  and `ResolveLazySlot` — per the audit, `(*TeamRunLauncher).ResolveLazySlot`
  is also currently unreachable in production (though `deadcode` doesn't
  catch it, same enclosing-type heuristic gap as above) since nothing calls
  the routing installation that would exercise it via a lazy slot.

### All production callers

None for `InstallTeamRunRouting`/`SendToSlot`. `handleLaunchTeam` is the one
production caller of `LaunchTeamRun`; whether it should also become a caller
of `InstallTeamRunRouting` is exactly this task's wire/defer/retire question.

## What to do

1. **Re-verify against current source that this is still unreachable.** Per
   the remediation guide's Wave 4 warning — *"check current source first;
   some were completed after the audited commit"* (audited commit
   `8feeee5c`) — before treating anything above as current:
   - Re-read `internal/api/team_runs.go`'s `handleLaunchTeam` in full and
     confirm no `InstallTeamRunRouting` call has been added since.
   - Grep for `InstallTeamRunRouting(` across `internal/` (excluding test
     files) and confirm the only non-test references remain the function
     definition and its own package's doc comments.
   - Check whether `TASKS/teams/HANDOFF.md` or any newer Teams-batch task
     file has been updated to record a resolution to the line-30 risk since
     this audit ran.
   - If any of this has changed, correct this task's disposition and note
     it in Work log before proceeding.

2. **If still unreachable, present the wire/defer/retire options below to the
   architect.** Do not pick one.

   **Option — wire.** Add the missing second half of the composition
   `TASKS/teams/HANDOFF.md:30` describes: after `LaunchTeamRun` succeeds in
   `handleLaunchTeam`, call `svc.InstallTeamRunRouting(ctx, result.RunID, teamID)`
   before responding, handling its error path (decide: does a routing-install
   failure fail the whole launch response, or does the launch still return
   success with routing best-effort — same "best-effort doesn't mask a real
   failure" tension the function already navigates for its member-listing
   call at lines 174-179, worth resolving consistently with that existing
   precedent rather than inventing a new policy).
   - Tradeoff: this closes exactly the gap the Teams batch's own authors
     anticipated and asked a future reader to check — completing a known,
     designed-for composition rather than building something new. The
     consuming logic (`SendToSlot`, reflex-row-based routing) is already
     built, reviewed, and tested per the Teams batch's own task 09 review.
   - Tradeoff: `TASKS/teams/HANDOFF.md`'s own "Known limitations" section
     (see the file's discussion of run-scoped slot resolution timing) notes
     a real, already-flagged limitation — a Team Slot resolved lazily
     *after* `InstallTeamRunRouting` runs does not retroactively get its own
     asking-side semantic-routing rows. Wiring this in surfaces that
     existing, documented limitation into a code path that's actually
     exercised for the first time; it doesn't need to be fixed by this task,
     but it does need to stop being purely theoretical once real TeamRuns
     start using it.

   **Option — defer.** Leave `handleLaunchTeam` as-is, but record the
   decision explicitly rather than leaving it as an open question a second
   audit had to re-discover. Per the guide's requirement that a deferred
   island **must not look production-live in docs and should not impose
   unnecessary boot/runtime cost**:
   - Update `TASKS/teams/HANDOFF.md:30` (or add a dated addendum near it, if
     editing historical task-batch handoff docs isn't this project's
     convention — check for precedent before choosing) to state plainly
     that the composition was checked twice (task 09's own author, and this
     audit) and deliberately deferred, with a reason and trigger.
   - Confirm no user-facing docs or API descriptions of `POST /api/teams/{id}/launch`
     imply `@slot` semantic routing is active for launched runs.
   - No boot/runtime cost is paid today (the routing service is constructed
     but its install/dispatch methods are simply never invoked) — confirm
     this stays true.

   **Option — retire.** Remove `TeamRoutingService`'s production surface
   (`SendToSlot`, `InstallTeamRunRouting`, `resolveAgentSlugForSlot`,
   `insertTeamRoutingReflex`) and its dedicated tests, and update
   `TASKS/teams/HANDOFF.md` to record that semantic routing was built,
   evaluated, and explicitly not adopted, so nobody re-implements it later
   under the impression it was simply forgotten rather than considered.
   - Tradeoff: TeamRuns today function correctly without this — messages
     presumably route via whatever coordinator-fallback or broadcast
     mechanism exists independent of `SendToSlot` (verify what that actual
     current behavior is before assuming; if there is no fallback at all
     today, that's a materially different situation than "routing degrades
     to a simpler mechanism," and changes the retire tradeoff significantly
     — check this as part of the re-verification step).
   - Tradeoff: this is the most fully-designed, most extensively-documented
     island in this folder (an entire dedicated Teams-batch task, a
     dedicated design-doc section per the package comment's reference to
     `docs/engineering/architecture/15-teams.md`'s "Routing: real reuse, and
     one real gap" section) — retiring it discards real design work, not
     just code, and the design doc itself would need a corresponding update
     to avoid describing a retired feature as current architecture.

## Non-goals

- This task does not re-litigate the routing-failure-mode design decisions
  `TASKS/teams/HANDOFF.md` already documents as locked (fail-loudly on
  unavailable target, single lazy-resolution attempt, no automatic
  coordinator reroute) — if "wire" is chosen, those decisions carry forward
  unchanged unless the architect explicitly reopens them.
- This task does not fix the separately-flagged lazy-slot-resolution-timing
  limitation `TASKS/teams/HANDOFF.md` already documents as a known,
  accepted gap — wiring may surface it into real use for the first time, but
  fixing it is out of scope here unless the architect decides otherwise.

## Tests required

- If **wire**: an HTTP-level regression test on `handleLaunchTeam` asserting
  that after a successful launch, the run-scoped `agent_reflexes` rows
  `InstallTeamRunRouting` is documented to install actually exist (per
  `team_routing.go`'s own existing unit-test pattern for
  `InstallTeamRunRouting` in isolation — extend to the real HTTP path rather
  than only the service-level call). Also a test exercising the
  routing-install-failure path (however its error handling is resolved) to
  confirm it doesn't silently mask a launch failure or vice versa.
- If **defer** or **retire**: no new regression test required, but confirm
  the existing `team_routing.go` test suite (built by task 09) still passes
  as-is if deferred, or is removed cleanly (no orphaned helper/fixture code)
  if retired.

## Prevention

- **Production Reachability** (remediation guide's own named standard, §4
  Wave 7): *"A feature is not done until its production entry point, wiring,
  invocation, and observable behavior are proven."* This finding is a direct
  case study for that standard — task 09 built and reviewed a feature whose
  production entry point (task 11's handler) hadn't shipped yet, and no
  process step caught the composition gap once it did ship. If "wire" is
  chosen, consider whether task 11's own review should have included this
  check, and whether a similar forward-reference ("caller X, not yet built,
  is expected to call this") should get a follow-up verification step
  automatically tracked once the referenced caller lands — noted as a
  possible input to `12-quality-ratchet-and-standards/`, not resolved here.

## Done means

- [x] Current-source reachability re-verified (grep + direct read of
      `handleLaunchTeam`) and confirmed still open, or disposition corrected
      if it's changed since the audit.
- [x] Architect decision recorded: wire, defer, or retire.
- [x] `TASKS/teams/HANDOFF.md` updated (or a dated addendum added) to record
      the resolved disposition, closing the open question its own line 30
      raises.
- [x] If **wire**: `handleLaunchTeam` calls `InstallTeamRunRouting` after a
      successful `LaunchTeamRun`; error-handling policy for a routing-install
      failure explicitly decided and implemented; new HTTP-level regression
      test passes; the already-documented lazy-slot-resolution-timing
      limitation confirmed still accurately described once the path is
      actually live.
- [x] **Defer/retire criteria are not applicable; AD-08 selected wire.**
- [x] `go build ./...` and `go test ./...` pass after whichever direction is
      implemented.

## Work log

**2026-08-23 — implementation.** Re-verified the finding against
current source before editing: `handleLaunchTeam` still called only
`LaunchTeamRun`; the only non-test `InstallTeamRunRouting(` occurrence under
`internal/` was its definition; `TASKS/teams/HANDOFF.md` still carried the
unresolved wiring warning. A second construction-level grep corrected one
statement in this task's Context: `NewTeamRoutingService` also had zero
non-test callers, so the type was not "constructed and live elsewhere."
Completing AD-08's four-step reachability chain therefore requires both the
handler invocation and production construction alongside `TeamRunLauncher`.

The current code also corrected another Context premise without changing the
locked action. Installed semantic/coordinator `dispatch_to_agent` reflexes run
through `chat_reflex_dispatch`/`task_execute`; they do not call `SendToSlot`.
`SendToSlot` and its `ResolveLazySlot` path belong to the separate explicit
Team-Slot messaging surface, which still has no production self-tool/UI caller.
Per the execution process's instruction-vs-rationale rule, this task implements
AD-08 exactly as decided — make `InstallTeamRunRouting` live from the HTTP
launch path and prove its `agent_reflexes` rows — without inventing an
out-of-scope explicit-addressing entry point.

Baseline before implementation: `go build ./cmd/nanite/`, `go vet ./...`, and
`go test ./...` all passed. A new HTTP-path assertion was then added to the
existing end-to-end launch test; before the handler call was added it failed
red with zero run-scoped routing rows, confirming the test detects this exact
production-island regression rather than merely re-testing the service in
isolation.

**Implementation.** Added `Container.TeamRouting` and constructed the service
in `cmd/nanite/main.go` immediately after `TeamRunLauncher`, using the live
Store, Messaging service, and launcher. `handleLaunchTeam` now preflights both
dependencies before creating a run, then invokes `InstallTeamRunRouting` only
after `LaunchTeamRun` returns the real run id and persisted member rows. The
existing HTTP end-to-end test now authors one semantic rule plus coordinator
fallback and observes four real run-scoped `dispatch_to_agent`
`agent_reflexes` rows through `ListAgentReflexesForWorkflowRun` (two rules for
each of two distinct eagerly-resolved asking agent identities).

**Routing-install failure policy (operator-approved 2026-08-23): fail closed
without pretending rollback.** `LaunchTeamRun` performs multiple committed
writes and no `DeleteWorkflowRun`/transactional rollback path exists.
`InstallTeamRunRouting` likewise inserts rows sequentially and returns the ids
inserted before an error. Therefore a routing-install failure returns HTTP 500
as a structured `teamLaunchResponse` containing the already-persisted
`workflow_run_id`, status, and explicit routing error stating that the run
remains persisted. The handler attempts to delete every returned partial
reflex id first, using `context.WithoutCancel` so request cancellation does not
prevent the cleanup attempt. It reports any cleanup failure rather than
masking it. The result is neither a best-effort 200 for an incompletely routed
TeamRun nor a generic error response that hides a live run and encourages a
duplicate retry.

Regression coverage locks both halves of that policy:

- `TestTeamRunLaunchAPI_ServiceUnavailableWhenRoutingNotWired` proves missing
  composition-root wiring returns 503 before the workflow-run count changes.
- `TestTeamRunLaunchAPI_RoutingInstallFailureReturnsRunAndCleansPartialRows`
  uses a valid-first/invalid-second routing definition: the first rule inserts
  two rows, the unknown target Team Slot fails the second rule, and the
  response returns the persistent run id/status plus the routing error. The
  test separately reads the store to verify that the run and member rows
  persist, while partial cleanup leaves zero run-scoped reflex rows.

`TASKS/teams/HANDOFF.md` now closes its original open wiring question, records
the failure policy, preserves the lazy-Team-Slot-after-install limitation, and
states the `SendToSlot`/`ResolveLazySlot` source correction explicitly.

**Verification after implementation.** Focused HTTP regression set passed;
`go test ./internal/api/... ./internal/service/... ./internal/store/...
./internal/agentworkflow/... -count=1` passed; the locked routing/lazy-resolution
tests passed under `go test -race ./internal/service -run
'Test(SendToSlot_TargetUnavailable|ResolveActiveMembers_ConcurrentLazyResolution_OnlyResolvesOnce|InstallTeamRunRouting)'
-count=1`; and the full `go build ./...`, `go vet ./...`, `go test ./...`
baseline passed. `gofmt` and `git diff --check` are clean. No schema migration
was involved.

## Review notes

**Fresh review (2026-08-23): FAIL — tracking-only corrections required.** The
reviewer found no runtime-code defect. Two low-severity documentation/tracking
issues remained: `TASKS/INDEX.md` still showed `09/03` as `not-started`, and
the Work log said the structured 500 returned member state even though its
actual fields are the persistent run id, status, and routing error.

**Fix applied.** Synchronized the index row to `implemented` and corrected the
failure-test description to distinguish response fields from the separately
verified persisted `team_run_members` store rows. Runtime code and tests were
not changed. Final re-review is pending; no pass is claimed here.
