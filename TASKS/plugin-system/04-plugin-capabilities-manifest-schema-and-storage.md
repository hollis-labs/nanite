# Add subprocess plugin capabilities manifest schema + storage

**Phase:** 3 — Tier 2 capability model: subprocess plugins (`TASKS/plugin-system`)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/plugin/config.go` (`PluginManifest`, new `Capabilities` field),
`internal/plugin/schemas/plugin.schema.v1.json` (optional — see Context, not strictly required
to avoid breaking, but required to actually *validate* the new field), new migration
`135_plugin_capability_grants.sql` (or equivalent — see Migration numbering note below)
**[⚠️ `135` MUST NOT BE USED — it is a hole and filling it fails the boot. Next free is 148 at
`5ec930c8`; re-derive. See the annotation on "Migration numbering" below]**,
`internal/store/plugins.go` (or a new `internal/store/plugin_capabilities.go`).

## Context

`docs/engineering/architecture/09-plugin-system.md`'s "Target design: capability model"
section: **"Tier 2 (subprocess) gets a manifest `capabilities` declaration, enforced twice"**
— a plugin lists what it needs (MCP tools, CRUD resources, event types) in `plugin.yaml`,
enforced at install-time (`05`, this task's sibling) and live at the RPC-proxy layer (`06`).
This task builds the schema and storage both of those depend on; it does not implement
enforcement itself.

This planning session's own research independently re-verified the starting state:

- **No `capabilities`-shaped field exists today.** `PluginManifest` (`internal/plugin/config.go:44-149`)
  has no field for declaring what a plugin *needs* from the host. What exists is
  `ManifestRegisters` (`config.go:127-149`) — the opposite direction, what a plugin
  *contributes* — and `ManifestRequires` (`config.go:118-123`: `McpServers []string`,
  `Plugins []string`, `Features []string`), a dependency list, unenforced today, structurally
  different from an authorization/capability grant (no methods/actions, describes deps not
  requested host-resource access). Don't conflate `ManifestRequires` with the new
  `capabilities` block — they answer different questions.
- **A closely related, already-precedented "declared but unenforced" field exists —
  `CRUDRegistration.Methods`** (`config.go:257-258`): a plugin already declares which CRUD
  verbs it needs, but `registrations.go:561-568` explicitly documents that all five REST
  routes are wired regardless of what's declared — a real, if narrow, precedent for exactly
  the "declared-but-not-enforced" gap this batch (`04`-`06`) closes for CRUD, and separately
  for MCP tools and events.
- **Adding `capabilities:` does not require a subprocess protocol version bump.** The
  subprocess protocol (`internal/plugin/subprocess/protocol.go`, backed by
  `github.com/hollis-labs/plugin-sdk@v0.3.0`) is yaml-authoritative for registrations — the
  host reads `plugin.yaml` directly; `LoadResult` (the only payload a subprocess returns at
  load time) carries no registration/capability data over the wire
  (`LoadParams struct{}` is empty; `LoadResult` only carries `SkippedRegistrations`).
  `ProtocolVersion = 1` is locked (`plugin-sdk`'s own `TestProtocolVersionLockedAt1`). This is
  purely a host-local manifest-schema change. The top-level manifest schema currently has
  `"additionalProperties": true` (`internal/plugin/schemas/plugin.schema.v1.json:7`), so an
  unrecognized `capabilities:` key would already parse without breaking anything today — but
  actually *enforcing* it requires adding the real struct field and (for real validation
  errors on malformed input, not silent-ignore) a schema update.
- **The `plugins` table (migration `121_plugins_installed_enabled_state.sql`) has no column
  for this.** Full current schema (six columns): `plugin_id TEXT PRIMARY KEY`,
  `kind TEXT NOT NULL CHECK (kind IN ('builtin','subprocess'))`, `installed INTEGER`,
  `enabled INTEGER`, `created_at`, `updated_at`. Confirmed no later migration (122-134)
  touches this table. A new migration is required — this task claims `135`.
  (Tangential, don't confuse: `internal/plugin/agent_profiles.go:457` has an unrelated
  `capabilities_json` column, but it's on the `agents` table, about per-agent tool
  capabilities — nothing to do with plugin RPC-proxy authorization.)
- **`kind` (`builtin`/`subprocess`) is confirmed real and `CHECK`-constrained** on this same
  table (migration `121`, line 39) — this task's new capability-grant storage should key off
  the same `plugin_id`, and enforcement (`06`) should gate on this same `kind` column to
  confirm only subprocess plugins are ever subject to capability checks (builtins never are —
  per the two-tier trust model, this is structural, not a new check to add).

## What to do

1. Add a `Capabilities` struct to `PluginManifest` (`internal/plugin/config.go`), parsed from
   a new top-level `capabilities:` key in `plugin.yaml`. Shape it around the three surfaces the
   architecture doc names: MCP tools (server + tool-name pairs, or however `03`'s sibling
   `ManifestRequires.McpServers`/`MCPServerRegistration.Tools` — `config.go:271` — already
   shapes a similar declaration; reuse that shape if it fits rather than inventing a
   parallel one), CRUD resource types, and event types. This is a **declared request**, not a
   grant — parsing and storing it here does not authorize anything; `05`/`06` do that.
2. Update the embedded JSON Schema (`internal/plugin/schemas/plugin.schema.v1.json`) to
   describe the new `capabilities:` shape, so malformed input produces a real validation error
   at parse/install time instead of silently vanishing.
3. Add migration `135_plugin_capability_grants.sql` — a new table (recommended over adding
   columns to `plugins` directly, since a plugin's capability set is naturally multi-row: one
   row per declared-and/or-granted resource) tracking, per `plugin_id`: the resource kind
   (mcp_tool / crud_resource / event_type), the specific resource identifier, whether it's
   currently **declared** (from the manifest, refreshed on every install/update) vs.
   **granted** (set only by `05`'s operator-approval flow — this task creates the column/table
   shape, `05` is what actually sets it true). Foreign-key or logically reference
   `plugins.plugin_id`.
4. Add a `internal/store` accessor layer (new file or extend `internal/store/plugins.go`) for
   reading/writing this table — plain CRUD, no business logic (approval/enforcement logic
   belongs in `05`/`06`, not here).
5. Add a GLOSSARY.md entry distinguishing "declared capability" (from the manifest) from
   "granted capability" (operator-approved) — this distinction is load-bearing for `05`/`06`
   and worth making explicit and discoverable, not just implicit in code comments.

## Migration numbering

> **⚠️ STALE CLAIM — annotated 2026-08-24 at `5ec930c8`. `135` is unoccupied and must still
> never be used.** The note below is kept as written so the correction is visible rather than
> silently applied; every `135` in this file (touches line, "What to do" item 3, "Done means")
> is superseded by this block.
>
> AD-24's freeze note recorded that "migration `135` remains unclaimed by Plugin System," which
> reads as *still available*. It is not. `135` is a **hole** — `63d79028` shifted the Loops
> batch's `135`-`143` up to `138`-`146` to clear a collision with Skills, and nothing filled the
> gap.
>
> Nanite builds its goose provider **without** `WithAllowOutofOrder`
> (`internal/store/store.go:153`), so `allowMissing` is false. A migration numbered below a
> database's highest applied version is a hard error, not a back-fill. Reproduced against goose
> v3.27.3 with Nanite's exact provider options:
>
> ```
> detected 1 missing (out-of-order) migration lower than database version (137): version 135
> ```
>
> `Store.migrate` surfaces that as `goose up: …` and **the service does not boot**. The live
> database is already past it — ledger max `137`, no `135` row. This task's own "Done means"
> requirement to land the migration against a real copy of the backed-up database would fail
> for that reason, not for anything to do with the DDL.
>
> **Next free is 148**, derived at `5ec930c8`:
>
> ```
> $ ls internal/store/migrations/ | sort -t_ -k1 -n | tail -1
> 147_remove_untouched_official_catalog_source.sql
> ```
>
> **Re-derive at the moment you write the file — do not carry `148` forward from here.** A
> number is claimed by the file existing on `main`, not by this file naming it, and several
> frozen batches resume in parallel. From a worktree branched before a sibling merged, ask
> `main`:
> `git ls-tree --name-only main -- internal/store/migrations/ | sort -t_ -k1 -n | tail -1`.
>
> Rule: `TASKS/INDEX.md`'s "Migration numbering — the claiming rule" banner;
> `docs/engineering/tracking-integrity.md` check 9.

Claims `135`. Re-check the migrations directory immediately before landing — `TASKS/agent-host-acp`
and `TASKS/filesystem-snapshots` are both concurrently in flight per `TASKS/INDEX.md` and may
have already claimed `135` by the time this is dispatched.

## Done means

- `plugin.yaml` files can declare a `capabilities:` block; parsing populates a new
  `PluginManifest.Capabilities` field, verified by a test analogous to
  `internal/plugin/config_manifest_v1_test.go`'s existing coverage.
- A malformed `capabilities:` block fails schema validation with a clear error (not a silent
  parse-through), verified by a negative test.
- Migration `135` lands cleanly against a real copy of the backed-up database (per
  `EXECUTION-PROCESS.md`'s worker step 5), with the new table correctly distinguishing
  declared vs. granted state.
- Installing/updating a subprocess plugin refreshes its **declared** capability rows from the
  current manifest (does not touch **granted** state — that's `05`'s job).
- No enforcement exists yet at the end of this task — that's `06`'s job. This task's own tests
  should not assert anything about actual access being blocked or allowed.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
