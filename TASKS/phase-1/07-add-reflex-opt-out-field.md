# Add the reflex opt-out field to `agent_reflexes`

**Phase:** 1
**Status:** not-started
**Depends on:** none
**Touches:** new migration (`agent_reflexes` — new column, e.g. `opt_out_allowed`), `internal/api/reflexes.go` (CRUD/validation for the new field), `internal/agent/reflexes/` (wherever reflex applicability to a given agent is currently resolved — must consult the new field)

## Context

Architecture doc `01-agent-construction.md`: *"Reflexes at construction time — `agent_reflexes` already supports what's needed structurally: `agent_id IS NULL` = class-bound/global reflex, a specific `agent_id` = per-agent reflex. Gap being closed: a field distinguishing 'required, cannot opt out' from 'default-on, agent may opt out.'"* Decision log §4 states this the same way. Phase 3's Steering work (`dispatch_to_agent` action kind, promptrouter migration) depends on this field existing per `TASKS/INDEX.md`'s Phase 3 summary row — land this early in Phase 1, it's small and low-risk.

### Current schema, exact state

`agent_reflexes` (`internal/store/migrations/075_agent_reflexes.sql:30-51`): `id`, `agent_id TEXT REFERENCES agent_profiles(id)` (nullable — confirmed `agent_id IS NULL` = class-bound/global, per the migration's own header comment), `class_tag`, `name`, `trigger_kind`, `trigger_spec`, `action_kind` (CHECK'd to the 5 existing values), `action_spec`, `status`, `priority`, `fired_count`, `last_fired_at`, `created_at`, `created_by`. **No existing column maps to "required, cannot opt out" vs. "default-on, may opt out"** — confirmed, this needs a genuinely new column, not a repurposed existing one.

## What to do

1. Add a new column to `agent_reflexes` — e.g. `opt_out_allowed BOOLEAN NOT NULL DEFAULT true` (default permissive: existing class-bound reflexes remain opt-out-able unless explicitly marked required, preserving current behavior for every existing row). Use a real goose migration.
2. Wire whatever code path currently determines "does this reflex apply to this agent" (grep `internal/agent/reflexes` for the applicability/filtering logic — likely in the engine's trigger-evaluation entry point) to respect `opt_out_allowed`: if `false`, the reflex applies unconditionally regardless of any per-agent override; if `true` (default), an agent-level override mechanism can suppress it. Confirm whether a per-agent opt-out override mechanism already exists anywhere (e.g. an agent-level reflex-suppression list) or needs to be built as part of this task — architecture doc frames this as "the gap being closed," implying both the field and its consumption are in scope, not just the column.
3. Seed `opt_out_allowed = false` for any reflex that's genuinely safety-critical and shouldn't be individually disable-able (identify these during implementation — check the current 12 base-reflex seeds in `internal/agent/reflexes/seeds.go` for candidates; do not guess without checking).

## Done means

- `agent_reflexes.opt_out_allowed` (or equivalent) exists, correctly defaulted, and is genuinely consulted by the reflex-applicability logic — verified with a real test: a required reflex still fires for an agent that has attempted to opt out; a default-on reflex can be suppressed per-agent.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- `internal/api/reflexes.go`'s CRUD/validation surface exposes and validates the new field.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
