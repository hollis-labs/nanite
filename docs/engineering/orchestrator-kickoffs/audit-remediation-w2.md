You are the Orchestrator for **Wave 2 of the Audit Remediation batch** —
dispatch units `W2a` (`TASKS/audit-remediation/04-container-reaper-lifecycle/`,
`05-subagent-execution-ordering/`) and `W2b`
(`TASKS/audit-remediation/06-store-correctness/01-*.md` and `02-*.md`,
`07-runtime-correctness-lifecycle/`) — the third and fourth of eleven dispatch
units implementing the remediation program derived from
`docs/audits/2026-08-21-go-quality/REPORT.md` as sequenced by
`docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`. You have no memory
of the audit, the planning pass, or Waves 0/1's own execution — everything you
need is in the repo. **This kickoff covers twelve tasks: `04/01`, `04/02`,
`04/03`, `04/04`, `04/05`, `05/01` (W2a), and `06/01`, `06/02`, `07/01`,
`07/02`, `07/03`, `07/04`, `07/05` (W2b).** Wave 0 and Wave 1 are both closed
(all eight of their tasks `reviewed`) — that is what makes this wave
dispatchable at all; do not re-open or re-verify their work beyond confirming
closure, covered below.

**`06/03` — a thirteenth, out-of-wave task in the same `06-store-correctness/`
folder — is NOT part of this kickoff and must not be dispatched by you.** It
already landed (`fe16e138`, `validated`, with companion fix `06/04`) before
this kickoff was written. It matters enormously to everything below anyway —
see gotcha (A), the most important thing in this entire document.

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
remains the only work authorized to proceed; the eleven other sibling
batches (Filesystem Snapshots, Plugin System, Loops, Turn vs. Run,
Feedback-Carrying Denial, Code Mode, and the rest) stay frozen. Do not act on
any of them even if asked to check on their status in passing.

---

## Five things about this specific wave that won't be obvious from the batch README alone — read all five before doing anything else

**(A) — READ THIS ONE FIRST. `06/03`'s context-propagation sweep landed
(`fe16e138`) after every one of this wave's twelve task files was authored,
and it mechanically rewrote six of the eight production files this wave
touches.** Independently re-verified against current `HEAD` while writing
this kickoff:

- `internal/store/agents.go` — `DeleteAgentByID`, `GetAgent`, `DeleteAgent`
  (`06/01`'s targets) **all now take `ctx context.Context` as their first
  parameter.** `06/01`'s own task file shows the *pre-sweep* signatures in
  its "Current behavior" and "What to do" code blocks — a worker who copies
  that file's proposed fix verbatim will write code that does not compile.
  The corrected fix (verified against current `HEAD`, logic otherwise
  unchanged from the task file's own proposal):
  ```go
  func (s *Store) DeleteAgentByID(ctx context.Context, id string) error {
      a, err := s.GetAgent(ctx, id)
      if err != nil {
          if errors.Is(err, sql.ErrNoRows) {
              return nil
          }
          return fmt.Errorf("delete agent by id %s: %w", id, err)
      }
      return s.DeleteAgent(ctx, a.Slug)
  }
  ```
  Brief the `06/01` worker on this directly — thread the existing `ctx`
  parameter through, don't add a new one, don't drop it.
- `internal/store/plugin_settings.go` and `internal/store/durable_agents.go`
  (`06/02`'s targets, Sub-sections A and C) — `GetPluginSettings`,
  `ListPluginSettings`, `SyncDurableAgentInstanceConfig` all now take `ctx`
  too. Same instruction: thread the existing parameter, the fix logic itself
  is unaffected.
- `internal/service/container.go` (`04/01`, `07/04`) — **8** new
  `context.TODO() /* TODO(ctx-sweep): ... */` markers inserted, shifting line
  numbers throughout. `04/01`'s cited lines (~1213-1451) and `07/04`'s cited
  line (~1477 for `Shutdown`) both need fresh `grep -n` verification — do not
  trust either task file's line citations, even though both were already
  once revalidated at Wave 0 before the sweep landed.
- `internal/service/agent_deps.go` (`04/04` Part B's own file) — **13** new
  markers. `04/04`'s own text already flags this file's `safego.` count as a
  drift signal worth re-checking (it found zero `safego.` matches during
  authoring); independently re-confirmed still zero after the sweep, so that
  specific claim holds — but every line number in this file has moved.
- `internal/subagent/service.go` (`05/01`) — **1** new marker. Minor, but
  re-verify `05/01`'s cited line numbers (871-919, 1087-1123, 1130-1162,
  966-984) before trusting them.
- `cmd/nanite/main.go` (`07/02`, `07/03`'s doc-comment half is elsewhere,
  `07/05`) — **2** new markers. Re-verify `07/02`'s seven `slogx.Fatal` site
  lines and `07/05`'s `loadPersistedMCPServers` range (1233-1277) before
  editing.
- `internal/service/tool_cache_wiring.go` and
  `internal/service/managed_durable_configs.go` (`04/05`) — **4** and **5**
  new markers respectively. `buildRepairConfig`'s and
  `discoverManagedDurableAgentConfigs`'s own signatures are **unaffected**
  (neither takes `ctx` itself, confirmed) — this task is otherwise safe as
  written, just re-verify the two functions' current line numbers (114 and
  326 respectively, both shifted from the task file's citations) before
  reading their bodies.
- `internal/worktree/manager.go` (`07/01`) and `internal/background/service.go`/
  `pty.go` (`07/03`) — **zero** markers in all three. These two tasks are
  genuinely unaffected by the sweep; their own cited line numbers are the
  only ones in this wave you can trust without re-verification (still worth
  a quick `grep -n` given how much churn this repo has seen, but there is no
  specific reason to expect drift here).

**The instruction for every worker in this wave, without exception: re-derive
every line-number citation and every inline code snippet from the task file
against current `HEAD` via `grep -n`/direct reading before editing anything.
Do not trust a task file's quoted code block for `internal/store`,
`internal/service/container.go`, `internal/service/agent_deps.go`,
`cmd/nanite/main.go`, or `internal/subagent/service.go` — all five were
mechanically rewritten after these task files were authored.**

**(B) `04/04`'s Part B (`GO-SVCCORE-002`) has a real, unresolved
architect-decision gap — the same shape as Wave 1's `01/02`/AD-25 gap, and
just as easy to miss.** `04/04`'s own task file is explicit that Part B
("decide package-wide which `safego.Go` sites in `internal/service` need
`lifecycle.Manager` tracking vs. remain fire-and-forget") requires an
architect decision before any site is migrated. Confirmed independently: `grep
-n "GO-SVCCORE-002" TASKS/audit-remediation/ARCHITECT-DECISIONS.md` returns
**zero matches** — no `AD-NN` entry exists anywhere for this finding, despite
`findings.json` also marking it `requires_architect_decision: true`. **Part A
of `04/04`** (`GO-SVCCORE-001`, `DelegateAndAggregate`'s collector hang) has
no such gate — `requires_architect_decision: false` in both the task file and
`findings.json`, consistent — and can be dispatched and implemented
independently of Part B. Do not dispatch Part B until this gap is closed the
same way Wave 1 closed AD-25: either ask the operator directly which
`safego.Go` sites need tracking (per the task file's own "if this goroutine
is still running when `Shutdown()` returns, does anything break" test) and
record the call as a new `AD-NN` entry in `ARCHITECT-DECISIONS.md` with
reasoning, or use `research-auditor` to confirm this gap still stands and then
escalate to the operator. **Do not let a worker resolve this unilaterally.**

**(C) `06/02`'s own "Depends on"/"Touches" frontmatter is stale relative to
its own re-scope banner — `06/01` and `06/02` are file-disjoint now and can
run in parallel, not sequenced.** `06/02`'s header still reads "Depends on:
`06/01` (same file, `internal/store/agents.go`)" and its "Touches" line still
lists `agents.go`/`sessions.go` — but the task file's own AD-14 re-scope
banner (added 2026-08-22, above the `## Context` section) removes all of that
scope: `GO-STORE-005`'s package-wide `agents.go`/`sessions.go` work moved
entirely to `06/03`, which has now landed. Post-re-scope, `06/02` is really
just two sub-sections against `internal/store/plugin_settings.go`
(Sub-section A) and `internal/store/durable_agents.go` (Sub-section C) —
neither touches `agents.go` at all. **`06/01`'s real touch is `agents.go`
only.** The two tasks are file-disjoint; dispatch them in parallel, not
sequentially — the "same file" rationale the header gives for sequencing them
no longer holds. (This also means the batch README's own Wave 2 row for
`06/02`, which still shows `06/01` as a dependency, is one step further
behind than this kickoff — treat this kickoff's reading as authoritative for
this specific point, and flag the README for a sync pass at end-of-wave.)

**(D) Two tasks in this wave (`04/01`, `05/01`) deliberately override
`findings.json`'s `requires_architect_decision: true` down to `false` in
their own frontmatter, with documented reasoning — this is intentional per
this batch's established convention, not an oversight to "fix" by re-gating
them.** `04/01` (`GO-LIFE-001`) and `05/01` (`GO-EXEC-001`/`GO-EXEC-002`) each
explain directly in their own Context sections why the underlying finding's
recommendation is a concrete mechanical direction, not an open design
question, and treat `false` as "this task's working assumption, a planner can
override." Neither needs an `AD-NN` entry before dispatch. This is a
different situation from gotcha (B) above — do not conflate the two. If you
disagree with either self-override on inspection, escalate before dispatch;
nothing commits you to accepting it, but the default is to trust it, matching
how this same convention was already validated (without objection) across
Wave 1's tasks.

**(E) `04/03` is an investigation task, not an implementation task — it has
no guaranteed fix, and its own "Done means" is a determination, not a patch.**
It has a **hard** dependency on `04/02` landing first (not just sequencing —
investigating a race timeout against a baseline with known un-shutdown test
containers measures the wrong thing) and no known root cause going in. Do not
staff or time-box it like the other five W2a tasks. If it concludes a fix is
needed beyond what `04/04` already covers, it must name that follow-up
explicitly (new task-file candidate) rather than leaving it as a loose Work
Log note — flag this to whoever picks up planning after this wave closes.

---

## Read, in full, before doing anything else

1. `.claude/agents/orchestrator.md` — your own role definition.
2. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure,
   including the two hard-won safety rules (no repo-global `git stash` across
   worktrees; live-verification writes target an explicit scratch path).
3. `TASKS/audit-remediation/README.md` **in full** — the freeze section
   (AD-24), the dispatch-model rationale, and the Wave 2 row of the
   dispatch-precondition table. **Note gotcha (C) above: this file's `06/02`
   row is stale; trust this kickoff over it on that one point.**
4. `TASKS/audit-remediation/WAVE-1-HANDOFF.md` — written for whoever authors
   this wave's kickoff; covers Wave 1's closing state and the tracking-hygiene
   gaps found and fixed there (a recurring pattern in this batch — see
   gotcha (C) above for this wave's instance of the same class of gap).
5. `TASKS/audit-remediation/ARCHITECT-DECISIONS.md` — read AD-14, AD-17, and
   AD-18 in full (not just their one-line queue entries); all three are
   `decided` and gate this wave. Also skim the rest of the 25-item queue —
   15 later-wave decisions are still `open` (AD-05, AD-06 through AD-11,
   AD-12, AD-13, AD-15, AD-16, AD-19 through AD-22) — none of them gate this
   wave, but you'll recognize them if a Wave 3+ cross-reference comes up.
6. All twelve task files, in full: `04-container-reaper-lifecycle/01-*.md`
   through `05-*.md`, `05-subagent-execution-ordering/01-*.md`,
   `06-store-correctness/01-*.md` and `02-*.md` (**not** `03-*.md` or
   `04-*.md` — those are `06/03`/`06/04`, already landed, not yours to
   dispatch), `07-runtime-correctness-lifecycle/01-*.md` through `05-*.md`.
7. `06-store-correctness/03-full-context-propagation-sweep.md` — **read this
   even though you will never dispatch it.** Its own "✅ CLOSED" banner and
   Work Log record exactly what changed and why; it is the primary source
   for gotcha (A) above, and its Verification section's commands
   (`grep -rhoE '\.Query\('` etc.) are a fast way to independently confirm
   the sweep's completeness claim yourself before trusting it.
8. `TASKS/INDEX.md`'s freeze banner at the top, and its own "Audit
   Remediation" section — the Wave 0, Wave 1, Wave 2a, and Wave 2b rows, plus
   the note directly under the Wave 2b table about `06/03`'s landed status.
9. `TASKS/ESCALATIONS.md` — the 2026-08-22 entries, especially the one titled
   "`06/03`'s context sweep exposed a latent cancellation hazard" (the
   `06/04` story) — useful context for why `internal/service`'s shutdown
   paths now behave slightly differently than these task files assumed, even
   though none of this wave's tasks touch the specific function that
   regressed.
10. `docs/engineering/GLOSSARY.md` — check before locking any new name.

## Mandatory pre-flight gate — confirm all of the following before dispatching anything

1. **Wave 0 and Wave 1 are both closed.** `TASKS/INDEX.md`'s Wave 0 and Wave 1
   rows should show all eight tasks (`00/01`, `00/02`, `01/01`, `01/02`,
   `02/01`, `02/02`, `03/01`, `12/02`) `reviewed`. Confirm directly.
2. **AD-14, AD-17, and AD-18 are `decided` in `ARCHITECT-DECISIONS.md`.**
   They were as of `eebe0f32`. Confirm the `**Status:**` line on each still
   reads `decided`.
3. **`06/03` is `validated` and `06/04` is closed alongside it.**
   `06-store-correctness/03-*.md`'s own status line should read `validated`,
   with the `✅ CLOSED` banner referencing `fe16e138`. If it does not — if
   `06/03` somehow regressed to `not-started` or is mid-flight again — **stop
   immediately and escalate to the operator rather than proceeding.**
   Everything in gotcha (A) above assumes `06/03`/`06/04` are done; if that
   assumption is wrong, this entire kickoff's line-number guidance is wrong
   in the other direction (citations would be pre-sweep-accurate again, not
   post-sweep-stale) and needs a fresh read before any task in this wave
   touches `internal/store` or its callers.
4. **The dev freeze (AD-24) is still in effect.** Check `TASKS/INDEX.md`'s
   banner. If it has lifted since this kickoff was written, that changes
   nothing about *this* wave's own authorization but means the "don't touch
   other batches" instructions above may be stale — confirm with the operator
   if genuinely unclear.
5. **Resolve the `04/04` Part B gap (gotcha B) before dispatching that
   specific sub-part.** Part A (`GO-SVCCORE-001`) has no such gate and can
   proceed on its own schedule.

## Dispatch plan

**W2a** (`04/01` ∥ `04/02` ∥ `04/04` ∥ `04/05` ∥ `05/01`, five-way parallel,
worktree-isolated — cross-checked against each task's own `Touches` list,
`04/01` and `04/04` are both in `internal/service` but disjoint files
(`container.go` vs. `delegation.go`/`events_composite.go`/`agent_deps.go`) so
this is safe), **then `04/03`** (hard dependency on `04/02`, not
sequencing-only — do not start it until `04/02` has actually landed and its
container-shutdown fix is in the baseline `04/03` measures against).
`04/04`'s Part A can dispatch immediately; hold Part B for gotcha (B)'s
resolution — consider dispatching Part A alone first and letting Part B join
once its decision lands, rather than blocking the whole task on the gap.

**W2b** (`06/01` ∥ `06/02` ∥ `07/01` ∥ `07/02` ∥ `07/03`, five-way parallel
per gotcha (C)'s correction — `06/01` and `06/02` are file-disjoint, not
sequenced), **then `07/05`** (depends on `07/02`, same file
`cmd/nanite/main.go`). `07/04` depends on `04/01` (same file
`internal/service/container.go`, cross-unit) — it can start as soon as
`04/01` lands, independent of the rest of W2a's progress; don't wait for all
of W2a to close before starting it.

You may run W2a and W2b concurrently as two separate dispatch efforts if you
prefer — nothing in either unit depends on the other except the single
`04/01` → `07/04` cross-unit edge noted above.

## Review discipline

A fresh reviewer (no shared context with the worker) independently
re-verifies every task, not just re-reads the Work Log — standard practice
from Wave 1, applies in full here. Specific things worth an independent check
given this wave's gotchas:

- **Every task touching one of the six ctx-sweep-affected files (gotcha A)**
  — confirm the worker actually re-derived current line numbers and current
  function signatures rather than copy-pasting the task file's own (stale)
  code blocks. This is the single highest-risk failure mode in this wave.
- `04/01` — re-run the two `go vet` "possible context leak" checks
  independently; don't take the Work Log's word that they're gone.
- `04/02` — confirm `go test -race ./internal/api` actually completes within
  the default 10-minute timeout (no override), not just "doesn't panic
  anymore."
- `04/03` — confirm the conclusion is backed by an actual goroutine dump or
  bisection artifact, not a narrative guess.
- `04/04` Part B — confirm an `AD-NN` entry actually exists in
  `ARCHITECT-DECISIONS.md` before this sub-part is marked done, not just
  referenced informally in the Work Log.
- `05/01` — confirm both new regression tests actually fail against a
  reverted (pre-fix) version of `internal/subagent/service.go`, not just that
  they pass post-fix.
- `06/01` — confirm the new failure-path test uses a real driver-level
  failure (e.g. a closed `s.DB`), not a mock that doesn't exercise the actual
  `errors.Is(err, sql.ErrNoRows)` branch.
- `07/01` — confirm the extended `TestCleanupOrphaned` actually asserts git
  branch state, not just directory/`List()` state.
- `07/02` — confirm all seven (re-verified current count) `slogx.Fatal` sites
  were converted, not just the terminal one.
- `07/03` — confirm `Status`/`Result` return a distinct "expired" state per
  AD-18, not a bare not-found — this is the part AD-18's own text flags as
  "most likely to be got wrong."

## Scope fences — restate per task, don't let any of these drift

`04/01` does not touch `internal/api`'s test-shutdown gap (`04/02`'s job) or
`internal/service`'s own race timeout (`04/03`'s job). `04/02` does not
attempt to fix `internal/service`'s own suite (confirmed zero `NewContainer`
calls there — no such gap exists). `04/04` does not migrate `safego.Go` sites
outside `internal/service`. `04/05` does not refactor either target function,
coverage only. `05/01` does not redesign the semaphore/cancellation
architecture, does not change `spawnFanoutCap`'s value. `06/01` does not
change `GetAgent`'s error-wrapping or touch `SweepPluginAgentProfiles`. `06/02`
does not turn into a Store abstraction rewrite (explicit non-goal, restated
from the guide) and does not re-open `GO-STORE-005`/`GO-STORE-001`/`GO-DEP-002`
— all three are closed, out of this task's scope entirely now. `07/01` does
not change the `cmd.Run() // best-effort` discard pattern. `07/02` does not
touch `message_cmd.go`/`plugin_cmd.go`'s `slogx.Fatal` sites unless Direction
B is explicitly chosen (AD-17 already chose Direction A — so this scope fence
is now absolute, not conditional). `07/03` does not build a general-purpose
eviction abstraction. `07/04` does not add idempotency guards to the
subsystems `Shutdown` fans out to, and does not touch
`internal/runtime/agent/manager.go`. `07/05` does not touch the `sse`/
`default` branches or `AddStdioServer`'s own error handling.

## At the end

When all twelve tasks are `reviewed` (Part A and Part B of `04/04` both
closed), dispatch `doc-writer` for the end-of-wave `WAVE-2-HANDOFF.md` and
`WAVE-2-SUMMARY.md` — mirroring Wave 1's pair, written for whoever authors
Wave 3's kickoff. Ask it to specifically record: the ctx-sweep drift this
wave had to work around (gotcha A), the `04/04` Part B decision once made and
its `AD-NN` number, and a note that `TASKS/audit-remediation/README.md`'s
`06/02` row needs a sync pass (gotcha C) if nobody has fixed it by then. Then
stop; the operator reviews before deciding what's next.
