# Add taxonomy lookup tables — action category, combining algorithm, provenance tier, recurrence defaults

**Phase:** 1 — Core Engine (`TASKS/reflex-taxonomy`)
**Status:** implemented
**Depends on:** none
**Touches:** `internal/store/migrations/` (new migration, numbered `124_...` — `123_reconcile_file_based_agent_ids.sql` is the current latest), `internal/store/agent_reflexes.go` (new lookup-table read helpers, `AgentReflex` struct additions, backfill logic for the new `provenance_tier` column).

## Context

`docs/engineering/architecture/10-reflex-action-taxonomy.md` (§"Facet 1" through "Facet 4") is the full design; `TASKS/phase-4/10-reflex-architecture-review.md`'s Work Log has the decision trail. Read both before starting.

Today `agent_reflexes.action_kind` is a plain `TEXT` column with an inline `CHECK (action_kind IN (...))` constraint (see `internal/store/migrations/119_agent_reflex_dispatch_to_agent.sql:41-42` for the current six-value list, and `store.ReflexAction*` constants in `internal/store/agent_reflexes.go:35-51` for the Go-side vocabulary every call site string-compares against). `priority` does two undeclared things depending on which code path touches it, and provenance is a free-text `created_by` string. This task adds the real lookup tables the rest of the taxonomy hangs off, without touching the *selection* logic itself (that's `03-shared-decision-engine.md`) or the *recurrence* logic (`02-recurrence-cascade.md` reads the columns this task adds).

**Explicit recommendation, not a mandate — document your call either way in this file's Work Log:** do NOT convert `agent_reflexes.action_kind` itself into an integer foreign key. Dozens of Go call sites (`internal/agent/reflexes/executor.go`'s `switch reflex.ActionKind`, `internal/service/chat_reflexes.go:formatReflexReminder`'s `switch action.ActionKind`, etc.) compare it directly against the `store.ReflexAction*` string constants — rewriting that column type would ripple through every one of them for no behavioral gain. Instead, add a new lookup table keyed by the *same string values* action_kind already holds, and reference it with a plain string join (`JOIN reflex_action_kinds ON agent_reflexes.action_kind = reflex_action_kinds.name`) or, if you want real SQLite-enforced referential integrity, a `REFERENCES reflex_action_kinds(name)` clause added to `action_kind`'s column definition during the table-rebuild (SQLite supports FK against a non-integer `UNIQUE`/`PK` column) — either is acceptable, pick one and document why.

## What to do

1. **New table `reflex_action_categories`** — `name TEXT PRIMARY KEY`. Seed exactly two rows: `system_message`, `execute_action`. (Facet 1 — modeled as a lookup table specifically so a third category can be added later without a schema rewrite; don't hardcode a two-value CHECK instead.)

2. **New table `reflex_action_kinds`** — one row per existing action kind, keyed by the same string the six `store.ReflexAction*` constants already use:
   - `name TEXT PRIMARY KEY` (`inject_reminder`, `force_tool_choice`, `send_message`, `halt_session`, `add_schedule`, `dispatch_to_agent`)
   - `category TEXT NOT NULL REFERENCES reflex_action_categories(name)`
   - `combining_algorithm TEXT NOT NULL CHECK (combining_algorithm IN ('deny_overrides','first_applicable','all_applicable'))`
   - `default_recurrence_seconds INTEGER` — nullable. `NULL` means "inherit the system default" (see `02-recurrence-cascade.md`, which defines that default as a Go constant, not a DB row). An explicit value, **including `0`**, is a real kind-level override meaning "no cooldown."

   Seed exactly per the design doc's per-kind reclassification table:

   | name | category | combining_algorithm | default_recurrence_seconds |
   |---|---|---|---|
   | `inject_reminder` | `system_message` | `all_applicable` | `NULL` |
   | `force_tool_choice` | `system_message` | `first_applicable` | `NULL` |
   | `send_message` | `execute_action` | `all_applicable` | `NULL` |
   | `add_schedule` | `execute_action` | `all_applicable` | `NULL` |
   | `halt_session` | `execute_action` | `deny_overrides` | `NULL` |
   | `dispatch_to_agent` | `execute_action` | `first_applicable` | `0` |

   (`dispatch_to_agent`'s `0` encodes today's de facto behavior — `attemptReflexDispatch`/`matchDispatchToAgentReflex` bypass the generic pass's debounce entirely, per `chat_reflex_dispatch.go`'s own header comment, design note 2. `02-recurrence-cascade.md` is what actually makes this value load-bearing; this task just seeds the fact.)

3. **New table `reflex_provenance_tiers`** — `name TEXT PRIMARY KEY`. Seed exactly three rows: `system`, `operator`, `plugin`. (Facet 3 — `agent_proposed` is deliberately **not** a fourth row; see Context below.)

4. **Extend `agent_reflexes`** (table-rebuild migration — same pattern as `internal/store/migrations/119_agent_reflex_dispatch_to_agent.sql`/`115_agent_reflex_opt_out.sql`, since SQLite can't `ALTER` a `CHECK` or add a real FK in place):
   - `provenance_tier TEXT NOT NULL DEFAULT 'operator' REFERENCES reflex_provenance_tiers(name)`
   - `recurrence_override_seconds INTEGER` — nullable, `NULL` = inherit the kind-level default from `reflex_action_kinds.default_recurrence_seconds`.

   **Backfill `provenance_tier` from existing `created_by` values**, don't leave it at the column default for pre-existing rows. Check the actual `created_by` values in use before writing the backfill — confirmed conventions as of this task's authoring: `internal/agent/reflexes/seeds.go:617` and `internal/agent/reflexes/loom_pilot_seeds.go:229` both write `CreatedBy: "system"`; `internal/api/reflexes.go:108` hardcodes `CreatedBy: "operator"` for the CRUD create path; `store.ApprovePendingReflex` (`internal/store/agent_reflexes.go:555-577`) collapses an approved pending reflex's `created_by` to `"operator:" + reviewedBy`. Backfill rule: `created_by = 'system'` → `provenance_tier = 'system'`; everything else (including any `"operator:..."`-prefixed value) → `provenance_tier = 'operator'`. No existing row should backfill to `'plugin'` — confirmed no live plugin-authored reflex path exists today (design doc: "no concrete plugin need exists today").

5. **New migration number**: `124_reflex_action_taxonomy.sql` (latest existing is `123_reconcile_file_based_agent_ids.sql`).

6. Add Go-side read support in `internal/store/agent_reflexes.go` (or a new `internal/store/reflex_taxonomy.go` if that reads cleaner) for the two new lookup tables — at minimum a way to fetch a `reflex_action_kinds` row by name (category + combining_algorithm + default_recurrence_seconds), since `02-recurrence-cascade.md` and `03-shared-decision-engine.md` both need it. Don't build a full CRUD surface for these tables in this task — they're seed-only lookup data for now, no operator-editable API exists yet and none is asked for here.

## Done means

- Migration `124_reflex_action_taxonomy.sql` applies cleanly against a **real backup copy** of the database (`~/.local/share/nanite/workspaces/default/backups/`), not just an empty fixture — per `EXECUTION-PROCESS.md`'s schema-migration testing requirement. All pre-existing `agent_reflexes` rows survive with `provenance_tier` correctly backfilled (spot-check the three `halt_session` seeds land at `provenance_tier='system'`).
- `reflex_action_kinds` has exactly six rows matching the table above, byte-for-byte.
- `reflex_action_categories` has exactly two rows; `reflex_provenance_tiers` has exactly three.
- A Go-side lookup exists to read a `reflex_action_kinds` row's category/combining_algorithm/default_recurrence_seconds by name, ready for `02`/`03` to consume.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Your `action_kind`-as-plain-string-vs-real-FK call (step 1's recommendation) is documented in this file's Work Log with your reasoning, whichever way you went.

## Work log

**2026-08-19 — implemented.**

**Migration:** `internal/store/migrations/124_reflex_action_taxonomy.sql` (goose, `NO TRANSACTION` + explicit `PRAGMA foreign_keys`/`BEGIN`/`END` toggle, mirroring 119's/115's rename-recreate-copy precedent). Creates and seeds `reflex_action_categories` (2 rows: `system_message`, `execute_action`), `reflex_action_kinds` (6 rows, exactly the design doc's per-kind table, `dispatch_to_agent` carrying `default_recurrence_seconds = 0`), and `reflex_provenance_tiers` (3 rows: `system`, `operator`, `plugin`). Rebuilds `agent_reflexes` to add `provenance_tier TEXT NOT NULL DEFAULT 'operator' REFERENCES reflex_provenance_tiers(name)` and `recurrence_override_seconds INTEGER` (nullable), backfilling `provenance_tier` in the rebuild's own `INSERT ... SELECT` via `CASE WHEN created_by = 'system' THEN 'system' ELSE 'operator' END`. Down migration reverts to 119's pre-124 shape (structure-only/lossy for the two new columns, same precedent as 115's Down for `opt_out_allowed`) and drops the three new lookup tables.

**Caught while writing the migration:** the first draft omitted the leading `-- +goose Up` directive (jumped straight to `-- +goose NO TRANSACTION` after the header comment block, unlike 119's file which has `-- +goose Up` as line 1). Goose failed to parse it ("unexpected state 0 on line 'PRAGMA foreign_keys = OFF;'") — caught immediately by the first `go test ./...` run (every reflex-related test that opens a store failed with the same parse error), fixed by moving `-- +goose Up` to line 1, `-- +goose NO TRANSACTION` to line 2 (matching 119's exact structure), and removing the now-duplicate `NO TRANSACTION` marker later in the file. Full suite green after the fix.

**action_kind string-vs-FK call (task's step 1 recommendation):** kept `agent_reflexes.action_kind` a plain TEXT column, per the task's own recommendation — dozens of Go call sites (`internal/agent/reflexes/executor.go`'s `switch reflex.ActionKind`, `internal/service/chat_reflexes.go:formatReflexReminder`, etc.) string-compare it directly against the `store.ReflexAction*` constants, and rewriting the column's storage type would ripple through all of them for zero behavioral gain. Did take the task's offered second option, though: added a `REFERENCES reflex_action_kinds(name)` clause to `action_kind`'s existing CHECK-constrained TEXT definition during the same table-rebuild this migration already has to do for `provenance_tier`/`recurrence_override_seconds` — free real SQLite-enforced referential integrity (string-keyed, not the column's Go type) at essentially zero marginal cost since the rebuild was already happening; it makes the seed rows the actual enforced source of truth for "what action kinds exist," on top of (not instead of) the pre-existing CHECK.

**Go-side lookup:** new file `internal/store/reflex_taxonomy.go` — `ReflexActionKind` struct (`Name`, `Category`, `CombiningAlgorithm`, `DefaultRecurrenceSeconds *int64`) and `func (s *Store) GetReflexActionKind(ctx context.Context, name string) (*ReflexActionKind, error)`, returning `ErrReflexActionKindNotFound` on a miss. No CRUD surface built for the three new lookup tables, per the task's explicit instruction (seed-only data, no operator-editable API asked for).

**`AgentReflex` struct additions** (`internal/store/agent_reflexes.go`, per the Touches line): added `ProvenanceTier string` and `RecurrenceOverrideSeconds *int64` fields, wired into `agentReflexColumns`/`scanAgentReflex` (read path, needed by every `Get*`/`List*` caller including 02/03), `InsertAgentReflex` (write path — see below), and `UpdateAgentReflex`'s `SET` clause for `recurrence_override_seconds` only.

Two deliberate scope decisions beyond the migration itself, both to avoid leaving a correctness gap right at this task's own boundary rather than expanding into 02/03's territory:
- `InsertAgentReflex` now defaults `ProvenanceTier` (when the caller leaves it empty) using the *same* rule the migration's backfill applies: `CreatedBy == "system"` → `"system"`, else `"operator"`. Without this, `internal/agent/reflexes/seeds.go`'s seeder (which always sets `CreatedBy: "system"` but never set a `ProvenanceTier` field, since it didn't exist before this task) would insert future system-seeded reflexes at `provenance_tier='operator'` — the column's own DB `DEFAULT 'operator'` — the moment a *new* base seed is added or a fresh database re-runs the seeder, silently contradicting the backfill rule this same migration just established for existing rows. Fixing it inside `InsertAgentReflex` (in scope, `agent_reflexes.go`) avoids having to also touch `seeds.go`/`loom_pilot_seeds.go`/`internal/api/reflexes.go` (out of this task's Touches).
- `ProvenanceTier` is *not* included in `UpdateAgentReflex`'s `SET` clause — kept immutable after creation, same treatment as `CreatedBy` (which it's derived from and which was already excluded from `UpdateAgentReflex` before this task). It's a security-relevant authority marker (Facet 3's future allow-list gate), not an operator-tunable field. `RecurrenceOverrideSeconds` *is* included in the `SET` clause (unlike `ProvenanceTier`) since it's a tunable cascade knob, not a security marker — no request-body field exposes it yet through `internal/api/reflexes.go`'s PATCH handler, so today every real call preserves whatever value was already on the row (the handler does `updated := *current` before applying any `req.*` overrides), but the plumbing is in place for 02/03 to wire an actual override surface without also having to touch this store function.

**Verification:**
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` all pass. (`go vet ./...` reports 2 pre-existing, unrelated findings in `internal/service/container.go` — confirmed via `git log -1 -- internal/service/container.go` that file was last touched by a prior commit, not this session; `go vet ./internal/store/...` alone is clean.)
- New regression test `internal/store/migration_124_reflex_action_taxonomy_test.go`: `TestMigrate124SeedsTaxonomyLookupTables` (exact row counts/content for all three new lookup tables, `GetReflexActionKind` round-trips, unknown-name miss returns `ErrReflexActionKindNotFound`, goose has nothing pending, a second `s.migrate()` is a clean no-op) and `TestMigrate124BackfillsProvenanceTierFromCreatedBy` (rolls back to migration 123 via goose `DownTo`, inserts probe rows under each `created_by` convention actually in use today — `system`, `operator`, `operator:alice` — replays migration 124's Up, asserts the backfilled `provenance_tier` per row).
- **Real-backup verification** (per `EXECUTION-PROCESS.md`'s schema-migration testing requirement and this task's own "Done means"): copied `~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726` (plus its matching `-wal` file) to an absolute scratch path under this session's scratchpad directory (`/private/tmp/claude-501/.../scratchpad/reflex-124-verify/main.db`) — never a relative path, never the tracked working directory. Pre-migration baseline via `sqlite3` directly: 14 `agent_reflexes` rows, all `created_by='system'` (no `goose_db_version` table at all — this backup pre-dates goose adoption, so `store.migrate()`'s legacy-ledger-seeding path was exercised for real, not just the new migration in isolation). Ran a throwaway `cmd/verify124temp/main.go` (deleted immediately after, confirmed via `git status --short` that nothing else changed) that calls `store.New(ctx, scratchDBPath)` against that scratch copy and queries the result: migration applied cleanly, `reflex_action_categories`=2 rows, `reflex_action_kinds`=6 rows (byte-for-byte match against the design table, spot-printed), `reflex_provenance_tiers`=3 rows, `agent_reflexes` still 14 rows (none lost), all 14 backfilled to `provenance_tier='system'` (matches — every row in this particular backup happens to have `created_by='system'`), 0 rows at `provenance_tier='plugin'`, and the three `halt_session` seeds (`drift_detector_echo`, `task_complete_self_terminate`, `task_timeout`) individually spot-checked at `provenance_tier='system'`. This verification only calls `store.New`/raw SQL reads — never touches `AgentConfigService`/`writeManaged` or any managed-agent file-write path, consistent with the task's own safety note that this is a pure schema/read-path task. `git status --short` run immediately after (and again at task completion) — clean apart from this task's own intended file changes and two pre-existing dirty files (`docs/engineering/ORCHESTRATOR-KICKOFF-TEMPLATE.md`, `docs/engineering/orchestrator-kickoffs/reflex-taxonomy.md`) and one pre-existing untracked directory (`data/artifacts/...`), none of which this session touched or authored.

**Not done (explicitly out of scope per the task file):** no shared decision engine, no recurrence-cascade logic, no per-tier action-kind allow-list enforcement, no CRUD API for the three new lookup tables. `docs/engineering/GLOSSARY.md` checked before naming anything — no collisions with `reflex_action_categories`/`reflex_action_kinds`/`reflex_provenance_tiers`/`provenance_tier`/`recurrence_override_seconds`; left the glossary itself unedited since this task didn't introduce any term load-bearing enough to need a standalone glossary entry beyond what the architecture doc already defines.

## Review notes

<!-- Reviewer fills in. -->
