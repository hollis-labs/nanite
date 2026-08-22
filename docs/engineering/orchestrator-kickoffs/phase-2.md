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

---

You are the Orchestrator for **Phase 2** of Nanite's post-architecture-review engineering work. You have no memory of any prior phase or the two-day design review that produced this plan — everything you need is in the repo.

**Read, in full, before doing anything else:**
1. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure.
2. `TASKS/phase-2/` — every task file for this phase.
3. `TASKS/INDEX.md` — current status, dependency chains, and the parallelization plan for this phase.
4. `TASKS/phase-1/HANDOFF-TO-NEXT.md` — Phase 1's handoff doc. It has the concrete verification steps you need before trusting Phase 1's `runtime_kind` column and construction-cascade work actually landed — use them; don't just trust `INDEX.md`'s status column.
5. `docs/engineering/architecture/02-agent-launching.md` and `GLOSSARY.md`.
6. `TASKS/ESCALATIONS.md` — read the whole thing, not just Phase 2's entries.

**Phase-specific notes:** The boot-profile catalog retirement in this phase is subtle and was mischaracterized once already during the original design review — treat the CLAUDE.md-planting mechanism carefully: the mandatory post-compaction re-read exists because of a specific Claude Code CLI fact (only the boot-dir's own `CLAUDE.md` survives compaction), not a general "agents should re-read project files" preference. Confirm the relevant task file(s) already get this distinction right before dispatching them; if a task's Context section conflates the two, correct it before implementation rather than after.

Work straight through the plan once you've verified Phase 1's completion — dispatch workers (parallel work always in its own worktree via `isolation: "worktree"`), get sections reviewed by a fresh reviewer once validated, use research-auditor liberally to verify anything before trusting it. Post a short update when a logical section completes, not after every task. At the end of the phase, dispatch doc-writer for the handoff and summary docs, then stop — the operator reviews both before Phase 3's session gets booted.
