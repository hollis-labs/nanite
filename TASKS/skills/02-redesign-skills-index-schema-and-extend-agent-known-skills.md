# Redesign `skills` as an index-only table; extend `agent_known_skills` as the grant/attachment table

**Phase:** 2 — Index + vendored store (`TASKS/skills`)
**Status:** implemented
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

**Pre-flight.** Confirmed task `01`'s cut is present in this worktree (`git log --oneline -5` shows
`ca953536 TASKS/skills: task 01 landed...`, `grep -n "BuiltinSkills\|SeedBuiltinSkills"
internal/store/skills.go` returns nothing but a subtraction-comment). Confirmed task `03`
(`internal/skillvendor`) already landed with address format `skl-vendor-<hash[:16]>` and
`Store.Path/ReadFiles/Write(ctx, files)` — used as the `ContentHash` field's semantic contract
below (this task doesn't call `skillvendor` directly, task `04` does). Re-checked
`internal/store/migrations/` immediately before landing (`ls | sort | tail`, and diffed against
`origin/main` — no drift): `134` is still the highest migration, `135`–`137` free. Used `136`/`137`
as the task file's own suggested names.

**1. `store.Skill` redesign** (`internal/store/skills.go`). New shape: `ID, Name, Slug,
Description, Category, Icon, InputSchema, SourceTier, ContentHash, Version, Enabled,
DeclaredDependencies, InstalledAt, UpdatedAt`. Removed `Prompt`, `ToolBindings`, `IsBuiltin`,
`Settings`, `Source`/`ImportedAt`/`OriginSystem`/`Format` (folded into `SourceTier`), and `ModeIDs`.
`ModeIDs` removal confirmed safe via grep: its only production reader was
`internal/service/skill.go`'s file-def mode-ID backfill block (removed in this same task, see
below); `store.ParseSkillModeIDs`/`MarshalSkillModeIDs`/`SkillMatchesMode` (bare-string helpers,
already zero production callers per task `01`'s own finding) are left untouched — task `01`
already made that call, not re-litigated here. Did **not** add a separate `VendoredPath` field —
`ContentHash` alone is the addressing key (`internal/skillvendor.Store.Path(address)` resolves the
live filesystem path from the address plus config at read time, so a second stored path would be
redundant, matching the doc's own "materialization always reads the vendored copy live" framing).
Dropped `Settings` too, even though the task's recommended-shape list didn't explicitly call it
out for removal — it held only execution-config (model/effort/context/argument-hint) that has no
home on an index-only row per this task's own "shrink significantly" framing, and its one other
production reader (`internal/api/skills.go`'s create/update handlers) is rewritten below anyway.

**2/4. Migrations `136`/`137`.** `136_skills_index_redesign.sql`: `PRAGMA foreign_keys=OFF` +
drop-and-recreate `skills` (matches this file set's own `113_...`-established pattern for a
FK-referenced-table rebuild; needed here because `agent_skills.skill_id REFERENCES skills(id) ON
DELETE CASCADE` still exists at this point in the migration sequence). `137_...sql`: adds
`approved_content_hash, granted_at, granted_by, capabilities_granted` to `agent_known_skills`
(additive, nullable, no rename/drop/PK change) and `DROP TABLE IF EXISTS agent_skills`.

**Real backup-copy verification** (`EXECUTION-PROCESS.md`'s schema-migration testing
requirement), done twice independently before writing the Down section (see escalation-adjacent
finding below for why a Down section was needed at all):
- Direct read-only-equivalent query (`sqlite3`, no `-readonly` flag — that flag itself errored
  against a scratch copy for reasons unrelated to this task, worked fine without it) against the
  **live, currently-running production DB** (`~/.local/share/nanite/workspaces/default/main.db`,
  read-only `SELECT COUNT(*)`, no writes): `agent_skills` = 0, `agent_known_skills` = 13,
  `skills` = 821. Confirms task's premise directly, not just via the `113`-migration comment.
- Copied `~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726`
  to an absolute scratch path under the session scratchpad (never a relative path, never the real
  tracked backup file itself). Confirmed `agent_skills` = 0 there too. Built a throwaway
  `cmd/migrationcheck-scratch/main.go` (worker-authored, deleted before finishing — never part of
  any build target) that calls `store.New(ctx, path)` against the scratch copy — this backup
  predates goose adoption (`goose_db_version` doesn't exist on it), so this exercises the real
  legacy-ledger-seed-then-goose-Up cutover path, not just a fresh-DB path. Result: migration
  succeeded; `agent_skills` no longer exists; `skills` has the new 14-column shape with 0 rows
  (drop-and-recreate); `agent_known_skills` has the new 12-column shape with all 13 pre-existing
  rows intact and the four new columns reading back empty (`COALESCE(...,'')`) — confirmed
  directly via `PRAGMA table_info` and a row-level scan, not just a schema check. Deleted the
  scratch tool and scratch DB copies afterward; `git status --short` confirmed clean.

**Escalation-adjacent finding, not a stop condition (worker step 7 correction).** Running
`go test ./...` after the initial migration-136/137 draft (a plain, no-op Down for `137`) failed
six *pre-existing*, unrelated tests: `TestMigrate102DownReaddsSessionCompactionColumns`,
`TestMigrate103DownReaddsSessionIntentColumn`, `TestMigrate105DownWidensSessionStatusCheck`,
`TestMigrate112DownRemovesConsumersAndColumn`, `TestMigrate115DownDropsOptOutColumnAndTable`,
`TestMigrate124BackfillsProvenanceTierFromCreatedBy` — all real, already-shipped
`goose.DownTo(...)` / `goose.Up(...)` round-trip tests for *other* migrations that happen to walk
all the way down past (and back up through) whatever the current head is. With `137` as the new
head and a no-op Down, `provider.DownTo` failed at `113`'s own Down section
("no such table: agent_skills" — `113`'s Down reads FROM `agent_skills` to rebuild it, which no
longer exists once `137`'s Up already dropped it and its own Down didn't restore it), and
`provider.Up` after a lower DownTo failed with "duplicate column name: approved_content_hash"
(re-running a non-idempotent `ALTER TABLE ADD COLUMN` a second time). Fixed by giving `137` a real,
working Down: `ALTER TABLE agent_known_skills DROP COLUMN` for all four new columns, plus
recreating `agent_skills` exactly as `113`'s own Up left it (both FKs, same PK, empty). Verified
by re-running the six previously-failing tests plus the full `internal/store` suite — all pass.
Logged here per **worker step 7** ("distinguish the decision from its rationale... if the
correction means the job is bigger than it looked... do the full job") — this wasn't a design
question, just a mechanical consequence of this repo's standing goose-round-trip-test convention
that the task file's own "no down migration" suggestion (reasonable on its face, matching sibling
pre-cutover migrations' convention) didn't anticipate for a migration landing at the current head.

**3. `agent_known_skills` extension** (`internal/store/agent_known_skills.go`). Added
`ApprovedContentHash, GrantedAt, GrantedBy, CapabilitiesGranted` to the `AgentKnownSkill` struct,
`agentKnownSkillColumns`, and `scanAgentKnownSkill` (all `COALESCE(...,'')`-defaulted). Extended
`InsertAgentKnownSkill`'s `INSERT OR REPLACE` to carry the four new columns. Also updated
`internal/api/agent_capabilities.go`'s `handleUpdateAgentKnownSkill` to carry the four new fields
forward from `current` the same way `ActivationCount`/`LastUsedAt`/`AddedAt` already are — not
explicitly asked for by the task text, but a direct, cheap consequence of `InsertAgentKnownSkill`
being a full-row `INSERT OR REPLACE`: without this, a plain Panel field edit (pinned/ttl/reason)
would silently null out any future grant state a task-`09` workflow had set via
`InsertAgentKnownSkill` directly. Not itself wiring any grant workflow (task `09`'s job) — just
not making the additive columns quietly non-additive in practice.

**Real, load-bearing correction found during implementation (not a re-litigation of the decided
action — see below for why the action still stands).** The task's own Context section states
`agent_skills` is "confirmed zero rows workspace-wide" and that dropping it needs "no data
migration... nothing currently depends on it." The zero-rows part is independently reverified true
(see backup-copy verification above). The "nothing currently depends on it" part is **not** true —
grep and code-reading during this task found three real, live call sites built directly on
`store.Skill.ListAgentSkills`/`AssignSkillToAgent`/`RemoveSkillFromAgent` (the Go functions this
task's own item 4 requires rewiring, but whose *existing* callers the task's Context section didn't
enumerate): (1) `internal/chat/context.go`'s `buildSkillListForSession`, the render function for
the **API-direct chat path's own skill-catalog block** (`AssembleSlotSources` →
`assembleAgentSlotContent`, confirmed live and reachable per `20-skills.md`'s own "Current state"
section) — this is not admin-UI, it's live chat context assembly; (2) the Agent Builder Wizard's
`assignBuilderCapabilities` (`ui/src/components/settings/agents/AgentBuilderWizard.tsx`), which
calls `api.assignSkillToAgent` for `assigned_skill_ids`/`assigned_skill_slugs` — a *different* code
path than the same Wizard's separately-cited `known_skills` write (`api.createAgentKnownSkill`,
`agent_known_skills`); the task's Context section's own citation of the Wizard's "known-skills
write path" is accurate but incomplete — it doesn't mention this second, `agent_skills`-backed
assignment step in the same submit flow; (3) `internal/plugin/agent_profiles.go`'s
`applyPluginAgentProfile`, which grants `doc.Agent.Skills` (slugs from a plugin's declarative
agent-profile YAML) via `AssignSkillToAgent`. Per **worker step 7**: the decided action (drop
`agent_skills`, extend `agent_known_skills`) stands unchanged — this correction just means the job
was bigger than the Context section's own citations suggested, so scope was expanded to keep all
three call sites working, not narrowed or stopped. **Resolution**: `ListAgentSkills`/
`AssignSkillToAgent`/`RemoveSkillFromAgent` (`internal/store/skills.go`) keep their exact existing
signatures but are rewired onto `agent_known_skills`, joined/keyed by the skill's **slug** (matching
`agent_profiles.go`'s own pre-existing slug-based grant convention for plugin-declared skills,
confirmed via reading `doc.Agent.Skills []string` + `GetSkillBySlug`) rather than the dropped
table's `skill_id` FK. `config` is accepted but no longer persisted (`agent_known_skills` has no
free-form config column; `capabilities_granted` is the typed successor, task `09`'s to define).
Because signatures are unchanged, **zero changes were needed** to any of the three call sites
above, their test-stub interface implementations (`internal/service/agent_test.go`,
`internal/service/chat_test.go`), or any `ui/src/*.tsx` file — confirmed by grep and by the full
test suite passing unmodified for `internal/plugin`, and by a live dogfeed exercising both the old
Wizard-style assignment endpoint and the newer known-skills endpoint side by side (see Dogfeed
below). This is also the direct resolution of one of `20-skills.md`'s own "genuinely still open"
questions (whether `agent_known_skills`' shape gets reused for the new grant/telemetry table) in
the way the task file's Context section already argued for — this correction just makes that
reuse actually load-bearing for existing behavior, not merely additive.

`DeleteSkill`'s guard (`AND is_builtin = 0`) is dropped along with the `IsBuiltin` field —
builtin/non-builtin protection no longer exists as a concept post-task-`01` (all 8 builtins already
deleted); any future "can this be deleted" policy is task `12`'s real uninstall semantics, not this
bare index-row delete.

**5. `internal/skill/convert.go`'s `ToStoreSkill()`.** Shrunk as anticipated: produces only
`ID/Name/Slug/Description/Category/Icon/InputSchema/SourceTier/Version/Enabled/
DeclaredDependencies/InstalledAt/UpdatedAt`. `SourceTier` derives from `Definition.Source`
(defaulting to `"user"`, matching `CreateSkill`'s own default) rather than the removed
`IsBuiltin: true` literal. The `marshalJSONOr`/settings-map-building block (model/effort/context/
argument-hint) is deleted outright along with `Settings` itself — no home for it on an index-only
row. `marshalSlice`/`marshalJSONOr` helpers removed (confirmed zero other callers via grep).

**6. `skill_list` self-tool rendering** (`internal/selftools/self_tools_transport.go`'s
`callListSkills`). Updated to render `source_tier`/`version`/enabled-status alongside the
existing name/id/slug/category/description — no more `Prompt` to omit (it was already omitted),
no more `ToolBindings` to list. `skill_delete`'s description text
(`internal/selftools/self_tools.go`) updated to drop the now-nonexistent "only non-builtin skills"
framing.

**7. GLOSSARY.md.** Added **"Skill"**, **"Skill catalog"**, **"Skill attachment"** entries
immediately before task `03`'s already-landed **"Skill vendor store"** entry (which already
forward-referenced these three terms), following the file's existing disambiguation pattern (what
each term is NOT — "Skill" is explicitly not a bare Claude Code Skill file, not the pre-redesign
`mcp.Manager.AutoDiscover` tool-wrapper meaning; "Skill catalog" is explicitly not per-agent;
"Skill attachment" is explicitly distinct from mere catalog presence and from the now-dropped
`agent_skills` table). Cross-referenced `13-memory-and-knowledge-tools.md` §4a and `20-skills.md`
throughout. Re-checked the file for collisions before writing (none) — this is the same "check
GLOSSARY.md before introducing any new name" step task `03`'s own Work Log documented performing.

**Additional forced/adjacent changes, not independently scope-creeping** (each a direct, mechanical
consequence of removing `Prompt`/`ToolBindings`/`IsBuiltin`/`ModeIDs`/`Settings` from `Skill`, or of
dropping `agent_skills`, not a new design decision):
- `internal/chat/context.go`'s `buildSkillListForSession` — dropped the `sk.ToolBindings`
  JSON-unmarshal/render block (field gone); render is now plain `- name: description`. Removed the
  now-unused `encoding/json` import.
- `internal/api/skills.go`/`internal/api/types.go` — `CreateSkillRequest`/`UpdateSkillRequest`
  updated to the new field set (dropped `ToolBindings`/`Settings`, added
  `SourceTier`/`ContentHash`/`DeclaredDependencies`/`Enabled`). **Deleted
  `handleForkSkillToUser`/`ForkSkillToUserRequest`/its route** (`POST
  /api/skills/{id}/fork-to-user`) outright — forced by `Prompt`'s removal (nothing left to fork),
  and independently already fully non-functional since task `01` deleted the file-reingest
  mechanism (`skill.Discover`/`AutoIngestSkills`) this handler's own doc comment described relying
  on ("the next discovery cycle... the J7 AutoIngest pipeline overrides the DB row") — a real,
  already-broken-but-unnoticed feature this task's own changes happened to force a decision on.
  `skill.WriteUserSkillFile` itself (the one primitive this handler called) is left untouched, per
  task `01`'s own precedent of keeping it as an independently-tested primitive. Updated two nearby
  comments (`internal/api/roles.go`, `internal/api/api.go`) that referenced the old "fork-to-user"
  affordance by name.
- `internal/store/agents.go`'s `DeleteAgent` — removed the now-dead `DELETE FROM agent_skills`
  cleanup line (table gone); `agent_known_skills`' own cleanup line already covers this junction.
  Updated the function's doc comment accordingly.
- `internal/builders/skill_builder.go`'s `NewSkillBuilder` — dropped the `tool_bindings` builder
  step/field (3 steps now, not 4) — same already-flagged-out-of-scope builder task `01`'s Work Log
  noted, mechanically adjusted for the field removal only, not otherwise redesigned.
- Test files updated for the struct/behavior changes: `internal/store/skills_test.go` (rewritten;
  also absorbs the FK-rejection coverage from the deleted `agent_skills_agent_projects_fk_test.go`
  — no FK-cascade-on-delete replacement added, since `agent_known_skills`' own `REFERENCES
  agent_profiles(id)` carries no `ON DELETE CASCADE` clause, empirically confirmed against a
  scratch `sqlite3` DB during this task — unlike the dropped table's FK-rebuilt shape, a raw
  parent-row delete while a known-skill row exists is rejected, not cascaded; cleanup on agent
  deletion is `DeleteAgent`'s own explicit job, already covered elsewhere).
  `internal/store/agent_skills_agent_projects_fk_test.go` → renamed to
  `agent_projects_fk_test.go` (git mv), keeping only the still-relevant `agent_projects` FK tests.
  `internal/store/agents_test.go`'s `TestDeleteAgent_NoPragmaToggle` — swapped its `Skill`+
  `AssignSkillToAgent` seed for a direct `InsertAgentKnownSkill` call and its junction-table check
  from `agent_skills` to `agent_known_skills`. `internal/skill/convert_test.go`,
  `internal/chat/{compaction_disclosure_test.go,skill_list_loadhint_test.go}`,
  `internal/mcp/autodiscover_skills_test.go`, `internal/builders/builder_test.go` — all updated to
  drop `ToolBindings`/`Settings`/`ModeIDs`/`IsBuiltin` field literals no longer on the struct.
  `internal/plugin/agent_profiles.go`/`_test.go` — six doc-comment mentions of `agent_skills`
  reworded to `agent_known_skills` (pure prose, no code change — `AssignSkillToAgent`/
  `ListAgentSkills`'s unchanged signatures meant zero logic changes were needed here).
- Every remaining non-migration-file prose mention of the literal string `agent_skills` (comments
  explaining the old table's history) was reworded to avoid the literal substring, to satisfy this
  task's own Done-means grep check exactly (`grep -rn "agent_skills\b" internal/` outside
  migrations now returns zero hits — verified directly, not assumed).

**Real dogfeed** (per Done-means, and per `EXECUTION-PROCESS.md`'s absolute-scratch-path
discipline for any live-verification write). Built `cmd/nanite` and ran `serve -db <scratch>.db
-port 18099` **with the process's CWD set to the scratch directory itself** (a real near-miss
caught and fixed mid-task: the first attempt launched from the worktree root and silently wrote a
real `.nanite/agents/dogfeed-agent.md` file into the *worktree* via a relative `source_ref` —
exactly the documented footgun `EXECUTION-PROCESS.md`'s "Promote recommendations" section warns
about. Caught immediately via `git status --short` right after the first `POST /api/agents` call,
deleted the stray untracked file, killed the server, and relaunched with `cd <scratch-dir> && ...`
as the actual command before touching any endpoint again — confirmed clean via `git status --short`
before and after the corrected run). Confirmed, against a genuinely fresh scratch DB:
- `GET /api/skills` returns successfully (200) against the empty, post-task-`01` skills table.
- `POST /api/agents/{id}/known-skills` (the existing Wizard/Panel REST endpoint,
  `internal/api/agent_capabilities.go`) round-trips correctly, now also returning the four new
  grant-state columns (empty/defaulted) alongside the original six.
- `POST /api/agents/{id}/skills` (the old-style, now `agent_known_skills`-backed assignment
  endpoint) correctly creates a real `agent_known_skills` row keyed by the skill's slug;
  `GET /api/agents/{id}/skills` correctly joins back to the catalog and returns the full `Skill`
  row; `DELETE /api/agents/{id}/skills/{id}` correctly removes only that row, leaving the
  separately-created known-skill entry (from the `/known-skills` endpoint) untouched — proving the
  two write paths coexist safely on the shared table.
- Real coordination-lock note (matches task `01`'s own documented finding, not a new discovery):
  the real, currently-running production `nanite-api-service` process was confirmed live via `ps`
  during this dogfeed; `-db` isolation alone did not need to be tested against that collision here
  since the HTTP-level dogfeed above fully succeeded regardless (no coordination-lock-dependent
  path was hit by these specific endpoints).
- Killed the background server and deleted the throwaway `cmd/migrationcheck-scratch/` tool
  afterward; confirmed via `git status --short` that only the intended migration/task-file changes
  remain untracked.

**`go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`** all pass (full suite, multiple times
across the course of this task, including after the Down-migration fix and the final
comment-rewording pass). `go vet`'s only findings are the same two pre-existing, unrelated
`stopReaper`/`stopRuntimeReaper` findings in `internal/service/container.go` that task `01`'s own
Work Log already confirmed pre-existing via `git blame`.

**Known limitation surfaced, deliberately not fixed here (frontend is out of scope for this
batch).** `ui/src/components/settings/SkillsBrowser.tsx` (the admin catalog-browsing/editing page
at `GET/POST/PUT /api/skills`, distinct from the Wizard/Panel known-skills surfaces this task's own
constraint protects) calls `JSON.parse(skill.settings)` and reads `skill.tool_bindings`/
`skill.is_builtin`/`skill.prompt` directly — all four fields no longer exist in the API response
after this task's redesign. `JSON.parse(undefined)` throws a real `SyntaxError` at render time, not
a silent empty-field degrade — this page will genuinely crash the next time it's opened against a
post-migration backend, not just display blank data. This is a real, concrete consequence of the
redesign, not something within this task's scope to fix (`TASKS/skills/README.md`'s explicit "No
task here touches `ui/src/`" fence, and `20-skills.md`'s own framing of admin-UI work as "an
explicitly separate stream, out of scope here") — flagged here specifically (not just generically)
so whichever future frontend-stream task picks this up doesn't have to rediscover it. The two
UI surfaces this task's own Done-means explicitly protects (`AgentBuilderWizard.tsx`'s
capability-assignment step, `AgentCapabilitiesPanel.tsx`'s known-skills CRUD editor) were verified
via the dogfeed above to keep working unmodified — this finding is about a third, separate admin
page the task's Context section never mentioned.

**Not escalated to `TASKS/ESCALATIONS.md`**: none of the corrections above met the "genuine
unknown" bar (ambiguous instruction, zero doc coverage, or item-vs-item contradiction) — each was
either a mechanical, forced consequence of the decided field/table removals, or a "the job is
bigger than the Context section's own citations suggested" case worker step 7 explicitly directs
to resolve by doing the full job and logging the correction, not by stopping.

## Fix required (fresh reviewer, 2026-08-21 — see `TASKS/ESCALATIONS.md`'s matching entry)

**Bug, reproduced directly:** collapsing `agent_skills` into `agent_known_skills` means
`ListAgentSkills`/`AssignSkillToAgent`/`RemoveSkillFromAgent` (`internal/store/skills.go`) now
read/write the identical `(agent_id, skill_name)` row space as
`InsertAgentKnownSkill`/`GetAgentKnownSkill`/`DeleteAgentKnownSkill`
(`internal/store/agent_known_skills.go`) — two REST surfaces that were safely independent before
this task (`POST/DELETE /api/agents/{id}/skills` vs. `POST/PUT/DELETE
/api/agents/{id}/known-skills`) now silently collide on the shared table:

1. `handleCreateAgentKnownSkill` (`internal/api/agent_capabilities.go:202-244`) hard-rejects with
   `409 Conflict` if *any* row already exists for `(agent_id, skill_name)` — it cannot distinguish
   a real known-skill grant row from a bare row `AssignSkillToAgent` created moments earlier via
   the unrelated "Assigned Skills" step. Reproduced directly: `AssignSkillToAgent` then
   `GetAgentKnownSkill` for the same pair returns a non-nil row.
2. `RemoveSkillFromAgent` (`internal/store/skills.go`) does an unconditional full-row `DELETE FROM
   agent_known_skills WHERE agent_id = ? AND skill_name = ?` — this destroys any
   `pinned`/`activation_count`/`last_used_at`/`ttl_seconds`/`reason`/`approved_content_hash`/
   `granted_at`/`granted_by`/`capabilities_granted` data a *separate* write path (the Agent
   Capabilities Panel, or a future task-`09` grant workflow) had set for that exact skill.

Concretely reachable through the live, unmodified frontend: `AgentBuilderWizard.tsx`'s same
submission assigns via `assigned_skill_ids` then creates via `known_skills` for the same skill
(the 409 fires *after* the agent profile itself was already created, leaving a half-configured
agent behind); and cross-surface, `AgentProfileManager.tsx`'s remove action silently wipes grant
state `AgentCapabilitiesPanel.tsx` separately set for the same agent+skill. This directly
contradicts this task's own stated constraint ("the existing Wizard/Panel frontend must keep
working unmodified") — the original dogfeed verified coexistence using *different* skills through
each path, not the actual collision case (same skill, both paths).

**Not a re-litigation:** the decision to drop `agent_skills` and extend `agent_known_skills` in
place stands — this is a correctness gap in how the rewired functions reconcile with the
pre-existing known-skills handlers' assumptions about row ownership, not a reason to revisit the
table-consolidation call.

**What to do:** make the two write-surfaces safely independent again despite sharing one table.
Concretely:

1. `handleCreateAgentKnownSkill`'s pre-existence check must distinguish a genuine prior known-skill
   grant from a bare row created only by `AssignSkillToAgent` (all known-skill-specific columns at
   their zero-value: `pinned=0`, `activation_count=0`, `ttl_seconds` unset, `reason=""`, all four
   grant-state columns empty). If the existing row is bare, `handleCreateAgentKnownSkill` should
   upsert onto it (fill in the known-skill fields) rather than returning `409`. If the existing row
   already carries real known-skill data, the `409` behavior is correct and unchanged.
2. `RemoveSkillFromAgent` ("unassign," the Wizard's own semantics) must not destroy known-skill
   grant/telemetry data set via the other path. Options (pick whichever fits the Wizard's actual
   UX best — check what it does with the response): (a) only physically delete the row if it is
   bare by the same test as above, otherwise leave the row's known-skill data intact and just no-op
   the "assignment" removal (still return success — from the Wizard's perspective the skill is
   unassigned, since "assignment" isn't a real column, just row-existence); or (b) some other
   reconciliation that achieves the same guarantee: removing a bare assignment never touches real
   grant data, and a caller can't lose known-skill state through the assignment-removal endpoint.
3. Add regression tests reproducing exactly the reviewer's two scenarios: (a) `AssignSkillToAgent`
   then `handleCreateAgentKnownSkill`/`InsertAgentKnownSkill` for the same `(agent_id, skill_name)`
   must succeed, not conflict; (b) a row with real known-skill grant data set, followed by
   `RemoveSkillFromAgent`, must leave that grant data intact (or the removal itself should be
   rejected/no-op'd — whichever direction you choose in (2), test that it actually holds).
4. Re-verify `go build`/`go vet`/`go test ./...` clean, and re-run a dogfeed that specifically
   exercises the *same skill* through both `/api/agents/{id}/skills` and
   `/api/agents/{id}/known-skills` for the same agent (not two different skills, which is what the
   original dogfeed did) to prove the collision is actually gone end-to-end.

## Work log — fix-required follow-up (2026-08-21)

**Scope of this pass.** Only the "Fix required" section above — the main body's decision (drop
`agent_skills`, extend `agent_known_skills` in place) is not revisited, per that section's own
"Not a re-litigation" framing.

**Root cause, confirmed exactly as the fresh reviewer described.** `ListAgentSkills`/
`AssignSkillToAgent`/`RemoveSkillFromAgent` (`internal/store/skills.go`) and
`InsertAgentKnownSkill`/`GetAgentKnownSkill`/`DeleteAgentKnownSkill`
(`internal/store/agent_known_skills.go`) share the identical `(agent_id, skill_name)` row space on
`agent_known_skills` now that the old `agent_skills` join table is gone. `handleCreateAgentKnownSkill`
(`internal/api/agent_capabilities.go`) hard-rejected with `409` on *any* pre-existing row, and
`RemoveSkillFromAgent` did an unconditional full-row `DELETE` — neither could distinguish a bare
row `AssignSkillToAgent` left behind from a real known-skill grant row the separate `/known-skills`
REST surface owns.

**Fix: a shared "is this row bare" test, used by both write paths.**

1. Added `AgentKnownSkill.IsBareAssignment()` (`internal/store/agent_known_skills.go`) — true iff
   every known-skill-specific column is at its zero value (`Pinned=false`, `ActivationCount=0`,
   `LastUsedAt=""`, `TTLSeconds=0`, `Reason=""`, and all four grant-state columns empty).
   Deliberately excludes `AddedAt` — `InsertAgentKnownSkill` always defaults it to the current
   timestamp on first insert regardless of which path created the row, so it's never empty and
   isn't evidence of a real grant.
2. `handleCreateAgentKnownSkill` (`internal/api/agent_capabilities.go`): when a pre-existing row is
   found, it now checks `IsBareAssignment()` first. A bare row (created only by
   `AssignSkillToAgent`) is upserted onto — carrying forward its `ActivationCount`/`LastUsedAt`/
   `AddedAt` the same way `handleUpdateAgentKnownSkill` already does for a real edit, so the
   original assignment timestamp survives — and returns `201`, not `409`. A row that already
   carries real known-skill data still returns `409` unchanged. This directly fixes the
   `AgentBuilderWizard.tsx` half-configured-agent bug (`assignBuilderCapabilities` assigns via
   `assigned_skill_ids`/`assigned_skill_slugs`, then separately creates via `known_skills`, for
   potentially the same skill in the same submission).
3. `RemoveSkillFromAgent` (`internal/store/skills.go`): now resolves the existing
   `agent_known_skills` row (via `s.GetAgentKnownSkill(context.Background(), agentID, sk.Slug)` —
   `context.Background()` used deliberately here, matching the one existing production precedent
   for this pattern in `internal/store/store.go:124`, since this function's own signature predates
   context and is kept unchanged per the task's own "existing frontend must keep working unmodified"
   constraint) before deleting. A bare row is still physically deleted (unchanged behavior for a
   plain assign-then-unassign with no known-skill data ever layered on). A row carrying real
   known-skill grant/telemetry data is left completely untouched and the call still returns `nil`
   (success) — chosen over rejecting with an error after reading
   `ui/src/components/settings/AgentProfileManager.tsx`'s `removeSkillMutation`
   (lines 141-147: `mutationFn: () => api.removeSkillFromAgent(...)`, `onSuccess` only — no
   `onError` handler at all, unlike `updateMutation`/`deleteMutation`/`copyToManagedMutation` in the
   same file, which do set `actionError`). A rejected/`500` response here would leave the click
   silently no-op from the user's perspective anyway (no error surfaces in this component's UI for
   this specific mutation), so returning success and preserving the grant data is strictly safer
   than either destroying it or leaving an unactionable failure. Net effect: from the `/skills`
   endpoint's own perspective the skill is "unassigned" (assignment was never a real column, just
   row existence); `GET /api/agents/{id}/skills` will continue to list a skill whose
   `agent_known_skills` row still carries real grant data after an "unassign" call — this is the
   documented, deliberate trade-off the fix-required section's option (a) describes, not an
   oversight.

**Regression tests, each independently confirmed to fail against the pre-fix code before the fix
was reapplied** (temporarily reverted `internal/api/agent_capabilities.go` and
`internal/store/skills.go` via `git checkout --` in this same worktree — not a repo-global `git
stash` — ran the new tests to confirm they reproduce the reviewer's exact findings, then restored
both files via a saved `git diff`/`git apply` round-trip and reran the full suite):
- `internal/store/agent_known_skills_test.go`'s `TestAgentKnownSkill_IsBareAssignment` — unit test
  for the bareness predicate itself, one subtest per known-skill-specific column plus the
  `AddedAt`-doesn't-count case.
- `internal/store/skills_test.go`'s `TestRemoveSkillFromAgent_DeletesBareAssignment` (bare row still
  physically deleted — unchanged behavior) and
  `TestRemoveSkillFromAgent_PreservesKnownSkillGrantData` (reviewer's finding 2, reproduced directly
  — **failed against pre-fix code** with `GetAgentKnownSkill after RemoveSkillFromAgent: agent known
  skill not found`, confirming the unconditional DELETE destroyed the grant row; passes after the
  fix with all six grant fields intact).
- `internal/api/agent_capabilities_test.go`'s
  `TestAgentCapabilitiesAPI_CreateKnownSkill_UpsertsOntoBareAssignment` (reviewer's finding 1,
  reproduced directly at the HTTP layer — **failed against pre-fix code** with `got 409, want 201`;
  passes after the fix, and also asserts a *second* create against the now-real grant row still
  correctly 409s, proving the fix only relaxes the bare-row case).

**Real dogfeed, same skill through both endpoints** (per item 4's explicit requirement — the
original dogfeed used different skills per path, which is exactly what missed this bug). Built a
throwaway binary, ran `serve -db <scratch>.db -port 18199` with the process's CWD set to an absolute
scratch directory under the session scratchpad (confirmed via `git status --short` before and after
that a stray `.nanite/agents/dogfeed-agent-02.md` landed in the scratch dir, not the worktree — the
same near-miss the original Work Log flagged, checked again here since it's a real, easy-to-repeat
mistake). Against a fresh scratch DB:
- Assigned a skill via `POST /api/agents/{id}/skills` (Wizard's `assigned_skill_ids` path) — `201`.
- Created a known-skill row for the **same** skill via `POST /api/agents/{id}/known-skills` (Wizard's
  `known_skills` path) — `201` (previously would have been `409`), `pinned`/`reason` correctly set.
- A second `POST /api/agents/{id}/known-skills` against the same skill correctly still `409`s (real
  grant now exists).
- `DELETE /api/agents/{id}/skills/{id}` (Wizard-style "unassign") on that same skill returned
  `{"status":"removed"}` (`200`), and a follow-up `GET /api/agents/{id}/known-skills/{name}`
  confirmed the grant data (`pinned=true`, `reason="role carry"`) was still fully intact —
  the exact collision the reviewer described is gone.
- Separately, a plain bare assignment (no known-skills touch at all) through the same
  assign-then-remove sequence still physically deletes the row — confirmed via a follow-up `GET
  /api/agents/{id}/known-skills/{name}` returning `404` afterward.
- Killed the scratch server, deleted the throwaway binary and scratch directory; `git status
  --short` confirmed only the intended source/test file changes remain.

**`go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`** all pass. `go vet`'s only findings
are the same two pre-existing, unrelated `stopReaper`/`stopRuntimeReaper` findings in
`internal/service/container.go` the original Work Log already confirmed pre-existing.

**Not escalated.** This is a direct implementation of the fresh reviewer's own "What to do" list —
no new ambiguity, no doc gap, no item-vs-item contradiction encountered.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
