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

You are the Orchestrator for **Phase 1** of Nanite's post-architecture-review engineering work. You have no memory of any prior phase or the two-day design review that produced this plan — everything you need is in the repo.

**Read, in full, before doing anything else:**
1. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure.
2. `TASKS/phase-1/` — every task file for this phase.
3. `TASKS/INDEX.md` — current status, dependency chains, and the parallelization plan for this phase.
4. `TASKS/phase-0/HANDOFF-TO-NEXT.md` — Phase 0's handoff doc. It has the concrete verification steps you need before trusting Phase 0's deliverables are actually in place — use them; don't just trust `INDEX.md`'s status column (see your own **Before you start this phase** instructions in `.claude/agents/orchestrator.md`).
5. `docs/engineering/architecture/01-agent-construction.md` and `GLOSSARY.md`.
6. `TASKS/ESCALATIONS.md` — read the whole thing, not just Phase 1's entries. It has the process-failure history (including a fabricated log entry from an earlier planning pass) that shapes why your dispatch roster is restricted the way it is.

**Phase-specific notes:** None known. Phase 1 is foundational (schema/construction work) — the main real risk is Phase 0's cuts having changed the schema out from under Phase 1's migrations, so verify the *current* schema state directly rather than planning against a stale mental model of it. One item worth double-checking as you dispatch: "kill the file-reingest-on-boot pattern generally" (in the relevant task file) should already enumerate which specific reingest paths it means — if it doesn't, pin that down before dispatching that task rather than letting a worker guess at "generally."

Work straight through the plan once you've verified Phase 0's completion — dispatch workers (parallel work always in its own worktree via `isolation: "worktree"`), get sections reviewed by a fresh reviewer once validated, use research-auditor liberally to verify anything before trusting it. Post a short update when a logical section completes, not after every task. At the end of the phase, dispatch doc-writer for the handoff and summary docs, then stop — the operator reviews both before Phase 2's session gets booted.
