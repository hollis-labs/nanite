You are the Orchestrator for **Wave 4 of the Audit Remediation batch** —
dispatch unit `W4`, `TASKS/audit-remediation/09-production-islands/` — the
sixth of eleven dispatch units implementing the remediation program derived
from `docs/audits/2026-08-21-go-quality/REPORT.md` as sequenced by
`docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`. You have no memory
of the audit, the planning pass, or Waves 0-3's own execution — everything
you need is in the repo. **This kickoff covers six tasks: `09/01` through
`09/06`, all currently `not-started`.** (Six, not five, not seven — verify
this count yourself against `TASKS/INDEX.md`'s Wave 4 table before trusting
this line if any time has passed since this kickoff was written; a prior
kickoff in this batch stated a task count that didn't match its own listing,
which is exactly the class of error this note exists to guard against.)
Waves 0 through 3 are all closed — that is what makes this wave dispatchable
at all; do not re-open or re-verify their work beyond confirming closure,
covered below.

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
batches (Filesystem Snapshots, Plugin System, Loops, Turn vs. Run,
Feedback-Carrying Denial, Code Mode) stay frozen.

**All six architect decisions this wave needs are already made.** AD-06
through AD-11 — the six production-island wire/defer/retire calls — were all
decided together on 2026-08-22: **five retire, one wire.** Ran the
`requires_architect_decision`-vs-`ARCHITECT-DECISIONS.md` cross-check that
caught a real gap in each of the three prior waves (Wave 1's `01/02`/AD-25,
Wave 2's `04/04`/AD-26, Wave 3's `08/09`'s AD-27/AD-28) — **this wave comes up
clean**: all six findings (`GO-MEM-001`, `GO-MEM-002`, `GO-SVCEXEC-003`,
`GO-MCPTOOL-001`, `GO-MCPTOOL-002`, `GO-MCPTOOL-003`) have a corresponding
decided entry. Re-run it yourself in the pre-flight gate below rather than
trusting this paragraph — it's cheap and this is exactly the kind of claim
worth independently confirming before dispatch, not after.

This is also, by a wide margin, **the most deletion-heavy wave in the
batch**: roughly 3,400 production lines and 2,600 test lines removed, against
one new call site added. Treat it as a deletion wave with one wiring
exception, not six symmetric tasks.

---

## Things about this specific wave that won't be obvious from the batch README alone — read all of these before doing anything else

**(A) — READ THIS ONE FIRST. The batch README's own Wave 4 parallelization
note is obsolete, and this is the load-bearing correction for this kickoff.**
`README.md` previously said `09/01` and `09/02` must run **sequentially
against each other** because both wire into `internal/service/container.go`.
That reasoning assumed "wire" as the eventual decision for both. **Both were
decided `retire`** (AD-06, AD-07) — and a retired island, by construction,
never reaches the wiring step at all. Independently re-verified against
current `HEAD` while writing this kickoff, not just inferred from the
decision text: `grep -n "grounding\|Grounding" internal/service/container.go`
returns exactly one hit, a comment (*"memory.Service stays load-bearing for
grounding/recall.go"*) — not a construction or field-assignment call.
`grep -n -i hadron internal/service/container.go` returns exactly one hit, a
comment about `apps/hadron/cmd/hadrond` in an unrelated context (a startup
sequencing note that happens to mention another application's binary name).
**Neither island touches `container.go` in any way that survives their
retire decisions.** `README.md` has been corrected to say so; the two task
files' own "Planner sequencing" blocks (written before AD-06/AD-07 existed)
still say "not parallel-safe with each other" — treat this kickoff and the
corrected `README.md` as authoritative over that stale line, not the other
way around. **All six tasks in this wave are file-disjoint and fully
parallel-safe.** Confirm this yourself in the pre-flight gate rather than
taking it purely on this kickoff's word, given how consequential getting it
wrong would be (two agents editing the same composition-root file
concurrently).

**(B) `09/01`'s deletion reaches outside `internal/grounding/` — two more
files, and the exact line ranges need a fresh check.** Beyond the package
itself (`recall.go`, `outcome.go`, `types.go` + tests, ~530 prod/~360 test
LOC), AD-06's decision banner names two more required removals:
`internal/selftools/self_tools_transport.go:219-231` (the
`GroundingRecaller`/`GroundingLogger` fields and their comments) and
`internal/selftools/self_tools_dispatch.go:109-118` (the E2 pre-strategy
recall block, guarded by `if st.GroundingRecaller != nil`, plus the
explanatory comment at `:29`). **`self_tools_transport.go` carries 18
`context.TODO() /* TODO(ctx-sweep) */` markers from the `06/03` sweep that
landed after this task file was authored** — re-derive the exact current
line range for the two grounding fields via `grep -n` before deleting;
`self_tools_dispatch.go` shows zero such markers, so its citations are more
likely to still be accurate, but verify anyway. This deletion is also why
`10/02` (Wave 5, `SelfToolsTransport` decomposition) and `11/11` (Wave 6,
edits `self_tools_dispatch.go`) both shrink once `09/01` lands — see gotcha
(F).

**(C) `09/03` is the wave's only "wire," and unit tests are explicitly
insufficient to close it — this is stated as the direct lesson of how the
island came to exist in the first place.** The site is `handleLaunchTeam`
(`internal/api/team_runs.go:130`), which today calls only `LaunchTeamRun` and
never the already-built, already-tested `InstallTeamRunRouting`. AD-08's own
text is blunt about the acceptance bar: *"the guide's four-step reachability
proof is the acceptance bar: production entry point →
construction/registration/wiring → feature invocation → observable
behaviour. Being unwired-but-well-tested is precisely how this island came to
exist."* `team_routing.go` already carries 927 lines of tests against 771 of
implementation — more test investment than any other island in this folder —
and it was still unreachable in production until this task. Brief the `09/03`
worker explicitly: a passing unit-test suite on `InstallTeamRunRouting` in
isolation (which already exists and already passes today) is not evidence of
anything for this task's Done-means. The reviewer must independently confirm
an HTTP-level test exercises the real `handleLaunchTeam` path and observes
the resulting `agent_reflexes` rows, not just that `team_routing.go`'s
existing package tests still pass.

**(D) `09/04`'s retire has a specific, easy-to-get-wrong trap: do not carry
`tool.go`'s package doc over to the surviving `cache.go`.** AD-09 keeps
exactly one file (`cache.go`, the package's sole live export —
`NewResultCache`/`ResultCache`/`ResultCacheConfig`, used by
`internal/service/chat.go` and `container.go`) and deletes the other five
(`adapt.go`, `builder.go`, `register.go`, `tool.go`, `yaml_loader.go`) — the
largest single deletion in the batch, ~1.8k prod / ~1.7k test lines.
`tool.go` is where the package's current doc comment lives, and it actively
and falsely claims the (dead) builder pattern is *"the primary way to
construct tools in Go code."* **That false claim is the finding's own
headline evidence** — deleting `tool.go` but reflexively moving its doc
comment to whatever file becomes the new home for the package doc would
resurrect the exact misstatement this task exists to remove. The worker must
write a fresh package doc describing what `internal/tool` actually is once
only `cache.go` remains: a result-cache package, not a tool-construction
package.

**(E) `09/05` and `09/06` were decided as one question, and `09/05` carries a
real, currently-paid, and easy-to-half-fix boot cost.** AD-10 (retire
`ranking.go`'s `RankTools`/`SelectWithSignals`/`SelectToolsAugmented`) and
AD-11 (retire `tool_knowledge.go`) were decided together because both answer
"what tools match this intent" alongside the live `intent.go` keyword
scorer — after both retires, `intent.go`'s `SelectByIntent` is the sole
surviving mechanism (down from three). **`09/05` is not just a file
deletion**: `internal/service/container.go:692-710` unconditionally calls
`SetMemoryRecaller`/`SetSkills` on every `nanite serve` boot — a real
memory-service round-trip and a real skills-directory filesystem read —
feeding fields that only `SelectToolsAugmented` (the code being deleted)
ever reads. Deleting `ranking.go` and `broker.go`'s dead methods without also
removing (or otherwise resolving) this boot-time wiring block leaves the I/O
cost being paid for literally nothing. The task file's own Done-means makes
this explicit; don't let a worker treat it as an optional cleanup separate
from the "real" deletion. **Both `container.go` (8 ctx-sweep markers) and
`broker.go` (1 marker) have drifted since this task file was authored** —
re-verify the `:692-710` line range and the exact method line numbers before
editing.

**(F) Four tasks outside this wave shrink once Wave 4 lands — informational
for whoever writes Wave 5/6/8's kickoffs, not action items for you, but
worth recording in the end-of-wave handoff:**
  - `10/02` (Wave 5, `SelfToolsTransport` decomposition) — its capability-domain
    surface shrinks once `09/01` removes the grounding fields (gotcha B).
  - `11/11` (Wave 6, the dispatch-reflex sync-test task) — edits
    `self_tools_dispatch.go`, the same file `09/01`'s E2-block deletion
    touches.
  - `13/01` (Wave 8, confirmed dead-code removal) — its dead-code list
    shrinks twice: once from AD-07 (the Hadron gate becomes a deletion `09/02`
    already performed, not `13/01`'s job anymore) and once from AD-09
    (`internal/tool`'s five dead files, same logic). Re-derive `13/01`'s list
    fresh after this wave lands — do not trust its authored version.
  - `11/12` (Wave 6, `DevServerName` constant dedup) — touches
    `internal/toolclient/broker.go`, which `09/05`'s retire also edits;
    re-verify its citations once `09/05` lands.
  None of these four are in scope for you to touch or verify further right
  now — just make sure whoever authors those later kickoffs knows to
  re-derive scope rather than trust the pre-Wave-4 task files verbatim.

---

## Read, in full, before doing anything else

1. `.claude/agents/orchestrator.md` — your own role definition.
2. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure,
   including the two hard-won safety rules (no repo-global `git stash` across
   worktrees; live-verification writes target an explicit scratch path).
3. `TASKS/audit-remediation/README.md` **in full** — the freeze section
   (AD-24), the dispatch-model rationale, and the (just-corrected) Wave 4
   row and parallelization note.
4. `TASKS/audit-remediation/ARCHITECT-DECISIONS.md` — read the full "Wave 4
   decisions — the six production islands" section (AD-06 through AD-11,
   decided together, one shared block) in full, not just the summary table.
   It carries the per-decision reasoning your workers need, including the
   "deletion coupling" note that names gotcha (F)'s four downstream tasks
   directly.
5. All six task files, in full: `09-production-islands/01-*.md` through
   `06-*.md`.
6. `TASKS/audit-remediation/WAVE-1-HANDOFF.md`, `WAVE-2-HANDOFF.md`,
   `WAVE-3-HANDOFF.md` (whichever exist by the time you read this) — prior
   waves' worked records.
7. `TASKS/teams/HANDOFF.md` — required background for `09/03` specifically;
   its line 30 is the origin of the self-flagged risk AD-08 resolves, and it
   documents routing-failure-mode design decisions (`09/03`'s own task file
   forbids re-litigating them) that any wire implementation must respect.
8. `TASKS/INDEX.md`'s freeze banner and its own "Audit Remediation" section —
   confirm Waves 0-3 (all thirty-one of their tasks across `00`-`08`) show
   `reviewed`/`validated`, and the Wave 4 row.
9. `TASKS/ESCALATIONS.md` — the 2026-08-22 entries, for context on what
   prior waves actually found.
10. `docs/engineering/GLOSSARY.md` — check before locking any new name.

## Mandatory pre-flight gate — confirm all of the following before dispatching anything

1. **Waves 0 through 3 are all closed.** `TASKS/INDEX.md`'s rows for
   `00/01`-`00/02`, `01/01`-`03/01`, `12/02`, `04/01`-`07/05` (plus
   `06/03`/`06/04`), and `08/01`-`08/10` should all show `reviewed` or
   `validated`. Confirm directly.
2. **AD-06 through AD-11 are all `decided` in `ARCHITECT-DECISIONS.md`.**
   Confirm each is still recorded as five retires and one wire (AD-08) — this
   kickoff's every gotcha above depends on that exact split still holding.
3. **Re-run the `requires_architect_decision`-vs-queue cross-check yourself**
   for `GO-MEM-001`, `GO-MEM-002`, `GO-SVCEXEC-003`, `GO-MCPTOOL-001`,
   `GO-MCPTOOL-002`, `GO-MCPTOOL-003` against `findings.json` and
   `ARCHITECT-DECISIONS.md`. It came up clean when this kickoff was written;
   confirm it still does before treating this wave as gap-free.
4. **Independently re-verify gotcha (A)'s parallel-safety claim** — the
   `grounding`/`hadron` greps against `internal/service/container.go` cited
   above — before treating all six tasks as safe to dispatch concurrently.
   This is cheap (two grep commands) and the cost of being wrong (a real
   merge conflict in the composition root) is not.
5. **The dev freeze (AD-24) is still in effect.** Check `TASKS/INDEX.md`'s
   banner.

## Dispatch plan

**All six run fully parallel, worktree-isolated** — per gotcha (A), verified
file-disjoint with no cross-task dependency inside this wave. No sequencing
is needed among `09/01` through `09/06`.

Given the wave's actual shape (five deletions, one wire), expect meaningfully
different runtimes:

- `09/06` is the smallest and most self-contained (405/162 LOC, `retire`,
  no cross-package reach) — likely finishes first.
- `09/01`, `09/02`, `09/05` each have a cross-file reach beyond their own
  package (gotchas B, E) — budget accordingly.
- `09/04` is the largest deletion in the batch (~3.5k combined LOC across
  five files) plus the package-doc rewrite (gotcha D) — likely the long pole
  among the five retires.
- `09/03` is the only task doing real, new integration work rather than
  subtraction (gotcha C) — treat it as this wave's `02/01`-equivalent (Wave
  1's long pole): budget it the most runway, and consider a mid-task
  check-in on its error-handling policy choice (does a routing-install
  failure fail the whole launch response, or degrade best-effort — the task
  file flags this as a real open sub-decision, not prescribed).

## Review discipline

A fresh reviewer (no shared context with the worker) independently
re-verifies every task, not just re-reads the Work Log. Specific things worth
an independent check given this wave's shape:

- **Every task (all six)** — confirm the mandatory "re-verify against current
  source" step actually happened and is recorded in the Work Log before
  trusting the deletion/wiring proceeded — the guide's own Wave 4 warning
  (*"check current source first; some islands may have been completed after
  the audited commit"*) applies to every one of these, and this batch is now
  two days and dozens of commits past the audit.
- `09/01` — confirm `deadcode -test ./internal/grounding/...` (or full
  package removal) shows nothing orphaned, and confirm the
  `self_tools_transport.go`/`self_tools_dispatch.go` line ranges were
  re-derived fresh, not copied from the task file (gotcha B).
- `09/02` — confirm `deadcode -test ./internal/contextbroker/...` shows
  nothing else in the package became newly unreachable as a side effect.
- `09/03` — confirm the HTTP-level regression test actually exercises
  `handleLaunchTeam` and asserts real `agent_reflexes` rows exist post-launch
  (gotcha C) — not a service-level test on `InstallTeamRunRouting` in
  isolation, which already existed and already passed before this task.
  Confirm `TASKS/teams/HANDOFF.md` was actually updated to close its own
  line-30 open question.
- `09/04` — confirm the new `cache.go` package doc does not carry forward any
  variant of "primary way to construct tools" language (gotcha D); confirm
  `internal/service/tool_concurrency_classification.go`'s comment (which
  references `adapt.go`) was updated to stop citing a removed file.
- `09/05` — confirm `container.go:692-710`'s wiring block was actually
  resolved (removed, or explicitly justified if retained for another reason)
  per gotcha (E) — not left silently paying its boot cost for nothing.
- `09/06` — confirm zero references anywhere outside the two files before
  the deletion is treated as safe (the task file's own re-verification step).

## Scope fences — restate per task, don't let any of these drift

`09/01` does not touch `internal/memory`'s broader `memory.Service` (the
"stays load-bearing" comment gotcha (A) found is about `internal/memory`, not
this island). `09/02` does not audit the 4 live `ContextSource`
implementations for the same unclamped-relevance risk (a separate,
lower-priority finding, out of scope). `09/03` does not re-litigate
`TASKS/teams/HANDOFF.md`'s already-locked routing-failure-mode decisions
(fail-loudly on unavailable target, single lazy-resolution attempt, no
automatic coordinator reroute) and does not fix the separately-documented
lazy-slot-resolution-timing limitation. `09/04` does not touch or refactor
`cache.go`/`ResultCache` beyond the package-doc rewrite, and does not
evaluate or redesign `mcp.Manager`. `09/05` does not modify
`SelectToolsAsProvider`'s live keyword-based selection path
(`selectToolsUncapped`/`SelectByIntent`). `09/06` does not resolve `09/04`'s
or `09/05`'s own dispositions — the three-way "what matches this intent"
relationship is context, not a mandate to touch the other two.

## At the end

When all six tasks are `reviewed`, dispatch `doc-writer` for the end-of-wave
`WAVE-4-HANDOFF.md` and `WAVE-4-SUMMARY.md`, mirroring the prior waves' pairs
— written for whoever authors Wave 5's kickoff. Ask it to specifically
record: the actual final line counts removed (cross-check against the
~3,400 prod / ~2,600 test estimate in `ARCHITECT-DECISIONS.md`), `09/03`'s
error-handling policy choice for a routing-install failure, and — most
importantly for Wave 5/6/8's future authors — an explicit pointer to gotcha
(F)'s four downstream tasks (`10/02`, `11/11`, `13/01`, `11/12`) so their
scope-shrink isn't rediscovered from scratch. Then stop; the operator reviews
before deciding what's next.
