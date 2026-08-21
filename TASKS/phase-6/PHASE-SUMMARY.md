# Phase 6 — Envelopes & Cards — Phase Summary

Operator-facing summary of what actually happened during Phase 6 execution, written after all six task files closed. Source: `TASKS/phase-6/01` through `06`'s Work Logs and Review notes, `TASKS/INDEX.md`'s Phase 6 section, `TASKS/ESCALATIONS.md`.

## What shipped

**Three Card types retired, folded into composed primitives** (per the architecture plan's "compose, don't multiply types" direction):

- `todo-list` → `list-card` with a new `data_source` field (`{kind:"todos", scope, scope_id}`). The live todo list (fetch + toggle) now renders through `list-card`'s own component via an internal live-data branch, not a separate card type. `TodoListCard.tsx` deleted.
- `plan-review` → `list-card` (steps) + `confirmation-card` (approve/reject/request-changes footer), both driven by the same `data_source` mechanism (`kind:"plans"` / `kind:"plan_approval"`). `PlanReviewCard.tsx` deleted. Notably, the real emitter turned out to be a prompt string inside the `plan_create` self-tool's description (`internal/selftools/self_tools.go`), not Go code in `internal/subagent/service.go` as originally assumed — this was traced and corrected during implementation, not left as a mismatch.
- `subagent-spawn-approval` → recomposed onto `ApprovalCard` (a materially bigger rebuild than the other two — a fully custom bespoke component, not a thin wrapper). The wire type string was deliberately kept unchanged; only the manifest's component wiring moved. `SubagentSpawnApprovalCard.tsx` deleted. Zero backend Go changes were needed.

**A new primitive: interactive tables with row/column actions**, built fresh (no prior implementation existed anywhere in this repo to port from). `table-card`'s schema now supports schema-validated `actions` at the row and column level; a new backend response handler (`TableCardActionHandler`) validates every action server-side against the persisted envelope, never trusting client-submitted data. This primitive is fully wired end-to-end but has **zero real production emitter yet** — flagged below.

**The phase's own highest-leverage item: Card data excluded from replayed conversation history.** Previously, once an agent showed a card (a todo list, a table, a plan review), its full JSON payload got baked into that turn's persisted message text and replayed in full into every subsequent turn's context, forever — the actual mechanism defeating the "harness injects rich data, agent context stays light" design goal at session scale. Fixed at the real read site (`internal/chat/context_client.go`'s `AssembleSlotSources`), not the compaction pipeline the architecture doc's wording implied (compaction structurally can't reach this data — traced and confirmed during implementation). Measured on a real historical session with 6 card-bearing turns: **44.5% reduction** in replayed-history size (76,936 → 42,716 chars; ~19,222 → ~10,669 estimated tokens).

**CLI-launched agents' boot content now sources the Card type list live**, instead of a hardcoded list (previously 7 types, 5 of them already cut in Phase 0) or pointers to two files that don't exist in this repo. Both planted files (`.sandbox/envelope-schema.md` and the CLAUDE.md addendum) now query the live in-process envelope registry at boot-plant time.

## Escalations this phase raised, and how they resolved

Per `TASKS/ESCALATIONS.md`'s 2026-08-18 "shared `.claude/libs/go-envelopes` replace-target" entry (a pre-existing, already-logged coordination hazard, not new to Phase 6): the shared external `go-envelopes` manifest directory is not worktree-isolated, so any task editing it can see other in-flight tasks' concurrent, uncommitted edits. Phase 6 hit this exactly as documented — tasks `01`, `02`, `03` all edited the same `manifest/envelopes.yaml`, and `04` touched a schema file in the same directory. **This is now resolved for Phase 6's specific edits**: the combined manifest/schema state, plus a related shared-library test-fixture fix (below), landed as go-envelopes commit `7978078c`, re-verified clean against this branch's full build/test/frontend baseline. The underlying hazard (the mirror still isn't worktree-isolated) remains a standing fact for any future phase that touches this shared library, not something this phase's fix eliminated structurally.

Two things worth distinguishing from a "the operator needed to make a call" escalation, since neither one was:

- **A real manual merge conflict in `ListCard.tsx`** (tasks `01` and `02` both independently added a live-rendering branch to the same file) was resolved by the Orchestrator by hand, keeping both additions side by side rather than force-unifying them, then re-verified with a full frontend build and test run (187/187 passing). Routine merge coordination, not an escalation.
- **A worker legitimately declined mid-task and was replaced.** The worker assigned task `03` was asked, partway through a chain of requests, to commit the combined go-envelopes checkpoint on behalf of four tasks (`01`-`04`), raised a reasonable concern about being asked to vouch for correctness beyond its own task's scope, and declined further action. The Orchestrator treated this as a legitimate call rather than pressing the worker, and re-routed the checkpoint-commit work to a fresh agent. That fresh agent additionally found the prescribed test-fixture fix for `registry_test.go`/`validator_test.go` (both hardcoded a now-nonexistent "schema-less core type" fixture) didn't actually work as suggested, and resolved it with a synthetic-fixture rewrite instead. **This was an Orchestrator-level process correction, not an operator escalation** — no operator decision was required or sought; full detail in `TASKS/INDEX.md`'s Phase 6 "Shared `libs/go-envelopes` mirror" note.

No unresolved Phase-6-specific entries remain open in `TASKS/ESCALATIONS.md`.

## Still flagged, deferred, or needing attention before the next phase starts

- **`table-card`'s interactive primitive has zero real production emitter.** Fully wired, reachable, and covered by full-stack integration tests, but nothing in the codebase currently emits an *interactive* table-card outside the passive, non-addressable `card_show` path. Both review passes judged this an intentional primitive-before-consumer situation, not a defect — but the next phase (or whoever picks up the first real table-card consumer) should know it's untested against a real browser click, only against the real HTTP handler chain via Go integration tests.
- **`docs/engineering/architecture/08-cards.md`'s "known live bug" section is now stale.** It describes the CLI boot-content staleness that task `06` fixed. Neither task `06` nor any other task in this batch owned updating that doc — flagged by both review passes as something for whoever next touches that file to clean up.
- **A pre-existing, unrelated schema/data mismatch in the `subagent-spawn-approval` emitter**, noted (not fixed) during task `03`: the emitter's payload includes a `"provider"` field the schema doesn't declare under `additionalProperties:false`, visible in 2 of 5 real historical DB rows. Invisible in practice because this emission path never calls `envelope.ValidateData`. Out of scope for task `03`'s UI-composition rebuild.
- **A stale `"type": "nanite"` example survives in the planted boot-content's static "Interactive envelopes" section.** Not itself a registered envelope type — would fail real validation if used verbatim. Task `06`'s worker and its reviewer both logged this as out of that task's scope (static wire-format examples, not the dynamic type-list table) and left it as-is.
- **`internal/api/envelopes.go`'s `handleEnvelopeRespond` sets `responded_at` before invoking the `ResponseHandler`**, so a handler rejection has no retry path. Pre-existing, generic framework behavior, not introduced by this phase — flagged only because table-card's click-driven UX makes it more reachable than prior interactive cards.
- **Recurring process pattern worth the operator's awareness**: all five workers in the `01`-`05` batch initially left their work uncommitted in their own worktrees, requiring the Orchestrator to explicitly ask each one to commit before merge (verified via `git log`/`git diff --stat` against each branch afterward, matching what each worker's own report described — no content was lost, but it cost a round-trip every time). This mirrors a prior instance already logged against Phase 5 task 12. Worth a standing instruction in future dispatch prompts rather than relying on each Orchestrator to catch it after the fact.

## `TASKS/INDEX.md` state for Phase 6

All six task files are `reviewed`:

| Task | Status |
|---|---|
| 01-rebuild-todo-list-as-composition | reviewed |
| 02-rebuild-plan-review-as-composition | reviewed |
| 03-rebuild-subagent-spawn-approval-as-composition | reviewed |
| 04-build-interactive-table-row-actions-primitive | reviewed |
| 05-exclude-card-data-from-replayed-context | reviewed |
| 06-fix-cli-boot-content-card-type-list | reviewed |

Two review passes covered the phase: a combined pass on `01`-`05` (fresh reviewer, no shared context with any implementing worker — independently re-ran the full backend + frontend baseline, independently verified the external `go-envelopes` repo at commit `7978078c`, independently confirmed the `02` emitter correction and the `ListCard.tsx` manual-merge correctness) and a separate pass on `06` (also a fresh reviewer — independently re-ran the baseline and executed a throwaway test against a real `envelopes.LoadCore()`-populated registry to confirm the planted content's type list). Both passes returned PASS with no fix-and-re-review cycle needed on any of the six tasks. A real dogfeed was also performed (not just green tests): a scratch server built from this branch, booted against an isolated copy of a real production DB backup, confirmed a clean boot with `"envelope registry loaded","count":18,"source":"go-envelopes v0.1.0"` and exercised a real read path (`/api/plans` returning real production data in the shape task `02`'s composition expects).
