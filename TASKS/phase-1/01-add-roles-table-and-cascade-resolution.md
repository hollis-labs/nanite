# Add `roles` table and the role → agent → task cascading override resolver

**Phase:** 1
**Status:** not-started
**Depends on:** none
**Touches:** new migration (`roles` table), `internal/store/agents.go` (`agent_profiles` read path — cascade resolution needs to consult both tables), `internal/service/agent.go` (`ResolveForSession`/`resolveForSession` — likely insertion point for cascade resolution), new `internal/store/roles.go` (CRUD), `GLOSSARY.md` (already defines Role — no rename needed, just confirm the doc matches what ships)

## Context

Architecture doc `01-agent-construction.md`: *"An Agent is built from three layers, resolved with closest-wins override — the same shape as RBAC/IAM's Role+Binding+cascading-scope pattern... `roles` — new table, the one genuinely new facet. Holds persona/identity (name, system_prompt) and optionally sensible defaults for tools/skills/permissions that a new composition can start from."* Decision log §6: *"Cascading override resolution, closest wins, confirmed direction: Role (broadest defaults) → Agent/composition (scope-specific settings) → Task/invocation (narrowest, most specific — overrides everything above it at runtime). Same resolution order as `.htaccess`/Kubernetes RBAC."*

This is the one genuinely new facet in the whole Phase 1 schema — everything else (`agents`, `agent_tools`, `agent_skills`, `agent_projects`) is either an existing table gaining columns/FKs or a new join table riding on facets that already exist. `roles` has no precedent in the current schema; this task builds it from nothing.

### What `agent_profiles` already carries that `roles` needs to make reusable

Verified against `internal/store/migrations/001_schema.sql:32-60` (plus 11 extending migrations): `agent_profiles` currently has `system_prompt`, `default_mode`, `default_model`, `default_provider`, `tool_permissions`, `tools`, `role_tools`, `role_skills`, `class` (`'advisor'` default, migration `074`), `activation_mode` (`'singleton'`/`'instance'`, same migration) all living on the single composition row — i.e. persona/identity and scope-specific settings are currently fused into one table with no way to reuse a persona across multiple scoped compositions without copy-pasting the whole row. This is the literal `.nanite/agents/*.md` sprawl problem the architecture doc names as the reason for splitting Role out.

## What to do

1. Create `roles` table: `id`, `slug` (unique), `name`, `system_prompt`, optional default-hint columns for tools/skills/permissions that a new `agents` composition can start from (mirror the shape of `agent_profiles.tools`/`role_tools`/`role_skills` as JSON-array defaults, since `02-add-agents-composition-columns.md` will add `agents.role_id` pointing here), `created_at`, `updated_at`. Do not make role-level tool/skill defaults authoritative — per architecture doc: *"Tools/skills/permissions bind at the composition (`agents`) level, not fixed by role... the actual grant is adjustable per composition/scope."*
2. Build the cascade resolver: given an `agents` row (with `role_id`) and, at invocation time, any task/dispatch-level overrides, resolve `system_prompt`/tool defaults/`class`/model selection with closest-wins semantics (role → agent → task). Confirm the natural insertion point is `internal/service/agent.go`'s `ResolveForSession`/`resolveForSession` (already the per-turn resolution seam) rather than inventing a new one.
3. `class` (`advisor`/`process`/`template`/`harness`) already exists on `agent_profiles` (migration `074`, TEXT NOT NULL DEFAULT `'advisor'`, no CHECK constraint — Go-layer validated) — per decision log §6, `class` follows the same 3-tier cascade as everything else, not a special case. Wire it into the cascade resolver rather than treating it as a fixed per-agent value; a role can supply a default `class`, a composition can override it, a task/dispatch can override again.
4. Build CRUD (`internal/store/roles.go`, REST routes) for `roles` — this is new infrastructure a worker will need alongside `09-build-assignment-ui-api.md`'s UI work, but land the store/API layer here since it's a natural extension of this task's own schema work.
5. Do **not** build a file-reingest path for `roles` — per the "database is the source of truth" principle and `08-kill-file-reingest-on-boot-pattern.md`'s scope, `roles` should be DB-authoritative from the moment it exists, with no YAML/`.md` source ever re-parsed into it on boot. `~/.nanite/roles/` (the developer-persona Claude-Code-boot convention documented in `GLOSSARY.md`) is unrelated and must not be confused with or read by this table — confirm no code accidentally wires the two together.

## Done means

- `roles` table exists, seeded with at least the roles implied by the current `.nanite/agents/*.md` corpus's distinct personas (finalized in `10-data-migrate-nanite-agents-md.md`, not necessarily seeded here).
- The cascade resolver produces the correct closest-wins result for `system_prompt`, tool/skill defaults, and `class` across role → agent → task, verified with a real test exercising all three override levels.
- `roles` CRUD (store + REST) exists and is exercised by at least one integration test.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- No code path re-reads a role definition from disk on boot; `roles` is DB-authoritative only.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
