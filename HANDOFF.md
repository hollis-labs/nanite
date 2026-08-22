# Where we are (2026-08-22, mid audit-remediation — PAUSED on `06/03`)

Picking this up after compaction: read this whole file first, then the
pointers it names. This replaces the earlier 2026-08-22 version — that draft
described AD-14/AD-17/AD-18 and the new `06/03` task as "uncommitted right
now," but a fresh `git log`/`git status` check at the start of this session
found they had already landed as commit `eebe0f32` in between. This version
corrects that, and also fixes a real tracking-sync gap `eebe0f32` left behind
(see "Tracking sync done this session" below).

## Headline

- **The repo-wide development freeze (AD-24) is still in effect.**
  `TASKS/audit-remediation/` remains the only work authorized to proceed;
  everything else (Plugin System, Loops, Turn vs. Run, Feedback-Carrying
  Denial, Code Mode, Filesystem Snapshots, and the rest) stays frozen. See
  `TASKS/INDEX.md`'s banner.
- **Skills batch is now fully complete** — 12/12 tasks `reviewed`. It was
  "executing, 4/12" at the last handoff; it finished before the freeze took
  hold (one of the two batches AD-24 names as "in-flight work that finishes").
- **Audit Remediation Wave 0 and Wave 1 are both complete and reviewed.**
  AD-14, AD-17, and AD-18 are decided, committed, and pushed. 10 of 25
  architect decisions are now resolved.
- **⏸ THE BATCH IS PAUSED. `06/03` is executing in an external Codex session
  right now, and nothing else runs until it lands.** The open question a
  previous version of this file raised — what "handed to an external session"
  means concretely — is answered: the operator dispatched it 2026-08-22 and
  will report back with a summary, at which point tracking and process get
  updated and Wave 2 can be scheduled.

  **This is not optional sequencing.** `06/03` rewrites every exported method
  signature in `internal/store` (371 of 505 lack `ctx`) plus call sites across
  32 packages. A half-swept package does not compile, so it must land as one
  merge from a clean `main`. It conflicts directly with `06/01` (which edits
  `agents.go`, one of the two zero-adoption hot tables), `06/02`, `11/13`,
  `13/01`, `13/02`, and with W2a via `internal/service`'s 76 direct
  `*store.Store` references. **Do not dispatch any Wave 2 task while it is in
  flight**, and do not treat an external agent touching `internal/store` as a
  freeze violation — `06/03` *is* audit-remediation work and is authorized.

## Audit Remediation — detailed status

**Wave 0 (`00/01`, `00/02`) — complete, reviewed** (`e3980e9b`). All 113
findings revalidated against HEAD `531dcfcc`; disposition counts and the
critical/high re-confirmation are in `00-revalidate-baseline/README.md`'s
`## Outcome (2026-08-22)` section. `findings.json` carries a real
`revalidation_note` per finding, no more `remediate` placeholders.

**Wave 1 (`01/01`, `01/02`, `02/01`, `02/02`, `03/01`, `12/02`) — complete,
reviewed.** Fixed the batch's most severe findings: the plugin-install
signature-bypass + path-traversal (2 critical + 1 high, `01/01`), the Linux
sandbox silent fail-open + network-allowlist inversion (critical, `02/01`),
and the agent-slug path-traversal (2 high, `03/01`). Full technical record in
`TASKS/audit-remediation/WAVE-1-HANDOFF.md` (written for whoever authors
Wave 2's kickoff) and `WAVE-1-SUMMARY.md` (operator-facing). Key things worth
knowing without reading either in full:

- **My own Wave 1 kickoff caught a real gap**: `01/02`'s wire-vs-retire
  question (`GO-PLUGIN-008`) had `requires_architect_decision: true` in its
  header and no decision anywhere in `ARCHITECT-DECISIONS.md`. The kickoff's
  pre-flight gate forced this to resolve before dispatch — it became **AD-25**
  ("wire it"), decided 2026-08-22, now on record.
- **A real, unfixed hygiene bug found on re-review, not blocking anything**:
  every catalog- or CLI-installed plugin ships with a leftover downloaded
  archive file inside its own install directory (`install.HTTPDownloader`
  writes it, nothing ever deletes it). Correctness/hygiene, not security.
  Logged in `TASKS/ESCALATIONS.md`'s 2026-08-22 entry as a fast-follow
  candidate for whoever next touches `internal/plugin/install/`.
- **`go vet ./...` is not clean right now — this is expected, not a
  regression.** 4 findings, all in `internal/service/container.go`
  (`stopReaper`/`stopRuntimeReaper` unused on some paths) — this is
  `GO-LIFE-001`, already catalogued, already assigned to `04/01` (Wave 2a's
  first task). Don't treat it as a new problem at Wave 2's start.
- **A real Linux-verification technique now exists in the record**: `02/01`'s
  worker had no Docker/cloud credentials available, installed `colima`+`docker`
  via Homebrew, and ran real `golang:1.25-alpine` containers (bwrap absent,
  then present via `apk add bubblewrap --privileged`). Found three real bugs
  darwin testing structurally cannot catch. Reusable pattern — see
  `WAVE-1-HANDOFF.md` §3 gotcha 3 for the exact commands.
- **Tracking hygiene, both already fixed same day**: all six task files were
  found still reading `implemented` despite real PASS reviews (fixed,
  advanced to `reviewed`, commit `671c165b` area); `findings.json`'s
  `task_status` was `not-started` for all 113 findings including the ten
  Wave 1 closed (synced — see below). **Current state: 10 of 113 findings
  `reviewed` in `findings.json`, 103 `not-started`.**

**`findings.json` and `ARCHITECT-DECISIONS.md` on disk are ahead of both of
those descriptions right now** — see the next section.

## AD-14, AD-17, AD-18 — decided and committed (`eebe0f32`, 2026-08-22)

`git status --short` at the start of this session showed only `M HANDOFF.md`
— the decisions below, plus the new `06/03` task file, `ARCHITECT-DECISIONS.md`,
and `findings.json`'s disposition updates, are all in `eebe0f32`. Full
rationale is in that commit's message; summary:

- **AD-14, in two halves.** Context propagation (`GO-STORE-005`, 371 of 505
  `*Store` methods lack `ctx`) gets a **full sweep**, split out as a
  standalone task, **`06/03`** — not folded into `06/02`. `06/03`'s own file
  ("READ THIS FIRST") is explicit that it's meant to run **in isolation,
  handed to an external session with no repo context**, because a half-swept
  `internal/store` does not compile and the sweep touches all 67 non-test
  files plus call sites across 32 importing packages — it cannot run
  concurrently with *anything* else in this batch (`06/01`, `06/02`, `11/13`,
  `13/01`, `13/02` all conflict directly). The narrow-interfaces half of
  AD-14 (`GO-STORE-001`, `GO-DEP-002`) was decided **accepted-risk, no code
  change** — `10/03` stays review-note-only. `06/02` is re-scoped down to its
  remaining two findings (`GO-STORE-004`, `GO-STORE-006`) and **loses its
  architect gate** now that `GO-STORE-005` moved out.
- **AD-17**: `cmdServe` returns `error`, `main()` does the `os.Exit` — no new
  cleanup-hook mechanism. The decision text argues this finding's "low"
  severity undersells it (all 7 `slogx.Fatal` sites in `cmdServe`, not just
  the terminal one, currently skip deferred cleanup including both SQLite
  closes).
- **AD-18**: TTL plus a hard count cap on the background job registry, with
  an explicit "expired ≠ not-found" requirement so an evicted job doesn't
  silently read as unknown.

## Tracking sync done this session

`eebe0f32` updated the task files, `ARCHITECT-DECISIONS.md`, and
`findings.json`, but **not** `TASKS/INDEX.md` or
`TASKS/audit-remediation/README.md` — both still showed `06/02` gated on
AD-14 (already-decided) and neither had a row for the new `06/03` task. Same
class of gap as Wave 1's tracking-hygiene fixes. Fixed this session:

- `TASKS/audit-remediation/README.md` — `06/02`'s row now shows the re-scope
  and lifted gate; added a `06/03` row; added a parallelization note under
  Wave 2b spelling out `06/03`'s "runs alone, clean `main`, one merge"
  constraint.
- `TASKS/INDEX.md` — same fix in the Wave 2b table, plus an explanatory note
  that `06/03` is out-of-wave and not part of the Wave 2b dispatch unit.

## Still open — resolve before writing Wave 2's kickoff

**What does "handed to an external session" mean for `06/03` concretely?**
Has it actually been dispatched anywhere (a separate Torque/Mux session, a
worker outside the normal Orchestrator flow), or is that just the task
file's own instruction for whoever eventually runs it? This determines
whether Wave 2's kickoff should treat `06/03` as already-claimed (don't
re-dispatch, just track it) or as still needing dispatch. **This is a
question for the operator, not something to assume.** Once answered, Wave
2a's task list (`04/*`, `05/01`, `06/01`, re-scoped `06/02`, `07/*`) is
otherwise ready to kick off — none of those depend on `06/03` landing first
(they conflict with it running *concurrently*, not with it being unstarted).

## Kickoffs I've written for this batch so far

`docs/engineering/orchestrator-kickoffs/audit-remediation-w0.md` and
`-w1.md`, both used, both committed. Two structural things worth remembering
for whoever writes Wave 2's (likely me, next):

- W0 was read-only-against-production-code (both tasks wrote only tracking
  files) — dispatched as `worker`, not `research-auditor`, since the latter
  can't write `findings.json`. Found a real file-overlap risk the batch
  README's "parallelizable" claim missed (`00/01` and `00/02` both touch the
  same five task files' `## Context` sections) and recommended landing
  `00/02` first to avoid it.
- W1 found the `01/02`/AD-25 gap (above) and the darwin-can't-validate-Linux
  problem for `02/01`, and pointed `12/02` at `PREVENTION.md` rather than its
  own already-quoted-but-thinner Context section.

Both kickoffs are a real, applied example of this project's kickoff-authoring
discipline — checking task files against current repo state rather than
trusting them, and catching real planning-pass gaps rather than just relaying
what was written. Read them as worked examples before writing Wave 2's.

## Landed and complete

| Batch | Design doc | Landed | How |
|---|---|---|---|
| Reflex Action Taxonomy | `10-reflex-action-taxonomy.md` | ✅ | PR #263, merged |
| Harness-Reactive Self-Tools | `11-harness-reactive-self-tools.md` | ✅ | PR #264, merged |
| Scheduling | `12-scheduling.md` | ✅ | direct commits to `main` |
| Teams | `15-teams.md` | ✅ | direct commits to `main` |
| Agent Host + ACP | `16-agent-host.md` + `17-acp.md` | ✅ | 24 tasks, direct commits |
| Phase 6 — Envelopes & Cards | (numbered phase, `08-cards.md`) | ✅ | 6 tasks, direct commits |
| Loops | `21-loops.md` | ✅ | migrations `138`-`146`, direct commits |
| **Skills** | `20-skills.md` | ✅ **new this handoff** | 12 tasks/8 waves, 7 real bugs found+fixed in review, 1 high-severity path-traversal bug (task `10`), migrations `136`/`137` |

## In progress / frozen

| Batch | Status | Notes |
|---|---|---|
| **Audit Remediation** | **executing — the only authorized work** | Wave 0 ✅, Wave 1 ✅, Wave 2 next (kickoff not yet written — see above) |
| Filesystem Snapshots | frozen at 1/3 done | Was queued to run right after Skills per the operator's own plan; now blocked by the freeze instead. Kickoff ready (`filesystem-snapshots.md`) for whenever the freeze lifts. |
| Plugin System | frozen, not-started | Kickoff ready (`plugin-system.md`). Migration `135` still unclaimed. |
| Turn vs. Run | frozen, not-started | Kickoff ready (`turn-vs-run.md`). No migration needed. |
| Feedback-Carrying Denial | frozen, not-started | Kickoff ready (`feedback-carrying-denial.md`). No migration needed. |
| Code Mode | frozen, not-started | Kickoff ready (`code-mode.md`). Real migration-`144` collision with Loops was flagged — now moot, Loops actually landed at `138`-`146` (renumbered before landing), so Code Mode's next real claim is `147`+. |

## Migration numbering

Real ceiling on disk: **`146`** (`146_agent_schedules_loop_run_tick_job_type.sql`,
Loops). Loops' own provisional `138`-`144` collided with Code Mode's `144`
during planning (flagged in both kickoffs); Loops was renumbered to `138`-`146`
before landing, so that collision never actually hit. **`135` remains
unclaimed** — Plugin System's provisional claim, still frozen/undispatched.
Audit Remediation itself claims no migration numbers and needs none (all 113
findings are Go-level, confirmed at Wave 0 planning time and unchanged since).

## Torque

Unchanged since the last handoff — not re-verified this session. `CW-20260819-0004`
confirmed `done`. `CW-20260820-0001` through `-0008` (Memory & Knowledge Tools
follow-ups) remain `todo`/`manual`, untouched since filing. No outstanding
Torque tool blockers as of the last check.

## Small housekeeping item, still open

`TASKS/audit-remediation/README.md`'s `## Status` block still shows
**"Approved for implementation | ☐ Not yet"**, even though two of its three
blocking prerequisites are now fully satisfied (the freeze, Wave 0+AD-01–04)
and Wave 1 has also landed since. Not a real blocker — the operator dispatching
Wave 0 and Wave 1 directly is itself the authorization signal, and both W0/W1
kickoffs said so explicitly — but worth flipping or rewording next time
someone's in that file, so it stops reading as more unapproved than it is.

## Key pointers

- `TASKS/INDEX.md` — live status tracker, always re-read fresh. Freeze banner
  at the top.
- `TASKS/audit-remediation/ARCHITECT-DECISIONS.md` — 25 decisions total
  (AD-01 through AD-25). **All of Wave 2's gates are decided** (AD-14, AD-17,
  AD-18, as of `eebe0f32`) — nothing blocks Wave 2's kickoff on this front.
  Several later-wave decisions are still genuinely `open` though — don't
  assume the whole queue is clear: AD-05 (Wave 1 follow-up, non-blocking),
  AD-15/AD-16 (Wave 3), AD-12/AD-13 (Wave 5), AD-19/AD-20/AD-21/AD-22
  (Wave 6-8). Re-check this file fresh when scoping those waves.
- `TASKS/audit-remediation/WAVE-1-HANDOFF.md` / `WAVE-1-SUMMARY.md` — the
  detailed and operator-facing records of what Wave 1 actually did.
- `TASKS/ESCALATIONS.md` — latest real entries are Wave 1's leftover-archive-file
  finding and the Skills batch's closing entries (seven bugs across the
  batch, all independently re-reviewed PASS).
- `docs/engineering/orchestrator-kickoffs/` — 20 files now: the six
  landed/executing-batch kickoffs kept for reference, `audit-remediation-w0.md`
  and `-w1.md` (both used), plus the five frozen-batch kickoffs
  (`filesystem-snapshots.md`, `plugin-system.md`, `loops.md` — now landed but
  kept — `turn-vs-run.md`, `feedback-carrying-denial.md`, `code-mode.md`).
- `docs/engineering/templates/` — the process-documentation package, committed
  (`b7bcaa8f`). Start here if reviving this process cold.
- Torque: project `PRJ-20260417-0002`, no outstanding tool blockers.

## What's next

1. **Resolve the `06/03` dispatch question** (see "Still open" above) with
   the operator — this is now the only real blocker on Wave 2's kickoff.
2. **Write Wave 2's kickoff** once #1 is settled — Wave 2a (`04/01`-`05`,
   `05/01`) and Wave 2b (`06/01`, re-scoped `06/02`, `07/01`-`05`) per the
   batch README/INDEX, both now synced against `eebe0f32`'s decisions (see
   "Tracking sync done this session"). `06/03` is tracked separately,
   out-of-wave — don't fold it into either unit's dispatch.
3. **The freeze stays in effect for everything else** — do not dispatch
   Filesystem Snapshots, Plugin System, or anything else no matter how ready
   its kickoff looks, without explicit fresh operator authorization.
4. Fast-follow candidate, not urgent: the leftover-archive-file hygiene bug
   in `internal/plugin/install/` (see Wave 1 notes above).
5. `TASKS/phase-2/07-audit-agent-roster.md` — still open, still needs review,
   unchanged for several handoffs now.
6. Architecture docs never reviewed, still untouched:
   `19-api-cli-runtime-parity.md`, `25-plugin-conformance-harness.md`,
   `26-architecture-enforcement-tests.md`, `docs/engineering/engineering-principles-draft.md`,
   `docs/launch-site/`.
