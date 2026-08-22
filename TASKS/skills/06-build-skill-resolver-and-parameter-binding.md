# Build the Skill Resolver and parameter binding

**Phase:** 4 — Materialization pipeline (`TASKS/skills`)
**Status:** implemented
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

**Pre-flight.** Read `docs/engineering/architecture/20-skills.md` in full and
`docs/engineering/EXECUTION-PROCESS.md`. Confirmed dependency state matches the task file's own
description: `skill.Definition.Parameters []ParameterSpec` (`Name`/`Description`/`Required`/
`ResolverSlot`) is on `main` in `internal/skill/parser.go`; `internal/skillvendor.Store`
(`New`/`Write`/`Path`/`ReadFiles`/`Delete`) and `internal/skillinstall.Installer` are landed;
`store.Skill.DeclaredDependencies` (JSON array of slugs) and `GetSkillBySlug` are on `main` in
`internal/store/skills.go`. Independently re-read `internal/store/agent_context_resolvers.go`'s
`AgentContextResolver` struct and `internal/runtime/agent/context_resolver.go`'s
`ResolveContextBlocks(ctx, rows []store.AgentContextResolver, workdir string) (map[string]string,
error)` signature myself (per the dispatch note flagging task `04`'s own review finding on this
exact point) before wiring anything — confirmed `ParameterSpec.ResolverSlot` and
`AgentContextResolver.SlotName` are different Go identifiers with different YAML/JSON tags; the
correspondence is semantic (both name an `agent_context_resolvers` row's slot name as a plain
string), never a literal field-name match. My own resolver.go doc comment and this Work Log both
state it this way, not as an "exact match."

**Import-cycle check before writing anything.** `internal/skill` already imports `internal/store`
(via `convert.go`). Confirmed via grep that neither `internal/store` nor `internal/runtime/agent`
imports `internal/skill` anywhere (direct or transitive through `internal/runtime/agent`'s own
import list) — so `internal/skill/resolver.go` importing `runtimeagent
"github.com/hollis-labs/nanite/internal/runtime/agent"` (the exact alias
`internal/service/chat_boot_drive.go` already uses for the same import) introduces no cycle.
Confirmed by `go build ./cmd/nanite/` succeeding immediately.

**GLOSSARY.md check before naming anything.** Grepped for `AgentContextResolverStore`,
`SkillIndexStore`, `MissingSkillParameterError`, and `DependencyAddress` across `internal/` and
`docs/` — zero collisions.

**1. `internal/skill/resolver.go` — `ResolveSkillParameters`.** Implemented as a package-level
function in `internal/skill` (not `skill.Definition` as a receiver, since the task's suggested
signature already takes a `skill skill.Definition` value parameter and this file already lives
inside package `skill`). Signature deviates from the task's own "e.g." suggestion in two ways,
both noted here per the task's explicit "state your actual choice" instruction:
- Added a `workdir string` parameter. `ResolveContextBlocks` requires a workdir (the base directory
  a `cmd`-kind resolver's CWD defaults against when its own row-level CWD is empty) — the task's
  suggested signature omitted it, but the underlying mechanism has no way to run without one.
  Threaded through exactly as `internal/service/chat_boot_drive.go`'s own
  `resolveAgentContextForBoot` already does for the equivalent boot-time call.
- Added a `resolvers AgentContextResolverStore` parameter (a narrow interface —
  `ListEnabledAgentContextResolvers(ctx, agentID) ([]store.AgentContextResolver, error)` — a real
  `*store.Store` satisfies it directly) rather than reaching for a global/injected store singleton,
  matching this batch's established `internal/skillinstall.Vendorer`/`IndexStore`
  DI-for-testability convention (there is only ever one real implementation in production; the
  interface exists so tests can inject a fake without hand-corrupting a real DB).

**Precedence decision (task's own explicit ask): static caller-supplied argument wins.** When a
parameter has both a static arg in `staticArgs` and a `ResolverSlot` binding, the static value is
used and the dynamic binding is never even looked up for that parameter (it's excluded from the
"needed slots" set before any DB/resolver call happens). This is the "more conventional choice" the
task file itself flags as the expected default, and it's what I implemented — an explicit,
per-invocation override always beats a standing per-agent default.

**Only the referenced resolver rows are ever fetched/resolved, not every enabled row the agent
has.** `resolveNeededDynamicBindings` computes the set of `ResolverSlot` names this skill's own
declared parameters actually reference (skipping any already satisfied by a static arg), calls
`ListEnabledAgentContextResolvers` once, filters to just the matching rows, and resolves all of
them in one `ResolveContextBlocks` call. Two consequences, both deliberate: (1) a single call
covers every dynamic parameter a skill needs, so "a single resolver-row failure aborts the whole
call" (item 3's explicit instruction) falls straight out of `ResolveContextBlocks`'s own existing
all-or-nothing contract, with no extra logic needed to enforce it; (2) an unrelated resolver row
the agent happens to have configured for something else entirely (a different skill, or ordinary
boot-time dynamic context) can be broken without ever affecting this skill's resolution — only
failures on rows this skill's own parameters actually name can abort this call.

**Missing-required-parameter handling.** A `Required` parameter with neither a static arg nor a
resolvable dynamic binding (whether because no row exists for its slot, or the row exists but is
disabled — `ListEnabledAgentContextResolvers` only returns `enabled=1` rows, mirroring the
boot-time convention exactly) is collected into a typed `*MissingSkillParameterError` (`Skill` +
sorted, deduplicated `Names []string`), returned once every parameter has been considered so a
caller sees every missing parameter in one pass, not one at a time. This is a plain Go error type
(`errors.As`-compatible), not a panic, and not a silently-empty map entry. An *optional* parameter
left unresolved is omitted from the output map entirely (not set to `""`) so a future Materializer
can tell "resolved to empty" apart from "never supplied."

**A resolver-lookup misconfiguration (a binding is needed but no `AgentContextResolverStore` was
passed at all) surfaces as a plain `fmt.Errorf`, not a `*MissingSkillParameterError`** — this is a
caller-side wiring bug (the equivalent of forgetting to pass a required dependency), not something
about the *invocation's own arguments* being incomplete, so it's deliberately a different error
shape a caller can't accidentally treat as "just fill in a default and retry."

**2. `internal/skill/resolver.go` — `ResolveDependencyAddresses` (nested-skill resolution
building block).** Takes `idx SkillIndexStore` (narrowed to `GetSkillBySlug(slug) (*store.Skill,
error)` — a real `*store.Store` satisfies it directly) and
`declaredDependenciesJSON string` — deliberately the *raw* `store.Skill.DeclaredDependencies`
column value (a JSON array of slugs), not a `skill.Definition`, because task 02's own column is
what a materializer would actually have in hand for an *installed* skill row at resolution time
(a freshly re-parsed `Definition` from the vendored `SKILL.md` is a separate, redundant source of
the same information task 07/08 would otherwise have to reconcile against the index row). For each
declared slug, looks up the corresponding installed `store.Skill` row and returns a
`DependencyAddress{Slug, Skill, Address}` triple (`Address` = that row's own `ContentHash` — the
literal key into task 03's vendored store). Single-level only: no transitive walk into a resolved
dependency's own further dependencies, and no cycle detection — both are explicitly task 07's job
per this task's own item 2 wording and per task 04's Work Log's own coordination note ("task 07's
Installer-pipeline extension... can read this column directly for every already-installed skill to
build its graph"). A declared slug that doesn't resolve to any installed skill is a hard error
(named exactly which slug), matching `ResolveContextBlocks`'s own "name the offending item, don't
silently skip" convention for an unresolvable reference.

**3. `docs/engineering/GLOSSARY.md`.** Added a **"Skill Resolver"** entry immediately after the
existing **"Skill vendor store"** entry (the file's current true end at the time of this task).
Cross-referenced **Skill catalog**, **Skill vendor store**, and the not-yet-landed **Skill
Materializer** term (tasks 07/08) using the same "Team Slot"-style disambiguation pattern already
established elsewhere in the file — explicitly stating what the Skill Resolver is *not* (a second
resolver-provider system; a replacement for `agent_context_resolvers`; a composition/execution
engine; a cycle detector), matching the task's own explicit instruction.

**4. Tests (`internal/skill/resolver_test.go` + `internal/skill/testdata/fixtures/{resolver-parent,resolver-child}/SKILL.md`).**
All new fixtures/tests live inside `internal/skill` itself rather than reusing/extending task 04's
`internal/skillinstall/testdata/fixtures/sample-skill` package — a deliberate deviation from the
Done-means' literal wording ("task 04's test fixture, extended with a second package"), logged
here: `internal/skillinstall` imports `internal/skill` (not the reverse), so a normal, non-test
extension of *this* task's own production code living in `internal/skill` cannot reach into
`internal/skillinstall`'s testdata without creating a test-only dependency on a sibling task's
package. A Go test file in package `skill` importing `internal/skillinstall` would not have created
an actual import cycle (a test binary's own extra imports don't feed back into the production
build graph), but doing so would have meant depending on `skillinstall`'s fuller install-pipeline
machinery (validation rules, rollback semantics) that this task doesn't need and isn't scoped to
exercise, just to reach one fixture directory. Instead, built two small, real, self-contained
fixtures directly under `internal/skill/testdata/fixtures/` (`resolver-parent`, declaring one
required `ResolverSlot`-bound parameter, one optional unbound parameter, and a `dependencies:
[resolver-child]` declaration; `resolver-child`, a minimal dependency-free package) and a small
`installFixture` test helper that runs the real `ParsePackageDir → skillvendor.Store.Write →
store.CreateSkill` sequence directly (mirroring, not importing, `skillinstall.upsertIndex`'s own
`fresh.ID = ""` UUID-minting fix) — a real, non-mocked install path exercising the actual
production `skillvendor.Store` and `store.Store`, just without `skillinstall`'s own state-machine
wrapper. This satisfies the Done-means' actual substance ("nested-dependency address resolution
works against a real installed dependency") without the cross-task testdata coupling the literal
wording would have implied.

Tests, mapped to Done-means:
- `TestResolveSkillParameters_DynamicBindingResolvesEndToEnd` — a real `agent_context_resolvers` row
  (`kind=cmd`, `run="printf hello-from-resolver"`) inserted via `store.InsertAgentContextResolver`
  against a real `store.New`-backed SQLite DB, resolved through the real, unmocked
  `runtimeagent.ResolveContextBlocks` call chain — confirms end-to-end dynamic resolution.
- `TestResolveSkillParameters_StaticArgOverridesDynamicBinding` — same setup, but a static arg is
  also supplied for the same parameter; confirms the static value wins and the resolver row's
  content never appears in the result.
- `TestResolveSkillParameters_MissingRequiredParameterProducesNamedError` and
  `TestResolveSkillParameters_MissingRequiredParameter_UnconfiguredResolverSlot` — a required
  parameter with no static arg and (respectively) no `ResolverSlot` at all, and a `ResolverSlot`
  naming a slot the agent has no row for — both produce a `*MissingSkillParameterError` naming the
  exact parameter, confirmed via `errors.As`, not a panic or an empty map.
- `TestResolveSkillParameters_OptionalUnresolvedParameterIsOmitted` — an optional, unresolved
  parameter is absent from the result map entirely (map-key-existence check), not present as `""`.
- `TestResolveSkillParameters_NeedsBindingButNoResolverStoreConfigured` — a `nil`
  `AgentContextResolverStore` with an actually-needed binding produces a plain configuration error,
  not a `*MissingSkillParameterError` and not a panic.
- `TestResolveSkillParameters_ResolverRowFailureAbortsWholeCall` — a real resolver row that fails
  (`run="exit 3"`) aborts the whole `ResolveSkillParameters` call with the underlying
  `ResolveContextBlocks` error, not a `*MissingSkillParameterError` and not a partial result.
- `TestResolveSkillParameters_NoParametersDeclared` — a skill with no declared parameters returns an
  empty, non-nil map with no resolver/DB interaction.
- `TestResolveDependencyAddresses_RealInstalledDependency` — installs both fixtures for real
  (`ParsePackageDir` → `skillvendor.Store.Write` → `store.CreateSkill`), confirms `resolver-parent`'s
  installed `DeclaredDependencies` is exactly `["resolver-child"]`, resolves it through
  `ResolveDependencyAddresses` against the real `store.Store`, and confirms the resolved
  `Address`/`Skill.ID` match the independently-installed `resolver-child` row's own `ContentHash`/
  `ID` — then independently re-reads the resolved address's bytes via `vendor.ReadFiles` to confirm
  it's a real, live vendored directory, not just an opaque matching string.
- `TestResolveDependencyAddresses_NoDependencies` — both `"[]"` and `""` inputs return `(nil, nil)`.
- `TestResolveDependencyAddresses_UnresolvedDependencyIsAHardError` — a declared slug with no
  matching installed skill is a hard error, not a silent skip.

**Validation.** `go build ./cmd/nanite/` clean. `go vet ./...` — only the two pre-existing,
unrelated `internal/service/container.go` `stopReaper`/`stopRuntimeReaper` findings every prior
task in this batch (`01`–`04`) already confirmed via `git blame` predate this batch; unchanged by
this task. `go test ./internal/skill/... -race -count=1` — all tests pass (including the six
pre-existing `skill` package tests, unmodified). `go test ./...` (full suite) — exit code 0, every
package `ok` or `[no test files]`, including `internal/skill`, `internal/store`,
`internal/runtime/agent`, `internal/skillinstall`, `internal/skillvendor`, `internal/service`,
`internal/api`. Re-ran `go test ./internal/skill/... ./internal/store/... ./internal/runtime/agent/...
./internal/skillinstall/... ./internal/skillvendor/... -race -count=1` specifically (the packages
this task actually touches or calls into) after adding the GLOSSARY.md entry — all pass under the
race detector.

**No live dogfeed / scratch-DB run performed for this task.** This task's own Done-means criteria
(a real `ResolveContextBlocks` call, real precedence behavior, a clear missing-parameter error, and
real nested-dependency resolution against a real installed dependency) are all fully satisfiable —
and were satisfied — through direct, real (non-mocked except where explicitly noted above)
package-level tests against a real `*store.Store` (via `store.New` against a temp SQLite file with
every migration applied) and a real `*skillvendor.Store` (rooted at a `t.TempDir()`). No schema
migration was touched by this task (task 02 already landed `136`/`137`), and no
`AgentConfigService`/`writeManaged`/managed-agent file-write path was exercised, so
`EXECUTION-PROCESS.md`'s backup-copy/scratch-CWD dogfeed discipline for schema or managed-agent-file
verification doesn't apply here. Every test uses `t.TempDir()`-rooted paths exclusively (no
relative, CWD-resolved paths anywhere in the new test file) — confirmed via `git status --short`
after every test run: only the intended new/modified files ever appear, no stray writes.

**Not escalated.** No genuine unknown was hit: the task's own instruction was unambiguous about
what to build, both real coverage gaps flagged in the dispatch note (the `ResolverSlot`/`SlotName`
wording precision, and the import-cycle risk) were checked directly against the live code before
writing anything and resolved without needing to guess, and the one deviation from the Done-means'
literal fixture-reuse wording (building fresh fixtures in `internal/skill` rather than reusing
`internal/skillinstall`'s testdata) is a mechanical scope-boundary choice with a documented
rationale, not an ambiguity about what the task wanted.

## Fix required (fresh reviewer, 2026-08-21 — see `TASKS/ESCALATIONS.md`'s matching entry)

**Overall verdict was PASS** — this is one small, non-blocking finding, not a rejection.

`MissingSkillParameterError.Names`'s doc comment (`internal/skill/resolver.go`) claims it is
"the sorted, **deduplicated** list of missing required parameter names," but
`ResolveSkillParameters`'s implementation only sorts (`sort.Strings(missing)` before returning) —
it never dedupes. Not currently reachable via the one real production path (`internal/skillinstall`'s
`DefaultValidator` already rejects a package with duplicate parameter names before a `Definition`
ever reaches this function), but `ResolveSkillParameters` is an exported function with no such
precondition documented or enforced on its own `Definition` argument, so a caller constructing one
directly (or from some future unvalidated source) could get a `Names` slice with a duplicate entry,
contradicting the doc's own claim.

**What to do:** add a real dedupe step when collecting `missing` in `ResolveSkillParameters` (e.g.
track already-added names via a `map[string]bool` before appending, or dedupe the slice
immediately before `sort.Strings`) — matching this project's stated preference for a real
guarantee over an implicit cross-package invariant. Add a small regression test: a `Definition`
with two `ParameterSpec` entries sharing the same `Required`, unresolvable `Name` should produce
a `MissingSkillParameterError.Names` with exactly one entry, not two. Re-verify
`go build`/`go vet`/`go test ./internal/skill/...` clean.

**2026-08-21 — Fix applied.** Addressed the fresh reviewer's finding directly: `ResolveSkillParameters`'s
missing-parameter collection loop in `internal/skill/resolver.go` now tracks already-added names via a
`seenMissing map[string]bool` before appending to `missing`, so a `Definition` with two (or more)
`ParameterSpec` entries sharing the same `Name` — both `Required` and both unresolvable — produces a
`MissingSkillParameterError.Names` with exactly one entry per distinct name, matching the doc comment's
existing "sorted, deduplicated" claim exactly instead of only half of it. `sort.Strings(missing)` is
unchanged (dedupe happens at collection time, immediately before the name is appended, rather than as a
separate post-pass over the slice — equivalent result, one fewer pass). No other logic in
`ResolveSkillParameters`, `resolveNeededDynamicBindings`, or `ResolveDependencyAddresses` was touched.

Added one regression test, `TestResolveSkillParameters_MissingRequiredParameterNamesAreDeduplicated`
(`internal/skill/resolver_test.go`, placed immediately before the existing
`TestResolveSkillParameters_MissingRequiredParameter_UnconfiguredResolverSlot`): a `Definition` with two
`ParameterSpec` entries both named `"target"`, both `Required: true`, neither bound to a `ResolverSlot`
nor covered by a static arg — asserts the resulting `*MissingSkillParameterError.Names` has exactly one
entry (`"target"`), not two, confirmed via `errors.As`.

Validation: `go build ./cmd/nanite/` clean. `go vet ./...` — same two pre-existing, unrelated
`internal/service/container.go` `stopReaper`/`stopRuntimeReaper` findings the original Work Log already
confirmed via `git blame` predate this task (re-confirmed again here via `git blame -L 1190,1220
internal/service/container.go`; both findings sit in code from commits `76df826a3` (2026-05-11) and
`7a0e37936` (2026-05-19)/`df08da8b5` (2026-08-18), nowhere near `internal/skill`) — unchanged by this fix.
`go test ./internal/skill/... -race -count=1` — all tests pass, including the new regression test and
every pre-existing test in the package. `go test ./...` (full suite) — exit code 0, every package `ok` or
`[no test files]`.

No scope expansion beyond `ResolveSkillParameters`'s missing-collection loop and its one new regression
test, per the fix request's own explicit instruction to keep this contained. Status set back to
`implemented`.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
