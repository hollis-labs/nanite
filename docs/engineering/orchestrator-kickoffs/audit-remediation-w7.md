You are the Orchestrator for **Wave 7 of the Audit Remediation batch** —
dispatch unit `W7`, `TASKS/audit-remediation/12-quality-ratchet-and-standards/`
— the tenth of eleven dispatch units implementing the remediation program
derived from `docs/audits/2026-08-21-go-quality/REPORT.md` as sequenced by
`docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`. You have no memory
of the audit, the planning pass, or Waves 0-6's own execution — everything
you need is in the repo. **This kickoff covers two tasks: `12/01` and
`12/03`.** `12/02` (engineering standards docs) was pulled forward into
Wave 1 during planning and is already `reviewed` — it lives in this same
folder on disk but is not part of this dispatch; don't count it, don't
re-open it. Waves 0 through 6 are all closed — that is what makes this wave
dispatchable at all.

**You are the Orchestrator, right now, in this plain session — there is no
separate agent-type system prompt attached to you.** Read
`.claude/agents/orchestrator.md` first (item 1 below); it defines your exact
dispatch roster in full. In short: you dispatch exactly four leaf agent types
via the Agent tool — **worker** (implements one task file end to end),
**reviewer** (fresh review of a validated section, no shared context with the
worker who did it), **research-auditor** (read-only, verifies any claim
before you trust it — cannot write files or dispatch further agents),
**doc-writer** (end-of-wave handoff + summary docs). None of these four can
dispatch further agents themselves.

**Do not spawn another `orchestrator`, and do not dispatch a general-purpose
agent asked to "run this batch" or "coordinate the tasks."** That would
recreate this coordinating layer redundantly underneath you — a real failure
mode that has already happened once in this project.

**The repo-wide dev freeze (AD-24) is still in effect** — confirm this
directly (`TASKS/INDEX.md`'s banner) rather than assuming. `TASKS/audit-remediation/`
remains the only work authorized to proceed.

**No open gates this wave.** AD-21 (`12/01`'s gate) is `decided`. `12/03`'s
one dependency, `04/04`, is `reviewed`. Small wave, but `12/01` carries a
real trap worth as much care as any larger task in this batch.

---

## The thing that makes this wave worth reading closely: `12/01` ships in two stages, and the split *is* the decision

AD-21 didn't decide "add a lint gate" — it decided **which linters get gated
now versus which wait on a named prerequisite**, and got specific about why,
because the guide's default framing ("baseline and reject regressions") is
not what's actually being shipped for three of the linters.

**Stage 1 — now, no prerequisite.** Baseline every linter under
`docs/audits/2026-08-21-go-quality/audit-golangci.yml` and fail the gate on
any *increase* over that baseline. The empirical case is concrete, not
aspirational: across 40 commits of ordinary development between the audit
and Wave 0's frozen-`HEAD` refresh, with nothing watching, the counts moved
**gosec +35, cyclop +25, gocyclo +25, gocognit +16, errcheck +10**. That
drift is the argument for Stage 1 existing at all, and it's already recorded
in AD-21's own text — cite it, don't re-derive it.

**Stage 2 — gated, do not activate.** Zero-tolerance (fail on *any* finding,
not just increases) on `errcheck`, `errorlint`, and `nilerr` specifically.
These three are singled out on real evidence, not swept in with the rest:
**`nilerr` already caught `GO-STORE-003`** (a high-severity finding from
earlier in this batch) and nobody saw it, because the fast pre-commit hook
runs `golangci-lint run --new` — changed lines only — which structurally
cannot surface a pre-existing finding sitting in untouched code. Stage 2's
prerequisite is `14/02` (the error-handling backlog paydown, `Wave 9 —
Follow-ups`), and `14/02` **has not run** — it's still `not-started` in
`TASKS/INDEX.md`, and its own row there states its dependency as `12/01`
stage 1, not the other way around: Stage 1 has to exist first so `14/02` has
something to paydown *against*, and Stage 2 can't activate until `14/02`'s
backlog is zero. **Do not activate Stage 2 in this wave.** A gate that fails
every merge from day one — which is exactly what zero-tolerance on a
365-finding (now re-measured, see below) backlog would do — gets disabled
within a week and takes Stage 1 down with it.

**The trap, named explicitly because it's the specific way this goes
wrong:** do not let an implementer read "zero-tolerance" and conclude
"regression-gate these three linters too, same as the rest, just with a
tighter threshold." **That is a weaker decision than the one AD-21 actually
made**, and it silently discards the entire reason these three were split
out in the first place — `nilerr`'s history with `GO-STORE-003` is
specifically why they need zero tolerance eventually, not why they should
be folded into Stage 1's baseline-and-reject-regressions treatment now.
Brief the `12/01` worker on this distinction directly; it is easy to
compress "two stages, one gated" into "one gate, two thresholds" while
implementing, and that compression is exactly wrong.

## Things about this specific wave that won't be obvious from the batch README alone

**(A) The 365-finding backlog figure in `12/01`'s own file is stale — here
is the current number, independently re-measured while writing this
kickoff.** `12/01`'s AD-21 banner cites errcheck 294 + errorlint 49 +
nilerr 22 = 365, measured at Wave 0's frozen `HEAD`. Six waves of production
code changes have landed since. Re-run directly against current `HEAD`:

```bash
golangci-lint run -c docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --max-issues-per-linter=0 --max-same-issues=0 --enable-only errcheck ./...
golangci-lint run -c docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --max-issues-per-linter=0 --max-same-issues=0 --enable-only errorlint ./...
golangci-lint run -c docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --max-issues-per-linter=0 --max-same-issues=0 --enable-only nilerr ./...
```

| | frozen `HEAD` | now |
|---|---:|---:|
| errcheck | 294 | **281** |
| errorlint | 49 | **48** |
| nilerr | 22 | **24** |
| **total** | 365 | **353** |

errcheck and errorlint moved down (expected — six waves of fixes touching
adjacent code). **`nilerr` moved up, from 22 to 24** — worth flagging
directly to whoever eventually runs `14/02`, since `nilerr` is specifically
the linter with the `GO-STORE-003` history motivating Stage 2's existence.
This doesn't change anything about *this* wave's dispatch (Stage 2 stays
inactive regardless of the exact number), but don't let `12/01`'s own
committed figure stand as the "current" one — update it, or at minimum note
in the Work Log that it's superseded, when this task lands.

**(B) `12/01` and `12/03` both nominally list `Makefile` under `Touches` —
this is not a real conflict, confirm before assuming it is.** `12/01`'s own
`Touches` field is explicit that it reads the `Makefile`'s `lint` target
only to establish a baseline and does **not** edit it — one of its own
Non-goals states this directly: *"Do not change `.golangci.yml` or the
`Makefile`'s `lint`/`vuln` targets."* `12/03` edits a different target
entirely (`lint-goroutines`, `Makefile:74-81`). Independently re-verified
against current source while writing this kickoff — the Makefile's
`lint-goroutines` target still matches `12/03`'s own citation exactly: the
same eight hardcoded packages (`internal/plugin`, `internal/worker`,
`internal/mcp`, `internal/service`, `internal/server`, `internal/api`,
`internal/memory`, `internal/workflow`), and the leading `@-` that keeps it
non-fatal — so `12/03`'s fix doesn't gate anything either, before or after.
`12/01` and `12/03` are safe to dispatch in parallel.

**(C) Race verification is now the standard, not an aspiration — Wave 6
already proved it out.** `14/03` (the test-fixture migration-cost fix)
removed the structural excuse that let every wave from Wave 2 through Wave 5
defer or skip its aggregate `-race` verdict. Wave 6 closed clean: the
aggregate `go test -race ./... -count=1` run **exited 0**, independently
re-confirmed while writing this kickoff (`WAVE-6-HANDOFF.md`/`-SUMMARY.md`
both record the same clean exit). Neither `12/01` nor `12/03` touches Go
source directly — `12/01` is pure tooling/config, `12/03` is a one-line
`Makefile` addition — so a race run isn't where either task's own risk
lives. But the standard this wave inherits is: **a task does not close at
`reviewed` without a completed verification run backing it, full stop.**
For `12/01` specifically, its own Verification section already includes
`go test -race ./...` as one of the six checks the gate itself is supposed
to wire up — treat running that command yourself, once, as part of closing
this task (not just specifying that the gate *would* run it), so `12/01`
doesn't become the wave that reintroduces "we assumed it would pass" as an
acceptable closing state.

---

## Read, in full, before doing anything else

1. `.claude/agents/orchestrator.md` — your own role definition.
2. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure.
3. `TASKS/audit-remediation/README.md` **in full** — the freeze section, the
   dispatch-model rationale, and the Wave 7 row.
4. `TASKS/audit-remediation/ARCHITECT-DECISIONS.md` — read AD-21 in full,
   not just the summary. Its own text already carries the Stage 1/Stage 2
   split, the empirical linter-drift numbers, and the explicit "do not
   reinterpret zero-tolerance" instruction — this kickoff restates it above
   because it's the single most important thing in this wave, not because
   the source doesn't already say it clearly.
5. Both task files, in full: `12-quality-ratchet-and-standards/01-*.md` and
   `03-*.md`.
6. `TASKS/audit-remediation/14-followups/README.md` and
   `02-error-handling-backlog-paydown.md` — read `14/02` even though it's
   not in this wave; you need to know exactly what Stage 2's trigger
   condition actually is, and `12/01`'s own worker needs to record the
   trigger explicitly in whatever config it ships.
7. `TASKS/audit-remediation/WAVE-6-HANDOFF.md` and `-SUMMARY.md` — the race
   verdict (gotcha C) and whatever else closed alongside Wave 6.
8. `TASKS/INDEX.md`'s freeze banner and its own "Audit Remediation" section
   — confirm Waves 0-6 all show `reviewed`/`validated`, and the Wave 7 row.
9. `TASKS/ESCALATIONS.md` — the 2026-08-23 entries, particularly the
   multi-wave race-suite saga (Waves 2 through 5) for context on why gotcha
   (C) matters as much as it does.
10. `docs/engineering/GLOSSARY.md` — check before locking any new name.

## Mandatory pre-flight gate — confirm all of the following before dispatching anything

1. **Waves 0 through 6 are all closed.** Confirm directly against
   `TASKS/INDEX.md`, including `12/02`'s `reviewed` status in Wave 1 (not
   this wave).
2. **AD-21 is `decided`**, and re-read its exact Stage 1/Stage 2 language
   yourself rather than trusting this kickoff's paraphrase — confirm the
   "do not reinterpret zero-tolerance" instruction is still there verbatim.
3. **Confirm `14/02` is still `not-started`** before dispatching `12/01` —
   if it has somehow landed since this kickoff was written, that changes
   whether Stage 2 can activate, and the operator should be consulted before
   treating Stage 2 as still-inactive by default.
4. **Re-run gotcha (A)'s three `golangci-lint` commands yourself** and
   confirm the current backlog numbers before treating them as settled —
   they moved once already since Wave 0, and six more waves have landed
   between when this kickoff was written and when you're reading it.
5. **The dev freeze (AD-24) is still in effect.** Check `TASKS/INDEX.md`'s
   banner.

## Dispatch plan

**`12/01` and `12/03` run fully in parallel** — no real file-overlap (gotcha
B), no shared dependency between them. `12/03` is small and mechanical
(single `Makefile` target, five packages added to an existing list) and
should close quickly. `12/01` is the wave's real work — an architect/operator
determination (does an existing CI mechanism already run `make lint`
anywhere this repo's own history doesn't show, per its own Context section's
open question) has to happen before any workflow/config file gets written;
budget for that conversation as part of the task, not as a blocker external
to it.

## Review discipline

A fresh reviewer (no shared context with the worker) independently
re-verifies every task, not just re-reads the Work Log.

- **`12/01`** — this is the one to scrutinize hardest. Confirm: the
  CI-mechanism-already-exists question was actually asked and answered, not
  assumed; the gate's baseline run does **not** fail against the current
  ~2,338-issue full backlog on its first run but **does** fail against a
  deliberately introduced throwaway issue (the task's own functional-
  correctness check); the `GO-SVCCORE-005` known-noise caveat
  (`internal/service/recovery_envelope_sink.go:223`) is documented in the
  gate's config/runbook; and — the single most important check for this
  task — **Stage 2 is not active**, anywhere, in whatever config shipped.
  Read the actual shipped config, don't take a Work Log claim of "Stage 2
  deferred" at face value.
- **`12/03`** — confirm `make lint-goroutines`, run after the change,
  actually reports `internal/sandbox/proxy.go:414` — this is the proof the
  five new packages are genuinely being scanned, not just present in the
  list. If it instead reports "no bare goroutines," the addition didn't
  take effect and needs to be re-checked before this task can close.

## Scope fences

`12/01` does not touch `lefthook.yml`'s fast pre-commit/pre-push gate, does
not drive the ~2,338-issue full backlog to zero, does not invent a CI
mechanism speculatively if the architect determination surfaces ambiguity
(stop and escalate instead), does not change `.golangci.yml` or the
`Makefile`'s existing `lint`/`vuln` targets, and — restating gotcha above
because it's worth restating twice — does not activate Stage 2 under any
framing. `12/03` does not modify `internal/sandbox/proxy.go` (the one bare
`go func()` it will newly surface is confirmed benign and is not this
task's job to fix), does not add `internal/safego` itself to the scanned
list, and does not expand the scan beyond the five named trust-boundary
packages.

---

## Where your findings go — read before you write your handoff

Anything you or your reviewers find that must **outlive this wave** goes to
`TASKS/ESCALATIONS.md`, not only into `WAVE-7-HANDOFF.md`. Your handoff is
read once, by the next wave's kickoff author, and then becomes historical.
`ESCALATIONS.md` is the project's running log across every batch.

Apply this test to each finding before you close:

> **If the next kickoff author never reads my handoff, does this still need to
> survive?**

If yes, write it in `ESCALATIONS.md` in full and reference it from the handoff.
Do not restate it in both — one authoritative copy, referenced.

Always durable, always `ESCALATIONS.md`:
- a real defect found and deliberately not fixed (out-of-scope is correct;
  handoff-only is not)
- any task you close **below `reviewed`**, and why
- any verification gate that **did not run or did not complete** — even when
  the task is legitimately `reviewed`. A passing task list and an unrun race
  suite are not the same claim
- any newly discovered unwired feature, dead subsystem, or island
- any process incident, especially one touching operator data or state outside
  the repo
- any correction to a stated fact in a task file or doc
- anything whose owner is undetermined

Use `docs/engineering/templates/05-escalation-entry-template.md`'s Shape B for
these. They are findings-for-the-record, not stop-and-escalate.

**Say in your handoff which items you logged**, so the next author can confirm
nothing was lost between the two files.

## At the end

When both tasks are `reviewed`, dispatch `doc-writer` for `WAVE-7-HANDOFF.md`
and `WAVE-7-SUMMARY.md`. Make sure the handoff states plainly: where Stage
2's trigger condition is recorded in whatever config `12/01` shipped, the
current (re-re-measured, if it moved again) errcheck/errorlint/nilerr
backlog numbers for whoever picks up `14/02`, and confirmation that the
aggregate race run was actually executed as part of closing `12/01`, not
just specified. Then stop; the operator reviews before deciding what's next.
