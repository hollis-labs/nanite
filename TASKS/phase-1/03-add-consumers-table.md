# Add `consumers` table and `agents.consumer_id` ownership tagging

**Phase:** 1
**Status:** not-started
**Depends on:** none
**Touches:** new migration (`consumers` table, `agent_profiles.consumer_id` column), `internal/store/agents.go`, new `internal/store/consumers.go` (CRUD)

## Context

Architecture doc `01-agent-construction.md`: *"`agents.consumer_id` (FK to a minimal `consumers` table, nullable = operator-owned) tags external ownership — Loom is the first real row."* Decision log §4: *"Ownership/tenancy tagging: `agents.consumer_id` (FK to a new minimal `consumers` table, nullable = internal/operator-owned) — reuses the 'consumer' concept from the earlier platform-scoping decision rather than introducing a separate one. Loom is the first real row."* `GLOSSARY.md`: *"**Consumer** — the ownership/tenancy tag on an Agent (`agents.consumer_id`). Identifies which external system an agent belongs to (e.g. Loom owns Curator) without giving that system its own isolated database instance. Distinct from **Instance**."*

### Confirmed genuinely new — no prior art, one naming risk worth a quick check

Verified: no `consumers` table, no `consumer_id` column, no tenancy concept anywhere in the current schema or code. The only pre-existing uses of the word "consumer" in this codebase are unrelated — `internal/classify/doc.go`/`internal/effort/effort.go`'s API-contract doc-comment terminology, and `internal/plugin/subprocess/plugin.go`'s `EnvelopeConsumer` interface (a Go interface name for "thing that consumes envelopes," a completely different concept in a different code region). Low collision risk, but worth a final `GLOSSARY.md` cross-check before landing — the word is now claimed by two genuinely unrelated meanings in the same codebase, which is exactly the class of thing this project's naming discipline exists to catch early.

### Loom is real, not a placeholder use case

`internal/api/loom_curator_wake.go` is a real, live, ~30-line handler routing wake requests for Loom's Curator agent. Curator's `.nanite/agents/loom-curator.md` and `atlas-curator.md` (and `loom-weaver.md`) are real files in the current agent-file corpus — confirm during `10-data-migrate-nanite-agents-md.md` which of these should carry the first real `consumer_id = <loom row>` tag once this table exists.

## What to do

1. Create `consumers` table: `id`, `slug` (unique, e.g. `loom`), `name`, `created_at`. Keep minimal — per architecture doc, this is a lightweight tenancy tag, not a facet catalog like `roles`.
2. Add `consumer_id TEXT REFERENCES consumers(id)` to `agent_profiles`, nullable (null = internal/operator-owned, per decision log §4).
3. Seed one real `consumers` row for Loom.
4. Build CRUD (`internal/store/consumers.go`, minimal REST if useful for the assignment UI in `09`) for `consumers`.
5. Do not build a `consumers`-per-tenant isolated database mechanism — per architecture doc `05-storage-and-migrations.md`: *"A separate-DB-per-instance mechanism and `consumer_id` tagging... solve different problems and are not steps on the same path... chosen specifically to avoid the N-separate-config-surfaces cost of an embedded-instance-per-consumer model."* This is a shared-DB, lightweight tag only.

## Done means

- `consumers` table exists with a real Loom row.
- `agent_profiles.consumer_id` exists, nullable, correctly tagged for Curator/Weaver once `10` runs its data migration (this task itself doesn't need to backfill every agent — that's `10`'s job once `roles`/`agents` are both ready).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
