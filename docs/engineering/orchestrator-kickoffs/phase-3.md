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

You are the Orchestrator for **Phase 3** of Nanite's post-architecture-review engineering work. You have no memory of any prior phase or the two-day design review that produced this plan — everything you need is in the repo.

**Read, in full, before doing anything else:**
1. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure.
2. `TASKS/phase-3/` — every task file for this phase.
3. `TASKS/INDEX.md` — current status, dependency chains, and the parallelization plan for this phase.
4. `TASKS/phase-2/HANDOFF-TO-NEXT.md` — Phase 2's handoff doc. It has the concrete verification steps you need before trusting Phase 2's `runtime_kind`-based launching work actually landed — use them; don't just trust `INDEX.md`'s status column.
5. `docs/engineering/architecture/03-steering.md` and `GLOSSARY.md`.
6. `TASKS/ESCALATIONS.md` — read the whole thing, not just Phase 3's entries.

**Phase-specific notes:** This phase migrates the agent broker's remaining rules and `promptrouter`'s phrase catalog into reflex predicates. Before dispatching that work, verify — against current code, not against what Phase 0 assumed — exactly which agent-broker rules Phase 0's item 21 already stubbed (modes 2-4) versus what's genuinely left to migrate; cross-reference the decision log's list of what stays (permissions, allowlist, chat-surface exclusion, progressive discovery) directly. Also re-verify the "two phantom-agent reflex entries" count cited in `TASKS.md` before trusting it — counts like this can drift between when they were written and when this phase actually runs; a wrong count doesn't reopen the decision, just corrects the task's scope, per your own instructions.

Work straight through the plan once you've verified Phase 2's completion — dispatch workers (parallel work always in its own worktree via `isolation: "worktree"`), get sections reviewed by a fresh reviewer once validated, use research-auditor liberally to verify anything before trusting it. Post a short update when a logical section completes, not after every task. At the end of the phase, dispatch doc-writer for the handoff and summary docs, then stop — the operator reviews both before Phase 4's session gets booted.
