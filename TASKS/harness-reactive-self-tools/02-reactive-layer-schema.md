# Reactive-layer schema — `selftool_reaction_kinds` + `selftool_reactions`

**Phase:** 1 — Core mechanism (`TASKS/harness-reactive-self-tools`)
**Status:** reviewed
**Depends on:** none. Parallel-safe with `01-move-self-tools-to-internal-selftools.md` — this task's entire file surface is `internal/store/`, no overlap with `01`'s `internal/mcp`/`internal/selftools`/`main.go` changes.
**Touches:** `internal/store/migrations/` (new migration), a new `internal/store/selftool_reactions.go` (Go read/write helpers, mirroring `internal/store/reflex_taxonomy.go`'s shape).

## Context

`docs/engineering/architecture/11-harness-reactive-self-tools.md`'s "Definition & reaction shape" section is the design. Key constraints this task must honor, not re-litigate:

- **Reactive layer only** — this is not a migration of the self-tool catalog itself. The ~70+ existing tool definitions (name/description/input schema) and their dispatch stay exactly where `01`'s move puts them: static Go in `internal/selftools`. Only self-tools that are actually harness-reactive get a row in the new tables.
- **Category/kind is a lookup table, not a hardcoded enum** — same extensibility reasoning as `docs/engineering/architecture/10-reflex-action-taxonomy.md`'s Facet 1, and the same pattern `TASKS/reflex-taxonomy/01-taxonomy-schema-foundation.md` already used for `reflex_action_kinds`/`reflex_action_categories`/`reflex_provenance_tiers` — read that task's own Work Log before writing this migration, both for the seed-vs-lookup-table pattern and for the table-rebuild-vs-plain-CREATE distinction (`01` needed a rebuild because it altered an existing table; `05-provenance-tier-enforcement.md`'s migration 125 was a plain `CREATE TABLE` because its table was brand new — this task's tables are both brand new, so the plain-`CREATE TABLE` pattern applies here, not the rebuild pattern).
- **Deliberately no combining-algorithm column.** Reflexes need one because multiple reflexes of the *same kind* can independently fire on the same state and something has to pick a winner. Self-tool reactions don't have that shape — one tool call looks up its own configured reactions, and firing multiple different kinds together (a card, an internal API call, both) is the normal case, not a conflict to resolve. Do not add one; this is a documented, deliberate omission, not a gap to fill in.
- **`implemented` bool on `selftool_reaction_kinds`** lets `external_api_call`/`callback` exist as real, queryable, documented rows before they're executable (built by `03-reaction-engine-core.md`, which only executes `render_card` and `internal_api_call` — the other two stay seeded-but-inert in this batch), without a later migration when they are built.

The design doc's own shape (illustrative, not migration-ready DDL, same precision level as the parent taxonomy doc):

- **`selftool_reaction_kinds`** (lookup): slug, category (`render` vs `execute`), `implemented` (bool), description.
- **`selftool_reactions`** (config, many rows per tool): id, `tool_name` (plain string matching the Go-defined tool's `Name` — **no FK to a tool-catalog table**, since definitions stay in Go, not DB), `reaction_kind_id` FK, `config` (JSON, kind-specific), `enabled`, `created_at`.

**Explicit recommendation, not a mandate — document your call in this file's Work Log:** key `selftool_reaction_kinds` by `slug TEXT PRIMARY KEY`, not a separate integer/UUID `id` plus a unique `slug` column — this matches `reflex_action_kinds`' own precedent exactly (`name TEXT PRIMARY KEY` in `TASKS/reflex-taxonomy/01-taxonomy-schema-foundation.md`), and there is no call site in this design that needs a surrogate key for a 4-row lookup table. `selftool_reactions`, by contrast, is a real per-tool config table with potentially many rows — key it `id TEXT PRIMARY KEY`, populated via `uuid.New().String()` at insert time, matching this codebase's own convention (`internal/store/agents.go:421`, `internal/store/artifacts.go:144`, etc. — grep `uuid.New().String()` in `internal/store/*.go` for more examples), not the reflex tables' plain-string-PK convention (those are lookup tables; this one isn't).

## What to do

1. **New table `selftool_reaction_kinds`**:
   ```sql
   CREATE TABLE selftool_reaction_kinds (
       slug        TEXT PRIMARY KEY,
       category    TEXT NOT NULL CHECK (category IN ('render', 'execute')),
       implemented INTEGER NOT NULL DEFAULT 0,
       description TEXT NOT NULL DEFAULT ''
   );
   ```
   (Adjust if you took the surrogate-id alternative above — document why.) Seed exactly four rows, per the design doc:

   | slug | category | implemented | description |
   |---|---|---|---|
   | `render_card` | `render` | `1` | Resolves the reaction's config into an envelope payload; the calling self-tool handler embeds it as an `<!--ENVELOPE_DATA:...-->` marker in its `ToolResult` text. See `04-render-card-construction.md`. |
   | `internal_api_call` | `execute` | `1` | Executes an HTTP call against a trusted, in-process/internal endpoint. See `03-reaction-engine-core.md`. |
   | `external_api_call` | `execute` | `0` | Reserved for a future trust-gated call to an external endpoint. Not executable in this batch — see this folder's README, "What this batch does NOT do." |
   | `callback` | `execute` | `0` | Reserved for a future opaque-callback-target reaction. Not executable in this batch. |

2. **New table `selftool_reactions`**:
   ```sql
   CREATE TABLE selftool_reactions (
       id               TEXT PRIMARY KEY,
       tool_name        TEXT NOT NULL,
       reaction_kind_id TEXT NOT NULL REFERENCES selftool_reaction_kinds(slug),
       config           TEXT NOT NULL DEFAULT '{}',
       enabled          INTEGER NOT NULL DEFAULT 1,
       created_at       TEXT NOT NULL
   );
   CREATE INDEX idx_selftool_reactions_tool_name ON selftool_reactions(tool_name, enabled);
   ```
   (`reaction_kind_id` stores the kind's `slug` value despite the column name — kept as `reaction_kind_id` to match the design doc's own naming; document in the Work Log if you rename it for clarity instead, e.g. `reaction_kind` or `reaction_kind_slug`.) No rows seeded by this task — `07-worked-example-task-update-report.md` seeds the first two real rows.

3. **New migration number**: `126_selftool_reactions.sql` (latest existing at this task's authoring time is `125_reflex_action_kind_provenance_allow.sql` — re-confirm by listing `internal/store/migrations/` before writing, in case something else has landed since).

4. **Go-side helpers**, new file `internal/store/selftool_reactions.go`:
   - `type SelftoolReactionKind struct { Slug, Category, Description string; Implemented bool }` and a lookup by slug.
   - `type SelftoolReaction struct { ID, ToolName, ReactionKindID, Config string; Enabled bool; CreatedAt time.Time }` (or your chosen field shape).
   - `func (s *Store) ListEnabledSelftoolReactions(ctx context.Context, toolName string) ([]SelftoolReaction, error)` — the read path `03-reaction-engine-core.md`'s `Fire()` needs: every enabled reaction row for a given tool name.
   - `func (s *Store) InsertSelftoolReaction(ctx context.Context, r SelftoolReaction) error` — the write path `07`'s worked example needs to seed its two reaction rows. Generate `ID` via `uuid.New().String()` when the caller leaves it empty, matching this codebase's own insert-time-ID-generation convention.
   - Don't build a CRUD API surface (`internal/api/...`) for either table in this task — no operator-facing UI/endpoint is asked for here, matching how `01-taxonomy-schema-foundation.md` scoped its own lookup-table helpers as read/insert-only, no CRUD surface.

## Done means

- Migration `126_selftool_reactions.sql` applies cleanly against a **real backup copy** of the database (`~/.local/share/nanite/workspaces/default/backups/`), not just an empty fixture, per `EXECUTION-PROCESS.md`'s schema-migration testing requirement.
- `selftool_reaction_kinds` has exactly the four rows above, byte-for-byte.
- `selftool_reactions` exists, empty, with the FK/index as specified (or your documented variant).
- `ListEnabledSelftoolReactions`/`InsertSelftoolReaction` (or your chosen names) round-trip correctly in a regression test, ready for `03` to consume.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Your `slug`-vs-surrogate-key call (step 1's recommendation) and your `reaction_kind_id`-naming call (step 2's note) are documented in this file's Work Log, whichever way you went.

## Work log

**2026-08-20 — implemented.**

**Worktree/dispatch note (environment, not scope):** this worker was dispatched into a git worktree (`.claude/worktrees/agent-a42bcd81976b776fc`) that was created before the Orchestrator's uncommitted `TASKS/harness-reactive-self-tools/` and `docs/engineering/architecture/11-harness-reactive-self-tools.md` files existed in the shared checkout. Since those files were untracked (not committed) in the main working tree, they were not visible inside this worktree via normal file listing — worktrees don't share uncommitted/untracked state. Worked around it by reading the task file and the architecture doc directly from the shared checkout's absolute path (the `Read` tool isn't worktree-sandboxed, only `Bash`'s `cd`-into-shared-checkout and `Edit`/`Write` against shared-checkout paths are blocked), then recreating this task file inside the worktree (at this same relative path) to record the Work Log, since the shared-checkout copy can't be edited from an isolated worktree. Reconciled by the Orchestrator directly after merge (this content copied back into the shared checkout's copy).

**Fresh migration-number recheck (per dispatch instruction):** ran `ls internal/store/migrations/ | sort -t_ -k1 -n | tail -5` immediately before writing the migration. Highest existing was still `125_reflex_action_kind_provenance_allow.sql` — `126` was confirmed free, no renumbering needed. Re-checked again after writing the migration file (before running tests) — still no collision with a sibling batch. Independently re-confirmed a second time by the Orchestrator immediately before merge — still no `126` collision.

**Migration:** `internal/store/migrations/126_selftool_reactions.sql`. Plain transactional `CREATE TABLE` (no `NO TRANSACTION` / `PRAGMA foreign_keys` toggling, no rename-recreate-copy rebuild) — both tables are brand new with no pre-existing rows to migrate, matching `125_reflex_action_kind_provenance_allow.sql`'s own precedent (also a brand-new table), not `124_reflex_action_taxonomy.sql`'s rebuild (that one was required only because `agent_reflexes` was an existing table with rows to preserve). Creates and seeds `selftool_reaction_kinds` (4 rows, exactly the table above, byte-for-byte) and creates `selftool_reactions` (empty, FK to `selftool_reaction_kinds(slug)`, `idx_selftool_reactions_tool_name` index on `(tool_name, enabled)`). Down migration drops both tables (lossy, structure-only — no pre-migration equivalent existed to restore, same posture as 125's Down).

**`slug`-vs-surrogate-key call (step 1's recommendation):** took the task's own recommendation as-is — `selftool_reaction_kinds.slug TEXT PRIMARY KEY`, no separate surrogate id column. Matches `reflex_action_kinds.name TEXT PRIMARY KEY`'s precedent exactly; no call site in this design needs a surrogate key for a 4-row lookup table, and a surrogate id would just be an extra indirection every reader of the table has to join through for no benefit.

**`reaction_kind_id`-naming call (step 2's note):** kept `reaction_kind_id` as the column/field name, despite it storing a slug string rather than a surrogate integer id — matches the design doc's own naming (`docs/engineering/architecture/11-harness-reactive-self-tools.md`'s "Definition & reaction shape" section explicitly calls it `reaction_kind_id`), and the task file's own step-2 DDL block uses the same name. Considered `reaction_kind`/`reaction_kind_slug` as the task file's own note suggested but didn't take it — no other column in this design or its neighbors (`reflex_action_kinds`, etc.) needs disambiguating from a same-named different-typed column, so the rename wouldn't buy any real clarity here, and staying byte-for-byte with the architecture doc's own vocabulary keeps this migration and its Go struct field trivially cross-referenceable against the design doc.

**Go-side helpers:** new file `internal/store/selftool_reactions.go` — `SelftoolReactionKind` struct (`Slug`, `Category`, `Implemented`, `Description`) with `GetSelftoolReactionKind(ctx, slug)` (returns `ErrSelftoolReactionKindNotFound` on a miss, mirroring `GetReflexActionKind`/`ErrReflexActionKindNotFound`'s shape exactly); `SelftoolReaction` struct (`ID`, `ToolName`, `ReactionKindID`, `Config`, `Enabled`, `CreatedAt time.Time`, matching the task's suggested field shape), `ListEnabledSelftoolReactions(ctx, toolName)` (enabled rows only, ordered by `created_at`), and `InsertSelftoolReaction(ctx, r)` (generates `r.ID` via `uuid.New().String()` when empty, defaults `Config` to `"{}"` when empty, stamps `CreatedAt` to now-UTC when zero-valued). No CRUD API surface built, per the task's explicit instruction.

**One implementation snag caught during testing, not anticipated from the design doc:** the task's own suggested `CreatedAt time.Time` field shape doesn't round-trip transparently against this migration's `created_at TEXT NOT NULL` column (as literally specified in the task's own step-2 DDL) the way `catalog_sources.created_at` does elsewhere in this codebase (`internal/store/catalog.go`) — `catalog_sources.created_at` is declared `DATETIME`, and this codebase's SQLite driver only auto-scans a column into `time.Time` when the column has `DATE`/`DATETIME`/`TIMESTAMP` type affinity; a plain `TEXT` column (what this task's DDL specifies) does not get that auto-conversion, and a first-draft `rows.Scan(..., &row.CreatedAt)` failed with `unsupported Scan, storing driver.Value type string into type *time.Time`. Kept the `TEXT` column type as the task specifies (not a call to relitigate) and kept the `time.Time` field shape (still the task's own suggestion), and instead made the read/write path explicit about the conversion: `InsertSelftoolReaction` formats `r.CreatedAt.UTC().Format(time.RFC3339)` before binding, `ListEnabledSelftoolReactions` scans into a `string` and `time.Parse(time.RFC3339, ...)`s it back into `row.CreatedAt`. Documented in a comment on the `SelftoolReaction` struct so a future reader isn't surprised by the manual conversion.

**Verification:**
- `go build ./cmd/nanite/`: pass.
- `go vet ./...`: reports 2 pre-existing, unrelated findings in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` possible context leak) — confirmed via `git status --short` that this session never touched that file; `go vet ./internal/store/...` alone is clean. Same pre-existing findings the `01-taxonomy-schema-foundation.md` precedent's Work Log already noted. Independently reconfirmed pre-existing on unmodified `HEAD` by the Orchestrator before merge.
- `go test ./...`: all packages pass. Independently re-run by the Orchestrator directly in this worktree before merge — confirmed clean.
- New regression test `internal/store/migration_126_selftool_reactions_test.go`: `TestMigrate126SeedsSelftoolReactionKinds` (exact row count/content for `selftool_reaction_kinds` byte-for-byte against the design table, `GetSelftoolReactionKind` round-trips for all four slugs, unknown-slug miss returns `ErrSelftoolReactionKindNotFound`, `selftool_reactions` starts at 0 rows, goose has nothing pending, a second `s.migrate()` is a clean no-op) and `TestSelftoolReactionsRoundTrip` (`InsertSelftoolReaction` with an empty ID generates a UUID, an explicit ID is preserved, `CreatedAt` round-trips exactly when explicitly set and is stamped when left zero-valued, `ListEnabledSelftoolReactions` returns only enabled rows for the requested tool — excludes a disabled row on the same tool and a row on a different tool — and returns an empty slice for an unknown tool name).
- **Real-backup verification** (per `EXECUTION-PROCESS.md`'s schema-migration testing requirement and this task's own "Done means"): copied `~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726` (plus its matching `-wal` file) to an absolute scratch path under this session's scratchpad directory (`/private/tmp/claude-501/.../scratchpad/selftool-reactions-126-verify/main.db`) — never a relative path, never the tracked working directory. Pre-migration baseline via `sqlite3` directly: no `selftool_*` tables, no `goose_db_version` table (this backup pre-dates goose adoption, same as the 124 task's own precedent — `store.migrate()`'s legacy-ledger-seeding path was exercised for real, not just the new migration in isolation). Ran a throwaway `cmd/verify126temp/main.go` (deleted immediately after, confirmed via `git status --short` that nothing else changed) that calls `store.New(ctx, scratchDBPath)` against that scratch copy: migration applied cleanly, `selftool_reaction_kinds` = 4 rows byte-for-byte (spot-printed, matches the design table exactly including `implemented`), `selftool_reactions` = 0 rows, `InsertSelftoolReaction`/`ListEnabledSelftoolReactions` round-tripped one real row against the backup copy, and pre-existing tables were untouched (`agent_reflexes` = 14 rows, `sessions` = 344 rows — the `agent_reflexes` count matches the 124 task's own recorded verification against this same backup file). This verification only calls `store.New`/plain `internal/store` reads and writes — never touches `AgentConfigService`/`writeManaged` or any managed-agent file-write path, consistent with this being a pure schema/read-write-path task. `git status --short` run immediately after (and again at task completion): clean apart from this task's own three intended new files (`internal/store/migrations/126_selftool_reactions.sql`, `internal/store/selftool_reactions.go`, `internal/store/migration_126_selftool_reactions_test.go`) plus this worktree's pre-existing branch marker line.

**Not done (explicitly out of scope per the task file):** no reaction engine/`Fire()` (`03-reaction-engine-core.md`), no envelope-card construction (`04`), no telemetry wiring (`05`), no worked-example seed rows (`07`), no CRUD API for either new table. `docs/engineering/GLOSSARY.md` checked before naming anything (`grep -n -i "self.?tool\|reaction"` against the worktree's copy) — no collisions with `selftool_reaction_kinds`/`selftool_reactions`/`reaction_kind_id`/`SelftoolReactionKind`/`SelftoolReaction`; left the glossary itself unedited since this task didn't introduce a term load-bearing enough to need a standalone glossary entry beyond what the architecture doc already defines, matching `01-taxonomy-schema-foundation.md`'s own precedent.

## Review notes

**2026-08-20 — PASS.** Fresh reviewer (no shared context with the worker), full batch review covering all of Phase 1 (`01`-`04`) together — see `03`'s Review notes for the full methodology and cross-task findings. For this task specifically: confirmed migration `126_selftool_reactions.sql` seeds exactly the four design-doc rows byte-for-byte (cross-checked against `TestMigrate126SeedsSelftoolReactionKinds`, which pins the full struct including description text), and the FK/index are correct. Independently confirmed the documented `time.Time`/manual-RFC3339 workaround for `created_at TEXT` is real, not self-inflicted — cross-checked `event_log`/`catalog_sources` (both `DATETIME`-affinity, auto-scan into `time.Time`) against `durable_agents.go`'s own pre-existing `parseStoreTime` helper (same TEXT-column problem, same fix shape already established in this codebase). `InsertSelftoolReaction`/`ListEnabledSelftoolReactions` are sound, parameterized, and covered by a real round-trip test.
