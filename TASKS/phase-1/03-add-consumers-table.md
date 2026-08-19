# Add `consumers` table and `agents.consumer_id` ownership tagging

**Phase:** 1
**Status:** implemented
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

**Worktree base correction (infra, not task content):** the worker's worktree
was initially branched from `main` at `df71e710` (pre-dating
`phase-1-execution`'s cut), so `TASKS/phase-1/` didn't exist yet and the
task file couldn't be found. Confirmed `df71e710` is an ancestor of
`phase-1-execution` (`505f1f8f`) with zero unique commits on the worker's
branch, so `git merge phase-1-execution --ff-only` was a lossless
fast-forward, not a destructive rewrite. Landed on `505f1f8f` before doing
any task work. Flagging this so the Orchestrator can check whether other
worktrees cut around the same time have the same base problem.

**Naming check (per this file's own flag):** re-verified `internal/classify/
doc.go`, `internal/effort/effort.go` (doc-comment "consumer"/"consumer
contract" terminology) and `internal/plugin/subprocess/plugin.go`'s
`EnvelopeConsumer` interface — confirmed no collision with the new
`consumers` table / `consumer_id` column / `internal/store/consumers.go`.
`GLOSSARY.md` already carries a dedicated Consumer entry distinguishing it
from Instance, so no glossary edit was needed.

**Scope note — task `10` and agent tagging:** per the Orchestrator's brief,
`10-data-migrate-nanite-agents-md.md` is out-of-scope for Phase 1 (operator
decision, cut in the `phase-1-execution` planning commit), so this task's
own note about confirming which of Curator/Weaver/Atlas-Curator gets the
first real `consumer_id` tag "once `10` runs" no longer applies. This task
seeds exactly one `consumers` row for Loom and tags no agent — no agent
data migration happened here.

**What was built:**
- Migration `internal/store/migrations/106_add_consumers_table.sql` (goose
  Up/Down, both tested): creates `consumers` (`id`, `slug` unique, `name`,
  `created_at`), adds nullable `agent_profiles.consumer_id TEXT REFERENCES
  consumers(id)`, and seeds one real row (`blt-loom-001` / slug `loom` /
  name `Loom`) via `INSERT OR IGNORE`. Down drops the column before the
  table (reverse order of Up) — verified this succeeds even with
  `PRAGMA foreign_keys=1` (this codebase's default, confirmed by reading
  `github.com/hollis-labs/go-sqlite/sqlitekit`) and with live child rows
  present, via both a raw `sqlite3` CLI probe and a goose `DownTo`/`Up`
  round-trip test.
- `internal/store/consumers.go`: `Consumer` struct + `ListConsumers`,
  `GetConsumer`, `GetConsumerBySlug`, `CreateConsumer` (requires slug +
  name, generates a uuid ID if unset), `UpdateConsumer`, `DeleteConsumer`.
  Followed the existing `mcp_servers.go`-style minimal CRUD shape. Did
  **not** build a REST layer — the task's own step 4 hedges this as "if
  useful for the assignment UI in `09`," and `09-build-assignment-ui-api.md`
  is a separate, not-yet-started task that owns that surface; adding
  untested, unconsumed HTTP handlers here would be scope creep against this
  task's own "Touches" list (which only names the migration,
  `internal/store/agents.go`, and the new `consumers.go`).
- `internal/store/agents.go`: added `AgentProfile.ConsumerID string` (empty
  = operator-owned, matching the existing nullable-string convention used
  for `Avatar`/`Description`/`DefaultModel`/etc. via `COALESCE(...,'')` +
  `nullIfEmpty`), wired into `agentColumns`, `scanAgent`, `CreateAgent`'s
  INSERT, and `UpdateAgent`'s UPDATE so the column is settable through the
  normal agent CRUD path, not just raw SQL.
- Tests: `internal/store/consumers_test.go` — seed-row verification (with
  re-migrate no-op check), full CRUD round-trip, `CreateConsumer` slug/name
  validation, `AgentProfile.ConsumerID` round-trip through
  Create/Update/Get including a real FK-violation check (deleting a
  referenced consumer fails; clearing the tag first allows it), and a
  goose `DownTo(105)`/`Up` round-trip test for migration 106's Down half
  (mirrors the pattern in `migration_102_drop_session_compaction_test.go`).

**Verification against a real backed-up database (not just an empty
fixture):** copied
`~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-
backup-20260818-132726` (the most recent backup at task start; pre-dates
goose adoption — no `goose_db_version` table) to a scratch path and ran the
real `store.New` migration path (including the pre-goose-to-goose ledger
cutover) against it via a temporary, not-committed test. Confirmed: all 28
pre-existing `agent_profiles` rows survived migration with `consumer_id=""`
(nullable, correctly defaulted, none backfilled per this task's own "Done
means"), `consumers` table created with the Loom row
(`blt-loom-001`/`loom`/`Loom`), `goose_db_version` ledger landed at `106`,
and a second `migrate()` call (simulated restart) was a clean no-op. The
temporary test file and scratch DB copy were deleted after verification —
not part of this task's committed deliverable.

**Verification:** `go build ./cmd/nanite/`, `go vet ./internal/store/...`,
and `go test ./...` (full suite, `-count=1`) all pass.
`go vet ./...` at the repo root surfaces a pre-existing, unrelated
lostcancel-style finding in `internal/service/container.go`
(`stopReaper`/`stopRuntimeReaper` not used on all paths) — confirmed via
`git show 505f1f8f:internal/service/container.go` that this file is
untouched by this task and the finding predates this branch's
`phase-1-execution` cut; not something this task's scope covers.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
