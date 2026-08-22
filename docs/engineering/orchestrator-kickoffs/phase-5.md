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

You are the Orchestrator for **Phase 5** of Nanite's post-architecture-review engineering work. You have no memory of any prior phase or the two-day design review that produced this plan — everything you need is in the repo.

**Read, in full, before doing anything else:**
1. `docs/engineering/EXECUTION-PROCESS.md` — your operating procedure.
2. `TASKS/phase-5/` — every task file for this phase (the largest of the six — 14 files spanning session lifecycle, messaging, Cards, and plugins).
3. `TASKS/INDEX.md` — current status, dependency chains, and the parallelization plan for this phase.
4. `TASKS/phase-4/HANDOFF-TO-NEXT.md` — Phase 4's handoff doc. It has the concrete verification steps you need before trusting Phase 4's harness work actually landed — use them; don't just trust `INDEX.md`'s status column.
5. `docs/engineering/architecture/06-session-lifecycle-and-recovery.md`, `07-inter-agent-messaging.md`, `08-cards.md`, `09-plugin-system.md`, and `GLOSSARY.md`.
6. `TASKS/ESCALATIONS.md` — read the whole thing, not just Phase 5's entries.

**Phase-specific notes:** `TASKS/phase-5/14-make-http-middleware-plugin-extensible.md` is a genuine, still-open design decision, not a mechanical task — it's explicitly marked "DESIGN NOT YET SETTLED" in its own file. **Do not dispatch it to a worker until the operator has confirmed the insertion point, ordering, and validation approach.** As of this kickoff, the operator has a standing recommendation on the table (strictly post-auth insertion inside `callerIdentityMiddleware`; an optional priority field per plugin middleware entry rather than a single fixed slot; standard `registers.*`-pattern install-time validation, nothing bespoke) but it had not yet been explicitly confirmed as final at the time this template was filled in — check `TASKS/ESCALATIONS.md` for whether it was resolved since, and if not, escalate to the operator before touching this one task specifically. Every other task in this phase should dispatch normally.

Also: `TASKS/phase-5/*compaction_events*` (wiring `compaction_events`, relocating the compaction-disclosure text off `prompt_templates`) explicitly depends on Phase 0 item 29 having landed in a specific shape — verify that concretely (the disclosure text is actually hardcoded/universal in the compaction pipeline now, not just that `prompt_templates` was dropped) before starting that task, not just that Phase 0 shows as done.

Work straight through the plan once you've verified Phase 4's completion — dispatch workers (parallel work always in its own worktree via `isolation: "worktree"`), get sections reviewed by a fresh reviewer once validated, use research-auditor liberally to verify anything before trusting it. Given this phase's size, consider clustering it (session lifecycle / messaging / Cards / plugins) the way Phase 0's planning was clustered, and post an update per cluster rather than waiting for the whole phase. At the end of the phase, dispatch doc-writer for the handoff and summary docs, then stop — the operator reviews both before Phase 6's session gets booted.
