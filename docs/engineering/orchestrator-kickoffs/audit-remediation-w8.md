You are the Orchestrator for **Wave 8 of the Audit Remediation batch** —
dispatch unit `W8`, `TASKS/audit-remediation/13-mechanical-cleanup/` — the
eleventh and final numbered dispatch unit implementing the remediation
program derived from `docs/audits/2026-08-21-go-quality/REPORT.md` as
sequenced by `docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`. You
have no memory of the audit, the planning pass, or Waves 0-7's own
execution — everything you need is in the repo. **This kickoff covers five
tasks: `13/01`, `13/02`, `13/03`, `13/04`, `13/05`.** Waves 0 through 7 are
all closed — that is what makes this wave dispatchable at all.

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

---

## Correction to the planning note before anything else — read this first

The planning note behind this kickoff says Wave 8 is "fully ungated, all
decisions made." **That's true at the task-dispatch level — no `Gated on:
AD-NN, open` line blocks any of the five tasks from starting — but it is not
true at the finding level, and the distinction matters for two of them.**
Independently re-verified against current `ARCHITECT-DECISIONS.md` and the
task files themselves while writing this kickoff: **six individual findings
inside `13/01`, `13/02`, and `13/05` are each explicitly marked "decision
needed, do not resolve unilaterally" in their own task file text, and none
of the six has a corresponding `AD-NN` entry anywhere in the decision
queue.** This is not a new discovery — it was logged to `TASKS/ESCALATIONS.md`
on 2026-08-23 (*"Wave 8's three mechanical-cleanup tasks carry six
`requires_architect_decision` items with no queue entry"*) specifically so
whoever wrote this wave's kickoff wouldn't have to rediscover it. Three
close cousins from that same entry *have* since been resolved with real
`AD-NN` records — AD-29 (`GO-MCPTOOL-004`, closed via `11/09` in Wave 6),
AD-33 (`GO-STORE-008`, `13/01` Bucket A), AD-30 (`GO-STORE-009`, `13/02`'s
trim pass) — which is very likely why the planning note now reads as
"decisions made": three of nine did get made. **The other six did not:**

| Finding | Task | What's undecided |
|---|---|---|
| `GO-SVCCORE-003` | `13/02` | Wire the promised rollback into `freshScaffold`, or correct the doc comment that falsely claims one exists — a real destructive-operation safety-net question, medium severity, heavier than the rest of this batch |
| `GO-CHAT-006` | `13/02` | Fix the doc comment to point at the real `//go:embed` path, or add a generate step that makes the documented path real |
| `GO-SVCCORE-007` | `13/01` Bucket B | Keep `WithAgentCycleKindForAPI` as documented scaffolding, or remove the writer + its two `internal/api` call sites |
| `GO-MCPTOOL-005` | `13/01` Bucket B | Same shape — keep `TurnToolNamesFromContext`'s dead-reader scaffolding, or remove both writer and reader |
| `GO-CHAT-005` | `13/01` Bucket B | Wire an actual resource-lock feature using `PrefixLock`/`PrefixState`/`LockTTL`, or remove all three — no live writer half to preserve either way |
| `GO-PLUGIN-004` | `13/05` | Close the traced TOCTOU window in concurrent plugin load/unload, or accept it as a documented limitation — a real, narrow concurrency bug, not a style observation |

**Do not dispatch `13/01`, `13/02`, or `13/05` past the point where a worker
would reach these rows without resolving this first.** Each of the six needs
either its own `AD-NN` entry (mirroring how AD-06 through AD-33 have been
recorded for every other wire/retire- or comment-vs-code-shaped call in this
batch) or an explicit operator confirmation that the task's own in-body
"present both options, don't decide unilaterally" framing is sufficient
without a queue entry. Given every other batch decision of this exact shape
got a real `AD-NN` record, the second path would be the outlier — raise this
with the operator before dispatch, don't default to the second path
yourself. The three of these tasks' *other* content (everything not in this
table) has no such gap and can proceed normally.

## Things about this specific wave that won't be obvious from the batch README alone

**(A) `13/03` needs `14/01` and `14/02` to land first, and neither is one of
this kickoff's five tasks.** `14-followups/README.md` states the ordering
directly: *"this wave lands after Wave 7, before `13/03`, or the sweep gets
rerun."* `13/03`'s own `Depends on` field ("every other task in the batch")
already technically includes `14/01`/`14/02` — they're part of the batch —
but that phrasing predates `14-followups/` existing at all (it was created
2026-08-23, after `13/03` was authored), so don't rely on `13/03`'s own text
to make this obvious; rely on this note and the `14-followups/README.md`
cross-reference instead. **Both `14/01` and `14/02` have their prerequisites
already satisfied** — `14/01` needs `01/01` (landed) and AD-05 (decided);
`14/02` needs `12/01` stage 1 (landed, Wave 7) and AD-21 (decided) — so
nothing is blocking either from being dispatched today. `14/03` already
landed (Wave 6 era). **Recommend dispatching `14/01` and `14/02` alongside
this wave's five tasks**, treating them as a bundled prerequisite for
`13/03` specifically rather than leaving them for a separate, later
session — but this is a scope expansion beyond what this kickoff's planning
note named, so confirm with the operator before folding them in rather than
deciding it unilaterally.

**(B) `13/03`'s backlog has moved again — re-measured while writing this
kickoff.** Its own file already warns "do not trust any number in this
file" and gives the right command. Current result:

```bash
gofmt -l ./internal ./cmd ./pkg
```

**89 files** — down from 105 at 2026-08-22 (Wave 3 era), 130 at frozen
`HEAD`, 122 at the audited commit. The trend that made AD-22's "ratchet now,
sweep last" call correct is continuing to hold — Waves 4 through 7 kept
formatting what they touched. **Re-run this again immediately before actually
sweeping**, not just once at kickoff time — more waves' worth of task
landings (including this one) will move it again before `13/03` is the last
thing left to dispatch. Use `gofmt -l ./internal ./cmd ./pkg`, not `gofmt -l
.` — the latter descends into `.claude/worktrees/` and returns a
five-figure, meaningless number, per `13/03`'s own explicit warning.

**(C) `13/03` is absolutely last, alone — this constraint doesn't loosen
just because it's this wave's own task.** Its own sequencing block is
unambiguous: *"runs absolutely last, alone, with no other worktree open in
the repository."* That means not just last within Wave 8 — last in the
**entire batch**, after `13/01`, `13/02`, `13/04`, `13/05`, **and** `14/01`/
`14/02` (per gotcha A) have all landed and no other worktree anywhere in the
repo is open. If AD-22's repo-wide sweep is what's chosen (it is — AD-22 is
already decided: "ratchet now, one sweep as the batch's final act"), it
rewrites ~89 files (and counting down) and conflicts with every other
outstanding branch. Do not parallelize this with anything, ever, regardless
of how idle the rest of the batch looks.

## Read, in full, before doing anything else

1. `.claude/agents/orchestrator.md` — your own role definition.
2. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure.
3. `TASKS/audit-remediation/README.md` **in full** — the freeze section, the
   dispatch-model rationale, and the Wave 8 row.
4. `TASKS/audit-remediation/ARCHITECT-DECISIONS.md` — read AD-22 (decided),
   AD-29, AD-30, AD-33 (all decided, all closing part of the original
   nine-item gap) in full. Confirm none of the six remaining findings in the
   table above has since gained an entry — re-check yourself, don't trust
   this kickoff's snapshot.
5. All five task files, in full: `13-mechanical-cleanup/01-*.md` through
   `05-*.md`.
6. `TASKS/audit-remediation/14-followups/README.md`,
   `01-remove-default-seeded-catalog-source.md`, and
   `02-error-handling-backlog-paydown.md` — required reading given gotcha
   (A), even though they're formally "Wave 9," not this wave.
7. `TASKS/audit-remediation/WAVE-7-HANDOFF.md`/`-SUMMARY.md` — prior wave's
   worked record.
8. `TASKS/INDEX.md`'s freeze banner and its own "Audit Remediation" section
   — confirm Waves 0-7 all show `reviewed`/`validated`, and the Wave 8 row.
9. `TASKS/ESCALATIONS.md` — specifically the 2026-08-23 entry naming the
   six-item gap (see the correction above) and the entries around AD-29/
   AD-30/AD-33's resolutions, for the full context of which three of nine
   got closed and how.
10. `docs/engineering/GLOSSARY.md` — check before locking any new name.

## Mandatory pre-flight gate — confirm all of the following before dispatching anything

1. **Waves 0 through 7 are all closed.** Confirm directly against
   `TASKS/INDEX.md`.
2. **Resolve (or get explicit operator sign-off to proceed without
   resolving) the six-item gap above before any worker reaches those
   specific rows in `13/01`, `13/02`, or `13/05`.** This is the single most
   important gate in this wave.
3. **Confirm with the operator whether `14/01`/`14/02` are being dispatched
   alongside this wave** (gotcha A) — don't assume either way.
4. **Re-run gotcha (B)'s `gofmt -l` command yourself** and confirm the
   current count before treating `13/03`'s scope as settled.
5. **The dev freeze (AD-24) is still in effect.** Check `TASKS/INDEX.md`'s
   banner.

## Dispatch plan

**`13/01`, `13/02`, `13/04` run in parallel** once the six-item gap (or the
subset touching each) is resolved — file-disjoint, no cross-task
dependency. `13/05` is also parallel-safe with `13/01`/`13/04` per its own
header, but shares no files with `13/02` either, so all four can run
together once the gap is cleared. If `14/01`/`14/02` are bundled in per
gotcha (A), they're independent of all four and can run alongside them too.
**`13/03` runs last, alone** (gotcha C) — after every other task in this
wave, and `14/01`/`14/02`, have landed.

## Review discipline

A fresh reviewer (no shared context with the worker) independently
re-verifies every task, not just re-reads the Work Log.

- **`13/01`, `13/02`, `13/05`** — confirm each of the six gap findings that
  landed in this wave was resolved through an actual recorded decision
  (an `AD-NN` entry, or the operator's explicit sign-off per the pre-flight
  gate), not a worker's own judgment call presented as settled.
- **`13/01`** — confirm Bucket A's re-verification against current `HEAD`
  actually happened (fresh `deadcode`/grep, not the stale citation trusted
  blindly) before treating any deletion as safe.
- **`13/02`** — spot-check that no load-bearing invariant/safety comment
  (the audit's own named examples: `user_settings.go:262-264`,
  `store.go:80-93`, `agent_runtime.go:227-244`) was accidentally trimmed
  alongside the bare task-ID citations that were supposed to go.
- **`13/03`** — confirm `gofmt -l ./internal ./cmd ./pkg` returns empty
  after the sweep, and confirm it genuinely landed alone (check for any
  other open worktree at merge time).
- **`13/04`** — confirm `GO-SVCEXEC-006` was treated as the priority item
  it's flagged as, not downgraded to "same as the other four" during
  implementation.
- **`13/05`** — confirm the `GO-PLUGIN-004` TOCTOU decision was actually
  recorded (not just the mechanical helper-extraction half, which has no
  decision gate), and confirm the Wave 5 constraint on `Host` decomposition
  (no all-category migration sweep) was respected.

## Scope fences

`13/01` does not touch anything outside its three buckets' named files —
Bucket C (`GO-MEM-008`) gets no code change at all. `13/02` does not attempt
a full architect-level documentation-consolidation pass, and does not expand
the trim to comments not named in its own findings. `13/03` does not fix any
other lint category while touching these files (naming/formatting only) and
does not wire the `make lint` enforcement gate itself (that's `12/01`,
already done). `13/04` does not change control flow beyond "log, don't
propagate" for any of its five findings. `13/05` does not perform an
all-category `Host` sub-registry migration sweep — only a category that
clears the Wave 5 constraint's bar (real ownership/locking/testability pain,
not just field/method counts) may seed a follow-on task, and this task
doesn't create that follow-on itself.

---

## Where your findings go — read before you write your handoff

Anything you or your reviewers find that must **outlive this wave** goes to
`TASKS/ESCALATIONS.md`, not only into `WAVE-8-HANDOFF.md`. Your handoff is
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

When all five tasks are `reviewed` (and `14/01`/`14/02` too, if bundled in
per gotcha A), dispatch `doc-writer` for `WAVE-8-HANDOFF.md` and
`WAVE-8-SUMMARY.md`. This is very likely the batch's last numbered wave —
have the handoff state plainly: whether all six gap findings from the
correction above got real `AD-NN` records or explicit operator sign-off,
`13/03`'s final gofmt count and confirmation it landed alone with a clean
`gofmt -l` afterward, and whether anything remains open in
`14-followups/README.md`'s candidate register that should block calling the
batch closed. Then stop; the operator reviews before deciding what's next.
