# Tool concurrency-safety classification — replace name-heuristic with declared metadata

**Phase:** 4
**Status:** implemented
**Depends on:** none
**Touches:** wherever the current name-heuristic lives (`GetToolMeta` per the architecture doc's own reference — locate and confirm exact file:line during implementation), `known_tools` catalog (Phase 1) if a declared-metadata field belongs there instead of/alongside the current mechanism.

## Context

Architecture doc `03-steering.md`, "Two correctness gaps carried into implementation": *"Tool concurrency-safety classification is currently pure name-heuristic (suffix/substring matching), not derived from declared tool metadata."* Decision log §11 restates this as a real, explicitly-not-forgotten gap (contrasted with things genuinely deferred by design) — a misleadingly-named destructive tool could be misclassified as safe to run concurrently with others, which is a real correctness/safety risk, not a style nit.

This item is listed under Steering in the architecture doc but is not spelled out as its own bullet in `TASKS.md`'s terse Phase 3 summary — it's included here because the architecture doc explicitly assigns it to this subsystem's "carried into implementation" list, and `TASKS.md` is deliberately terse (`docs/engineering/TASKS.md:1`: "Concrete, sequenced work implementing `architecture/*.md`").

## What to do

1. Locate the current heuristic (find `GetToolMeta` or equivalent — grep for the concurrency/parallel-safety classification logic; the architecture doc names it as suffix/substring name matching, e.g. treating tools named like `*_read`/`*_get` as safe and `*_write`/`*_delete` as unsafe, or similar).
2. Audit the heuristic against the full live tool catalog (built-in tools + any registered plugin/MCP tools) for misclassifications — a tool whose name doesn't match the expected pattern but is genuinely destructive (or vice versa: a tool that looks destructive by name but is actually safe).
3. Design a declared-metadata mechanism: a real field (likely on `known_tools`, Phase 1's new catalog table, or wherever tool metadata is registered today if `known_tools` isn't ready yet) that explicitly marks a tool's concurrency safety, set at registration time by whoever defines the tool — not inferred from its name.
4. Migrate the classification logic to read the declared field, falling back to the name-heuristic only for tools that haven't declared it yet (if a staged rollout is needed), or requiring every tool to declare it up front if that's more consistent with the "real relational references, not free-text strings" principle already governing Phase 1.
5. Fix every misclassification found in step 2 by setting the correct declared value.

## Done means

- Every built-in and currently-registered tool has an explicit, correct concurrency-safety classification — not inferred from its name.
- The name-heuristic is either removed entirely or reduced to a documented, narrow fallback (not the primary mechanism).
- The audit from step 2 and its findings are recorded in this file's Work Log, including any misclassifications found and fixed.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

### 1. Located the heuristic, and confirmed which tools actually hit it

`GetToolMeta` lives in `internal/service/tool.go` — at task-dispatch time the
concurrency-safety switch was at lines 726-736 (the task's pointer said
677-682; it had drifted a bit from earlier unrelated edits to the same
file, confirmed live by reading the file directly rather than trusting the
line numbers). The old logic: `IsReadOnly` tools (suffix `_read`/`_glob`/
`_grep`/`_search`/`_list`/`_get`, or substring `web_fetch`/`web_search`)
were concurrency-safe; otherwise a second substring check for `json_parse`/
`datetime`/`hash`/`uuid` also counted as safe; everything else defaulted to
unsafe.

Per the task brief's instruction to confirm which tools actually hit this
heuristic vs. `internal/tool`'s builder-declared path
(`WithConcurrencySafe`/`WithConcurrencySafeFunc`,
`Tool.IsConcurrencySafe(input)`): **the builder-declared path has zero
production callers.** `internal/tool.WrapExistingTools`/`WrapProviderDef`
(the only place `inferSafetyOptions` — itself a second, unwired name-based
switch keyed on exact tool names rather than suffixes — ever runs) are
referenced only from that package's own test files
(`internal/tool/adapt_test.go`). `internal/tool/register.go`'s
`WrapExistingTools` has no caller anywhere in `cmd/`, `internal/api`,
`internal/chat`, or `internal/service`. The only pieces of `internal/tool`
actually wired into the running binary are `tool.ResultCache`/
`tool.NewResultCache` (the unrelated S4a tool-result cache), used by
`internal/service/container.go` and `chat.go`. Confirmed further: `go-
toolbroker` (which `internal/tool`'s builder pattern appears to have been
scaffolded to eventually replace/interop with) was already cut in Phase 0
item 22, superseded by `internal/toolclient`'s own broker
(`internal/toolclient/broker.go`), which has no concurrency-safety concept
at all. **Every real tool call in the running system — the parallel-safety
decision in `chat_tool_executor.go`'s `preCheckTools`
(`plan.concurrent = toolInfo.IsConcurrencySafe`) and the permission-engine
metadata at the same call site — goes through `service.GetToolMeta`
exclusively.** This is a correction to the task's own hint (which treated
`internal/tool`'s builder mechanism as a live alternative path the
heuristic might be filling a gap for): it isn't live at all. I did not
expand scope to wire up or remove `internal/tool`'s dead builder/registry
code — that's a separate, larger dead-code question outside this task's
brief (concurrency-safety classification specifically), and no other
`TASKS/*` file currently claims it. Noting it here as a heads-up for a
future dead-code sweep, not actioning it.

### 2. Audit of the live catalog against the old heuristic

Enumerated the full compiled-in tool catalog by grepping every `Name:
"..."` tool definition across `internal/mcp/*.go` (dev, general, code-exec,
self-service transports) and `internal/toolclient/meta_tools.go` (the S4a
result-cache meta-tools) — ~90 real tool names, cross-checked against
`cmd/nanite/main.go`'s actual `RegisterBuiltins`/`AddBuiltinServer`/
`AutoDiscover` wiring to confirm every one of them is really reachable in
the live catalog `ToolClient.ListTools()` returns (the same catalog
`SyncKnownTools` consumes at boot). For each tool I read its handler
function name and description (not just its name) to judge real
concurrency safety, then hand-applied the OLD heuristic's exact logic to
find divergences. Real misclassifications found (all false negatives — a
genuinely safe, side-effect-free tool the old heuristic marked unsafe
because its name didn't fit the suffix/substring pattern):

- `base64_encode`, `base64_decode`, `url_encode`, `url_decode`, `math_eval`
  — pure, deterministic, side-effect-free transforms (same family as
  `json_parse`/`datetime`/`hash`, which the heuristic *did* special-case),
  but none of these five names contain `"hash"`/`"datetime"`/`"json_parse"`/
  `"uuid"` as a substring, so they fell through to the unsafe default.
- `install_diff` — its handler (`callInstallDiff`,
  `internal/mcp/self_tools_transport.go`) is currently a stub that returns
  "install diff not yet implemented" and mutates nothing; genuinely
  read-only, but `"diff"` doesn't match any read-only suffix.
- `message_inbox`, `message_thread`, `message_catch_up`,
  `handoff_pointers_expand`, `subagent_status`, `subagent_role_audit`,
  `background_status`, `whoami`, `tool_describe`, `tool_validate`,
  `fetch_tool_result` — all genuinely read/query-only handlers (confirmed
  by reading `self_tools_transport.go`'s dispatch — `call*Status`,
  `call*Inbox`, `call*Thread`, `call*CatchUp`, `call*Audit`,
  `call*Describe`, `call*Validate`, `executeWhoami`,
  `call*PointersExpand`), but none end in a matched suffix (`_status`,
  `_inbox`, `_thread`, `_audit`, `_describe`, `_validate` are all absent
  from the suffix list) or contain a matched substring.
- `search_tool_result` — the single most illustrative finding: this tool
  is a pure regex-search read over already-cached data (exactly the kind
  of thing the heuristic was designed to catch), but `"search"` is a
  *prefix* here, not a suffix — `strings.HasSuffix(name, "_search")` can
  never match `search_tool_result`. This is a concrete example of exactly
  the fragility the architecture doc flags: a suffix/substring heuristic's
  correctness depends entirely on naming convention discipline that
  nothing enforces.

No false positives were found (no tool that reads as destructive by
semantics but the old heuristic marked safe) among the compiled-in
catalog — the closest near-miss is `dev_bash`, whose real safety is
input-dependent (`ls` vs. `rm -rf`); the heuristic already defaulted it to
unsafe, which is correct, just via the conservative default rather than
real classification.

Two dead branches in the old heuristic, noted for the record: `"uuid"`
never matches any real registered tool name (no tool called anything with
`uuid` exists in the catalog), and `"web_search"` likewise doesn't exist
as a real tool (only `web_fetch` is registered) — both were presumably
aspirational/copy-pasted from a sibling heuristic rather than reflecting
the actual catalog.

Cross-checked against a copy of the real production database
(`~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-
backup-20260818-132726`, copied to an isolated scratchpad path, opened
read-only via `sqlite3`, deleted after use — never opened via `go test`/a
running server against the real tracked path). That backup predates
migration 116 (`known_tools` didn't exist yet), so I queried
`agent_known_tools.tool_name` instead (the live per-agent roster table)
for real historical tool names actually used in production. Confirmed
every compiled-in name in this task's declared table that also appears in
that list (`dev_read`/`dev_write`/`dev_edit`/`dev_bash`, `card_show`,
`fetch_tool_result`/`search_tool_result`, `message_*`, `procedure_get`,
`request_tools`, `scratchpad_read`/`scratchpad_write`, `subagent_cancel`/
`subagent_spawn`, `tool_describe`/`tool_list`, `workflow_run`) matches my
classification. The large remainder of that list (`memory_*`/`knowledge_*`
/`context_search` from Vanta, `loom_*`, `torque_*`, `tether_*`,
`cerberus_*`, `mux_*`, `hadron_*`, `glyph_*`, `relay_*`, `repo_*`,
`fragments_*`, `tesseract_*`, `narrative_*`, `git_*`, `docs_*`,
`agent_db_*`, `skill_get`/`skill_search`, `nil_create_item`,
`stack_explorer_*`, plus two names I could not account for at all,
`bash_run`/`code_run`, possibly stale pre-rename entries) are external
MCP-server tools, not part of this compiled binary's own catalog — they
correctly fall through to the fail-closed default (see below) rather than
being guessed at from their names, which is exactly the point of this
task.

### 3. Declared-metadata mechanism — design and where it lives

Confirmed `known_tools.concurrency_safe` (added by migration
`116_known_tools_and_agent_tools.sql`, Phase 1 item 04) is the intended
home: its own doc comment already says *"placeholder column for Phase 3
item 06 [now renumbered Phase 4 item 07] ... expected to populate it from
declared tool metadata, replacing the current name-heuristic ... this
migration only reserves the column, it does not populate or consume it."*
`internal/store/known_tools.go`'s `KnownTool.ConcurrencySafe *bool` doc
comment says the same. I did not need to add a migration — the schema was
already correctly reserved and unpopulated.

I considered, per the task's own prompt, whether `internal/tool`'s
builder-level mechanism should be the actual source of truth with
`known_tools` mirroring it (a "sync from the real registration site"
pattern, matching how `agent_tools` mirrors `agent_profiles`). Rejected
this: (a) that mechanism is dead code in production (see §1) — making it
load-bearing would mean building an entirely new registration flow just
for this task, well beyond its scope; (b) it's ALSO name-keyed
(`inferSafetyOptions` switches on exact tool-name strings), so promoting
it wouldn't actually eliminate a name-heuristic, just relocate one; (c) it
expresses per-input safety (`func(map[string]any) bool`), which
`known_tools.concurrency_safe`'s single static `BOOLEAN` column
structurally cannot represent anyway — the two mechanisms answer different
questions at different granularity, so a "mirror" relationship would be
misleading. `known_tools.concurrency_safe` stays the single, real,
DB-level source of truth for the coarse per-tool-name classification the
live decision path actually consumes.

The declared VALUES themselves are seeded from a new curated Go table,
`internal/service/tool_concurrency_classification.go`
(`declaredToolConcurrencySafety map[string]bool` +
`declaredConcurrencySafety(name string) (bool, bool)`), covering every
tool in §2's audit. Each entry is a literal per-name assertion set by
reading that tool's actual handler/description — not a pattern match —
with inline comments explaining the reasoning for every non-obvious case
(especially the 16 misclassification fixes, `dev_bash`'s deliberate
conservative default, and `workflow_verify_step`'s mixed-mode caveat).
`SyncKnownTools` (`internal/service/known_tools_sync.go`) now calls a new
store method, `Store.SetKnownToolConcurrencySafeIfUnset(ctx, name, safe)`
(`internal/store/known_tools.go`), right after each tool's
`UpsertKnownTool`, for every name the curated table declares. That method
is a plain `UPDATE ... WHERE name = ? AND concurrency_safe IS NULL` — it
only ever fills a NULL column, so a routine boot-time re-sync can never
clobber a later operator override, mirroring the exact precedent
`UpsertKnownTool` already established for `always_included` (per that
method's own doc comment, which explicitly calls both columns
"operator/Phase-3-classification-owned"). `SyncKnownTools`'s signature was
deliberately left unchanged (no new parameter) — the classification table
lives in the same package and is called directly — to avoid touching its
several existing test call sites for a change orthogonal to what they
test.

### 4. Migrated `GetToolMeta` to read the declared field

Chose **"removed entirely,"** not "narrow heuristic fallback" (the task
explicitly offered both as valid). `GetToolMeta`'s `IsConcurrencySafe`
assignment no longer inspects `toolName` at all: it calls
`s.declaredConcurrencySafe(ctx, toolName)`, which reads
`known_tools.concurrency_safe` via the already-existing
`agentToolsStore()`/`GetKnownToolByName` pattern this file already uses
for `resolveAlwaysIncludedTools`. Default is **false (fail closed)** for
every case where no explicit `true` is on record: no store wired (test
constructions), no `known_tools` row for that name yet, or a row whose
`concurrency_safe` column is still NULL. This is a permanent fix, not just
a one-time catalog snapshot — a brand-new tool added after this task
(compiled-in or MCP-discovered) defaults to serial execution until
someone explicitly reviews and declares it safe, rather than silently
inheriting whatever a name-pattern happens to match. `IsReadOnly`/
`IsDestructive` were deliberately left untouched (still the old
suffix/substring heuristic) — the task and the architecture doc's "Two
correctness gaps" both name concurrency-safety specifically, not those two
fields, and changing them isn't needed to satisfy this task's Done-means.

Signature change: `ToolService.GetToolMeta` now takes `ctx context.Context`
as its first argument (needed for the DB read). Updated both real call
sites (`internal/service/chat_tool_executor.go`, `preCheckTools` —
`ctx` was already in scope at both) and all four test-stub
implementations of the `ToolService` interface (`chat_test.go`,
`chat_reflex_dispatch_integration_test.go`,
`workflow_step_executor_test.go`, `chat_tool_executor_loop_test.go`).

### 5. Fixed every misclassification

Every real misclassification found in §2 now has a correct `true` entry in
`declaredToolConcurrencySafety`; every genuinely unsafe tool keeps `false`.
Rewrote `TestToolMetaInfo_ConcurrencySafe`
(`internal/service/chat_loop_state_test.go`) into an end-to-end test that
runs the real `SyncKnownTools` → `GetToolMeta` path against a real
in-memory-equivalent `*store.Store` (`t.TempDir()`-rooted, per the
migrations-testing convention already used throughout this package) and
pins every misclassification fix as its own labeled case, plus two
genuinely-unsafe controls (`skill_delete`, `code_execute`) so a future
regression that flips a real destructive tool to "safe" would fail loudly.
Added `TestToolMetaInfo_ConcurrencySafe_UndeclaredFailsClosed` for the two
"fail closed" paths (no store; unclassified/unknown tool). Added store-level
tests for the new `SetKnownToolConcurrencySafeIfUnset` method
(`internal/store/known_tools_test.go`): backfill-then-preserve-override,
and no-op-on-missing-row.

### Verification against a real backup

Per `EXECUTION-PROCESS.md` step 5's schema-migration testing note: this
task added no new migration (the `known_tools.concurrency_safe` column
already existed from migration 116), so that requirement doesn't strictly
apply — but I still cross-checked the real production backup (see §2) as
a live sanity check on catalog coverage, via read-only `sqlite3` queries
against an isolated scratchpad copy, never against the real tracked DB
path and never via `go test`/a running server pointed at it. Copy deleted
after use.

### Build/test/vet

`go build ./cmd/nanite/` — passes. `go test ./...` — all packages pass
(full run, ~4 min). `go vet ./...` reports one pre-existing failure
unrelated to this task, confirmed via `git diff HEAD --
internal/service/container.go` returning empty (the file is untouched by
this task's changes): `stopReaper`/`stopRuntimeReaper` "not used on all
paths" in `container.go:1136/1156/1207`. Scoped `go vet
./internal/store/...` (the only other package this task touches) passes
clean.

### Files changed

- `internal/service/tool_concurrency_classification.go` (new) — declared
  classification table.
- `internal/service/tool.go` — `GetToolMeta` signature + implementation.
- `internal/service/known_tools_sync.go` — backfill wiring.
- `internal/service/chat_tool_executor.go` — two call sites pass `ctx`.
- `internal/store/known_tools.go` — `SetKnownToolConcurrencySafeIfUnset`.
- `internal/store/known_tools_test.go` — new store-level tests.
- `internal/service/chat_loop_state_test.go` — rewritten concurrency test
  + fail-closed test, new imports (`context`, `toolclient`).
- `internal/service/chat_test.go`,
  `internal/service/chat_reflex_dispatch_integration_test.go`,
  `internal/service/workflow_step_executor_test.go`,
  `internal/service/chat_tool_executor_loop_test.go` — stub signature
  updates only.

No escalation was needed — the task's own instruction was unambiguous
once the two open design questions it explicitly flagged (where the
declared field lives; heuristic-fallback vs. removed-entirely) were
resolved by direct investigation of the code, as intended.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
