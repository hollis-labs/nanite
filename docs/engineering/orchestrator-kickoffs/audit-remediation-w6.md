You are the Orchestrator for **Wave 6 of the Audit Remediation batch** —
`TASKS/audit-remediation/11-semantic-duplication-migration-drift/`, dispatched
as two units, `W6a` and `W6b` — the eighth and ninth of eleven dispatch units
implementing the remediation program derived from
`docs/audits/2026-08-21-go-quality/REPORT.md` as sequenced by
`docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`. You have no memory
of the audit, the planning pass, or Waves 0-5's own execution — everything
you need is in the repo. **This kickoff covers sixteen tasks — nine in W6a,
seven in W6b:**

**W6a (9):** `11/01`, `11/02`, `11/05`, `11/06`, `11/07`, `11/08`, `11/09`,
`11/10`, `11/11`.
**W6b (7):** `11/03`, `11/04`, `11/12`, `11/13`, `11/14`, `11/15`, `11/16`.

*(Count from this list; if a different number appears anywhere else below,
this line wins.)* Waves 0 through 5 are all closed — that is what makes this
wave dispatchable at all; do not re-open or re-verify their work beyond
confirming closure, covered below.

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
mode that has already happened once in this project. If the Agent tool
doesn't actually offer `worker`/`reviewer`/`research-auditor`/`doc-writer` as
usable types when you check, stop and tell the operator directly.

**The repo-wide dev freeze (AD-24) is still in effect** — confirm this
directly (`TASKS/INDEX.md`'s banner) rather than assuming. `TASKS/audit-remediation/`
remains the only work authorized to proceed; the other frozen sibling
batches stay frozen.

---

## The thing that makes Wave 6 different — read this before anything else

AD-19 is the **last open decision in the batch's original 24-item queue**
(the queue has since grown to 28 with later-surfaced gaps; AD-19 is the last
of the ones planned from the start), and — like AD-12/AD-13 in Wave 5 — it
**cannot be made yet, by design.** It needs a classification table (the
guide's five-way split: `textual-only boilerplate | same semantics/stable |
same semantics/divergent behavior | migration drift | intentionally
independent`) before "share implementation vs. parity test" is even a
well-formed question. Same map-then-decide-then-act shape Wave 5 just
proved out. Structure this kickoff the same way:

1. **Classify** — the evidence for the table already exists; see below.
2. **Decide** — the operator resolves AD-19 against that table, as a
   per-instance call, not one blanket answer.
3. **Act** — implement per the decided policy, per instance.

**One real structural difference from Wave 5, worth stating explicitly so
you don't over-apply the precedent:** Wave 5's `10/01`/`10/02` were each a
*single task whose entire deliverable was the map* — implementation was
explicitly forbidden and became separate follow-on tasks (`10/04`, `10/05`)
after the decision. **Wave 6a's nine tasks are not shaped that way.** Each
one's own "Proposed direction" section already presents its classification
and options — the analysis Wave 5 had to newly produce is already written,
task by task, across these nine files. And each task's own Done-means
includes the actual implementation, not just the decision. So: **do not
dispatch any W6a task to a worker until AD-19 is resolved** — a worker
dispatched early would either stall mid-task waiting on a decision it can't
make, or (worse) guess. Instead:

- Synthesize the classification table yourself (or via `research-auditor`)
  from the nine tasks' own "Proposed direction"/"Root cause" sections — the
  raw material is already there, this is compilation, not new analysis.
- Present that table to the operator for one AD-19 resolution session,
  covering all nine at once (per-instance answers, one sitting).
- Then dispatch all nine W6a workers for real, with the decision already
  locked in, so each goes straight to implementation instead of stopping
  partway through.

## Things about this specific wave that won't be obvious from the batch README alone

**(A) Two of AD-19's own findings are named explicitly; the rest are covered
by a catch-all — and one of them doesn't actually fit AD-19's shape.**
AD-19's `**Findings:**` line reads `GO-SVCEXEC-004, GO-API-007, GO-CHAT-002,
GO-INFRA-004 (+ the rest of 11/)`. Seven `needs-architect-decision` findings
sit in this wave total — those four, plus `GO-MCPTOOL-004`, `GO-MCPTOOL-012`
(no architect decision actually needed — `11/05`'s own header sets
`requires_architect_decision: false`, so treat that one as already resolved
by its own task file, not a live AD-19 dependency), and `GO-CHAT-004`. All
should resolve through AD-19 as the "+ the rest of `11/`" catch-all — **except
`GO-MCPTOOL-004`, which genuinely does not fit.** It lives in `11/09`
alongside `GO-CHAT-004` (both are in the same file,
`internal/mcp/elicitation.go`), but where `GO-CHAT-004` is a real "two
near-duplicate implementations, share or keep separate with a parity test"
question — AD-19's actual shape — `GO-MCPTOOL-004` is "delete confirmed-dead
code implementing a described-but-never-built feature, or build that
feature" — structurally a **wire/retire** question, the same shape as Wave
4's AD-06 through AD-11, not a duplication-classification question at all.
`11/09`'s own text already resolves the *sequencing* between the two (decide
`GO-MCPTOOL-004` first, since it determines whether there's anything left to
unify under `GO-CHAT-004`) but does not resolve *which decision framework*
governs `GO-MCPTOOL-004` itself. **Confirm explicitly with the operator,
before dispatching `11/09`, whether AD-19's resolution is meant to cover
`GO-MCPTOOL-004` too, or whether it needs its own decision recorded
separately in `ARCHITECT-DECISIONS.md`** (mirroring how AD-06 was recorded
for a structurally identical delete-vs-build call). This was flagged back in
Wave 3 as "probably folds under AD-19 — confirm explicitly"; that
confirmation is due now, and it is cheaper to settle at kickoff than to
discover mid-wave that a worker implemented `11/09` against an assumption
nobody actually checked.

**(B) Good news, stated plainly: none of Wave 6's sixteen tasks reference any
of Wave 4's eight retired paths.** Independently verified against current
`HEAD` while writing this kickoff — grepped all sixteen task files for
`internal/grounding`, `internal/contextbroker/gate_hadron_blueprints`,
`internal/tool/{adapt,builder,register,tool,yaml_loader}.go`,
`internal/toolclient/ranking.go`, and `internal/toolclient/tool_knowledge.go`
(the full retired-file list from AD-06/AD-07/AD-09/AD-10/AD-11) — **zero
hits, across all sixteen files.** The Wave 4 retires didn't invalidate
anything in this wave's scope; you don't need to re-derive anything here on
their account.

**(C) Six of this wave's target files have seen real commit churn since
Wave 3 — spot-checked, and the two smallest, easiest-to-get-wrong premises
both still hold.** `cmd/nanite/main.go` (11 commits, affects `11/10`),
`internal/toolclient/broker.go` (3 commits, `11/12`),
`internal/selftools/self_tools_dispatch.go` (2 commits, `11/11`), and
`internal/mcp/manager.go`/`internal/api/harness_v1.go`/
`internal/plugin/host.go` (1 commit each, `11/05`/`11/02`/`11/10`) have all
moved. Two premises worth independently confirming before trusting any task
file's line citations — both checked directly against current `HEAD` while
writing this kickoff: `DevServerName` (`11/12`) is genuinely still declared
twice — `internal/mcp/naming.go:36` and `internal/toolclient/broker.go:291`
— so the finding is still live and the fix still needed. And `11/10`'s three
manual setter calls are still all present in `main.go`:
`chat.SetEnvelopeRegistry` (:239), `envelope.SetEnvelopeRegistry` (:240),
and `pluginHost.SetEnvelopeRegistry` (:343) — the triplication finding still
holds despite the file's churn. **Neither of these being still-true is a
license to skip re-verifying the rest** — every task in this wave should
re-derive its own line citations fresh, same standing instruction as every
prior wave.

**(D) `11/13`'s ground has moved the most, and it's already measured for
you.** Its own file says "~20 files, 41 `dupl` hits" at the audited commit,
refreshed once by `00/02` to 40 hits across 22 files at frozen `HEAD` — and
then `06/03`/`06/04` rewrote all 67 non-test files in `internal/store`.
**Re-run this yourself before dispatch — already done once while writing
this kickoff, so you have a real number, not just a warning:**

```bash
golangci-lint run -c docs/audits/2026-08-21-go-quality/audit-golangci.yml \
  --max-issues-per-linter=0 --max-same-issues=0 --enable-only dupl ./internal/store/...
```

Current result: **46 hits across 23 files** — up from 40/22. The shape and
count both changed, as expected. This doesn't block `11/13` (it's explicitly
optional — `requires_architect_decision: false`, "do not treat as must-fix" is
the audit's own framing) but whoever picks it up needs the current list, not
the stale one, and should re-run this command again themselves rather than
trust the number in this kickoff either, given how much more churn is likely
between now and actual dispatch.

**(E) Scope shrinks already recorded in four task files — read the file, not
just the batch README (now synced to match, but check both):** `11/11`
shrinks because AD-06 removed the grounding dispatch block it partially
overlapped with. `11/12` was already correctly sequenced behind `09/05`
(Wave 4's `ranking.go` retire shifted `broker.go`'s line numbers) — no
action needed, just confirms the dependency is now satisfied. `11/13`'s
prerequisite (`06/01`, `06/02`) landed in Wave 2. `11/08` **no longer needs
to run alone** — its own `AD-20 DECIDED` banner measured 11 real references
(`Config` ×5, `AppConfig` ×6), not the tree-wide sweep its original
`Touches` field warned about; the batch README previously still said "runs
alone if AD-20 picks tree-wide rename" and has been corrected as part of
writing this kickoff.

**(F) Wave 6 must produce a real, complete aggregate `-race` verdict — this
supersedes an earlier draft of this kickoff's guidance, so don't act on any
stale copy of it.** The test-fixture migration-cost fix (`14/03`) is
**done and independently reviewed**, ahead of this wave as planned —
`14-followups/README.md`'s own register confirms: *"Completed and
independently reviewed ahead of Wave 6; full-repo race gate now passes."*
Every prior wave from Wave 2 onward either timed out or had to defer its
aggregate `-race` verdict; that structural excuse is gone. **Do not accept
"focused suites passed, aggregate timed out" as a closing state for this
wave.** If `go test -race ./...` doesn't complete cleanly for Wave 6, that
is now a new, real finding requiring investigation in its own right — log it
per the Logging discipline below, don't wave it through as "expected."

---

## Read, in full, before doing anything else

1. `.claude/agents/orchestrator.md` — your own role definition.
2. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure.
3. `TASKS/audit-remediation/README.md` **in full** — the freeze section, the
   dispatch-model rationale, and the (just-corrected) Wave 6a/6b rows and
   parallelization notes.
4. `TASKS/audit-remediation/ARCHITECT-DECISIONS.md` — read AD-19 and AD-20 in
   full. AD-20 is `decided`; AD-19 is `open` by design (see above). Skim the
   rest of the 28-item queue — several later-wave decisions remain `open`
   (AD-12/13 are actually now decided per Wave 5; check AD-15/16 and the
   Wave 8 gap logged 2026-08-23 in `ESCALATIONS.md` if you need current
   status) — none gate this wave.
5. All sixteen task files, in full: `11-semantic-duplication-migration-drift/01-*.md`
   through `16-*.md`.
6. `TASKS/audit-remediation/WAVE-5-HANDOFF.md` (and earlier wave handoffs if
   useful) — prior waves' worked records, and Wave 5's own worked example of
   the map-then-decide-then-act pattern this wave repeats.
7. `TASKS/audit-remediation/14-followups/03-test-fixture-migration-cost.md`
   — read its final Work Log/Review notes, not just the register entry, if
   you want the actual before/after race-suite numbers before this wave's
   own aggregate run.
8. `TASKS/INDEX.md`'s freeze banner and its own "Audit Remediation" section —
   confirm Waves 0-5 all show `reviewed`/`validated` (including `10/04`/
   `10/05`, Wave 5's post-decision extraction tasks), and the Wave 6a/6b rows.
9. `TASKS/ESCALATIONS.md` — the 2026-08-23 entries, especially the Wave 8
   unqueued-decision entry (informational for you, not a blocker) and
   anything about Wave 5's own closing race-suite numbers.
10. `docs/engineering/GLOSSARY.md` — check before locking any new name.

## Mandatory pre-flight gate — confirm all of the following before dispatching anything

1. **Waves 0 through 5 are all closed**, including Wave 5's post-decision
   extraction tasks `10/04`/`10/05`. Confirm directly against `TASKS/INDEX.md`.
2. **AD-20 is `decided`.** Confirm `11/08`'s gate is satisfied and it's
   parallel-safe (per gotcha E).
3. **Confirm `GO-MCPTOOL-004`'s decision framework (gotcha A) before
   dispatching `11/09`** — either get an explicit operator confirmation that
   AD-19 covers it, or get it its own `AD-NN` entry the way AD-06 was
   recorded for the structurally identical Wave 4 question. Do not let a
   worker assume either way.
4. **Re-run gotcha (D)'s `dupl` command yourself** and confirm the current
   count before treating `11/13`'s scope as settled — it has moved once
   already since this kickoff was written and may move again.
5. **Confirm `14/03` is genuinely `reviewed`** (not just `implemented`) and
   its register entry in `14-followups/README.md` reflects completion —
   this is what licenses gotcha (F)'s "no more excuses" framing. If it
   somehow isn't, that changes this wave's own race-gate expectations and
   needs operator input before proceeding on the assumption it's resolved.
6. **The dev freeze (AD-24) is still in effect.** Check `TASKS/INDEX.md`'s
   banner.

## Dispatch plan

**W6a — classify, then decide AD-19 (and the `GO-MCPTOOL-004` question),
then dispatch all nine for implementation.** Once AD-19 is resolved, all
nine (`11/01`, `02`, `05`, `06`, `07`, `08`, `09`, `10`, `11`) are
file-disjoint and fully parallel-safe — cross-checked against each task's
own `Touches` list. `11/07` is a cross-reference-only tracking entry (its
real implementation already landed in `08/09` under AD-28); it needs no
worker, just confirmation the cross-reference still resolves.

**W6b — sequenced after W6a's tasks land, not after AD-19's decision alone.**
Several W6b tasks (`11/03`, `11/14`, `11/16`) explicitly depend on "Wave 6a
complete"; the other four (`11/04`, `11/12`, `11/13`, `11/15`) have their own
already-satisfied prerequisites from earlier waves, but `11/15` wants
`internal/api` exclusively and `11/02` (W6a) touches `internal/api/*.go` —
real file-overlap risk if run concurrently across units. Keep the wave
boundary clean: close all of W6a first, then dispatch W6b. Within W6b:
`11/03` ∥ `11/12` ∥ `11/14` ∥ `11/16` run freely; `11/13` wants
`internal/store` to itself (not concurrent with anything, per gotcha D); `11/15`
wants `internal/api` to itself. `11/04` has no real conflict with any of
these — file-disjoint (`internal/inspector`, `internal/service/inspector_producers.go`)
— dispatch it alongside whichever of `11/13`/`11/15` isn't currently running.

## Review discipline

A fresh reviewer (no shared context with the worker) independently
re-verifies every task, not just re-reads the Work Log.

- **`11/09`** — confirm the `GO-MCPTOOL-004` decision was actually obtained
  through the right framework (gotcha A), not silently folded into AD-19's
  resolution without operator sign-off on that specific point.
- **`11/02`, `11/06`** — both are dual-reading "accidental vs. deliberate"
  findings with real behavioral risk if the wrong reading is picked; confirm
  the required caller-enumeration / downstream-consumer trace was actually
  done and recorded, not assumed.
- **`11/03`** — confirm the import-cycle claim was independently re-verified
  (`go list -deps`) rather than re-citing the audit's or this task file's own
  prior conclusion.
- **`11/08`** — confirm all 11 references were actually updated, not just
  the ones in the three named files.
- **`11/13`, `11/14`** — both are explicitly optional; confirm whatever was
  decided (undertaken vs. deferred) is recorded with reasoning, not silently
  skipped without a note.
- **Every task** — confirm re-derived line citations against current
  `HEAD`, given the drift documented in gotcha (C).
- **At wave close** — confirm the aggregate `go test -race ./...` run
  (gotcha F) actually happened and actually passed; do not accept a
  wave-summary claim without seeing the real output.

## Scope fences — the recurring ones worth restating

Every dual-reading task (`11/02`, `11/06`, `11/09`'s `GO-MCPTOOL-004` half)
implements **whichever option the architect chooses**, not the option that
looks more defensible in isolation — do not let a worker's own judgment
substitute for the recorded decision. Every "intentionally independent"
task (`11/11`, `11/16`) does not merge or unify what the audit already
confirmed is deliberately separate — `11/11` adds a sync test on shared
*interpretation* logic only, `11/16` makes no code change at all. Every
"optional" task (`11/13`, `11/14`) may be legitimately deferred without
further justification beyond citing the audit's own false-positive framing
— don't let scope creep turn an optional DRY cleanup into a mandatory one.
`11/10` does not build the hot-reload/test-isolation feature its own risk
framing anticipates — it only removes the *shape* of the bug that feature
would trip over.

---

## Where your findings go — read before you write your handoff

Anything you or your reviewers find that must **outlive this wave** goes to
`TASKS/ESCALATIONS.md`, not only into `WAVE-6-HANDOFF.md`. Your handoff is
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

When all sixteen tasks are `reviewed` (or explicitly, individually deferred
with reasoning, for the optional ones), dispatch `doc-writer` for
`WAVE-6-HANDOFF.md` and `WAVE-6-SUMMARY.md`. Make sure the handoff states
plainly: whether the aggregate `-race` verdict (gotcha F) actually landed
clean, how `GO-MCPTOOL-004`'s decision was resolved and under which
framework, and the final `11/13` `dupl` count if that task was undertaken.
Then stop; the operator reviews before deciding what's next.
