# Cut legacy skill discovery, auto-discovery, dead markers, and ad-hoc authoring

**Phase:** 1 — Clean-slate cut (`TASKS/skills`)
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
