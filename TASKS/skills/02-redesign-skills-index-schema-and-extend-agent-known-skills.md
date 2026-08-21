# Redesign `skills` as an index-only table; extend `agent_known_skills` as the grant/attachment table

**Phase:** 2 — Index + vendored store (`TASKS/skills`)
**Status:** not-started
**Depends on:** `01` (both touch `internal/store/skills.go` and its migrations — real collision
risk if run concurrently, sequence the merge)
**Touches:** `internal/store/skills.go` (`Skill` struct, all exported functions),
`internal/store/agent_known_skills.go` (`AgentKnownSkill` struct + functions), new migration
`136_skills_index_redesign.sql` (or next available — re-check before landing, see
`TASKS/skills/README.md`'s "Migration numbering"), new migration
`137_agent_known_skills_grant_state_and_drop_agent_skills.sql`, `internal/skill/convert.go`
(`ToStoreSkill` — needs updating to match the new `Skill` shape), `docs/engineering/GLOSSARY.md`
(new entries), `internal/selftools/self_tools.go`/`self_tools_transport.go` (`skill_list`'s
rendering, to match new columns).

## Context

`docs/engineering/architecture/20-skills.md`'s "The model: DB is an index, a vendored store is
content" section: *"DB row (the `skills` table, or its successor) is purely an index: vendored-
store location, content hash, version, source tier, enablement, grant/trust state, and (once
composition ships) declared dependencies. It never holds `SKILL.md` body text, script contents,
or asset bytes."* This task builds that row shape and the per-agent grant/attachment table
tasks `04`-`12` depend on; it does not build the vendored store itself (task `03`) or any
install/materialization logic (tasks `04`, `06`-`09`).

**Current `Skill` struct** (`internal/store/skills.go:12-35`, confirmed via independent research
this planning session): `ID, Name, Slug, Description, Category, ToolBindings, InputSchema,
IsBuiltin, Settings, Icon, Prompt, CreatedAt, UpdatedAt, Source, ImportedAt, OriginSystem,
Format, Version, ModeIDs`. `Prompt` (the markdown body) and `ToolBindings` (populated almost
entirely by `AutoDiscover`, cut in task `01`) have no place in an index-only row per the target
design above — both are being removed here, not merely deprecated.

**`agent_known_skills`** (`internal/store/migrations/069_per_agent_state.sql:37-50`,
`internal/store/agent_known_skills.go:16-25`): `agent_id, skill_name, pinned, activation_count,
last_used_at, added_at, ttl_seconds, reason`, PK `(agent_id, skill_name)`. Confirmed **not**
dead overall — only dead for prompt assembly (`TASKS/phase-0/17`'s own finding, re-verified this
session). Live REST API (`internal/api/agent_capabilities.go`, routes at `internal/api/api.go:104-108`)
and two live frontend surfaces (`AgentBuilderWizard.tsx`'s submit-time seeding loop,
`AgentCapabilitiesPanel.tsx`'s standalone CRUD editor) still write real rows against this exact
table and schema today. `docs/engineering/architecture/13-memory-and-knowledge-tools.md`'s §4a
independently cites this table (paired with `skills`) as *"a genuine catalog+attachment split...
the reference pattern"* a sibling Procedures redesign should mirror — grounds for extending it
in place rather than building a third table from scratch, resolving one of `20-skills.md`'s
"genuinely still open" questions. See `TASKS/skills/README.md`'s corrections section for the
full reasoning; this is a design-latitude call logged in `TASKS/ESCALATIONS.md`'s 2026-08-21
entry, not drawn directly from `20-skills.md`'s own text.

**`agent_skills`** (`internal/store/migrations/001_schema.sql:217-222`, rebuilt with an enforced
FK in `113_agent_skills_agent_projects_fk.sql`) is confirmed **zero rows workspace-wide** — that
migration's own header comment records a verified row-count check against a real production
backup immediately before the FK rebuild. `13-memory-and-knowledge-tools.md`'s §4a cites
`agent_known_skills`, not `agent_skills`, as the real pattern. Drop `agent_skills` outright —
no data migration needed, nothing currently depends on it, and keeping two parallel
"skill attached to agent" tables (one dead-weight, one live) is exactly the kind of duplication
this project's standing dead-code policy exists to remove.

**Constraint — the existing Agent Builder Wizard / Agent Capabilities Panel frontend must keep
working unmodified.** This task's `agent_known_skills` changes must be strictly additive
(new nullable/defaulted columns only) — do not rename or remove any existing column
(`pinned`, `activation_count`, `last_used_at`, `added_at`, `ttl_seconds`, `reason`), and do not
change the primary key shape. Frontend work is explicitly out of scope for this batch
(`TASKS/skills/README.md`'s scope fences) — the existing UI reading/writing the original columns
must continue to function exactly as it does today.

## What to do

1. Redesign `store.Skill` (`internal/store/skills.go`) as an index-only row. Recommended shape
   (adjust field names to match this codebase's existing conventions, e.g. `snake_case` DB /
   `PascalCase` Go, but keep the semantic set): `ID, Name, Slug, Description, Category, Icon,
   InputSchema` (kept — schema for declared parameters, now sourced from the package's own
   frontmatter via task `04`, not agent-authored), `SourceTier` (replaces the old bare `Source`
   string — builtin/user/project/plugin/claude-ecosystem, whatever the real source-tier taxonomy
   ends up being per task `04`'s parser), `VendoredPath` or `ContentHash` (the addressing key
   into task `03`'s store — coordinate the exact shape with whoever lands `03` first, or land
   both together if sequenced serially), `Version`, `Enabled` (bool — replaces any implicit
   enablement the old `is_builtin`/`removed`-in-Settings hack approximated), `DeclaredDependencies`
   (JSON array of skill slugs — the install-time dependency graph task `07`'s cycle detection
   walks), `InstalledAt`, `UpdatedAt`. **Remove** `Prompt`, `ToolBindings`, `IsBuiltin`,
   `ImportedAt`/`OriginSystem`/`Format` (folded into the new `SourceTier`/provenance shape if
   still meaningful — check task `04`'s actual parser output before deciding what survives),
   `ModeIDs` (mode-binding was an E2-era feature tied to the now-cut steering modes system —
   confirm via grep this is genuinely unused before dropping; if something still reads it,
   escalate rather than guess).
2. Write the new migration (`136_...` or next available number — re-check the migrations
   directory immediately before landing) that alters `skills` to the new shape. Given the table
   is being emptied by task `01` (no builtin/auto-discovered rows survive) and confirmed to have
   no other live production data at this point in the batch, a drop-and-recreate is acceptable —
   but confirm against a real backup copy first per `EXECUTION-PROCESS.md`'s schema-migration
   testing requirement, don't assume emptiness without checking.
3. Extend `agent_known_skills` (`internal/store/agent_known_skills.go` +
   `internal/store/migrations/069_per_agent_state.sql`'s table, via a new migration `137_...`)
   with the grant-state columns the redesign needs: `approved_content_hash` (the hash a human
   approved access against — trust invalidation per `20-skills.md`'s "Security, sandboxing, and
   trust" section: *"Approval is granted against a specific hash; a changed source requires a
   new explicit install before it affects anything, and that new install carries a new hash
   requiring its own approval"*), `granted_at`, `granted_by` (operator/system identifier),
   `capabilities_granted` (JSON — what the granted skill's script/materializer is authorized for;
   shape coordinates with task `09`, keep loose/JSON here since task `09` owns the real
   vocabulary). All new columns nullable/defaulted so existing rows and the existing frontend
   read/write path are unaffected.
4. In the same migration (or a follow-up in this same task), `DROP TABLE agent_skills`. Confirm
   zero rows via a direct query against a real backup copy first, per
   `EXECUTION-PROCESS.md`'s schema-migration testing requirement — don't just trust the prior
   migration's comment, independently re-verify.
5. Update `internal/skill/convert.go`'s `ToStoreSkill()` to build the new `Skill` shape from
   `skill.Definition` — this will likely shrink significantly now that body/tool-binding content
   isn't stored in this struct at all (that lives in the vendored store per task `03`,
   materialized on demand, never in this row).
6. Rewire `skill_list`'s self-tool body (`internal/selftools/self_tools.go`/
   `self_tools_transport.go`) to render the new columns sensibly (no more `Prompt` to omit, no
   more `ToolBindings` to list) — keep its output format close to the original CRUD-list shape
   unless the new columns genuinely require a different rendering.
7. Add the missing baseline `docs/engineering/GLOSSARY.md` entries this batch surfaces as a real,
   pre-existing gap: **"Skill"** (the authored-package meaning this redesign locks in — general,
   agent-independent capability, distinct from a *Procedure*; cross-reference
   `13-memory-and-knowledge-tools.md`'s §4a), **"Skill catalog"** (the `skills` index table — one
   global row per installed package), **"Skill attachment"** (`agent_known_skills` — per-agent
   grant/telemetry, distinct from mere catalog presence). Follow the same disambiguation pattern
   as the existing "Team Slot" and "Snapshot (filesystem)" entries — each should note what it is
   NOT (e.g., "Skill" is not a *Claude Code Skill* file in isolation — it's Nanite's DB-indexed,
   vendored wrapper around one).

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Migrations `136`/`137` (or whatever numbers are actually free at land time) apply cleanly
  against a real backup copy of the database, confirmed via direct query before and after.
- `store.Skill` has no `Prompt`, `ToolBindings`, `IsBuiltin`, or `ModeIDs` fields; a test
  analogous to the existing skill-CRUD tests confirms the new shape round-trips correctly.
- `agent_known_skills` has the new grant-state columns; a test confirms an existing row (created
  via the *original* column set only, simulating a pre-existing Wizard-created row) still reads
  back correctly with the new columns defaulted/null, and the existing REST API handlers
  (`internal/api/agent_capabilities.go`) still compile and pass their existing tests unmodified.
- `agent_skills` table no longer exists; `grep -rn "agent_skills\b" internal/` (excluding
  `agent_known_skills` matches) returns zero hits outside historical migration files.
- `GLOSSARY.md` has "Skill," "Skill catalog," and "Skill attachment" entries, each following the
  file's existing disambiguation-entry pattern.
- A real dogfeed (scratch DB, `nanite serve`) confirms: creating an `agent_known_skills` row via
  the existing REST endpoint still works exactly as before; `skill_list` runs without error
  against an empty (post-task-`01`) skills table.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
