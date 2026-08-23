You are the Orchestrator for **Wave 5 of the Audit Remediation batch** —
dispatch unit `W5`, `TASKS/audit-remediation/10-architectural-concentration/`
— the seventh of eleven dispatch units implementing the remediation program
derived from `docs/audits/2026-08-21-go-quality/REPORT.md` as sequenced by
`docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`. You have no memory
of the audit, the planning pass, or Waves 0-4's own execution — everything
you need is in the repo. **This kickoff covers three tasks: `10/01`, `10/02`,
`10/03`, all currently `not-started`.** Waves 0 through 4 are all closed —
that is what makes this wave dispatchable at all; do not re-open or
re-verify their work beyond confirming closure, covered below.

Three tasks is the smallest task count of any wave so far in this batch. **Do
not read that as "the easiest wave."** This is the batch's hardest wave, and
its gating shape is unlike anything you've dispatched in Waves 0-4.

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

---

## The thing that makes Wave 5 different — read this before anything else

Every prior wave's architect decisions were either already made before the
kickoff was written, or resolvable from evidence that already existed. **Two
of this wave's three tasks are gated on decisions that cannot be made until
the tasks themselves produce the evidence the decision needs.** AD-12
(`10/01`'s gate) and AD-13 (`10/02`'s gate) are both `open`, and **both are
supposed to be** — each one's own text in `ARCHITECT-DECISIONS.md` states
directly that it has a prerequisite deliverable: a responsibility map, which
only the task itself can produce. There is no version of "resolve the
decision, then dispatch" available here. The order is inverted.

**Wave 5 runs in three beats. Say this explicitly to whoever picks this
kickoff up, because the two failure modes are opposite and both real:**

1. **Map phase.** `10/01` and `10/02` run now. Each produces a responsibility
   map (and, for `10/01`, a phase-boundary proposal plus a characterization
   test suite) and **stops there** — no extraction, no refactor, no code
   moved. Both task files are explicit and repeated about this: *"do not
   attempt the actual decomposition in this task."*
2. **Decision.** The operator resolves AD-12 and AD-13 against the maps these
   two tasks produce. This is not something you or a worker can do — the
   decision's entire premise is that it needs evidence that doesn't exist
   yet.
3. **Extraction phase.** Once AD-12/AD-13 are decided, follow-on tasks
   (not yet written — do not pre-create them, that's planner/architect work
   per this batch's own README) execute the approved boundaries.

**This kickoff dispatches beat 1 only.** If you find yourself waiting on a
decision before dispatching `10/01`/`10/02` — stop, that's backwards, the
map is the input to the decision, not the output of it. If you find yourself
extracting code, planning phase 2 work, or scoping follow-on tasks as part of
`10/01`/`10/02` — also stop, that's the opposite mistake, jumping straight to
beat 3. Both task files' own Non-goals sections say this directly; the risk
is an Orchestrator reading the wave's *goal* (decompose two god-objects) and
skipping past what these two specific tasks actually deliver.

**`10/03` is unaffected by any of this and can land any time.** AD-14 is
already `decided` (accepted-risk, no code change — the narrow-interfaces
question this task documents was resolved back in Wave 2). `10/03` writes no
code; it is a review note recording that `Container`, `internal/store`, and
`internal/plugin.Host`'s high field/method counts were deliberately *not*
scheduled for a refactor, per the remediation guide's own explicit
guardrails. Dispatch it independently of the map-phase/decision/extraction
sequence above.

---

## Things about this specific wave that won't be obvious from the batch README alone

**(A) `10/01`'s target has grown since its task file was authored — re-derive
the span, don't trust the citation.** The task file cites `generateResponse`
at `chat_generate.go:122-2028`. Independently re-verified against current
`HEAD`: the file is now **3,684 lines** (was smaller at authoring time — the
ctx sweep and three subsequent waves' worth of changes landed in this
package since), and `generateResponse` itself now starts at **line 121**, not
122 — a one-line drift that's a useful canary for how much else has likely
shifted throughout the function's body. `chat.go` (the struct definition) is
now **1,407 lines**. None of the internal phase-boundary line numbers the
task file's own table cites (session load, agent resolve, tool-use loop,
etc.) should be trusted without a fresh read. This doesn't change the task's
scope or difficulty — it changes how literally the worker can follow the
task file's own citations, which is: not very. Brief the worker explicitly.

**(B) `10/02` is smaller than its task file suggests — Wave 4's `09/01`
already removed part of its surface, and this is worth independently
confirming rather than trusting the wave-ordering claim on faith.**
`10/02`'s own "Depends on" line (`09/01`, same file's fields) exists exactly
because AD-06 (Wave 4) removed the `GroundingRecaller`/`GroundingLogger`
fields and their consuming block from `self_tools_transport.go`. Independently
re-verified against current `HEAD` while writing this kickoff, not assumed
from the dependency line alone: `grep -n -i grounding
internal/selftools/self_tools_transport.go` returns exactly two hits, both
comments about an unrelated concept — a `tool_use_id` **"grounding check"**
(shallow vs. deep validation of a tool-call ID, nothing to do with the
retired `internal/grounding` package) — and `grep -n "internal/grounding"`
on the same file returns zero matches. The retire is complete; `09/01`'s
dependency is satisfied. `self_tools_transport.go` is now **2,481 lines**
(down from whatever it was pre-`09/01`) — re-verify the file's still-current
31-field/81-method counts before treating the task file's cited numbers as
exact, since Wave 4 already changed them once.

**(C) The guide prescribes `10/01`'s exact method, in order — follow it, do
not re-derive a lighter-weight approach.** Characterization tests first
(covering a no-tool-call turn, single-tool-call, multi-tool-call, provider
error mid-stream, context-overflow/compaction trigger, and plugin-initiated
cancel) → improve coverage specifically on the provider-error /
compaction-recovery / plugin-cancel branches (the 36.5% baseline coverage
figure is a whole-function average and may over- or under-state any one of
these three families — measure each directly, don't assume) → identify 3-6
coherent phases → write a candidate extraction boundary per phase → produce
the full `chatServiceImpl` responsibility map in the guide's exact format
(capability / fields owned / methods owned / shared mutable state /
dependencies / callers / candidate extraction boundary) → state explicitly
that any follow-on extraction reruns behavior/race/complexity checks after
**each individual** phase, not once at the end → frame a full rewrite as a
last resort, not a default. **`StreamManager`
(`internal/service/stream.go`, verified: 730 lines, its own doc comment
reads "Extracted from Engine's 6 sync.Map fields") is the named in-repo
precedent** — it is a real, already-completed instance of exactly this move,
applied to a smaller field cluster. Point the worker at it directly, not
just at the abstract method description.

**(D) `10/02`'s explicit constraint, worth restating because it's the
literal failure mode the guide warns against for this task specifically:
do not split solely to reduce field or method counts.** 31 fields / 81
methods is the audit's *signal* that something is worth examining, not the
justification for any specific split. The capability-domain map's evidence
of genuinely coherent, separable domains — or the lack thereof — is the only
valid basis for `10/02`'s direction recommendation. A worker who proposes
splitting `SelfToolsTransport` into N domain owners because "31 fields is a
lot" without the map actually demonstrating N cohesive, low-entanglement
domains has failed this task, even if the resulting design looks
individually reasonable.

**(E) `10/01` takes an exclusive lock on `internal/service` — this is a
binding constraint on the whole wave's sequencing, not just a note in one
task file.** It is a multi-phase characterization effort with behavior
re-verified after each phase (once extraction actually begins, in a future
wave); any concurrent edit to the package invalidates its characterization
tests. **Nothing else in this batch may touch `internal/service` while
`10/01` is in flight** — this includes not just `10/02` (different package,
`internal/selftools`, genuinely fine to run in parallel) but any Wave 6+
task that might otherwise look independently dispatchable. Since this
kickoff only covers Wave 5, the practical instruction is: don't let `10/01`
overlap with anything you might be tempted to pull forward from a later
wave, and note this constraint in the end-of-wave handoff for whoever plans
Wave 6.

---

## Read, in full, before doing anything else

1. `.claude/agents/orchestrator.md` — your own role definition.
2. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure,
   including the two hard-won safety rules (no repo-global `git stash` across
   worktrees; live-verification writes target an explicit scratch path).
3. `TASKS/audit-remediation/README.md` **in full** — the freeze section
   (AD-24), the dispatch-model rationale, and the Wave 5 row.
4. `TASKS/audit-remediation/ARCHITECT-DECISIONS.md` — read AD-12, AD-13, and
   AD-14 in full. AD-12 and AD-13 are short but load-bearing: both state
   their own prerequisite-deliverable shape directly. Skim the rest of the
   queue so you recognize later-wave references if a task file cross-cites
   one — do not re-derive their status; if you need it, re-check directly.
5. All three task files, in full: `10-architectural-concentration/01-*.md`,
   `02-*.md`, `03-*.md`.
6. `TASKS/audit-remediation/WAVE-1-HANDOFF.md` through `WAVE-4-HANDOFF.md`
   (whichever exist by the time you read this) — prior waves' worked records.
7. `internal/service/stream.go` — read `StreamManager` directly, not just
   the task file's description of it. `10/01`'s worker needs to study its
   actual shape (constructor, method surface, how `chatServiceImpl` delegates
   to it via a single `streams *StreamManager` field) to propose the same
   pattern credibly for the PTY/agent-runtime field cluster.
8. `TASKS/INDEX.md`'s freeze banner and its own "Audit Remediation" section —
   confirm Waves 0-4 all show `reviewed`/`validated`, and the Wave 5 row.
9. `TASKS/ESCALATIONS.md` — the 2026-08-22/23 entries, including one dated
   2026-08-23 about six unqueued architect-decision items in Wave 8's
   `13/01`/`13/02`/`13/05` (found while re-running this batch's
   `requires_architect_decision`-vs-queue cross-check across *all* remaining
   waves, not just this one — see "Standard hygiene" below). It does not
   block Wave 5; it's there so you don't have to rediscover it if anything
   in this wave leads you to skim ahead.
10. `docs/engineering/GLOSSARY.md` — check before locking any new name.

## Mandatory pre-flight gate — confirm all of the following before dispatching anything

1. **Waves 0 through 4 are all closed.** `TASKS/INDEX.md`'s rows for
   `00`-`09` (all forty-one prior tasks) should show `reviewed` or
   `validated`. Confirm directly.
2. **AD-12 and AD-13 are `open`, and confirm they're *supposed* to be.**
   Read each entry's own text and confirm it states a prerequisite
   deliverable (a responsibility map) rather than being an ordinary pending
   decision. If either has somehow been decided already without the map
   existing, stop and escalate — that would mean the process this kickoff
   describes was bypassed, not completed.
3. **AD-14 is `decided`.** Confirm `10/03`'s gate is genuinely satisfied and
   it can be dispatched independent of `10/01`/`10/02`'s beat structure.
4. **Re-run the `requires_architect_decision`-vs-queue cross-check for this
   wave's own findings** — `GO-SVCEXEC-001`, `GO-SVCEXEC-002` (AD-12),
   `GO-MCPTOOL-006` (AD-13), `GO-MCPTOOL-007` (informational, no action,
   `10/02`'s own Part B), `GO-DEP-001`, `GO-DEP-002`, `GO-STORE-001`,
   `GO-STORE-002`, `GO-PLUGIN-006` (AD-14) — against
   `ARCHITECT-DECISIONS.md`. It came up clean when this kickoff was written;
   confirm it still does.
5. **Independently re-verify gotcha (B)'s "10/02 shrank" claim** — the
   `grounding` greps against `self_tools_transport.go` — before treating
   `09/01`'s dependency as satisfied. Cheap, and this task's whole
   entry-point framing depends on it.
6. **The dev freeze (AD-24) is still in effect.** Check `TASKS/INDEX.md`'s
   banner.

## Dispatch plan

**`10/01` and `10/02` run in parallel with each other** — different
packages (`internal/service` vs. `internal/selftools`), no shared code, no
cross-task dependency inside this wave. Per gotcha (E), `10/01` still takes
an exclusive lock on `internal/service` for the duration of its own work;
that just means nothing *else* (in this wave or borrowed from a later one)
touches that package concurrently, not that `10/02` needs to wait for it.

**`10/03` runs independently, any time** — parallel-safe with both, writes
no code, has no dependency.

Expect `10/01` to be this wave's long pole by a wide margin — it is
producing a characterization test suite across six representative turn
shapes plus targeted coverage improvements plus a full responsibility map
plus a phase-boundary proposal, against a 3,684-line function that is, by a
5x margin, the single highest-complexity function this entire audit found.
Budget accordingly; consider a mid-task check-in once the characterization
suite exists and before the responsibility map/phase-boundary work begins,
so a reviewer can sanity-check the six turn shapes are genuinely exercising
real behavior before the rest of the task builds on top of that suite.

## Review discipline

A fresh reviewer (no shared context with the worker) independently
re-verifies every task, not just re-reads the Work Log.

- **`10/01`** — confirm the characterization suite exercises `generateResponse`
  through a real production entry point (the Dispatcher/`chatRunnerAdapter`
  path, per the task file's own "Current behavior" section), not a bare
  function call with hand-built arguments. Confirm the before/after coverage
  numbers for the three named branch families are real, scoped measurements
  (`go tool cover -func`), not the whole-file average restated three times.
  Confirm the responsibility map covers every visible cluster on the
  52-field/84-method struct, not just the PTY/agent-runtime cluster the task
  file pre-populates as an example. Confirm no production code in `chat.go`
  or `chat_generate.go` was touched — this task's entire deliverable is
  tests plus a written proposal.
- **`10/02`** — confirm the capability-domain map covers every tool name
  currently in the `CallTool` switch (re-counted against current source, not
  the task file's sampled/possibly-stale list). Confirm the direction
  recommendation's rationale cites domain cohesion/coupling evidence from the
  map, not field/method counts (gotcha D). Confirm Part B (`ToolClient`)
  was re-confirmed against current source, not just copied from the task
  file. Confirm no production code in `internal/selftools/` or
  `internal/toolclient/` was touched.
- **`10/03`** — confirm the five findings' cited metrics (`Container` 60
  fields/2 methods; `Store` 349 methods/30 fan-in/76 references; `Host` 38
  fields/123 methods) were re-checked against current source, and confirm an
  actual architect decision was obtained and recorded — this task's own
  "done" is the decision existing on record, not the file merely being
  present.

## Scope fences — restate per task, don't let any of these drift

`10/01` does not attempt any extraction, does not "fix" any behavior that
looks wrong while writing characterization tests (surface it as a discovery
instead — see the logging discipline below), does not treat metric reduction
as a goal, does not propose extracting all 52 fields/84 methods into new
types speculatively. `10/02` does not implement any extraction, does not
propose splitting `ToolClient`'s three responsibilities (the audit's own
verdict is explicitly "no action implied"), does not assume every one of the
~15 tool-domains must resolve the same way. `10/03` makes no code changes to
`Container`, `internal/store/*.go`, or `internal/plugin/host.go` under any
circumstance arising from this task alone — not a blanket interface-
segregation pass, not a `Container` restructuring, not a `Host` sub-registry
migration sweep, even if the metrics look alarming in isolation.

---

## Where your findings go — read before you write your handoff

Anything you or your reviewers find that must **outlive this wave** goes to
`TASKS/ESCALATIONS.md`, not only into `WAVE-5-HANDOFF.md`. Your handoff is
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
- any newly discovered unwired feature, dead subsystem, or island
- any process incident, especially one touching operator data or state outside
  the repo
- any correction to a stated fact in a task file or doc
- anything whose owner is undetermined

Use `docs/engineering/templates/05-escalation-entry-template.md`'s Shape B for
these. They are findings-for-the-record, not stop-and-escalate.

**Say in your handoff which items you logged**, so the next author can confirm
nothing was lost between the two files.

**This wave is unusually likely to generate durable findings.** A
characterization pass across a 3,684-line function and a capability-domain
map across an 81-method god-object will surface things that are correctly
out of scope for `10/01`/`10/02` themselves — that's exactly the category
that got lost three times in this batch before this discipline existed. Err
toward logging.

## At the end

When all three tasks are `reviewed`, dispatch `doc-writer` for the
end-of-wave `WAVE-5-HANDOFF.md` and `WAVE-5-SUMMARY.md`. Because `10/01`/
`10/02`'s "done" is a map plus a pending decision, not a shipped refactor,
make sure the handoff states plainly: AD-12 and AD-13 are still `open`
pending operator review of the two maps, and no extraction work should be
scoped or dispatched until that review happens — this is not a normal
"wave closed, move on" handoff. Name explicitly which findings you logged to
`ESCALATIONS.md` per the discipline above. Then stop; the operator reviews
the maps and decides AD-12/AD-13 before anyone plans what comes after.
