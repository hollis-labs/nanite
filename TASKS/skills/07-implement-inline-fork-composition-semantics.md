# Implement `inline`/`fork` composition semantics + install-time cycle detection

**Phase:** 4 — Materialization pipeline (`TASKS/skills`)
**Status:** not-started
**Depends on:** `04` (install-time dependency graph), `06` (Resolver's nested-dependency address
lookup)
**Touches:** new file `internal/skill/compose.go`, `internal/skillinstall/` (task `04`'s
pipeline — add a cycle/recursion-limit check as an install-time validation step),
`docs/engineering/GLOSSARY.md` ("Skill Materializer" entry, or fold into whichever entry task
`08` also touches — coordinate to avoid two tasks each half-writing the same entry).

## Context

`docs/engineering/architecture/20-skills.md`'s "Composition: inline vs. fork" section:
*"`Context: 'inline'|'fork'` already exists as a frontmatter field and is fully dead — parsed,
stored, never read. Given real semantics: **`inline`** — a nested skill's materialized content
is spliced into the parent's materialized output before the model ever sees either. Pure content
composition... No new execution path — it's the Resolver recursively materializing a dependency
and concatenating the result. **`fork`** — invoking the parent instead delegates the nested
skill to its own agent turn/subagent invocation (riding the harness's existing subagent/fork
machinery, not a new execution mechanism), and only that invocation's *result* folds back into
the parent's materialized output. Real delegation, not text-splicing."* And: *"Provenance is
tracked as a chain — user → agent → root skill → nested skill → script/materializer → requested
capability... cycle/recursion-limit detection runs in the Skill Resolver against the install-time
dependency graph (checked once, at install/sync time, against already-installed dependencies)
rather than discovered live during a materialization pass."*

This closes two follow-ups `docs/engineering/architecture/13-memory-and-knowledge-tools.md`'s
§4a filed against a future skills deep-dive: "skill composability" (this task, directly) and
part of the provenance-tracking concern (the chain-tracking half, not the triggering half — see
`TASKS/skills/README.md`'s scope fence on "explicit skill triggering," which stays out of scope).

**The field already exists and is parsed** — `skill.Definition.Context` (`parser.go:28`,
default `"inline"`), folded into `Settings["context"]` at ingest (`convert.go:65-67`, confirmed
by this planning session's research: `if d.Context != "" { settings["context"] = d.Context }`).
Task `01` was instructed *not* to delete this field (only the dead execution logic around the
old `!\`cmd\`` marker) — confirm it's still present before starting this task; if task `01`
removed it by mistake, restore it first and note the correction in your Work Log.

**Subagent/fork machinery to ride, not rebuild**: this codebase already has a real subagent/fork
mechanism (the same one used to dispatch this batch's own worker/reviewer tasks). Find the
actual runtime-facing entry point for spawning a subagent turn from *within* a running agent
session (not the planning-time `Agent`-tool concept — the live, in-product mechanism an agent
uses mid-conversation) before implementing `fork`'s delegation — grep for how `session-task`
envelopes or subagent dispatch is wired in `internal/chat`/`internal/service`, since that's the
real target `fork` needs to invoke, not a new spawning mechanism.

## What to do

1. Implement `inline` composition: given a skill whose `Context == "inline"` and a resolved
   nested-dependency address (from task `06`'s Resolver), recursively materialize the
   dependency's own content (its `SKILL.md` body, with its own parameters resolved) and splice
   it into the parent's materialized output at whatever insertion point the package format
   specifies (check the real Agent-Skills-spec convention for where a composed skill's content
   is meant to appear relative to the parent's own body — don't invent a placement convention
   if the spec already has one).
2. Implement `fork` composition: given a skill whose `Context == "fork"`, delegate the nested
   skill's invocation to the real subagent/fork mechanism you identified in Context above,
   passing through the resolved parameters, and fold only the delegated invocation's *result*
   (not its full transcript) back into the parent's materialized output.
3. Implement install-time cycle/recursion-limit detection in task `04`'s `Installer` pipeline
   (not live during materialization, per the architecture doc's explicit instruction above): when
   a package declares dependencies, walk the graph of already-installed skills' own declared
   dependencies (task `02`'s `DeclaredDependencies` column) and reject the install if it would
   introduce a cycle, or exceed a defined recursion-depth limit (pick a concrete number — e.g. 5 —
   and document why in your Work Log; this is a real design call the architecture doc leaves to
   this task).
4. Provenance chain tracking: as materialization recurses (inline) or delegates (fork), track the
   chain (root skill → nested skill → ...) so a later error or policy decision (task `09`) can
   report which skill in the chain actually failed/was denied, not just "materialization failed."
5. Add or extend the `docs/engineering/GLOSSARY.md` "Skill Materializer" entry — coordinate with
   task `08` if it's running concurrently (both introduce/use this term); whichever task lands
   first writes the entry, the second only extends it if genuinely incomplete.

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- A real `inline`-composed skill (two installed test packages, one declaring the other as an
  `inline` dependency) materializes with the nested content spliced in correctly.
- A real `fork`-composed skill delegates to an actual subagent/fork invocation (not mocked) and
  folds the result back correctly.
- Installing a package that would introduce a dependency cycle is rejected at install time with a
  clear error naming the cycle; installing one that exceeds the recursion-depth limit is
  similarly rejected.
- A provenance chain is available (logged or returned) for a real multi-level composed
  materialization, showing the full root → nested → ... path.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
