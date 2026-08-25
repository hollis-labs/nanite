# Orchestrator kickoff — Gate Integrity, Wave A

Authored 2026-08-25 against nanite `77137106`. **Re-derive HEAD; it moves.**
Copy everything below the line.

---

You are the Orchestrator for **Wave A of the Gate Integrity batch**
(`TASKS/gate-integrity/`). You have no memory of the planning session that
produced it — everything you need is in the repo and in this message.

**You are the Orchestrator, right now, in this plain session — there is no
separate agent-type system prompt attached to you.** Read
`.claude/agents/orchestrator.md` first; it is a real file in this repo (35
lines) and defines your dispatch roster in full. In short: you dispatch
**worker**, **reviewer**, **research-auditor**, and **doc-writer** via the
Agent tool. None of those four can dispatch further agents, which is
load-bearing.

**Do not spawn another orchestrator, and do not dispatch a general-purpose
agent asked to "run this batch" or "coordinate the tasks."** That recreates
this coordinating layer underneath you — a real failure that already happened
once on the Reflex Action Taxonomy batch. If the Agent tool does not offer
`worker`/`reviewer`/`research-auditor`/`doc-writer` when you check, stop and
tell the operator rather than improvising.

## What is different about this batch — read before anything else

**There is no architecture design doc, and you should not go looking for one.**
Every other sibling batch implements a `docs/engineering/architecture/NN-*.md`
design. This one does not. It was drawn from the hand-off register left by the
pre-unfreeze batch, then triaged and re-verified against live code by a
planning session on 2026-08-25. **Its authorization is a set of operator
decisions made in that session, recorded in `TASKS/gate-integrity/README.md`.**
Treat that README the way you would normally treat a design doc.

**You are running Wave A only — five items, serial.** The batch has Waves
A-D. Waves B, C and D are real, planned work and are explicitly **not** yours.
Do not pull them forward, and do not let a worker do so opportunistically.

## Read, in full, before dispatching anything

1. `.claude/agents/orchestrator.md` — your role definition.
2. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure.
3. `docs/engineering/agent-verification-discipline.md` — **in full.** This
   batch is almost entirely numbers, citations and path resolution. §1.1
   (derive at the moment of use), §1.2 (ship the command next to the number)
   and §2.2 (re-derive cited line numbers) are most of the job. Its companion
   `docs/engineering/failure-modes.md` is also required.
4. `docs/engineering/testing-workflow.md` — Tiers 0-4 and "which tier does my
   task need." Load-bearing for `08`: the landing script maps onto these tiers
   and must not invent a competing scheme.
5. `TASKS/gate-integrity/README.md` — the corrections, the wave ordering, the
   scope fence.
6. The five task files you are running: `08`, `01`, `02`, `03`, and step 6 of
   `04` (which the file itself labels `04a`).
7. `TASKS/INDEX.md`'s **"What changed during the freeze"** section, its
   **"Migration numbering"** section, and its **"Gate Integrity"** section.
8. `docs/engineering/runbooks/full-repo-quality-gate.md`'s **"What this gate
   guarantees"** section — this is how you report a gate result without
   overclaiming, and you will be reporting several.
9. `TASKS/ESCALATIONS.md` — the whole file. Note its closing **"Standing
   caveat — the gate detects, it does not gate"** paragraph contains a claim
   this batch has since disproved; see "Corrections" below.

## Dispatch order — strictly serial

The operator chose serial execution. **Do not parallelize, and do not use
out-of-tree git worktrees** — that breakage is part of what `01`-`03` fixes,
and using it to run this batch would be self-defeating. Work in the main tree,
or in a worktree placed as a sibling under `~/dev/hollis-labs/apps/`.

| # | Task | Dispatch |
|---|---|---|
| 1 | `08-lighten-commit-time-checks` | worker |
| 2 | `01-drop-published-sibling-replaces` | worker |
| 3 | `02-release-harness-filters-and-runtime-events` | worker — **sibling repos, not nanite** |
| 4 | `03-drop-remaining-replaces-and-sibling-checkouts` | worker |
| 5 | `04a` — step 6 of `04-gosec-determinism-and-coverage-floor` only | worker |

`08` goes first because it removes the friction every subsequent worker would
otherwise fight. `03` genuinely depends on `01` and `02` — not as a sequencing
preference; without `02`'s published tags it cannot resolve the modules at all.

**Between `02` and `03`, you must gate.** `02` pushes tags; the module proxy is
not instantaneous. Confirm before dispatching `03`:

```
for m in go-harness-filters go-runtime-events; do
  GOPROXY=https://proxy.golang.org go list -m -versions github.com/hollis-labs/$m
done
```

Both must list `v0.1.1`. If not, wait — do not dispatch `03` and do not let a
worker "work around" it.

## What Wave A is actually for

**`01`-`03` make the repo self-contained, and that is the point of this wave.**
It was originally filed as CI-pin hygiene, which undersold it badly. `go.mod`
carries four `replace` directives using relative `../../libs/<module>` paths.
That one fact is why:

- a second person cannot `git clone && go build` without reproducing the exact
  `hollis-labs/{apps,libs}` directory layout;
- a git worktree outside `~/dev/hollis-labs/apps/` cannot pass `go-lint`
  (~16 unrelated `undefined: envelopes` typecheck errors — tested 2026-08-25);
- a Docker container would need the sibling repos mounted at a relative path;
- CI validates sibling source that exists in no published release.

The operator's roadmap is **serial now → parallel git worktrees → Docker
soon.** Wave A is what unblocks the second and third of those.

## Corrections this batch's planning found — do not let a worker re-litigate them

Each was verified against live code at `77137106`. **Re-derive before acting;
these will drift.** Use research-auditor freely — it is read-only and cannot
dispatch further agents.

- **Two of four sibling pins are one commit past their newest published tag.**
  `go-harness-filters` and `go-runtime-events`. The other two (`go-modelsdev`
  v0.2.0, `go-envelopes` v0.3.0) are byte-identical to published tags, which is
  why `01` is a zero-risk no-op for what compiles and `02`/`03` are not.
- **`go.mod:20` requires `go-envelopes v0.1.1`** while the replace builds
  v0.3.0's source. Three minor versions stale.
- **`go.mod:115` says "Mirrors the go-agent-wrapper replace immediately
  above."** No such replace exists: `grep -c 'go-agent-wrapper =>' go.mod`
  returns `0`.
- **Worktrees DO inherit the hooks.** `core.hooksPath` is an absolute path into
  the parent clone. `TASKS/INDEX.md` point 3 has been corrected;
  `TASKS/ESCALATIONS.md`'s closing standing-caveat paragraph still carries the
  old claim that "a fresh clone **or a new worktree** has no checks at all."
  The fresh-clone half is true, the worktree half is not. **If you correct that
  paragraph, correct only that clause** — the rest of the caveat stands.
- **The gate has one soft spot, not a general trust problem.** Of 17 workflow
  steps, 8 assert and 7 of those are sound. Regressions fail correctly. The gap
  is that a *spurious decrease* in gosec is indistinguishable from a real
  improvement, and the comparator advises lowering the baseline — which is what
  `04a` fixes. **Do not let any worker or reviewer describe the gate as
  untrustworthy**; it is not supported by the evidence and the operator has
  pushed back on that framing specifically.

## Scope fence

Restate this to every worker. A worker finding "it would be easy to also do X"
is not grounds to expand scope.

- **Not in Wave A:** `07` (migration guard — Wave B), `04b` (gosec wrapper and
  coverage floor), `05` (runbook false-green), `06` (citation drift sweep).
  All real, all planned, none yours.
- **Not in this batch at all:** branch protection and merge gating (an operator
  cost/plan decision, deliberately unplanned); gosec root-cause archaeology;
  the go-envelopes half-migrated enum; the tesseract bump; the `frontend-lint`
  repair (`CW-20260816-0087`); the fresh-clone `lefthook install` gap; and the
  ~25 drifted migration-filename citations in landed Work Logs, which are
  historical records rather than live claims.
- **No schema work.** Wave A touches no migrations. If a worker believes it
  needs one, that is a surprise worth stopping for, not a number to claim.

## How to raise something you find

The operator has been explicit about this, and it matters more than usual here.

**Severity is not binary.** "Might block a workflow in an edge case" and "will
break every existing deployment" must not be reported in the same register.
When you find something that could affect a standard workflow, raise it with:
what specifically breaks and the command that shows it; who hits it and when
(everyone on every build, or one path under one config); the implication of
leaving it; and your severity read plus your confidence in it. Then let the
operator decide.

**Do not classify something as a blocker on your own authority, and do not
stop the wave for it** unless it genuinely makes continuing unsafe. Keeping
standard workflows working is the priority, but it is a priority the operator
overrides — not a rule to defend.

## Verification you should insist on

Three of these five tasks have acceptance criteria that **cannot be satisfied
by inspection**, and workers reliably try. Hold the line:

- **`08`** — prove `pre-commit` no longer blocks by committing a file with a
  deliberate `go vet` failure and showing the commit *succeeds*; and prove a
  misformatted file is still *rejected*. Both, with real output.
- **`01` and `03`** — prove decoupling by building with the sibling checkouts
  **moved aside**. A build that passes with `~/dev/hollis-labs/libs/` present
  cannot distinguish success from the replaces still silently working.
- **`04a`** — the reduction advisory no longer tells the reader to lower the
  baseline unconditionally.

`01` and `03` are also the only two whose acceptance genuinely needs CI,
because what they change *is* what CI resolves. After each lands:

```
gh workflow run "Full-repo quality gate" --ref main
```

Known intermittent, report and move on rather than chasing: `internal/memory`
failing with `SQLITE_BUSY` (Torque `CW-20260825-0001`), root-caused and fixed
in tesseract, not yet picked up by nanite's pin.

## `08`'s pre-push decision — resolved, do not re-open

This was flagged as adjustable in an earlier draft. **The operator resolved it
2026-08-25 and `08` has been updated.** Do not let a worker re-litigate it:

- `go test ./...` **stays on `pre-push`**, and is **scoped to `main`** via
  lefthook's `only: - ref: main`. Push time is not a concern — the operator
  pushes only at the end of a piece of work. WIP-branch pushes skip it.
- Syntax and behavior were verified at `lefthook 2.1.4` before the task was
  written. Have the worker re-verify at whatever version is installed, and
  prove both directions: `(skip) by condition` on a feature branch, and the
  suite actually running on `main`.

## The test cadence is defined and mid-implementation — `08` is one step of it

`docs/engineering/testing-workflow.md` defines Tiers 0-4 and "which tier does
my task need." **That work is in flight, not missing.** The tiers were written
first; the hooks went live 2026-08-24; wiring the two together is the step
`08` performs. Treat it as continuing an implementation already underway —
**do not present it as a discovered gap, and do not design a new scheme.**

The operator's constraint, stated 2026-08-25: **30 seconds to two minutes is
fine; 30+ minutes must never sit between someone and a commit or a push.** That
maps cleanly onto the existing tiers:

- **Tier 1** (~1-2 min) is the landing script — feature done, before commit.
- **Tier 3** (`-race` full suite, ~9 min) is what the nightly gate runs. Not a
  hook, not the script.
- **Tier 4** (flake hunting, `-timeout=180m`) is the 30+ minute case. Deliberate
  and manual, never near a hook.

The `pre-push` suite is the **no-`-race`** run — 45s cold, 6-9s cached, well
inside tolerance. Do not confuse it with Tier 3.

**Note for whoever measures anything here:** `lefthook.yml`'s header claims
`4.32s with everything cached`. Two back-to-back cached runs on 2026-08-25
measured 6.32s and 8.85s. `08` asks the worker to re-derive it. Do not let a
number get copied forward.

## Closing out

Get each task reviewed by a fresh **reviewer** once validated — one that did not
implement it. Post a short update when a task closes, not after every edit.

When all five are reviewed and closed, dispatch **doc-writer** for the handoff
and summary docs, then **stop.** The operator reviews both before deciding
whether Wave B starts. Do not begin Wave B, and do not pre-scope Wave C's
container work — the README says explicitly that its shape depends on `01`-`03`
having landed first, and scoping it earlier would bake in the workaround.
