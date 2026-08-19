# Data-migrate every current `.nanite/agents/*.md` onto `roles`/`agents`

**Phase:** 1
**Status:** out-of-scope (operator-directed, 2026-08-18) — do not dispatch
**Depends on:** n/a — cut for Phase 1, see banner below
**Touches:** n/a — no code changes; this task is not executed as part of Phase 1

## ⚠️ Out of scope for Phase 1 — operator decision, 2026-08-18

The operator has backed up all 24 current `.nanite/agents/*.md` files (plus the durable-agent definitions) to a separate directory outside this repo. **None of them will be data-migrated into `roles`/`agents` as part of Phase 1** — including `loom-curator.md`/`atlas-curator.md`/`loom-weaver.md`, despite this file's original framing of Curator/Weaver as needing an end-to-end post-migration verification gate. No agent will be run/dispatched at all until Phases 1-5 are complete, so there is no urgency to preserve any specific legacy agent's behavior right now.

Going forward, the operator will create new agents **selectively, manually**, in the new `roles`/`agents` composition system once Phase 1's schema/API work (`01`-`09`) lands — using `09-build-assignment-ui-api.md`'s UI, not a bulk migration. This task's original "real design work" (grouping the 24-26 legacy personas into shared `roles` vs. distinct `agents` compositions) is not being done at all, by anyone, as part of this effort.

**Practical effects on other Phase 1 tasks**, already reflected in their own files and in `TASKS/INDEX.md`'s Phase 1 section:
- `05-fix-agent-skills-and-agent-projects-fks.md` no longer depends on this task — its "no file-based, DB-row-less agent exists" precondition holds independent of any migration, since nothing populates these two zero-row tables either way.
- `08-kill-file-reingest-on-boot-pattern.md` no longer needs to sequence immediately after this task landing — the legacy `.md` files stay in place, undisturbed, and continue being auto-ingested into `agent_profiles` exactly as they are today (harmless, since nothing dispatches them). `08`'s own value (a DB-side edit surviving a restart) stands regardless.
- The `.nanite/agents/*.md` files themselves are **not deleted** by this decision — they remain in place (backed-up copies exist separately) and continue to be discovered/ingested as `agent_profiles` rows exactly as today. Nothing in Phase 1 touches or removes them.

The original task content below is preserved for reference only — it describes work that is **not being done**, not a plan still pending execution.

---

## Context (historical — not executed)

TASKS.md Phase 1: *"Data-migrate every current `.nanite/agents/*.md` onto `roles`/`agents`."* Confirmed via `internal/agent/discovery.go`: `.nanite/agents/*.md` (project-source, priority 2 in `Discover()`) is genuinely Nanite's own runtime agent-profile source — not the unrelated `~/.nanite/roles/` developer-persona convention `GLOSSARY.md` warns not to confuse this with. 26 real files exist today: `agent-builder.md`, `agridd-project-manager.md`, `analyst.md`, `atlas-curator.md`, `atlas-librarian.md`, `backend.md`, `code-auditor.md`, `conductor.md`, `content-strategist.md`, `content-writer.md`, `file-backend.md`, `frontend.md`, `ideation-partner.md`, `loom-curator.md`, `loom-weaver.md`, `orchestrator.md`, `planner.md`, `plugin-dev-tasks.md`, `plugin-dev.md`, `project-manager.md`, `proxima.md`, `reviewer-backend.md`, `reviewer-frontend.md`, `task-planner.md`, `torque-supervisor.md`, `torque-task-writer.md`.

**Cross-reference before starting**: Phase 0 item 16 (`16-cut-external-agent-import.md`) cuts the adapter-discovery tier, not this project-source tier — unaffected. Phase 0 item 24 (`24-housekeeping-agent-profile-files.md`) reviews/deletes `agridd-project-manager.md`/`proxima.md` and confirms `torque-task-writer.md` as keep — **confirm that task's actual disposition before running this migration**, since a file it deletes shouldn't be migrated, and its "review then delete" framing means the final file list this task migrates may be 24, not 26.

### The real design work: grouping files into `roles` vs. leaving them as distinct `agents` compositions

This is the actual point of the role/scope/agent split — per architecture doc `01-agent-construction.md`, the motivation is *"the flatter pattern doesn't solve 'same persona, reused across different contexts' cleanly, which is the direct cause of today's `.nanite/agents/*.md` sprawl (multiple near-duplicate profiles that are really the same role at a different scope)."* This task must do real analysis, not a mechanical 1-file-to-1-role-to-1-agent conversion: identify which of the 24-26 files are genuinely distinct personas (→ separate `roles`) vs. which are the same underlying persona bound to a different scope/project (→ one `role`, multiple `agents` compositions). Candidates worth checking directly rather than guessing: `backend.md`/`frontend.md`/`reviewer-backend.md`/`reviewer-frontend.md` (engineering-role family — likely genuinely distinct roles, not scope variants, but verify), `loom-curator.md`/`atlas-curator.md` (same "Curator" persona name — check whether these are the same role at two different scopes/consumers, which is exactly the sprawl case this model is meant to collapse), `loom-weaver.md` (Loom's Weaver — `consumer_id` candidate, per `03`), `torque-supervisor.md`/`torque-task-writer.md` (Torque-branded — check for a similar same-persona-different-scope pattern).

## What to do (historical — not executed)

1. Confirm Phase 0 #24's final disposition of `agridd-project-manager.md`/`proxima.md` before building the migration's file list.
2. Read every remaining file's frontmatter and system-prompt content in full. Group into distinct `roles` (persona/system_prompt/default hints) vs. `agents` compositions bound to each role (scope, tool/skill grants, model, consumer, instance_mode). Document the grouping decision (which files collapsed into a shared role, and why) in this file's Work Log — this is the real deliverable, not just "26 rows inserted somewhere."
3. For each resulting `agents` composition: set `role_id`, `consumer_id` (per `03` — tag Loom's agents), `model_id` (per `06`, resolved from whatever `default_model`/`default_provider` the source file specified, or left to inherit the role/system default if unset), `runtime_kind` (backfilled per `02`'s instructions), tool/skill grants migrated into `agent_tools`/`agent_skills` (per `04`/`05` — note `05`'s FK fix is sequenced to depend on this task landing first, so this task's migration is what makes `05` safe to run, not the other way around).
4. Do not delete the source `.nanite/agents/*.md` files as part of this task — leave them in place until `08`'s reingest-prevention has also landed and the migration's correctness has been verified in a real running session (an agent from the migrated set actually dispatches correctly, resolves its cascade correctly, uses its assigned tools correctly). Deleting the files is a natural follow-up once verified, not this task's own Done criterion.
5. Verify Curator/Weaver specifically (Loom's real, production-consequential agents) end to end after migration — a wake dispatched to a migrated Curator composition must behave identically to today's file-based version before this task is considered safe.

## Done means (historical — not executed)

- Every real (post-Phase-0-#24) `.nanite/agents/*.md` file has a corresponding `roles`/`agents` row, with a documented grouping rationale in this file's Work Log.
- Curator and Weaver specifically verified end to end in a real session post-migration.
- Tool/skill grants correctly carried over (spot-check several agents' pre- and post-migration tool lists match).
- Source `.md` files left in place, not deleted, pending the follow-up verification window described above.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Tested against a real copy of the backed-up database.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
