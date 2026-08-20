# Add taxonomy lookup tables — action category, combining algorithm, provenance tier, recurrence defaults

**Phase:** 1 — Core Engine (`TASKS/reflex-taxonomy`)
**Status:** not-started
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

<!-- Worker fills in as it goes. -->

## Review notes

<!-- Reviewer fills in. -->
