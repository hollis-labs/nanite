# Build the Skill Resolver and parameter binding

**Phase:** 4 — Materialization pipeline (`TASKS/skills`)
**Status:** not-started
**Depends on:** `02`, `03`, `04` (reuses `04`'s parsed `Parameters []ParameterSpec` declaration
shape on `skill.Definition`)
**Touches:** new file `internal/skill/resolver.go`, `internal/runtime/agent/context_resolver.go`
(reused, read-mostly — this task's binding logic calls `ResolveContextBlocks`, it does not
modify that mechanism's own implementation), `docs/engineering/GLOSSARY.md` ("Skill Resolver"
entry).

## Context

`docs/engineering/architecture/20-skills.md`'s "Materialization pipeline" section: *"Skill
Resolver → Skill Materializer → Policy/Sandbox → Materialized Skill... Parameters. Static
invocation arguments come from the caller... Dynamically-resolved values reuse
`agent_context_resolvers` as-is (`cmd`/`http` resolver kinds, DB-configurable per agent)... No
second resolver-provider system gets built for skills specifically."*

This task builds the Resolver half only — given a skill's address (task `03`'s store) and a set
of invocation-time arguments, produce the resolved parameter values a Materializer (tasks `07`,
`08`) will splice into the final output. It does not implement composition (`inline`/`fork` —
task `07`) or script/marker execution (task `08`).

**The reuse target, confirmed this planning session — real, but needs adaptation, not literal
as-is reuse:** `agent_context_resolvers` (`internal/store/migrations/118_agent_context_resolvers.sql`,
`internal/store/agent_context_resolvers.go`) is keyed `UNIQUE (agent_id, slot_name)` — one
resolver per agent per named slot, resolved via
`internal/runtime/agent/context_resolver.go:62-100`'s `ResolveContextBlocks(ctx, rows,
workdir) (map[string]string, error)`, which converts each row to an `agentcontext.SlotSpec` and
executes through the shared `agentkit/agentcontext` provider (`resolvers.NewCmdResolver()`,
`NewHTTPTextResolver()`, `NewHTTPJSONResolver()`). This table has no concept of "this skill's
parameter X, for this specific invocation" — it's a flat per-agent slot registry. **The real
design decision this task makes**: a skill's declared parameter (task `04`'s `ParameterSpec`)
can name an existing `agent_context_resolvers` row by `slot_name` as its dynamic-value source;
at materialization time, this task calls the *same* `ResolveContextBlocks` function (not a
reimplementation) against whichever resolver rows the invoking agent has that match the skill's
declared parameter bindings, and folds the results into the parameter set alongside any
caller-supplied static arguments. `20-skills.md`'s own instruction is explicit that no second
resolver-provider system gets built — this task's job is the binding logic connecting a skill's
declared parameter names to existing per-agent resolver rows, not a new execution mechanism.

## What to do

1. Define the Resolver's public entry point (e.g. `ResolveSkillParameters(ctx, skill
   skill.Definition, agentID string, staticArgs map[string]string) (map[string]string, error)`):
   for each declared `ParameterSpec`, if a static arg was supplied by the caller, use it; else if
   the parameter declares a dynamic-resolver binding (a `slot_name` reference), look up that
   agent's matching `agent_context_resolvers` row (via the existing store accessor) and resolve
   it through `ResolveContextBlocks`; else if `Required` and neither is present, return a clear
   error naming the missing parameter.
2. Nested-skill resolution (the "resolved through the same vendored store, by declared
   dependency" half of `20-skills.md`'s Materialization section): given a skill's
   `DeclaredDependencies` (task `02`'s column, populated by task `04`), resolve each dependency
   slug to its own vendored address via task `03`'s store — this task exposes that lookup as a
   building block; task `07` is what actually decides *what to do* with a resolved dependency
   (splice vs. delegate).
3. A single resolver-row failure aborts the whole call (matching `ResolveContextBlocks`'s own
   existing all-or-nothing behavior, per its doc comment) — don't build a partial-success mode
   here that the underlying mechanism doesn't support.
4. Add a `docs/engineering/GLOSSARY.md` entry for "Skill Resolver," cross-referenced against the
   "Skill Materializer" term task `07`/`08` will introduce and the existing "Team Slot"-style
   disambiguation pattern — note explicitly that this is distinct from `agent_context_resolvers`
   itself (which it wraps, not replaces).

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- A test confirms: a skill with a required parameter bound to an existing agent context-resolver
  row resolves correctly end-to-end (real `ResolveContextBlocks` call, not mocked); a static
  caller-supplied arg overrides/coexists correctly per the precedence rule you implement (decide
  and document which wins if both are present — static-caller-supplied taking precedence over a
  dynamic default is the more conventional choice, but state your actual choice in the Work Log);
  a missing required parameter with no binding and no static arg produces a clear error, not a
  panic or a silently-empty value.
- Nested-dependency address resolution works against a real installed dependency (task `04`'s
  test fixture, extended with a second package it declares a dependency on).

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
