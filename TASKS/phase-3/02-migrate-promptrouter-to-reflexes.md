# Migrate promptrouter's phrase catalog into reflex predicates; fix phantom entries; retire `internal/promptrouter`

**Phase:** 3
**Status:** not-started
**Depends on:** `01-dispatch-to-agent-reflex-action-kind-and-broker-migration.md` (needs the new action kind, and its documented decision on whether the reflex-table match directly feeds dispatch or is subsumed into the action kind's own trigger evaluation).
**Touches:** `internal/promptrouter/*` (full package: `catalog.go`, `dispatcher.go`, `matcher.go`, `loader.go` — retire after migration), `internal/mcp/self_tools_dispatch.go` (`callExecuteTask`'s separate `st.ReflexSet`-driven hint mechanism — a second consumer of the same catalog, see Context), `internal/service/chat_broker_dispatch.go` (already emptied by task 01; confirm no remaining `promptrouter` import), `internal/agent/builtin/profiles/{researcher,reviewer}.md` (reference only — confirms these two phantom-adjacent slugs already resolve correctly), `~/.nanite/reflexes/*.yaml` user-override convention (`loader.go`'s `LoadUserReflexes` — decide what replaces this once the catalog moves into the DB-backed `agent_reflexes` table), `docs/promptrouter-catalog.md`, `docs/promptrouter-authoring.md`, `docs/agent-pattern-catalog.md` (doc cleanup).

## Context

TASKS.md Phase 3: *"...promptrouter's phrase catalog into reflex predicates (fixing the two phantom-agent reflex entries, and doing the `promptrouter` naming cleanup as part of this migration)."* TASKS.md Phase 0 explicitly deferred this: *"Deliberately not included here: renaming `promptrouter`'s 'reflex' vocabulary... since `promptrouter` itself is being absorbed into reflexes in Phase 3, renaming its internals now and migrating them shortly after is likely wasted motion."*

### The catalog and the naming collision

`internal/promptrouter/catalog.go` (274 lines) carries a `Reflex` struct (`Triggers`/`Resolution`/`SideEffects` fields) and `BuiltinReflexes()` — a hardcoded Go-literal list of 8 entries. Its own package doc-comment (lines 1-27) records that this package was **previously named `internal/reflex`**, renamed specifically to stop it colliding with `internal/agent/reflexes` — per `GLOSSARY.md`, "reflexes" is now reserved exclusively for the agent-steering engine. Every identifier below still carries the old "reflex" vocabulary and needs renaming as part of this migration (not before — see Phase 0's deferral note above):

- `Reflex` struct (`catalog.go:91-107`) — fields `ID`, `Triggers`, `ResolvesTo`, `SideEffects`, `Priority`.
- `Triggers` struct (`:32-45`) — `UserPhraseAnyOf`, `ScopeTierHint`, `ExecutionPatternHint`.
- `Resolution` struct (`:48-73`) — `Pattern`, `Role`, `Profile`, `WorkflowName`.
- `SideEffects` struct (`:76-86`) — `ModeSignal`, `DispatchVia`.
- `BuiltinReflexes()` (`:118`) — the catalog itself, 8 entries.
- `ReflexMatch`, `Match()`, `MatchAll()` (`matcher.go:13-98`).
- `MatchLogger`, `LogReflexMatch`, `ReflexMatchEntry`, `AssignRoleWithReflex()`, `reflexMatchToAssignment()` (`dispatcher.go:10-137`).
- `MergeReflexes()` (`dispatcher.go:174-186`).
- `ReflexYAML`/`TriggersYAML`/`ResolutionYAML`/`SideEffectsYAML`, `LoadUserReflexes()`, `reflexFromYAML()` (`loader.go` — the `~/.nanite/reflexes/*.yaml` user-override schema).

### The two phantom entries — definitively identified

`documentor-mention` (`catalog.go:206-228`) and `strategist-mention` (`catalog.go:229-254`) both leave `Resolution.Profile` empty on purpose — inline comments confirm no matching agent profile exists (`documentor-mention`: no `documentor` profile at all; `strategist-mention`: `.nanite/agents/content-strategist.md` exists but is an unrelated, narrower Glyph-editorial role). Both fall back to `AssignRole`'s generic `worker` default rather than routing anywhere meaningful. The other 6 catalog entries (`worker`/`planner`/`researcher`/`reviewer`-resolving) all correctly resolve to real, existing profiles today — `researcher.md`/`reviewer.md` were added specifically to close this exact gap for those two slugs (see their own header comments). **"Fixing" the two phantom entries means one of: (a) build/assign a real profile each can resolve to, or (b) delete the phantom entries from the catalog if no real profile is wanted.** This task doesn't pre-decide which — the migration is the natural point to make that call, since a still-phantom entry has no sensible reflex-predicate translation (a `dispatch_to_agent` reflex needs a real target).

### A second, independent consumer must not be left stranded

`internal/mcp/self_tools_dispatch.go`'s `callExecuteTask` (~lines 116-154) runs its own separate match against `st.ReflexSet` (loaded via `promptrouter.LoadUserReflexes()`, potentially including user overrides beyond `BuiltinReflexes()`) to populate `dispatch.ReflexHints` for the `task_execute` self-tool's dispatch call. `chat_broker_dispatch.go`'s header comment explicitly treats this as a deliberately separate layer from the upstream broker path task 01 retires ("Both layers run. Don't collapse them."). Retiring `internal/promptrouter` outright removes this consumer's data source — this task must give it an equivalent (reading from the migrated reflex predicates instead of the old catalog), not just delete it and let `task_execute`'s dispatch hints silently go blank.

## What to do

1. For each of the 8 `BuiltinReflexes()` entries, design and create an equivalent DB-backed reflex row (using the `dispatch_to_agent` action kind from task 01, or whatever trigger/action shape task 01 settled on): phrase-list triggers (`UserPhraseAnyOf`) map onto the reflex engine's regex-match trigger support (a documented superset per architecture doc 03 — confirm the regex engine can express a simple "any of these phrases" match cleanly, escaping as needed) plus the existing `ScopeTierHint`/`ExecutionPatternHint` predicates.
2. Resolve the two phantom entries per the Context discussion — either assign a real target profile or drop the entry; document the choice made.
3. Decide what replaces the `~/.nanite/reflexes/*.yaml` user-override convention (`loader.go`) — the DB is now authoritative for reflexes generally (per the construction-model principle), so this is likely "operators use the same `agent_reflexes` CRUD path everyone else uses," not a parallel YAML-file override system. Confirm no real user currently relies on a populated override file before removing the loader (check `~/.nanite/reflexes/` for actual content, not just the code path).
4. Give `internal/mcp/self_tools_dispatch.go`'s `callExecuteTask` an equivalent data source for `dispatch.ReflexHints` against the migrated reflex predicates, preserving its deliberate independence from the upstream dispatch path (per the header-comment instruction not to collapse the two layers).
5. Delete `internal/promptrouter` in full once nothing references it. Confirm via grep that `chat_broker_dispatch.go` (emptied by task 01) has no lingering import.
6. Update or retire `docs/promptrouter-catalog.md`, `docs/promptrouter-authoring.md`, `docs/agent-pattern-catalog.md` — don't leave docs describing a deleted package.

## Done means

- All 6 non-phantom `BuiltinReflexes()` entries have real, working DB-backed reflex equivalents, verified by triggering each phrase in a real session and confirming the same dispatch target fires as before.
- The two phantom entries are resolved one way or the other (real target assigned, or deliberately dropped) — not carried forward still-phantom.
- `internal/mcp/self_tools_dispatch.go`'s `task_execute` self-tool still produces real `ReflexHints` after the migration, verified by a real call.
- `internal/promptrouter` package is deleted; `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass with no remaining references.
- No code or active docs still use "reflex" naming to mean anything other than `internal/agent/reflexes` — confirmed via a repo-wide grep for the retired identifiers listed in Context.
- `docs/promptrouter-*.md`/`docs/agent-pattern-catalog.md` no longer describe a live system that doesn't exist (updated or removed).

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
