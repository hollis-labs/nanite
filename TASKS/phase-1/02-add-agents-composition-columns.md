# Add `agents` composition columns: `role_id`, `model_id`, `instance_mode` resolution, `runtime_kind`

**Phase:** 1
**Status:** not-started
**Depends on:** `01-add-roles-table-and-cascade-resolution.md` (`role_id` FKs against `roles`), `06-fix-models-table-sync-target.md` should land first or in the same change (`model_id` FKs against `models`, which is only meaningful once the sync gap is fixed — see Context)
**Touches:** new migration (`agent_profiles` ALTER — `role_id`, `model_id`, `runtime_kind` columns; `activation_mode` resolution decision, see Context), `internal/store/agents.go`, `internal/service/durable_wake.go` (`wakeSkipReason` — the real consumer of whatever `instance_mode` resolution this task settles on)

## Context

Architecture doc `01-agent-construction.md` lists the target composition columns on `agents` (not renamed — stays `agent_profiles` under the hood, per decision log §6: *"`agents` is not renamed... every existing `agent_id` FK... keeps its current meaning"*): `role_id (FK -> roles)`, `consumer_id (FK -> consumers, nullable)`, `model_id (FK -> models)`, `instance_mode`, `runtime_kind`, `class`, `enabled`.

**`consumer_id` is a separate task** (`03-add-consumers-table.md`) since it depends on a new `consumers` table with no schema precedent. **`class` already exists** (migration `074`, wired into the cascade resolver by `01`). This task is the remaining three: `role_id`, `model_id`, `runtime_kind` — plus a real design decision on `instance_mode` this task must make explicit, not silently pick.

### `activation_mode` already exists and is NOT what `instance_mode`'s real consumer reads — a genuine design choice, not a rename

Verified directly: `agent_profiles.activation_mode` (migration `074_agent_profiles_multi_agent.sql:27`) is TEXT NOT NULL DEFAULT `'singleton'`, Go-layer-validated (`internal/store/agents.go:131-134`) to exactly two values — `'singleton'` or `'instance'` — not the three (singleton/fresh-per-wake/concurrent) the decision log implies for `instance_mode`. Meanwhile, the actual behavior decision log §4 calls "no explicit field exists today" — `wakeSkipReason` (`internal/service/durable_wake.go:325-338`) — switches on `durable_agent_instances.status` + `durable_agent_instances.lifecycle_class`, **not** `agent_profiles.activation_mode` at all. So today's schema has a 2-value column that isn't consulted by the logic the architecture doc describes, and separately a real hardcoded-per-`lifecycle_class` behavior in `wakeSkipReason` with no column backing it.

**This task must decide and document, not silently default**: (a) extend `activation_mode` to a real 3-value enum (singleton/fresh-per-wake/concurrent) and rewire `wakeSkipReason` to read it, retiring the implicit per-`lifecycle_class` switch, or (b) introduce a genuinely new `instance_mode` column and deprecate `activation_mode`. Option (a) reuses existing (if under-scoped) infrastructure; option (b) avoids overloading a column whose current 2-value meaning may already be relied on elsewhere — grep every `activation_mode` read site before choosing, and record the choice and reasoning in this file's Work Log.

### `runtime_kind` — the column, not the routing wiring

This task adds the typed `runtime_kind` column (`cli` | `api`) to `agent_profiles`. **Wiring it as the actual thing that decides CLI-vs-API routing is Phase 2's job**, not this task's — do not touch `chat.IsCLIProvider`/`NormalizeCLIProvider`/`shouldUsePTY`/`agent_deps.go`'s `stripRegistryPrefix` here. This task's scope ends at "the column exists, is populated with a correct value per agent, and nothing reads it yet for routing decisions."

## What to do

1. Add `role_id TEXT REFERENCES roles(id)` to `agent_profiles` (new migration). Nullable during transition (existing rows have no role yet until `10-data-migrate-nanite-agents-md.md` backfills), non-null enforced at the application layer once migration completes — do not add a NOT NULL constraint that would break existing rows before the data migration runs.
2. Add `model_id TEXT REFERENCES models(id)` to `agent_profiles`. Depends on `06`'s fix landing first (or in the same change) — a `model_id` FK against a `models` table whose rows aren't kept in sync with `models.dev` is a real-looking but practically-useless reference.
3. Add `runtime_kind TEXT CHECK (runtime_kind IN ('cli','api'))` to `agent_profiles`, backfilled per-agent from whatever `chat.IsCLIProvider`/`NormalizeCLIProvider` would currently infer from that agent's configured provider/model — this backfill is what makes Phase 2's later wiring safe (a correct value already exists for every agent before Phase 2 starts reading it for routing).
4. Resolve the `activation_mode`/`instance_mode` design question above; implement the chosen option, including rewiring `wakeSkipReason` if option (a) is chosen.
5. Wire `role_id`/`model_id`/`class`/`runtime_kind` (once Phase 2 makes the last one live) into `01`'s cascade resolver — a composition's own `role_id` supplies role-level defaults; the composition's own columns override at the agent level; do not build a second, parallel resolution path.

## Done means

- `agent_profiles.role_id`/`model_id`/`runtime_kind` exist, correctly backfilled for every existing row (verified against the real `.nanite/agents/*.md`-derived rows and the seeded builtins).
- The `activation_mode`/`instance_mode` design decision is made, documented in this file's Work Log with the reasoning, and implemented — `wakeSkipReason` reads whichever column is now authoritative.
- No routing behavior changes as a result of this task — `runtime_kind` is populated but not yet consulted by anything (confirmed by grep: zero new read sites for `runtime_kind` outside this task's own backfill logic and tests).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Tested against a real copy of the backed-up database, not just an empty fixture.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
