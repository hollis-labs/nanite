# Audit the agent roster — inventory, classify, and recommend a cull/consolidation plan

**Phase:** 2 (Clean-up)
**Status:** not-started — planning deliverable, hold for operator review before any implementation dispatch
**Depends on:** none to *start* the audit itself, but its recommendations assume `TASKS/adhoc/01-eliminate-file-based-agent-runtime.md` has landed (agents are all real DB rows via the standard creation path by the time any cull/consolidation work actually executes)
**Touches:** nothing — this is a research/planning task. It produces a document and follow-up task file proposals, not code changes.

## Context

Operator, 2026-08-19: *"We are going to audit the agents and align them with the new system anyway and we'll be culling most of these agents."* Raised alongside the `tool_permissions`/`agent_tools` cleanup work — related but distinct: `adhoc/01`/`adhoc/02` fix the *mechanism* (every agent is a real, uniformly-addressed DB row with no legacy permission fallback); this task is about the *roster* — which specific agents should actually exist going forward, under the roles/`agent_profiles` composition model Phase 1 introduced.

This is explicitly a **planning deliverable, not a code change**. Dispatch as a `planner`-type agent, not a `worker` — it should read, classify, and propose; it should not delete or edit any agent.

## What to do

1. **Inventory every agent that currently exists**, across every population:
   - The 9 compiled-in builtin profiles (`internal/agent/builtin/profiles/*.md`: `backend`, `background-job`, `default`, `hint-selector`, `planner`, `researcher`, `reviewer`, `system-architect`, `worker`).
   - Any project-level agents under `.nanite/agents/*.md` in this repo.
   - Any real DB-only `agent_profiles` rows created via the API/GUI that have no backing file at all.
   - For each, capture: slug, `Source` (`internal`/`user`/`plugin`), whether it's referenced by any hardcoded string constant elsewhere in the codebase (e.g. `internal/background/service.go`'s `SenderAgentID = "background-job"`, `internal/service/session_intent.go`'s tag-matching for `backend`, `parentDispatchAllowlist` entries in `default.md`'s frontmatter, reflex `dispatch_to_agent` targets, or anything else a grep turns up), real usage evidence (recent `sessions`/`event_log` activity if a live DB is reachable), and current `role_id`/composition status under the Phase 1 roles model.

2. **Classify each agent**: actively used and correctly modeled under the new roles/composition system; actively used but needs realignment (e.g. still carries legacy fields, hardcoded prompt content that should move to a role); redundant/overlapping with another agent; unused/dead weight, safe to cull.

3. **Identify every hardcoded reference** to a specific agent slug/ID anywhere in the codebase (dispatch allowlists, reflex targets, sender-ID constants, intent-classification tag matching, etc.) — culling an agent without finding these first risks a silent breakage (dispatch to a slug that no longer exists, a reflex that never fires, an envelope sender ID pointing at nothing). This is the single most important safety check for this task — a cull recommendation without this list attached is not usable.

4. **Produce a recommendation document** (not a decision — the operator decides): which agents to keep as-is, which to consolidate (and into what), which to cull, and for anything non-obvious, the specific hardcoded dependency that makes it risky to touch. Write real follow-up task files for whichever specific actions the operator approves after reviewing this — do not pre-write cull task files speculatively before that review.

## Done means

- A complete, accurate inventory of every current agent across all populations, with source/usage/hardcoded-dependency evidence for each — not a guess, actually checked against the code and (where reachable) the live DB.
- A clear, non-destructive recommendation document the operator can approve or redirect.
- Zero code changes, zero agent deletions — this task only produces the audit and proposed follow-up task files for review.

## Out of scope

- Actually culling, merging, or editing any agent — that's follow-up work, gated on operator approval of this audit's recommendations.
- The `tool_permissions`/file-based-agent-runtime mechanism work — `TASKS/adhoc/01`/`02`, a prerequisite in spirit but not a hard dependency for starting this inventory.
