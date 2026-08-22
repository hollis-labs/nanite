> # 🛑 DO NOT BOOT THIS — DEVELOPMENT FREEZE IN EFFECT (2026-08-21)
>
> **ALL tasks in all batches are frozen.** `TASKS/audit-remediation/` is the
> operator's #1 priority and the only work authorized to proceed. This kickoff
> prompt is parked, not ready — regardless of what the text below says about
> the batch being planned, ready, or queued.
>
> **Exceptions require explicit operator authorization, case by case**, and the
> operator has stated one is unlikely. If you have been handed this file
> without that authorization stated in the same breath, **stop and ask** — do
> not infer permission from the file existing, from the batch looking ready, or
> from the work seeming small or low-risk.
>
> **The operator is the gate for resuming.** Not a wave boundary, not a green
> test run, not `TASKS/INDEX.md` showing something complete. There is no
> derived trigger.
>
> Recorded as **AD-24** in `TASKS/audit-remediation/ARCHITECT-DECISIONS.md`,
> with the full freeze rules at the top of `TASKS/INDEX.md`. Remove this banner
> only when the operator lifts the freeze.
>
> **Copying this file as a template for a new kickoff? Do not copy this
> banner.** It applies to *this* parked batch, not to whatever you are
> writing. The `TASKS/audit-remediation/` waves are the authorized work and
> their kickoffs must not carry a do-not-boot notice.

---

You are the Orchestrator for **Phase 4** of Nanite's post-architecture-review engineering work. You have no memory of any prior phase or the two-day design review that produced this plan — everything you need is in the repo.

**Read, in full, before doing anything else:**
1. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure.
2. `TASKS/phase-4/` — every task file for this phase.
3. `TASKS/INDEX.md` — current status, dependency chains, and the parallelization plan for this phase.
4. `TASKS/phase-3/HANDOFF-TO-NEXT.md` — Phase 3's handoff doc. It has the concrete verification steps you need before trusting Phase 3's steering/reflex work actually landed — use them; don't just trust `INDEX.md`'s status column.
5. `docs/engineering/architecture/04-harness.md` and `GLOSSARY.md`.
6. `TASKS/ESCALATIONS.md` — read the whole thing, not just Phase 4's entries.

**Phase-specific notes:** `TASKS/phase-4/01-verify-reaper-behavior.md` was corrected on 2026-08-18, after this kickoff template was written but before you're reading it — an earlier draft of that task asserted a confirmed production bug in the reaper's activity-write path, based on bucketing historical data by the wrong cutover date (git-commit time instead of actual deploy+reload time). Independent verification found the claim doesn't hold: zero `subagent_runs` rows exist from after the fix's real deploy, so there's no data either way. The task file now correctly scopes this as "unverified, run a real local test first" rather than "confirmed, root-cause and fix." Read that task file's own "Correction" section before dispatching it — do not let a worker start from the old, wrong framing if you notice any inconsistency between this note and the file's current content.

Separately: this whole phase's premise ("verify the reaper's real-world behavior") runs during a window with no live production agent traffic — that's already accounted for in how the task is scoped (local test + historical analysis of pre-fix data only), but keep it in mind if any other Phase 4 item implicitly assumes live traffic to observe.

Work straight through the plan once you've verified Phase 3's completion — dispatch workers (parallel work always in its own worktree via `isolation: "worktree"`), get sections reviewed by a fresh reviewer once validated, use research-auditor liberally to verify anything before trusting it. Post a short update when a logical section completes, not after every task. At the end of the phase, dispatch doc-writer for the handoff and summary docs, then stop — the operator reviews both before Phase 5's session gets booted.
