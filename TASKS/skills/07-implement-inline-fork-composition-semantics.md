# Implement `inline`/`fork` composition semantics + install-time cycle detection

**Phase:** 4 — Materialization pipeline (`TASKS/skills`)
**Status:** implemented
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

**Pre-flight.** Confirmed `skill.Definition.Context` (`internal/skill/parser.go:29`) is still
present — task `01` did not remove it, only the dead `!`cmd`` execution logic in the now-deleted
`internal/skill/context.go`. Confirmed `internal/skill/resolver.go` (task `06`) has
`ResolveSkillParameters`/`ResolveDependencyAddresses` on `main`, and `internal/skillinstall.Installer`
(task `04`) has `Install`/`DefaultValidator`/`extractDeclaredDependencies` — matched the dispatch
note's description exactly. Read `docs/engineering/architecture/20-skills.md` in full and
`docs/engineering/EXECUTION-PROCESS.md` before writing any code.

**Real subagent/fork entry point, found before writing anything (per the dispatch note's explicit
instruction).** Grepped `internal/chat`/`internal/service`/`internal/selftools` for how
`subagent_spawn` gets dispatched mid-conversation. The real, live, callable-from-within-a-running-
session mechanism is `internal/subagent.Service.Spawn(ctx, SpawnRequest{Mode: ModeSync, ...})`
(`string, error` — a run ID) + `.Status(ctx, runID) (*Run, error)` — the exact same `*subagent.Service`
instance `internal/selftools.SelfToolsTransport.Subagent` calls from `callSpawnSubagent` to serve the
`subagent_spawn` self-tool. `ModeSync`'s `Spawn` already blocks internally (`executeWithSlot`) until
the run is terminal, so by the time `Spawn` returns, `Status` re-reads an already-final row. This *is*
a real, already-live, callable-from-within-materialization entry point — not something needing to be
built — so no escalation was warranted here; `fork` composition rides it directly via a narrow
`SubagentDispatcher{Spawn; Status}` interface (`internal/skill/compose.go`), which the real, concrete
`*subagent.Service` satisfies without any wrapper. Confirmed no import cycle: `internal/subagent`
imports `internal/dispatch`/`internal/messaging`/`internal/safego`/`internal/store` only, none of which
import `internal/skill` — `internal/skill/compose.go` importing `internal/subagent` directly is safe.

**Real Agent-Skills-spec check for a composition-placement convention (per the task's own
instruction not to invent one if the spec already has one).** Fetched
`platform.claude.com/docs/en/agents-and-tools/agent-skills/overview` directly during this task. The
real spec has **no** wire-level convention for embedding one skill's content inside another's body —
its own "compose capabilities" language describes Claude choosing to read multiple *independent*
`SKILL.md` files via bash as separate context loads, never a splicing/insertion mechanism. Confirmed:
there is nothing to defer to here, matching task `04`'s own prior finding for the `dependencies:`
frontmatter key ("no established Agent-Skills-spec convention exists for this key yet"). This task's
own concrete, documented choice (see `composedSectionHeader`'s doc comment in `compose.go`): append
each composed dependency's content after the parent's own body, under a
`## Composed skill: <slug> (<inline|fork>)` heading.

**1. `internal/skill/compose.go` — composition semantics.**
- **Which skill's own `Context` field governs inline vs. fork was the one real design question this
  task had to resolve and the architecture doc doesn't spell out explicitly**: since `Context` is a
  single field on a `SKILL.md`'s own frontmatter (not a per-declared-dependency annotation — no such
  shape exists anywhere in the package format), the only coherent reading is that a dependency's
  *own* `Context` value (re-parsed from its vendored copy at materialization time, matching "the
  vendored store is content... materialization always reads the vendored copy live") decides how
  *it* wants to be composed when someone else includes it — not the parent's `Context` field, which
  only matters for how the parent itself would be composed if some other skill included *it*. This
  is the design compose.go implements throughout; documented in the file's own package doc comment.
- **`MaterializeSkill(ctx, MaterializerDeps, def Definition, input MaterializeInput)
  (*MaterializedSkill, error)`** is the entry point. `MaterializerDeps` reuses `resolver.go`'s own
  `AgentContextResolverStore`/`SkillIndexStore` interfaces as-is (no second store-access shape for
  what task `06` already covers), plus a new `VendorReader` (`ReadFiles(address) (skillvendor.FileMap,
  error)` — a real `*skillvendor.Store` satisfies it directly) and `SubagentDispatcher` (above).
  `MaterializeInput` carries one flat `StaticArgs`/`AgentID`/`Workdir` applied identically at every
  level of the composition (documented as this task's own choice — no established
  per-nested-dependency argument-scoping mechanism exists anywhere in this batch), plus
  `ParentSessionID`/`ParentAgentID`/`AgentProfileID`/`ForkRole`/`ForkTimeoutSeconds` for fork
  composition specifically (only required when a fork dependency is actually encountered).
- **`inline`**: `materializeOne` recursively resolves a dependency's own parameters
  (`ResolveSkillParameters`, task `06`, reused as-is), substitutes them into its body, recurses into
  *its* own declared dependencies the same way, and splices the fully-materialized result into the
  parent's output under the composed-section header. No new execution path, matching the
  architecture doc's own "no new execution path" instruction.
- **`fork`**: the nested dependency's own content is still materialized in-process first (the exact
  same recursive call `inline` uses — both modes produce a nested skill's materialized content the
  same way), then that materialized text becomes a real `subagent.SpawnRequest.Prompt`, dispatched
  via `ModeSync` through the real `SubagentDispatcher`. Only the terminal run's real, persisted
  `Run.ResultJSON` (read back through the public `Status` API, not a private child-session-message-
  scraping heuristic like `internal/selftools`'s own `recoverSyncSummary`) folds back into the
  parent's output — never the raw instructional text, never a full transcript. This is the concrete
  mechanism behind "Real delegation, not text-splicing": inline splices text directly into the
  parent's own context; fork hands that same text to a separate agent turn to act on and folds back
  only what that turn produced.
- **Parameter substitution (`{{name}}`)**: no established Agent-Skills-spec or Nanite convention
  exists for substituting a resolved parameter value into a `SKILL.md` body — this task's own
  provisional choice (documented in `compose.go`'s `paramPlaceholder` doc comment), following the
  same "genuinely unspecified — pick something concrete, document it" precedent task `04` set for
  `dependencies:`. Deliberately **not** fence-aware (unlike task `08`'s planned marker rebuild) since
  this is inert text substitution with no execution/security hazard, not the code-execution class of
  bug task `08` is rebuilt to close — documented as an accepted, narrower-scoped limitation.
- **Provenance**: `ProvenanceChain`/`ProvenanceLink` track the root → nested segment of the
  architecture doc's full chain (the user/agent segment is the caller's own context; the
  script/materializer → capability segment is task `08`/`09`'s). A `CompositionError` attributes any
  materialization failure to the specific skill in the chain whose own processing produced it (via
  `Unwrap`, `errors.As`-compatible) — verified directly by a test asserting a nested child's own
  missing-required-parameter failure is attributed to the *child*, not the parent.
- **Runtime recursion backstop (`maxCompositionDepth = 5`)**: per the architecture doc's own
  instruction, cycle/recursion-limit detection is install-time only, not discovered live during
  materialization — this constant is a defensive backstop only (a bug in the install-time gate, or a
  row edited outside the install pipeline, fails cleanly instead of recursing unboundedly), not the
  primary enforcement. Mirrors `internal/skillinstall.DefaultMaxDependencyDepth`'s value; the two
  packages can't share the literal constant (`skillinstall` depends on `skill`, not the reverse) so
  both are documented to be kept in sync manually.

**2. `internal/skillinstall/dependency_graph.go` — install-time cycle/recursion-limit detection.**
- **`DefaultMaxDependencyDepth = 5`** — this task's own concrete design call, since the architecture
  doc names only the mechanism ("checked once, at install/sync time... rather than discovered live
  during a materialization pass"), not a number. Rationale (in the constant's own doc comment): a
  `SKILL.md` package is meant to be a small, tightly-scoped procedural unit (confirmed against the
  real spec's own "reduce repetition... specialize Claude" framing during this task's spec check,
  above) — a composition chain nested deeper than 5 levels is a decomposition smell, not a
  legitimate deep hierarchy. 5 also bounds how large a single top-level invocation's fork-composition
  subagent fan-out tree can grow, while staying generous enough for a real multi-level composition
  (e.g. a 4-level "release-review → changelog → commit-log → git-log → repo-metadata" chain still
  fits comfortably).
- **`checkDependencyGraph(index IndexStore, slug string, deps []string, maxDepth int) error`** walks
  the graph of already-installed skills' own `DeclaredDependencies` (task `02`'s column) starting
  from the package's own newly-declared deps — exactly the instruction in task `04`'s own Work Log
  ("task 07's Installer-pipeline extension... can read this column directly for every
  already-installed skill to build its graph"). Detects both a cycle back to the slug being installed
  (`*CycleError`, naming the full path) and exceeding `maxDepth` levels even acyclically
  (`*RecursionLimitError`). A declared slug that doesn't resolve to any currently-installed skill is
  a dead end, not an error (that's `ResolveDependencyAddresses`'s job at materialization time, per
  task `06`'s own established behavior) — confirmed this correctly catches the load-bearing,
  order-independent case: installing "a" which declares a dependency on already-installed "b", where
  "b" already (possibly installed before "a" ever existed) declares a dependency back on "a" — the
  walk compares against the plain slug string, not a resolved row, so it doesn't matter that "a"'s
  own row doesn't exist yet at check time. A `visited` set defensively prevents an infinite loop on a
  pre-existing, unrelated cycle elsewhere in the graph (shouldn't be possible going forward given
  this same gate, but tested directly with a 5-second timeout tripwire).
- **Wired into `Installer.Install`** (`install.go`) as a step within the existing `StateValidating`
  phase, immediately after `DefaultValidator.Validate` and before `Vendor.Write` — a rejected package
  leaves zero vendored bytes and zero index rows, matching every other validation failure's
  no-partial-state guarantee. Added `Installer.MaxDependencyDepth int` (0 = use
  `DefaultMaxDependencyDepth`) mirroring the existing `Validate Validator` override-field convention.

**3. `docs/engineering/GLOSSARY.md`.** Re-checked the file's current end immediately before writing
(per the dispatch note's explicit instruction) — confirmed no "Skill Materializer" entry exists yet
on this task's base (task `08` had not landed one as of this check). Added the entry immediately
after **Skill Resolver**, covering both this task's composition half and forward-referencing task
`08`'s script/marker-execution half by name/file (`internal/skill/exec.go`) without describing its
internals, per the task's own "whichever task lands first writes the entry, the second only extends
it if genuinely incomplete" instruction. If task `08` lands its own version of this entry first (or
concurrently) before this task's branch merges, the two entries will need reconciling at merge time —
noted here since I have no visibility into task `08`'s actual landing order from this worktree.

**4. Tests.** `internal/skillinstall/dependency_graph_test.go` — seven direct unit tests against
`checkDependencyGraph` (no dependencies ⇒ zero Index calls; a dangling reference is not an error; a
direct 2-node cycle; a transitive 3-node cycle; exactly-at-the-limit succeeds; one-past-the-limit
fails; a pre-existing unrelated cycle doesn't infinite-loop, verified via a timeout tripwire) plus two
real, non-mocked end-to-end tests through `Installer.Install` itself (`TestInstall_
DependencyCycle_RejectedAtInstallTime`, `TestInstall_RecursionLimitExceeded_RejectedAtInstallTime`) —
both confirm the real pipeline rejects with the right typed error, `StateFailed`, and zero partial
vendored/indexed state, against a real `*skillvendor.Store` + real `*store.Store`.

`internal/skill/compose_test.go` — six tests, all against real installed fixtures (`ParsePackageDir`
→ `skillvendor.Store.Write` → `store.CreateSkill`, reusing `resolver_test.go`'s own `installFixture`/
`newResolverTestStore`/`newResolverTestVendor` helpers, same package): a real inline composition
splices a nested dependency's content and substitutes its `{{target}}` parameter correctly; a missing
required parameter on the *nested* skill produces a `*CompositionError` correctly attributed to the
child, wrapping a `*MissingSkillParameterError`; a real fork composition dispatches through a real
`*subagent.Service` (backed by a `capturingRunner` test double — a real `subagent.Runner`
implementation, not a fake `SubagentDispatcher`, so the actual `Spawn`/`Status`/DB-persistence
machinery runs for real) and folds back only the runner's structured `ResultJSON`, never the fork
child's raw instructional text; fork with no `SubagentDispatcher` configured fails clearly wrapping
the new `ErrForkNotConfigured` sentinel; fork with no `ParentSessionID` fails before ever dispatching;
and a 3-level `root(inline) → mid(fork) → leaf(inline)` chain proves both that `mid`'s own inline
composition of `leaf` happens *before* `mid` is forked (the delegated subagent's captured prompt
contains `leaf`'s content) and that the full `ProvenanceChain` (`root`, `mid(fork)`, `leaf(inline)`)
is reported — the Done-means' explicit "provenance chain... for a real multi-level composed
materialization" requirement.

One test-design correction made mid-work, logged for the record: the first draft of the fork test
used a `capturingRunner` that echoed the input prompt back inside its result JSON, which made the
"fork must not leak raw instructional text" assertion trivially fail — the runner's own result
*was* the raw prompt, so of course it appeared in the output, but that failure said nothing about
`compose.go`'s actual behavior (which correctly only ever splices whatever the runner returns).
Fixed by having `capturingRunner` separately record the received prompt (for asserting the subagent
was dispatched with the right content) while returning a distinct, synthesized result payload (for
asserting only the result — not the raw prompt — folds back).

**Validation.** `go build ./cmd/nanite/` clean. `go vet ./...` — same two pre-existing, unrelated
`internal/service/container.go` `stopReaper`/`stopRuntimeReaper` findings every prior task in this
batch has already confirmed via `git blame` predate this batch (re-confirmed again: commits
`76df826a3` 2026-05-11 and `7a0e37936`/`df08da8b5` 2026-05-19/2026-08-18, nowhere near any file this
task touches). `go test ./internal/skill/... ./internal/skillinstall/... -race -count=1` — all pass
(skill: 99.5s; skillinstall: 67.4s). `go test ./...` (full repo suite) — exit code 0, every package
`ok` or `[no test files]`, including `internal/skill`, `internal/skillinstall`, `internal/skillvendor`,
`internal/subagent`, `internal/selftools`, `internal/service`, `internal/api`.

`git status --short` after all changes: `docs/engineering/GLOSSARY.md` and
`internal/skillinstall/install.go` modified; `internal/skill/compose.go`,
`internal/skill/compose_test.go`, `internal/skillinstall/dependency_graph.go`,
`internal/skillinstall/dependency_graph_test.go`, and the six new
`internal/skill/testdata/fixtures/compose-*` directories added — no other files touched, no stray
writes. No schema migration was needed or touched (cycle/recursion-limit detection is pure Go logic
over task `02`'s already-landed `DeclaredDependencies` column, exactly as the dispatch note
anticipated) — `EXECUTION-PROCESS.md`'s backup-copy/scratch-CWD dogfeed discipline for schema or
managed-agent-file verification doesn't apply here. Every test uses `t.TempDir()`-rooted paths
exclusively; no relative, CWD-resolved paths anywhere in any new test file.

**Not escalated.** No genuine unknown was hit. The two open design questions the task file itself
flagged as real design latitude (the Agent-Skills-spec placement convention, and the
recursion-depth-limit number) were each resolved by checking the real, authoritative source first
(a live fetch of the real spec; the architecture doc's own explicit "pick a concrete number... document
why" instruction) rather than guessed past, and both choices are documented in the code and here for a
future task to adopt, rename, or extend rather than silently re-guessing. The one genuinely open
question the task file raised — whether a real subagent/fork entry point exists at all — resolved to
"yes, it exists and is directly usable" after the required grep, so no escalation was warranted per
the dispatch note's own explicit instruction to only escalate if it turned out not to exist.

**Fix-required work log entry, 2026-08-21 — production-blocking bug fixed.** Implemented all five
numbered items from the "Fix required" section below, in `internal/skill/compose.go` and
`internal/skill/compose_test.go` only (no other files touched).

1. **Corrected `runFork`'s doc comment.** Removed the "ModeSync already blocks Spawn until the run
   is terminal... bar a vanishingly rare race" claim outright — added a "Correction (fresh-reviewer
   fix, 2026-08-21...)" block stating plainly that `internal/subagent/service.go`'s `Spawn`, whenever
   trust doesn't resolve to `TrustTrusted` and the approval gate fires
   (`SubagentApprovalRequired && !DeveloperMode` — the documented **production default**, since
   `SubagentApprovalRequired` defaults to `true` — or `Mode == ModeInteractive`), takes a distinct,
   deterministic control-flow path that returns `StatusRequested` *before* ever reaching `ModeSync`'s
   blocking-exec branch. This is the common case for any non-trusted role in a default deployment,
   not an edge case.
2. **`ForkPendingApprovalError{Slug, RunID, EnvelopeInstanceID, Status}`** — a new typed error
   (not a bare sentinel, since the task's own instruction was "carrying enough identifying
   information... at minimum the run ID," and a sentinel `var` can't carry per-call fields). `runFork`
   now checks `run.Status == subagent.StatusRequested || run.Status == subagent.StatusApproved`
   immediately after the nil-run check and *before* the pre-existing generic
   `!subagent.IsTerminalStatus` branch (those two statuses are themselves non-terminal per
   `subagent.IsTerminalStatus`, so the new check has to come first or it would never fire — the old
   generic branch would swallow it). Doc comment on the new type mirrors
   `subagent/envelope.go`'s `EnvelopeFromRun` "this is NOT a failure... pending-approval IS the
   design" framing, then explicitly contrasts it with composition's own constraint: unlike a chat-turn
   reply, `MaterializeSkill` cannot defer producing content until a human approves later — there is no
   "materialize now, backfill after approval" mechanism anywhere in this pipeline — so the composition
   attempt still fails, but now with a typed, `errors.As`-able reason (`Slug`/`RunID`/
   `EnvelopeInstanceID`/`Status`) instead of the old opaque "did not terminate synchronously" string. A
   future caller (task `11`'s `skill_get` self-tool) can `errors.As` through the wrapping
   `*CompositionError` (its `Unwrap` already supports this — verified directly, no change needed
   there) to build real UX around "this fork composition is waiting on a human."
3. **`forkResultText` partial-capture fix.** Added a `Partial bool \`json:"partial"\`` field to the
   local decode struct and changed the extraction condition to `!payload.Partial && payload.Summary
   != ""` — mirrors `internal/subagent/service.go`'s own `extractLiftableEnvelopes` discriminator
   (`obj.Partial`) for the exact same `{"partial":true,"summary":...,"envelope":{...},"tools":{...}}`
   shape. A partial-capture-shaped `ResultJSON` now falls through to the verbatim-JSON return instead
   of being reduced to just its `summary` string, matching the function's own pre-existing doc-comment
   promise ("folded back verbatim... since 'the result' isn't necessarily prose").
4. **Regression tests added to `internal/skill/compose_test.go`** (six new tests: `gatedSettingsReader`
   / `gatedApprovalEmitter` / `gatedNotCalledRunner` test-double types plus four `Test...` functions):
   - `TestMaterializeSkill_Fork_PendingApproval_ReturnsDistinguishableError` — constructed exactly the
     way the reviewer's own scratch test was and the way `internal/selftools`'s own established
     gated-test pattern does (`self_tools_subagent_envelope_test.go`'s `gatedSettingsReader`/
     `gatedApprovalEmitter`, reproduced here for this package's own DI need): a real
     `*subagent.Service` backed by a real (non-mocked) `idx.DB`, `SubagentApprovalRequired: true`
     settings, and a `gatedNotCalledRunner` that fails the test outright if the runner is ever
     invoked. Asserts the returned error `errors.As`s to `*ForkPendingApprovalError` with a non-empty
     `RunID` and `EnvelopeInstanceID`, `Status == subagent.StatusRequested`, `Slug ==
     "compose-fork-child"`, and that the error message no longer contains the old "did not terminate
     synchronously" phrasing.
   - `TestForkResultText_PartialCaptureShape_FoldsBackVerbatim` — a genuine
     `{"partial":true,"summary":...,"envelope":{...},"tools":{...}}` `ResultJSON` must fold back
     verbatim (envelope/tools fields intact), not just the extracted `summary`.
   - `TestForkResultText_PlainSummaryShape_StillExtractsSummary` — an added companion guard (not
     explicitly requested, but cheap insurance) confirming the fix didn't regress the original,
     non-partial `{"summary":...}` fallback shape's extraction behavior.
   - **Verified both new tests are load-bearing, not vacuous**: temporarily reverted each specific fix
     in isolation (the `StatusRequested`/`StatusApproved` special-case branch, then separately the
     `Partial` discriminator check) via scratch, in-place edits — restored immediately after, no
     `git stash` used at any point, consistent with this batch's own process rule. Confirmed each
     reverted state makes its own new regression test fail with exactly the old, pre-fix symptom
     (`*skill.CompositionError` wrapping the generic "did not terminate synchronously" message for
     item 2; `forkResultText` returning only the truncated summary, dropping `envelope`/`tools`, for
     item 3), then confirmed both pass again once restored.
5. **Re-verification.** `go build ./cmd/nanite/` clean. `go vet ./...` — same two pre-existing,
   unrelated `internal/service/container.go` `stopReaper`/`stopRuntimeReaper` findings every prior
   task in this batch (including this task's own original landing) already confirmed via `git blame`
   predate this batch — untouched by this fix. `go test ./internal/skill/... ./internal/skillinstall/...
   -race -count=1` — all pass (skill: 120.0s including the two new tests; skillinstall: 82.9s,
   unaffected since this fix touched no `skillinstall` file). `go test ./...` (full repo suite) run
   for extra confidence beyond the fix section's own explicit ask — exit code 0, no regressions
   anywhere else in the tree.

`git status --short` after this fix: only `internal/skill/compose.go` (modified) and
`internal/skill/compose_test.go` (modified) — no other files touched, no stray writes, no schema
migration involved.

## Fix required (fresh reviewer, 2026-08-21 — see `TASKS/ESCALATIONS.md`'s matching entry)

**Bug, reproduced directly: `fork` composition is unreachable under this codebase's own documented
production default.** `runFork`'s (`internal/skill/compose.go`) own doc comment claims
`subagent.Service`'s `ModeSync` branch "already blocks `Spawn` until the run is terminal... (bar a
vanishingly rare race)." This is factually wrong, not a rare race: `internal/subagent/service.go`'s
`Spawn`, when trust doesn't resolve to `TrustTrusted` and `SubagentApprovalRequired &&
!DeveloperMode` (the **documented production default** — `SubagentApprovalRequired` defaults to
`true`), inserts the run as `StatusRequested` and returns *before ever reaching* the `ModeSync`
blocking-exec branch — a distinct, deterministic control-flow path, not a race window. `runFork`
has no handling for this: it falls into its generic `!subagent.IsTerminalStatus(run.Status)`
branch and returns an opaque `fmt.Errorf("... did not terminate synchronously ...")`,
indistinguishable from any other internal failure, with no reference to the approval envelope
that was actually emitted and no typed/sentinel error a caller could branch on. Reviewer verified
this empirically (scratch test, removed after): with `SubagentApprovalRequired: true`, the runner
is never invoked and the failure is generic.

**Why this matters:** `fork` composition has a passing test today only because that test uses
`settings: nil` (a documented test-only bypass). Under the real, already-live default
configuration, every `fork`-composed skill materialization would fail on first contact for any
non-`TrustTrusted` role — wired, with a passing test, but not actually reachable in production.
The sibling caller of this exact same `Spawn`/`Status` API
(`internal/selftools.callSpawnSubagent`/`subagent.EnvelopeFromRun`) already has correct, tested
handling for precisely this case — `EnvelopeFromRun`'s own doc comment: *"StatusRequested /
StatusApproved → Success=true... This is NOT a failure — the spawn is gated on human approval...
Pending-approval IS the design (SubagentApprovalRequired defaults true in production)."*

**What to do:**

1. Correct `runFork`'s doc comment — remove the incorrect "vanishingly rare race" framing; state
   plainly that an approval-gated spawn returning `StatusRequested`/`StatusApproved` without
   executing is a real, common, by-design outcome under the production default, not an edge case.
2. Special-case `run.Status == subagent.StatusRequested` or `subagent.StatusApproved` in `runFork`
   as a distinguishable outcome from a genuine failure — mirroring `EnvelopeFromRun`'s own "this is
   NOT a failure" framing, adapted to composition's own constraint (materialization needs real
   content to splice/fold back *now*; it can't wait for an asynchronous human approval the way a
   chat-turn reply can). Concretely: introduce a typed error or sentinel (e.g. a
   `ForkPendingApprovalError{RunID string, EnvelopeInstanceID string}` a caller can `errors.As`
   against, or an exported `ErrForkPendingApproval` sentinel similar to the existing
   `ErrForkNotConfigured`) carrying enough identifying information (at minimum the run ID) that a
   future caller (e.g. task `11`'s `skill_get` self-tool) can build real UX around "this fork
   composition is waiting on a human" rather than treating it as an opaque internal error. This
   composition attempt still does not produce materialized content in this case — the point is
   making the *reason* attributable and actionable, not making pending-approval silently succeed
   with placeholder text.
3. Fix the secondary, non-blocking finding in the same function: `forkResultText`'s doc comment
   claims a genuinely structured `ResultJSON` is "folded back verbatim," but its actual
   discriminator (any JSON with a non-empty top-level `summary` string) also matches
   `internal/subagent`'s own documented **partial-capture** shape
   (`{"partial":true,"summary":...,"envelope":{...},"tools":{...}}`, see `service.go`'s
   `extractLiftableEnvelopes`) — a subagent run cut mid-task but with real captured state would
   have its `envelope`/`tools` fields silently dropped, keeping only the truncated summary. Check
   for the partial-capture shape's own discriminator field (`"partial"`) before falling back to
   the plain-summary extraction, so a genuinely structured partial result folds back verbatim as
   the doc comment already claims it should.
4. Add regression tests: (a) a fork composition against a run that lands in `StatusRequested`
   (construct the test the same way the reviewer's scratch test did — a real `subagent.Service`
   with `SubagentApprovalRequired: true` settings, mirroring `internal/selftools`'s own established
   gated-test pattern) must surface the new distinguishable error/sentinel, not the old generic
   message; (b) `forkResultText` against a genuine partial-capture-shaped `ResultJSON`
   (`{"partial":true,"summary":"...","envelope":{...}}`) must return the full JSON verbatim, not
   just the extracted `summary` string.
5. Re-verify `go build`/`go vet`/`go test ./internal/skill/... ./internal/skillinstall/... -race
   -count=1` clean when done.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
