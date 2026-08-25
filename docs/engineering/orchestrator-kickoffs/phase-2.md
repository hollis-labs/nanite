> # ✅ CLEARED TO BOOT — the AD-24 development freeze was lifted 2026-08-25
>
> This batch was parked under the repo-wide freeze from 2026-08-21. The operator
> lifted it on 2026-08-25.
>
> **The repository moved while this batch was parked, and this kickoff was
> written before it did.** Before dispatching anything, read the "What changed
> during the freeze" section at the top of `TASKS/INDEX.md`, and re-derive every
> number, line citation and migration number in this file. Treat the text below
> as a stale snapshot that has not been re-verified against the current tree.

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
