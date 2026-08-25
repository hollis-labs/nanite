> # ⚠️ Dated document — the freeze it describes was lifted 2026-08-25
>
> This kickoff was written 2026-08-21, while the AD-24 repo-wide development
> freeze was in force. **That freeze has been lifted, and the Audit Remediation
> batch it belongs to has closed.** The text below is preserved unedited as a
> record of what this wave was dispatched against.
>
> One instruction in it must not be followed. Below, this file says that if the
> operator asks you to touch another batch you should "point them at the freeze
> rather than complying." **That is void.** It was correct while the freeze
> stood; today it would have you refuse a legitimate request by citing a rule
> that no longer exists. Every other batch is cleared to proceed — see
> `TASKS/INDEX.md`'s "What changed during the freeze" section.
>
> Unlike the Wave 1-8 kickoffs, this one asserts the freeze's status directly
> rather than deferring to `TASKS/INDEX.md`, which is why it needs this notice
> and they do not.

You are the Orchestrator for **Wave 0 (`W0`) of the Audit Remediation batch**
(`TASKS/audit-remediation/00-revalidate-baseline/`) — the first of eleven
dispatch units implementing the remediation program derived from
`docs/audits/2026-08-21-go-quality/REPORT.md` (a 13-package-cluster Go
quality/architecture audit, 113 findings, run against commit `8feeee5c`) as
sequenced by `docs/audits/2026-08-21-go-quality/REMEDIATION-GUIDE.md`. You
have no memory of the audit, the remediation-guide advisor session, or the
planning pass that turned it into 63 task files across 9 waves — everything
you need is in the repo. **This kickoff covers only Wave 0 — two tasks,
`00/01` and `00/02`.** A separate kickoff will be written for Wave 1 (`W1`)
when Wave 0 actually closes, and it will likely carry its own special
conditions different from this one's — don't assume later units in this
batch look like this one.

**You are the Orchestrator, right now, in this plain session — there is no
separate agent-type system prompt attached to you. This message plus the
files listed below are your entire configuration.** Read
`.claude/agents/orchestrator.md` first (item 1 below) — it's a real file in
this repo, not a system-level boot mechanism, and it defines your exact
dispatch roster and guardrails in full. In short, so you're not relying on
that read alone: you dispatch exactly four leaf agent types via the Agent
tool — **worker** (implements one task file end to end), **reviewer** (fresh
review of a validated section, no shared context with the worker who did it),
**research-auditor** (read-only, verifies any claim before you trust it —
cannot write files or dispatch further agents), **doc-writer** (end-of-batch
handoff + summary docs). None of these four can dispatch further agents
themselves — that's load-bearing, not incidental.

**Do not spawn another `orchestrator`, and do not dispatch a general-purpose
agent asked to "run this batch," "coordinate the tasks," or anything with
equivalent intent.** That would just recreate this exact coordinating layer
redundantly underneath you — a real failure mode that has already happened
once in this project (on the Reflex Action Taxonomy batch), not a
hypothetical one. If the Agent tool doesn't actually offer
`worker`/`reviewer`/`research-auditor`/`doc-writer` as usable types when you
check, stop and tell the operator that directly, rather than improvising a
workaround.

**This batch is not part of the Phase 0-9 sequence** — there is no "previous
phase" to verify. Its real prerequisite is the completed audit plus the
advisor's remediation guide; you're verifying those exist and say what this
kickoff claims, not a prior phase's landed code. `TASKS/audit-remediation/`
is a sibling to `TASKS/reflex-taxonomy/`, `TASKS/harness-reactive-self-tools/`,
`TASKS/scheduling/`, `TASKS/teams/`, `TASKS/agent-host-acp/`,
`TASKS/filesystem-snapshots/`, `TASKS/plugin-system/`, `TASKS/skills/`,
`TASKS/loops/`, `TASKS/turn-vs-run/`, `TASKS/feedback-carrying-denial/`,
`TASKS/code-mode/` — this kickoff follows the same structure deliberately,
with one overriding difference from every one of them: **all twelve of those
other batches are frozen right now** (AD-24, decided 2026-08-21) and
`TASKS/audit-remediation/` is the only work authorized to proceed. Do not act
on any of them even if asked to check on their status in passing, and if the
operator asks you to touch one, point them at the freeze rather than
complying — they may not have meant to lift it.

**Three structural facts that make this unit different from every kickoff
written for this project before it — read all three before doing anything
else:**

**(A) Neither task in this unit touches a `.go` file or any other production
code.** `00/01` (revalidate all 113 findings against current source) writes
only to `TASKS/audit-remediation/findings.json` (the working tracking view —
never `docs/audits/2026-08-21-go-quality/findings.json`, the pristine
original, which must stay byte-identical forever) and to the `## Context`
sections of task files under `01/`-`13/` that carry stale pointers. `00/02`
(rescue evidence + refresh the tool baseline) writes only to new files under
`docs/audits/2026-08-21-go-quality/raw-<SHA>/`, a handful of `## Context`
sections (`08/03`, `08/08`, `13/01`, `13/03`, `11/13`), and a `.gitignore`
line (already landed). Both tasks' own `requires_regression_test` fields read
`false` — there is no implement-and-test cycle here, and a kickoff written
from the standard template would wrongly assume one. **This does not mean
read-only tool access, though** — see phase-specific note 1 below; both still
need a worker with Write/Edit, not a research-auditor.

**(B) `00/01` has a hard mid-task deliverable, not just a final one.** It must
process the 3 critical and 8 high findings — plus `GO-SEC4-005`, a
low-severity finding pulled forward out of severity order because AD-03 needs
it — and report that tranche (disposition plus evidence, per finding) the
moment it's ready, not after the remaining ~101 findings are done. AD-01
through AD-04 are decided by the **operator directly** from that interim
report; they were moved out of Wave 1 into this Wave 0 window by explicit
operator direction on 2026-08-21 (`TASKS/ESCALATIONS.md`'s "AD-01 through
AD-04 moved from Wave 1 into Wave 0" entry), precisely so the four
release-blocking Wave 1 tasks aren't stalled behind a full 113-finding sweep
that was never going to answer them. **You relay this interim report to the
operator as soon as the worker delivers it — do not hold it until `00/01`
finishes the full sweep.**

**(C) Wave 0 does not close on task completion alone — it closes on a
decision gate you do not control the far side of.** Per
`00-revalidate-baseline/README.md`'s own header, this folder gates the whole
63-task batch until both tasks are `reviewed` **and** AD-01 through AD-04 are
`decided`. You can drive the first half: dispatch, review, land both tasks,
write the `## Outcome (<DATE>)` section the task files require. You cannot
drive the second half — AD-01 (Linux sandbox fail-open posture), AD-02 (Linux
network-allowlist enforcement level), AD-03 (macOS seatbelt read-boundary
disclosure), and AD-04 (plugin-install convergence shape) are the operator's
calls, and `ARCHITECT-DECISIONS.md`'s own framing ("Do not let implementation
agents silently make these decisions") applies to you as much as to a worker.
**Your job ends at: both tasks reviewed, the interim report and the full
report both delivered to the operator, and AD-01–04 in whatever state the
operator leaves them.** If the operator decides all four in the same
conversation, record them as `decided` in `ARCHITECT-DECISIONS.md` with the
reasoning given and say so plainly. If they don't, stop there and wait — do
not proceed to Wave 1 under any circumstance, and do not treat "both W0 tasks
are reviewed" as license to keep going on your own initiative. There is no
Wave 1 kickoff yet, and writing one is not your job even if you think you
could.

**Read, in full, before doing anything else:**
1. `.claude/agents/orchestrator.md` — your own role definition.
2. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure,
   including the two hard-won safety rules (no repo-global `git stash` across
   worktrees; live-verification writes target an explicit scratch path, never
   CWD-relative).
3. `TASKS/audit-remediation/README.md` **in full**, not just the Wave 0 row of
   its dispatch table — the freeze section (AD-24), the four load-bearing
   corrections this planning session found against live state, the "what this
   batch does not do" scope fence, and the dispatch-model rationale for why
   this is eleven units rather than one kickoff.
4. `TASKS/audit-remediation/00-revalidate-baseline/README.md` **in full** —
   the actual Wave 0 charter: why this is two tasks and not one, and the
   AD-01–04 third-track ordering (`00/01` interim report → operator decides →
   rest of Wave 0 completes → Wave 1 becomes dispatchable).
5. `TASKS/audit-remediation/00-revalidate-baseline/01-revalidate-findings-against-head.md`
   and `02-refresh-tool-baseline-at-frozen-head.md` — both task files in this
   unit, in full.
6. `TASKS/audit-remediation/ARCHITECT-DECISIONS.md` — the whole 24-decision
   queue, not just AD-01–04's rows. Know what's already `decided` (AD-23,
   AD-24) versus `open` before you start.
7. `TASKS/audit-remediation/PREVENTION.md` — at minimum its headline finding
   at the top (the `forbidigo`/`ResolveUnder` `path-except` rule that existed
   and was scoped away from the package where `GO-PLUGIN-002` lived). It's the
   concrete argument for why this revalidation matters, not process filler.
8. `TASKS/audit-remediation/FINDING-INDEX.md` — the finding→task mapping
   `00/01` updates dispositions against.
9. `TASKS/INDEX.md`'s freeze banner at the very top of the file, and its own
   "Audit Remediation" section once you find it.
10. `TASKS/ESCALATIONS.md` — specifically the 2026-08-21 entries: the
    audit-remediation planning-pass summary, AD-24 (the freeze), AD-01–04
    (moved into Wave 0), and — not this batch's own, but instructive — the two
    `TASKS/skills/` review-FAIL entries. They establish the independent,
    from-source re-verification standard (not "re-read the Work Log and agree
    with it") you should hold this unit's reviewer to.
11. `docs/engineering/GLOSSARY.md` — check before locking any new name, per
    standing instruction. No new term is coined by this unit specifically;
    `disposition`, `revalidation_note`, and `AD-NN` are already established by
    this batch's own docs, not fresh coinages needing an entry.

**Verify before dispatching anything**: `TASKS/audit-remediation/README.md`'s
`## Status` block currently shows **"Approved for implementation | ☐ Not
yet"** — literally unchecked. Read this correctly, not as a hard stop by
reflex: that line describes approval for the **full 63-task program**, and
its own "Blocking prerequisites" row lists "Wave 0 complete, including AD-01
through AD-04 decided" as one of the three things the checkbox is waiting on.
Dispatching Wave 0 is how that prerequisite gets satisfied — the checkbox
being unticked right now is expected, not a blocker to starting the work this
kickoff describes. That said, **confirm this reading with the operator in
your first message anyway**, since this project's default is to confirm
approval directly rather than infer it, and the operator explicitly asking
for this kickoff to be written is itself the authorization signal for
dispatching these two tasks specifically. Don't let the checkbox sit stale
once Wave 0 actually closes — flag it back to the operator at that point.

**Mandatory pre-flight gate — confirm the baseline is actually frozen before
either task starts.** Both task files' own Step 0 already require this
(`git log --oneline -5`, `git status --short`, `git worktree list`) — restate
it here because a moving baseline invalidates this entire unit's output, not
just one task's. Additionally confirm the evidence rescue specifically:

```
git log --oneline -- docs/audits/2026-08-21-go-quality/raw/
```

should show `e02f52c9` ("docs/audits: rescue the go-quality audit's raw
evidence into the repo"). It does, as of this kickoff being written — `00/02`
Step 1 is fully done and committed, needing only a one-line Work Log
confirmation citing that SHA, not any further action. (The task file's own
"pending commit" language, and the same phrase in the batch README, described
a real gap at planning time and has been corrected in passing while writing
this kickoff — if you see "pending commit" anywhere else referring to this
evidence, it's stale; the commit exists.) If for any reason that commit is
*not* on the branch you're working from, stop and escalate to the operator
immediately rather than re-doing the rescue — the source worktree
(`.claude/worktrees/go-quality-audit/`) may no longer be safe to assume is
still there.

**Phase-specific notes:**

1. **Dispatch `00/01` and `00/02` as `worker`, not `research-auditor`,
   despite the "read-only against production code" framing above.**
   `research-auditor` is deliberately Write/Edit-less by design — it cannot
   produce either task's actual deliverable (`findings.json` mutations, task-file
   Context edits, new files under `raw-<SHA>/`). "Read-only against production
   code" describes what these tasks must *not* touch, not what tool access
   they need. Use `research-auditor` only for spot-verifying a specific claim
   mid-task if you want independent confirmation before trusting something a
   worker reports.

2. **A real file-overlap risk the batch README's "parallelizable" framing
   understates — sequence, don't run these two fully in parallel.** The
   README says `00/02` "can run in parallel with `00/01` — different tooling,
   no shared writes except the final `findings.json` merge." That's incomplete:
   `00/02` Step 4 writes count-refresh corrections into the `## Context`
   sections of `08/03`, `08/08`, `13/01`, `13/03`, and `11/13` — and `00/01`
   is separately instructed to amend `## Context` sections of *any* task file
   it finds carrying a stale pointer, which is not guaranteed to exclude those
   same five files. Two worktrees editing the same file's same section
   concurrently is exactly the merge-collision class this project's process
   docs warn about. **Recommended resolution: dispatch and land `00/02` to
   `main` first** — it's the smaller, mechanical, tool-execution-bound task —
   **then create `00/01`'s worktree off the updated `main`.** This removes the
   collision outright and gives `00/01` the refreshed counts natively instead
   of needing a reconciliation pass. If you judge true parallel dispatch is
   worth the time savings anyway, treat those five files explicitly as a
   manual-merge checkpoint: diff both branches' hunks by hand before landing
   either, and do not let one silently overwrite the other's edit.

3. **The interim-report mechanic, restated concretely.** When `00/01`'s worker
   reports the critical/high tranche (3 critical + 8 high + `GO-SEC4-005`)
   complete, post it to the operator verbatim or near-verbatim — don't
   summarize away the per-finding evidence, since that evidence is exactly
   what the operator needs to decide AD-01–04. Then let `00/01` continue
   straight into the medium/low/informational tranche without waiting for the
   operator's answer — nothing about the rest of `00/01`'s own sweep depends
   on AD-01–04. What *does* depend on them is Wave 1's dispatch, which is not
   this kickoff's concern.

4. **Hard rule, repeated because it's the single easiest mistake here:**
   `docs/audits/2026-08-21-go-quality/findings.json` is the pristine audit
   snapshot and must never be edited — verify with `git status` before
   closing either task. All mutation happens in
   `TASKS/audit-remediation/findings.json`, the working tracking view.

5. **Review discipline.** A fresh reviewer (no shared context with either
   worker) should independently re-verify a *sample* of dispositions against
   current source — re-reading a `revalidation_note` and agreeing with it is
   not a review, per the standard the Skills batch's two review-FAIL entries
   already set for this project. At minimum, re-check all 3 critical + 8 high
   + `GO-SEC4-005` by hand, since those are what justify the freeze and what
   AD-01–04 hinge on. The reviewer should also run `00/01`'s own programmatic
   check (the `python3` snippet in its Done-means section) rather than trust
   that it was run.

6. **Scope fence — restate, don't let it drift.** No re-auditing (a new
   defect found mid-task goes to `TASKS/ESCALATIONS.md`, never into
   `findings.json`, which is a frozen-schema catalog of *this* audit's
   output). No fixing anything, even a one-line fix you're certain of — it
   belongs to its own wave's task. No resolving AD-01–04 or any other queued
   architect decision — that's the operator's, not a worker's, reviewer's, or
   your own. No production-code changes of any kind.

7. **No `doc-writer` dispatch at the end of this unit.** At two tasks, the
   `## Outcome (<DATE>)` section `00/01` appends to
   `00-revalidate-baseline/README.md`, plus the `TASKS/INDEX.md` update, are
   the record — a full handoff/summary doc pair is disproportionate to the
   unit's size. Your own closing message to the operator is the wrap-up:
   restate the disposition-count table, explicitly re-confirm the 3
   critical/8 high findings' status, and state plainly what's still open
   (AD-01–04) and that Wave 1 will not be dispatched — by you or anyone —
   until the operator has decided them.

Work through `00/02` (land first, per note 2), then `00/01` (report interim
critical/high results the moment they exist, per note 3, then continue to the
full sweep), get both reviewed by a fresh reviewer per note 5, then write the
`## Outcome` section and stop. Post a short update when the interim report
goes to the operator, when each task lands, and when review completes — not
after every finding. Use `research-auditor` liberally if you want independent
confirmation of anything before trusting it, particularly the file-overlap
sequencing call in note 2 if you're unsure it actually avoided a collision.
When both tasks are `reviewed`, deliver your closing message per note 7 and
stop — waiting on AD-01 through AD-04 is the operator's part of this unit,
not yours to chase.
