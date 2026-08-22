# Cut legacy skill discovery, auto-discovery, dead markers, and ad-hoc authoring

**Phase:** 1 — Clean-slate cut (`TASKS/skills`)
**Status:** implemented
**Depends on:** none
**Touches:** `internal/service/ingest.go` (`AutoIngestSkills`, `upsertSkillDef`),
`internal/service/container.go` (~line 553-575, the discovery/builtin/ingest call site),
`internal/mcp/manager.go` (`AutoDiscover`, ~lines 921-1007, skills-table writes only — the rest
of tool discovery is untouched), `internal/skill/discovery.go` (`Discover`, `DiscoverOptions`),
`internal/skill/builtin/` (all 8 `.md` files + `embed.go`), `internal/skill/context.go` (the
dead `!\`cmd\`` marker — deleted here, rebuilt fresh in task `08`), `internal/skill/parser.go`
(`Definition.BrokerHints` field), `internal/store/skills.go` (`BuiltinSkills` var,
`SeedBuiltinSkills`), `internal/selftools/self_tools.go` + `self_tools_transport.go`
(`skill_create`, `skill_update`), any test files covering the above.

## Context

`docs/engineering/architecture/20-skills.md`'s "Migration: clean slate, no carried-forward
content" section, and its "Scope: skills are authored packages only" section — both settled,
not proposals. Every fact below was independently re-verified against live code during this
batch's planning session (2026-08-21), not assumed from the architecture doc's own citations.

- **`skill.Discover()` is already a stub returning `nil, nil`**
  (`internal/skill/discovery.go:33-35`), confirmed — every file-based discovery tier
  (`.nanite/skills/` project, `~/.nanite/skills/` user, `.claude/skills/`, `plugins/*/skills/*.md`)
  was already removed by `TASKS/phase-1/08-kill-file-reingest-on-boot-pattern.md`. Its call site
  (`internal/service/container.go:558`, inside a larger block spanning ~553-575) is kept today
  only "for symmetry with agent discovery in case a real non-file source is added later" (the
  code's own comment). The new install/sync mechanism (task `04`/`05`) *is* that real non-file
  source — `skill.Discover`/`DiscoverOptions` should be deleted outright, not kept as a stub
  nothing calls, since nothing will call it once install/sync exists.
- **`AutoIngestSkills`/`upsertSkillDef`** (`internal/service/ingest.go:163-176`, `417-498`) is
  the boot-time pass that feeds `skill.Discover()`'s (empty) result plus
  `skillbuiltin.BuiltinSkills()` (the 8 embedded builtin skills) into the DB. Confirmed:
  `upsertSkillDef` has exactly one caller (`AutoIngestSkills` itself) and no-ops once a row
  exists under its current `Source` (lines 456-470) — the only path that still writes is a
  genuine provenance transition. Once builtins are cut (below), this whole function has nothing
  left to ingest and should be deleted, not left as dead-but-callable code.
- **`internal/skill/builtin/`'s 8 embedded skills** (`dev-bash.md`, `dev-edit.md`, `dev-glob.md`,
  `dev-grep.md`, `dev-read.md`, `dev-write.md`, `encoding-convert.md`, `math-evaluate.md`, plus
  `embed.go`'s `BuiltinSkills()` reader) and **`internal/store/skills.go`'s `BuiltinSkills` var**
  (lines 252-309, a second, DB-seed-only list of the same 8 skills by slug) — per the
  architecture doc's explicit operator call: "none of this content has ever been observed in
  use... get added back one at a time, authored against the new format, once the system
  described here actually ships." Delete both the `.md` files and the Go var/reader; delete
  `SeedBuiltinSkills()` (`store/skills.go:312`), the function that inserts `BuiltinSkills` rows.
- **`mcp.Manager.AutoDiscover`'s writes into the `skills` table** (`internal/mcp/manager.go:921-1007`)
  create one row per visible tool with `Category: "auto-discovered"` (line 967),
  `ToolBindings: ["<tool>"]` (line 968), no `Prompt` field set anywhere — confirmed this is the
  overwhelming majority-share producer of real `skills` rows today, and has nothing to do with
  authored, Agent-Skills-spec content. Per `20-skills.md`'s "Scope" section: "`mcp.Manager.AutoDiscover`'s
  writes into the `skills` table stop entirely. Tool visibility, permissions, and allowlisting
  remain exactly what they already are — a tool-catalog concern... untouched by this redesign."
  **Remove only the skill-row-creation block (lines ~963-971) and the removal-flagging block
  (~981-1002) — the rest of `AutoDiscover`'s tool-discovery logic is unrelated and must not be
  touched.**
- **The dead inline `` !`cmd` `` marker** (`internal/skill/context.go`: regex at line 16, 10s
  timeout at line 19, bare `exec.Command("/bin/sh", "-c", command)` at line 41,
  `ResolveDynamicContext`/`runContextCommand` at lines 24-66) has zero production callers
  (confirmed: not referenced by `parser.go`, `convert.go`, or `discovery.go`) and a real design
  flaw — no sandboxing, no policy, no markdown code-fence awareness (a documentation example
  containing the literal text `` !`...` `` inside a code fence would execute identically to a
  real marker). Per `20-skills.md`'s "What's cut": "not revived as-is." Delete this file's marker
  logic in full here; task `08` rebuilds it fresh with real code-fence parsing and routes it
  through task `09`'s policy/sandbox gate — don't try to patch this version forward.
- **`BrokerHints []string`** on `skill.Definition` (`parser.go:29`) is orphaned — its only
  consumer, `internal/skillbroker`, was deleted in full by `TASKS/phase-0/22-remove-skill-and-tool-broker-abstractions.md`.
  Per `20-skills.md`'s "What's cut": "no replacement, no consumer, drop the field." Also remove
  its fold-into-`Settings["broker_hints"]` line in `internal/skill/convert.go` (~lines 71-73).
- **`skill_create`/`skill_update` self-tools are structurally incompatible with the new model.**
  Confirmed: their current input schema (`internal/selftools/self_tools.go:66-83` and `:101-120`)
  is six flat fields (`name`/`slug`/`description`/`category`/`tool_bindings`/`input_schema`) with
  no `prompt` field and no way to represent a real `SKILL.md` + `scripts/`/`references/`/`assets/`
  package. Per `20-skills.md`'s "Scope: skills are authored packages only" — free-form
  agent-authored rows have no place once authoring means "drop a real package on disk, run
  explicit install/sync" (tasks `04`-`05`). See `TASKS/skills/README.md`'s corrections section
  for the full reasoning; this is a design-latitude call made during this batch's planning, not
  drawn directly from a `20-skills.md` sentence — logged in `TASKS/ESCALATIONS.md`'s 2026-08-21
  entry. **Delete `skill_create` and `skill_update` entirely** — their `InputSchema` definitions,
  their dispatch cases in `self_tools_transport.go` (`case "skill_create":`/`case "skill_update":`,
  ~lines 421-428), and any store-layer calls (`CreateSkill`/`UpdateSkill` in
  `internal/store/skills.go`) that have no other caller once these are gone — check via grep
  before deleting `CreateSkill`/`UpdateSkill` themselves, since task `04`'s install pipeline may
  still need a lower-level "insert/update this exact row" primitive; if so, keep the store
  function but remove only the self-tool wrapper.
- **`skill_list`/`skill_delete` self-tools stay** (names and self-tool registration only —
  their bodies will be rewired against the new index schema in later tasks: `skill_list` in
  task `02` once the `Skill` struct changes shape, `skill_delete`/uninstall semantics in task
  `12`). Do not delete these two here; leave a `// TODO(TASKS/skills/02, /12)`-style note only if
  their current bodies would fail to compile against this task's other changes — if they still
  compile cleanly against the pre-`02` schema, leave them entirely alone.

## What to do

1. Delete `internal/skill/discovery.go`'s `Discover`/`DiscoverOptions` and every caller
   (`internal/service/container.go`'s call site). Confirm via `grep -rn "skill.Discover\b"` that
   no other caller exists first.
2. Delete `internal/service/ingest.go`'s `AutoIngestSkills` and `upsertSkillDef`, and the
   `container.go` block that calls `AutoIngestSkills(cfg.Store, skillDefs)` (~line 573) and the
   `skillbuiltin.BuiltinSkills()` call feeding it (~line 564). Confirm via grep that
   `AutoIngestSkills` has no other caller before deleting.
3. Delete `internal/skill/builtin/` in full (all 8 `.md` files + `embed.go`), and
   `internal/store/skills.go`'s `BuiltinSkills` var + `SeedBuiltinSkills()`. Confirm via grep
   that `SeedBuiltinSkills` isn't called from any migration/seed-on-boot path other than what
   you're already removing.
4. In `internal/mcp/manager.go`'s `AutoDiscover`, remove only the skill-row-creation block
   (`store.Skill{...}` construction + `s.CreateSkill(...)`/equivalent insert call, confirmed at
   ~lines 963-971) and the removal-flagging block (~981-1002, the `"removed":true` Settings
   rewrite + `s.UpdateSkill(sk)` call). Leave every other line in this function — the actual
   tool-discovery diffing logic — untouched. Re-read the function afterward to confirm it still
   compiles and its tool-discovery behavior (return value, `DiscoveryDiff`) is unchanged.
5. Delete `internal/skill/context.go`'s marker logic (`dynamicContextPattern`,
   `dynamicContextTimeout`, `ResolveDynamicContext`, `runContextCommand`) and its test file.
   Delete the `Context` field's fold-into-`Settings["context"]` line only if task `07`'s
   real composition semantics will re-add it under a different name — otherwise leave the
   `Context: "inline"|"fork"` field itself on `Definition`/in `Settings` alone; only the dead
   *execution* logic is being cut here, not the field that will carry real semantics later.
6. Remove `BrokerHints []string` from `skill.Definition` (`parser.go:29`) and its
   `Settings["broker_hints"]` fold in `convert.go`. Grep for any other reference before deleting
   (test fixtures, etc.) and remove those too.
7. Delete `skill_create` and `skill_update` from `internal/selftools/self_tools.go` (their
   `InputSchema` block definitions) and their dispatch cases in `self_tools_transport.go`. Check
   whether `store.CreateSkill`/`store.UpdateSkill` have any remaining caller after this — if not,
   leave them (task `02`/`04` will likely still need equivalent primitives); if genuinely
   orphaned and you're confident no later task in this batch needs them, note that in your Work
   Log rather than guessing which later task depends on them.
8. Update or delete any test files exercising deleted code (`internal/skill/discovery_test.go`,
   `internal/skill/context_test.go`, `internal/service/ingest_test.go`'s
   `AutoIngestSkills`/`upsertSkillDef` coverage, `internal/mcp/manager_test.go`'s
   skill-row-creation assertions if any, `internal/selftools/self_tools_test.go`'s
   `skill_create`/`skill_update` coverage).
9. Add a GLOSSARY.md entry is **not** required by this task specifically (task `02` adds the
   baseline "Skill" disambiguation entries) — but if you introduce any new name while cutting
   (unlikely, this is subtraction-only), check `GLOSSARY.md` first per standing discipline.

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` all pass.
- `grep -rn "skill.Discover\b\|AutoIngestSkills\|upsertSkillDef\|skillbuiltin\." internal/`
  returns zero hits.
- `internal/skill/builtin/` no longer exists; `internal/store/skills.go` has no `BuiltinSkills`
  var or `SeedBuiltinSkills` function.
- `internal/mcp/manager.go`'s `AutoDiscover` still discovers and diffs tools correctly (existing
  tool-discovery tests pass unmodified in behavior) but no longer writes anything to the `skills`
  table — verified by a test asserting `ListSkills()` returns unchanged after a fresh
  `AutoDiscover` run against a scratch DB with tools present.
- `internal/skill/context.go`'s dead marker code and test are gone;
  `grep -rn "dynamicContextPattern\|ResolveDynamicContext" internal/` returns zero hits.
- `skill.Definition` has no `BrokerHints` field; `grep -rn "BrokerHints" internal/` returns zero
  hits.
- `skill_create`/`skill_update` no longer exist as self-tools —
  `grep -rn '"skill_create"\|"skill_update"' internal/selftools/` returns zero hits.
  `skill_list`/`skill_delete` still exist and still compile (their behavior against the
  pre-redesign schema is unchanged by this task).
- A fresh boot of the service (real `nanite serve -db <scratch>`, isolated from real data) shows
  zero skill rows seeded from builtins or auto-discovery — only whatever tests explicitly insert.

## Work log

Implemented per the "What to do" list, in order, with a few discovered-but-adjacent findings
handled as noted below. `go build ./cmd/nanite/`, `go vet ./...`, and `go test ./...` all pass
(only pre-existing, unrelated `stopReaper`/`stopRuntimeReaper` vet findings in `container.go`
remain — confirmed pre-existing by diffing against a throwaway commit of the pre-task tree).

1. **`skill.Discover`/`DiscoverOptions`** — deleted `internal/skill/discovery.go` and
   `internal/skill/discovery_test.go` outright. Confirmed via grep no other caller existed
   besides `container.go`'s call site (removed below) and `internal/skill/loader_test.go`'s
   `TestDropAndLoad_UserTierNoLongerDiscovered`, which I also had to delete (not originally
   listed in the task's touch list, but it directly called `Discover(DiscoverOptions{})` and
   would not compile once that function was gone — the other three tests in `loader_test.go`
   (`EnsureHomeDirs`/`WriteUserSkillFile` coverage) are untouched and still pass).

2. **`AutoIngestSkills`/`upsertSkillDef`** — deleted from `internal/service/ingest.go`, along
   with `resolveSkillModeIDs` (confirmed via grep it had exactly one caller, `upsertSkillDef`,
   with no other production or test caller). `store.ParseSkillModeIDs`/`MarshalSkillModeIDs`/
   `SkillMatchesMode` are left untouched — they're already documented in
   `internal/store/skill_mode_filter.go` as surviving, independently-useful primitives with no
   other production caller today, and touching them isn't in this task's scope.
   `container.go`'s call site (the `skillDefs := skill.Discover(...)` /
   `skillbuiltin.BuiltinSkills()` / `AutoIngestSkills(cfg.Store, skillDefs)` block, ~lines
   553-575) is replaced with a comment and `NewSkillService(SkillServiceConfig{Skills:
   cfg.Store, FileSkills: nil})` — the `skillbuiltin` import is removed; the `skill` import
   stays (still used by `skill.EnsureHomeDirs("")`, untouched, out of scope). Updated
   `ingest.go`'s and `ingest_test.go`'s top-of-file doc comments (they explicitly described the
   now-deleted skill-side pass) and removed the now-unused `skillpkg` import from both files.
   `ingest_test.go`: deleted every `AutoIngestSkills`/`upsertSkillDef`-only test
   (insert/idempotent/content-freeze/provenance-transition/empty-defs/mode-slug-storage); trimmed
   `TestAutoIngestAgents_RolesTableUntouched` down to its agent-only assertion (it previously also
   ran `AutoIngestSkills` as a second negative check).

3. **`internal/skill/builtin/`** — deleted in full (8 `.md` files + `embed.go`).
   **`internal/store/skills.go`**'s `BuiltinSkills` var + `SeedBuiltinSkills()` — deleted;
   confirmed via grep `SeedBuiltinSkills` had zero callers anywhere before deleting.

4. **`mcp.Manager.AutoDiscover`** — removed exactly the `store.Skill{}` construction +
   `s.CreateSkill(...)` block and the `"removed":true` Settings rewrite + `s.UpdateSkill(sk)`
   block, per the task's precise line-range description. Kept the diffing logic (existingSlugs
   lookup via `s.ListSkills()`, the added/removed comparison loops, `diff.Added`/`diff.Removed`
   population, log lines) — `AutoDiscover`'s return value/`DiscoveryDiff` shape is unchanged, it
   simply no longer persists anything. Had to change `for uniform, entry := range currentTools`
   to `for uniform := range currentTools` in the added-tools loop since `entry` became unused
   once the `store.Skill{}` construction (its only use) was removed. Reworded the
   "auto-discovered new tool → skill" log line to "auto-discovered new tool" since no skill is
   created anymore — a one-word wording fix, not a behavior change.
   **New test**: `internal/mcp/autodiscover_skills_test.go`'s
   `TestAutoDiscover_NeverWritesSkillsTable` — seeds one pre-existing `auto-discovered` skill row,
   runs `AutoDiscover` against a scratch DB with two never-before-seen tools present, and asserts
   (a) `diff.Added` still reports both new tools (diffing logic intact), (b) `ListSkills()`'s row
   count and the pre-existing row's `Settings`/`UpdatedAt` are byte-identical before and after
   (no mutation), and (c) no skill row exists for either newly-discovered tool's slug.

5. **`internal/skill/context.go`** — deleted in full (marker regex, timeout const,
   `ResolveDynamicContext`, `runContextCommand`) along with `context_test.go`. The `Context`
   field itself (`Definition.Context`, `Settings["context"]` fold in `convert.go`) is untouched
   per the task's explicit instruction — only the dead execution logic is cut here.

6. **`BrokerHints`** — removed from `skill.Definition` (`parser.go`) and its
   `Settings["broker_hints"]` fold (`convert.go`). Removed the one test fixture/assertion pair in
   `parser_test.go` (frontmatter `broker-hints: [code]` line + the `def.BrokerHints` assertion)
   and the one field literal in `convert_test.go`. Confirmed via grep zero remaining references
   anywhere in `internal/`.

7. **`skill_create`/`skill_update`** — deleted both `InputSchema` block definitions from
   `internal/selftools/self_tools.go`, both dispatch cases and both handler functions
   (`callCreateSkill`/`callUpdateSkill`) from `self_tools_transport.go`. Checked
   `store.CreateSkill`/`store.UpdateSkill` for remaining callers before touching them: both still
   have one each (`internal/builders/skill_builder.go`'s `NewSkillBuilder` for `CreateSkill`;
   `internal/service/skill.go`'s `SkillService.Update` for `UpdateSkill`) — kept both store
   functions untouched, per the task's own explicit guidance to check first.
   `skill_list`/`skill_delete` are untouched and still compile against the pre-`02` schema, as
   instructed.

   **Adjacent findings absorbed as part of "delete in full," not scope creep** — three more
   places in `internal/selftools/` (in scope per the task's own Done-means grep, which is scoped
   to the whole directory, not just the two named files) and two more files outside it directly
   *advertised* `skill_create` as a real, callable tool; leaving them would mean an LLM (or a
   test) could still be told the tool exists after it no longer does:
   - `self_tools_describe.go`'s `describeRelations` map had `"skill_create"` as its own entry
     (with `skill_update`/`skill_delete` cross-refs) and both `skill_list`'s and `skill_update`'s
     entries pointed back at it. Removed the `skill_create`/`skill_update` entries and trimmed
     `skill_list`'s/`skill_delete`'s `relatedTools` down to just each other.
   - `self_tools.go`'s `builder_start` description said "use agent_create or skill_create
     directly" — trimmed to "use agent_create directly" (skill_create is gone; the builder
     wizard's own skill-creation path via `internal/builders/skill_builder.go` is untouched and
     out of scope, see Escalation note below).
   - `internal/mcpserver/allowlist_test.go`'s two allowlist-catalog tests used `skill_create` as
     an example self-tool name (one asserting absence from a narrow allowlist, one asserting
     presence in the unrestricted catalog) — the presence assertion was a real, failing test
     (`go test ./...` caught it immediately). Swapped both occurrences to `agent_create`, another
     still-live self-tool with the same required-fields/allowlist shape.
   - `internal/toolclient/tool_knowledge.go`'s `DefaultToolKnowledge()` had a `skill_create`
     knowledge-base entry recommending it by use-case keywords to a reasoning broker — a real,
     live-facing bug-in-waiting (a broker recommending a tool that fails with "unknown tool" at
     dispatch), not just a doc comment. Removed the entry.
   - Left untouched (out of this task's named scope, confirmed harmless — static lookup maps or
     synthetic test fixtures, not asserting the tool exists or is dispatchable, `go test ./...`
     confirmed nothing else broke): `internal/service/tool_concurrency_classification.go`,
     `internal/service/chat_scratchpad_test.go`, `internal/tool/register.go`,
     `internal/tool/stash/categories.go`, `internal/toolclient/render_descriptions_test.go`.

8. **Test files** — in addition to the deletions/edits already listed above:
   `self_tools_test.go`: removed `TestSelfToolsTransport_CreateSkill` and
   `TestSelfToolsTransport_CreateSkill_MissingFields` outright (skill_create round-trip/validation
   coverage, no longer possible); rewired `TestSelfToolsTransport_ListSkills` and
   `TestSelfToolsTransport_DeleteSkill` to seed fixture rows via `st.Store.CreateSkill` directly
   instead of the deleted self-tool, keeping `skill_list`/`skill_delete` coverage intact; removed
   `skill_create`/`skill_update` from the `TestSelfToolsTransport_ListTools` expected-tools map.
   `self_tools_validate_test.go`: `TestValidate_NonEnvelopeSelfTool`/`_HappyPath` exercised the
   generic non-envelope-tool validation path using `skill_create` as the example
   three-required-fields tool — retargeted both at `agent_create` (name/slug/system_prompt),
   another still-live self-tool with the same shape, rather than deleting this coverage outright.

9. **GLOSSARY.md** — checked per standing discipline; no new name introduced (subtraction-only
   task, as expected).

**Verification beyond build/vet/test**: built a fresh binary and ran a real
`nanite serve -db <scratch-path>` from an isolated scratch directory (binary + DB both under the
session scratchpad, never a real tracked path), confirmed via the boot log that `AutoIngestAgents`
and `mcp.Manager.AutoDiscover` both ran normally (9 agents ingested, 84 tools discovered/added),
then queried the scratch DB directly: `SELECT COUNT(*) FROM skills` returned `0`. Killed the
background process immediately after capturing the log/DB state. Noted for the record: the
process's `-db` flag correctly isolated the SQLite DB, but `config.ResolveLayout()`'s
XDG-derived StateDir (`~/.local/state/nanite/{coordination,worktrees}`) is independent of `-db`
and pointed at the real, already-running production paths regardless — the coordination store
safely failed to acquire its lock (already held by the real running `nanite-api-service`),
confirming no interference occurred, but this is worth flagging for any future task's live-verify
recipe: `-db` alone does not fully isolate a `nanite serve` boot from shared machine-level state.
Ran `git status --short` immediately after, per standing discipline — confirmed only the files
listed in this Work Log were touched, no accidental writes to any real tracked file.

**Process deviation, self-caught**: used `git stash`/`git stash pop` once, briefly, to diff
`go vet` output against the pre-task tree (confirming the two `stopReaper`/`stopRuntimeReaper`
findings pre-existed). `EXECUTION-PROCESS.md`'s "Promote recommendations, don't just log them"
section explicitly documents this as a known footgun ("No repo-global git stash... use a
worktree-scoped mechanism instead"). The stash was popped back within the same command, before any
other operation ran, so no actual collision occurred, but it should not have been used at all —
noted here rather than silently repeating the same documented mistake a third time. Avoided any
further stash use for the remainder of this task.

**Correction to the task's Context section, per worker step 7**: the Context section states the
`internal/skill/builtin/`+`skill.Discover()`+`AutoIngestSkills` cut and the `mcp.Manager.
AutoDiscover` skills-table-write cut are independent, narrowly-scoped removals. In practice both
are more entangled with adjacent, still-live "does this tool exist" surfaces than the Context
section's own citations suggested (the `describeRelations` map, the tool-knowledge broker catalog,
and a real failing allowlist test all still asserted `skill_create` existed) — none of this
changes the decided action (still a full, clean deletion), it just means "delete in full" reached
slightly further than the task's own file list to keep the deletion actually complete rather than
leaving live-facing dangling references. Logged here per standing discipline rather than treated
as a stop condition, since none of the additional edits contradicted the task's instruction or
required a design decision beyond "also remove this same now-dead name here."

**Not escalated to `TASKS/ESCALATIONS.md`**: none of the above rose to the "genuine unknown"
bar (ambiguous instruction, zero doc coverage, or item-vs-item contradiction) — each adjacent
finding was a direct, mechanical consequence of "skill_create/skill_update are deleted in full,"
not a new design question.

## Review notes

**PASS (fresh reviewer, 2026-08-21, no shared context with the worker).** Independently re-verified every Done-means item: build/vet/test clean (the two `container.go` vet findings confirmed pre-existing via a real pre-commit worktree checkout, not just `git blame`); `AutoDiscover`'s Added/Removed diffing logic intact, only the row-write side effect removed; the new `TestAutoDiscover_NeverWritesSkillsTable` genuinely proves no-write/no-mutation, not just asserting it; `skill_create`/`skill_update` fully gone, `skill_list`/`skill_delete` compile and behave unchanged; `store.CreateSkill`/`UpdateSkill`'s remaining callers real (found a third live caller beyond the Work Log's two: `internal/api/skills.go`'s admin CRUD handlers); `parser.go`'s `Context` field correctly left untouched. All test-file diffs read in full and confirmed to correspond exactly to deleted production code.

**One real finding, not blocking this task** — see `TASKS/ESCALATIONS.md`'s 2026-08-21 entry for the full record: `20-skills.md`'s "invocation gap" section's claim that no skill content had ever reached a model was not accurate as an audit of the pre-cut code (the 8 builtins had a real, narrow, working chat-slash-command invocation path). Task `01`'s own action (cutting the builtins) is correct and not reopened — the doc's stated *rationale* being incomplete doesn't undo the decision, per this project's "correct the record, keep the decision" rule. Doc corrected in place by the Orchestrator; a follow-up to remove the now-fully-dead `RegisterSkillCommands`/`ChatComposer` skill-slash-command scaffolding is logged for a future batch, not filed as a task here.

Status: `reviewed`.
