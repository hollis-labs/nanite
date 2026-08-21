# Remaining REST surface: list/get, assign/revoke, grants/policy view, invoke/preview, uninstall

**Phase:** 7 — Remaining REST API surface (`TASKS/skills`)
**Status:** not-started
**Depends on:** `02`, `05` (install pipeline this uninstall path mirrors), `09` (grant-state
model this task exposes read/write access to)
**Touches:** `internal/api/skills.go` (extends whatever task `05` started), `internal/api/api.go`
(route registration), `internal/selftools/self_tools.go`/`self_tools_transport.go`
(`skill_delete`'s rewired body — real uninstall, per task `01`'s forward pointer).

## Context

`docs/engineering/architecture/20-skills.md`'s "API surface" section lists the full REST
contract; task `05` covers install/sync. This task covers everything else named there:
*"List/get indexed skills — trust tier, content hash, version, install provenance, declared
composition dependencies. Assign/revoke a skill to an agent (the actual grant, distinct from
merely existing in the index). Grants/policy view — what capabilities a skill's materializer is
approved for, and whether that approval is still valid against the skill's current vendored
hash (surfacing 'content changed since approval, re-approval required' states). Invoke/preview
materialization — run the Resolver/Materializer pipeline for a given skill + params outside of a
live agent turn, so authoring/debugging doesn't require a real chat session. Delete/uninstall a
skill from the vendored store and index."* The doc explicitly defers "additional endpoints
(authoring/scaffold helpers, usage/activation telemetry, etc.)... until the frontend stream
actually needs them" — do not build those here.

**`skill_delete`'s forward pointer from task `01`**: that task kept the self-tool's name/
registration but left its body against the pre-redesign schema. This task rewires it to perform
a real uninstall (index row removal + optional vendored-store deletion — task `03`'s store
supports address deletion per its own Done-means) rather than the old flat-row delete.

## What to do

1. `GET /api/skills` / `GET /api/skills/{slug}` — list/get indexed skills with trust tier,
   content hash, version, install provenance, declared dependencies (task `02`'s final `Skill`
   shape).
2. `POST /api/agents/{id}/skills/{slug}/grant` and `DELETE /api/agents/{id}/skills/{slug}/grant`
   (or fold into `agent_known_skills`' existing REST surface at
   `internal/api/agent_capabilities.go` if that reads more consistently — task `02` already
   extended that table's schema, so extending its existing handlers rather than adding a
   parallel route family may be the better fit; use judgment, note the choice) — the actual
   assign/revoke-with-approval action: setting `approved_content_hash` to the skill's current
   hash, `granted_at`, `granted_by`, and `capabilities_granted` (task `09`'s vocabulary).
3. `GET /api/agents/{id}/skills/{slug}/grant` (or equivalent) — the grants/policy view: current
   grant state, whether `approved_content_hash` still matches the skill's live current hash
   (surfacing the "re-approval required" state task `09` enforces at execution time, so an
   operator can see it before hitting a real denial).
4. `POST /api/skills/{slug}/preview` — invoke/preview materialization outside a live agent turn:
   runs the same Resolver → Materializer → Policy/Sandbox pipeline (tasks `06`-`09`) a real
   `skill_get` call would, for a given slug + params, returning the materialized result for
   authoring/debugging. Decide whether preview bypasses the grant-check (since it's an
   operator-driven debugging tool, not agent-driven) or still requires one — document your choice
   and reasoning in the Work Log; if genuinely unclear from the architecture doc, treat as a real
   design call you're making, not something to leave ambiguous in the implementation.
5. `DELETE /api/skills/{slug}` — real uninstall: remove the index row (task `02`) and the
   vendored copy (task `03`'s deletion primitive). Rewire `skill_delete`'s self-tool body
   (`internal/selftools/self_tools.go`/`self_tools_transport.go`) to call the same underlying
   logic, so both the REST and self-tool paths share one implementation.
6. Confirm nothing here builds authoring/scaffold helpers or usage/activation telemetry beyond
   what `agent_known_skills`' existing `activation_count`/`pinned`/`last_used_at` columns already
   provide — those stay deferred per the architecture doc's own explicit instruction.

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- A real dogfeed exercises the full lifecycle end-to-end through these endpoints: install (task
  `05`) → grant → list shows it with correct provenance → preview materializes correctly →
  content changes via re-sync (task `04`) → grants view shows "re-approval required" → uninstall
  removes both the index row and the vendored copy, confirmed via direct query/filesystem check.
- `skill_delete` (self-tool) and `DELETE /api/skills/{slug}` (REST) both call the same
  uninstall logic — a test confirms deleting via either path leaves the system in the same state.
- No authoring/scaffold or telemetry endpoints beyond what `agent_known_skills` already provides
  were added.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
