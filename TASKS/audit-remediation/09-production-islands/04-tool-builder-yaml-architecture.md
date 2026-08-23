# Decide the fate of `internal/tool`'s canonical Tool-interface/builder architecture

**Phase:** Wave 4 — Production islands (per remediation guide §4)
**Status:** reviewed
**Depends on:** none within this batch — see this folder's `README.md` for a
non-blocking cross-reference note relating this task to `05-reasoning-augmented-tool-selection.md`
and `06-curated-tool-knowledge-matcher.md`.
**Touches:** `internal/tool/tool.go`, `internal/tool/builder.go`,
`internal/tool/register.go`, `internal/tool/adapt.go`,
`internal/tool/yaml_loader.go` (all dead); `internal/tool/cache.go`
(the package's one live export — do not touch, see Non-goals);
`internal/tool/doc.go` (fresh package documentation);
`internal/tool/stash/categories.go` (detach live categorizer from retired types);
`internal/mcp/manager.go` (or wherever `mcp.Manager`'s live dispatch actually
lives — confirm exact file per "What to do" before assuming) if "wire" is
chosen; `internal/service/tool_concurrency_classification.go` (stale comment
reference only).

```yaml
requires_architect_decision: true
requires_security_review: false
requires_regression_test: true
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 4 — production islands · **Dispatch unit:** `W4`
> - **Depends on:** `00/01`'s reachability report
> - **Blocks:** `11/05` (shares `internal/mcp/manager.go`), `13/01` — **this task's outcome changes `13/01`'s scope**: retired files become dead-code removal, wired files must not be touched
> - **Parallel-safe with:** `09/01`, `09/02`, `09/03`, `09/05`, `09/06`
> - **Gated on:** AD-09 (wire / defer / retire)
> - **requires_security_review:** false · **requires_regression_test:** true

> ## ✅ AD-09 DECIDED (2026-08-22) — RETIRE, PRESERVE `cache.go`
>
> Delete `adapt.go`, `builder.go`, `register.go`, `tool.go`, `yaml_loader.go`
> and their test files. **Keep `cache.go`.** The package survives as a
> result-cache package.
>
> **Verified live surface** (do not re-derive from the audit's prose, which is
> looser): exactly three symbols are used outside `internal/tool` —
> `NewResultCache`, `ResultCache`, `ResultCacheConfig` — by
> `internal/service/chat.go` and `internal/service/container.go`. Nothing else
> escapes the package.
>
> **⚠ `tool.go` holds the package doc that falsely calls this "the primary way
> to construct tools in Go code."** Do not carry it over to `cache.go`. Write a
> new package doc describing what the package actually becomes. Carrying the
> old text forward would preserve the exact misstatement this finding is about.
>
> Largest deletion in the batch (~1.8k prod / ~1.7k test). `13/01`'s dead-code
> list shrinks — re-derive it after this lands.

## Context

### Findings addressed

- **GO-MCPTOOL-001** (medium, confidence high) — `internal/tool`'s canonical
  `Tool`-interface/builder architecture (`tool.go`, `builder.go`,
  `register.go`, `adapt.go`, `yaml_loader.go`) is entirely dead — every
  exported symbol deadcode-flagged; only `ResultCache` (a different, live
  part of the same package) is unaffected. `tool.go`'s package doc still
  actively claims this is "the primary way to construct tools in Go code" —
  false of the current runtime, where the real live mechanism is
  `mcp.Manager`'s registry + per-transport dispatch switch, an entirely
  different design.

Source: `docs/audits/2026-08-21-go-quality/REPORT.md` §8.7 and
`docs/audits/2026-08-21-go-quality/findings.json`.

### This is not a fresh discovery either

A prior task's own comment already reached the "not wired into the live
runtime" conclusion and left it in place, rather than acting on it:

```go
// internal/service/tool_concurrency_classification.go:36-40
//	        internal/tool/adapt.go's WithConcurrencySafeFunc *can* express
//	        per-input safety, but that mechanism is not wired into the live
//	        runtime (WrapExistingTools/WrapProviderDef have no production
//	        caller as of this task; see the Work Log), so it is not treated
//	        as a competing source of truth here.
```

That comment was written by a different, unrelated task working on tool
concurrency classification — it needed to decide whether to trust
`adapt.go`'s per-input safety mechanism as a source of truth, checked, found
it unwired, and correctly declined to build on dead code rather than fixing
the dead code itself (reasonably out of scope for that task). This audit is
the first to treat the unwired state as its own finding rather than an
aside.

### Root cause

The package doc for `internal/tool` actively misdescribes the current
architecture:

```go
// internal/tool/tool.go:1-7
// Package tool defines the canonical tool interface for Nanite.
//
// All tools — builtin, MCP-bridged, YAML-defined, plugin-provided —
// implement the Tool interface. The builder pattern (NewTool + ToolOption)
// is the primary way to construct tools in Go code. YAML-defined tools
// are loaded separately via the yaml_loader.
package tool
```

This describes an architecture where `tool.Tool` is the universal interface
every tool implements, constructed via `NewTool`/`ToolOption` in
`builder.go`, with `register.go` cataloging them and `yaml_loader.go`
handling the YAML-defined subset. None of that is what actually happens at
runtime. The real live mechanism — confirmed by this audit's explicit check
for duplicate tool-routing, which found none — is `mcp.Manager`'s registry
plus a per-transport dispatch switch, reached uniformly through
`mcp.Manager.ExecuteTool`. `internal/tool`'s builder architecture and
`mcp.Manager`'s registry are two structurally different designs for the same
underlying problem ("what tools exist and how do we call them"), and only
one of them is connected to anything.

### Current behavior

- `internal/tool/tool.go` (82 lines) — the `Tool` interface, category/source
  constants, package doc claiming the architecture below is "the primary
  way." No production code implements or type-asserts `tool.Tool`.
- `internal/tool/builder.go` (194 lines) — `NewTool`, `ToolOption`
  functional-options builder. Zero production callers.
- `internal/tool/register.go` (83 lines) — a tool-category registry.
  Confirmed elsewhere in the same audit section (§8.7's "Duplication
  synthesis" paragraph) as containing "already stale" hardcoded tool lists —
  "reference tools since removed elsewhere" — direct evidence this isn't
  just unwired, it's drifted out of sync with the tools that actually exist,
  which would itself need fixing before any "wire" option could reuse it
  as-is.
- `internal/tool/adapt.go` (129 lines) — `WithConcurrencySafeFunc`,
  `WrapExistingTools`, `WrapProviderDef`. Confirmed zero production callers
  independently by the concurrency-classification task's own comment above.
- `internal/tool/yaml_loader.go` (309 lines) — YAML-defined tool loading.
  Zero production callers.
- `internal/tool/cache.go` — **not** part of this finding; `ResultCache`
  is the package's one live, correctly-used export (backing the S4a
  tool-result-cache/pointer mechanism described in this project's own
  `CLAUDE.md`). Do not conflate its health with the rest of the package's
  deadness — see Non-goals.

Total dead surface: `82 + 194 + 83 + 129 + 309 = 797` lines across the five
files, all `deadcode`-flagged.

### Desired invariant

Same disposition-clarity invariant as the other islands in this folder. One
addition specific to this island: regardless of which option is chosen, the
package doc at `tool.go:1-7` must stop making a false claim about current
architecture — even "defer" requires correcting "is the primary way" to
something accurate (e.g. "is a planned future direction, not yet wired" or
similar), since a stale, confidently-worded package doc actively misleads a
future engineer more than an empty package would.

### Scope

- `internal/tool/tool.go`, `builder.go`, `register.go`, `adapt.go`,
  `yaml_loader.go` — the dead architecture in full.
- `internal/tool/cache.go` — explicitly out of scope for
  removal/rework; read-only reference point establishing what stays.
- `internal/mcp` (`Manager`, `ExecuteTool`, the per-transport dispatch
  switch) — the live counterpart architecture; needs to be read and
  understood in full before scoping any "wire" integration, since "wire"
  here means connecting two structurally different designs, not populating
  two struct fields (contrast with `01`/`02` in this folder).
- `internal/service/tool_concurrency_classification.go` — read for context
  on `adapt.go`'s `WithConcurrencySafeFunc`; not modified by this task
  unless "wire" is chosen and concurrency classification is judged to
  actually depend on the outcome (unlikely — that task already built its own
  independent classification table specifically because this mechanism
  wasn't available; check before assuming a dependency exists).

### All production callers

None for any of the five dead files. Confirmed by `deadcode` (per the audit)
and independently corroborated by the concurrency-classification task's own
comment for `adapt.go` specifically.

## What to do

1. **Re-verify against current source that this is still unreachable.** Per
   the remediation guide's Wave 4 warning — *"check current source first;
   some were completed after the audited commit"* (audited commit
   `8feeee5c`) — before treating anything above as current:
   - Run `deadcode -test ./internal/tool/...` and confirm the same five
     files' exported symbols are still flagged (and that `ResultCache`
     remains the one exception).
   - Grep for `tool.NewTool(`, `tool.WrapExistingTools(`, `tool.WrapProviderDef(`,
     and `yaml_loader.` usage across `internal/` and `cmd/` (excluding this
     package's own files and tests) to confirm zero production callers.
   - Re-read `tool.go`'s package doc and confirm the "primary way to
     construct tools" claim hasn't been corrected or made accurate by
     intervening work.
   - If any of this has changed, correct this task's disposition and note
     it in Work log before proceeding.

2. **If still unreachable, present the wire/defer/retire options below to the
   architect.** Do not pick one. **Note the "wire" option here is a
   materially bigger lift than the other five islands in this folder** — it
   is not a matter of assigning two struct fields at a known composition
   point; it requires designing and building the actual integration between
   two independently-complete tool-definition architectures.

   **Option — wire.** Build the actual integration between `internal/tool`'s
   `Tool` interface/builder pattern and `mcp.Manager`'s live registry +
   dispatch switch, so tools defined via `tool.NewTool`/`ToolOption` (in Go)
   or via `yaml_loader.go` (YAML-defined) become real, callable tools through
   the same production path builtin/MCP/plugin tools already use.
   - This requires, at minimum: (a) deciding whether `tool.Tool` becomes a
     genuine common interface `mcp.Manager` dispatches through, or whether
     `internal/tool`'s constructs get adapted into whatever shape
     `mcp.Manager`'s registry already expects (an adapter layer, not a
     shared interface) — a real architecture decision, not an implementation
     detail; (b) fixing `register.go`'s already-stale hardcoded tool
     category lists before relying on them for anything live; (c) deciding
     whether YAML-defined tools (`yaml_loader.go`) are still a wanted
     capability at all, given the runtime has apparently gotten this far
     without them — check whether any current tool actually needs to be
     YAML-authored rather than Go-authored or MCP-provided, or whether this
     was speculative infrastructure for a use case that never materialized.
   - Tradeoff: if YAML-defined or programmatically-built (non-MCP,
     non-plugin) Go tools are a real, wanted authoring path, this is
     legitimate missing infrastructure worth completing — the interface
     design itself was reviewed as reasonable in isolation by the audit
     (it wasn't flagged as poorly designed, only as unwired).
   - Tradeoff: this is real, non-trivial design and implementation work —
     unlike `01`/`02` in this folder, there is no existing call site already
     written and waiting for a `nil` check to become non-nil. Before
     committing to "wire," the architect should weigh whether the actual
     underlying need (a third, Go/YAML-native way to define tools alongside
     builtin-Go and MCP) is still real, given `mcp.Manager`'s registry +
     dispatch switch has evidently served every current production tool
     without it.

   **Option — defer.** Leave the code in place, but fix the package doc's
   false claim immediately regardless of deferred status (see "Desired
   invariant" above — this is not optional under "defer" the way it might be
   for a less actively-misleading doc comment). Per the guide's requirement
   that a deferred island **must not look production-live in docs and should
   not impose unnecessary boot/runtime cost**:
   - Rewrite `tool.go:1-7`'s doc comment to state accurately that this is a
     planned-but-unwired alternative construction path, not "the primary
     way" — this single sentence is the most actively misleading claim found
     across all six islands in this folder and should be corrected whether
     or not anything else about this task proceeds.
   - No boot/runtime cost is paid today (none of these five files run
     init-time logic or are constructed by anything live) — confirm this
     stays true.
   - Record a trigger/owner for when "wire" would actually get picked up.

   **Option — retire.** Remove all five dead files
   (`tool.go`, `builder.go`, `register.go`, `adapt.go`, `yaml_loader.go`),
   leaving `result_cache.go` and any other genuinely-used package members
   intact. Run `deadcode -test ./internal/tool/...` afterward to confirm the
   package's remaining surface (`ResultCache` and whatever it depends on) is
   still fully live with nothing orphaned by the removal.
   - Tradeoff: removes 797 lines of misleadingly-documented, entirely-dead
     architecture with a stale, drifted registry (`register.go`'s tool lists
     already reference removed tools) — the "already decaying, not just
     unused" evidence is stronger here than for any other island in this
     folder, which weakens the "well-built, worth reviving" case relative to
     `01`/`02`/`03`.
   - Tradeoff: if there's a real unmet need for Go/YAML-native tool
     authoring outside `mcp.Manager`'s model, retiring removes a real design
     effort's starting point, and any future rebuild starts from scratch
     rather than fixing up what exists.

## Non-goals

- **`internal/tool/cache.go` and `ResultCache` are not part of this
  finding and must not be touched, removed, or refactored as a side effect
  of whichever option is chosen** — they're the package's one live,
  correctly-used export, backing the S4a tool-result-cache mechanism
  documented in this project's own `CLAUDE.md`. Any "retire" implementation
  must leave this file and its dependents fully intact and passing.
- This task does not evaluate or redesign `mcp.Manager`'s own architecture —
  it's reviewed and confirmed healthy elsewhere in the same audit section
  (single live dispatch switch per logical server, no duplicate routing
  found). Any "wire" work integrates with it as it exists, not as a
  co-redesign.
- This task does not resolve `internal/tool/stash`/`internal/tool/intent`
  (the audit notes these subpackages are live, cohesive, and correctly
  separated from the dead parent-package machinery) — out of scope, already
  healthy.

## Tests required

- If **wire**: integration tests proving a tool defined via
  `tool.NewTool`/`ToolOption` (and, if YAML authoring is retained, via
  `yaml_loader.go`) is actually discoverable and callable through
  `mcp.Manager.ExecuteTool` in the real production dispatch path — not just
  unit tests on the `internal/tool` package in isolation, which is exactly
  the kind of test that already exists today and already passes despite the
  code being completely unreachable in practice.
- If **defer**: no new test required. Confirm existing package tests still
  pass (they test the builder pattern in isolation, which remains valid
  regardless of wiring status).
- If **retire**: confirm `deadcode -test ./internal/tool/...` shows no
  remaining package member depends on removed code, and that
  `internal/service/tool_concurrency_classification.go`'s comment (which
  references `adapt.go`) is updated to stop citing a file that no longer
  exists.

## Prevention

- **Semantic Duplication** (remediation guide's own named standard, §4 Wave
  7): *"Duplicating syntax is a maintainability concern. Duplicating a
  semantic rule is a correctness concern."* Two structurally different "how
  do tools get defined and dispatched" architectures existing side by side —
  one live, one dead but still documented as canonical — is exactly the
  condition this standard exists to catch. If "wire" is chosen, the
  resulting single architecture should make a future duplicate mechanism
  structurally harder to introduce accidentally (e.g. one clearly-documented
  entry point for "how do I add a new tool," not two).
- Package docs claiming architectural primacy (`"is the primary way to..."`)
  are a durable, high-trust surface a future engineer reads before code —
  consider whether a lint/review checklist item for "does this package doc's
  claim match a `deadcode` check" belongs in `12-quality-ratchet-and-standards/`.

## Done means

- [x] Current-source reachability re-verified (deadcode + grep across all
      five files) and confirmed still open, or disposition corrected if it's
      changed since the audit.
- [x] Architect decision recorded: wire, defer, or retire.
- [x] `tool.go`'s package doc no longer claims the builder pattern is "the
      primary way to construct tools in Go code" unless "wire" has made that
      claim true — corrected regardless of which option is chosen.
- [ ] If **wire**: real integration with `mcp.Manager`'s live dispatch
      exists and is proven by an integration test through the actual
      production tool-call path; `register.go`'s stale tool-category lists
      fixed before being relied upon; a decision recorded on whether YAML
      authoring is retained.
- [ ] If **defer**: trigger/owner recorded; confirmed no boot/runtime cost
      is paid; package doc corrected as above.
- [x] If **retire**: `tool.go`, `builder.go`, `register.go`, `adapt.go`,
      `yaml_loader.go` removed; `cache.go` and its dependents
      confirmed untouched and passing; `tool_concurrency_classification.go`'s
      comment updated to stop referencing removed files.
- [x] `go build ./...` and `go test ./...` pass after whichever direction is
      implemented.

## Work log

- 2026-08-23 — Re-verified AD-09 against current source before editing.
  `deadcode -test ./internal/tool/...` did not reproduce the audit's exact
  root-package symbol list because the island's own tests exercised those
  exports; it reported only unrelated `intent`/`stash` symbols. Direct caller
  searches across `internal/` and `cmd/` nevertheless confirmed zero
  production calls to `NewTool`, `WrapExistingTools`, `WrapProviderDef`,
  `WithConcurrencySafeFunc`, or the YAML loader, and `tool.go` still carried
  the false "primary way to construct tools" package claim. The AD-09 retire
  disposition therefore remained current.
- The task's outside-package inventory was incomplete: live
  `internal/tool/stash/categories.go` imported the retiring root `Tool`
  interface and `Category*` constants, and `stash.BuiltinCategorizer` is
  constructed at `internal/service/container.go`'s production composition
  root. Preserved the live stash package and behavior, removed only its dead
  `registryView`/`RegistryCategorizer` attachment to the retired architecture,
  and moved the category values it needs to private stash-local constants.
  Added `categories_test.go` to cover builtin, MCP-prefixed, `nanite_`-heuristic,
  and unknown-tool categorization. This is the only scope correction from the
  planned five-file retirement.
- Implemented AD-09 RETIRE: deleted `adapt.go`, `builder.go`, `register.go`,
  `tool.go`, `yaml_loader.go` and their three test files. Left `cache.go` and
  `cache_test.go` unchanged. Added a fresh `doc.go` package comment describing
  oversized-result caching and pointer retrieval; none of the retired builder
  architecture's package-doc language was carried forward.
- Removed the stale `adapt.go`/`WithConcurrencySafeFunc` discussion from
  `internal/service/tool_concurrency_classification.go` while retaining its
  conservative static-classification rationale.
- Verification passed: `go test ./internal/tool/...`; focused stash/service
  categorizer tests; `go build ./cmd/nanite/`; `go build ./...`; `go vet ./...`;
  `go test ./...`; and `deadcode -test ./...`. The final deadcode output
  contains no symbol from the retired root-tool architecture; its remaining
  findings are pre-existing, out-of-scope items (including
  `stash.ComposeCategorizers`). No escalation.
- 2026-08-23 review-fix worker pass — Updated three comments that survived the
  retirement but still described the removed root-tool adapter/registry
  architecture. `GetToolMeta` now describes its actual suffix/substring
  checks, `Categorizer` names `BuiltinCategorizer` as the production
  implementation, and `ComposeCategorizers` describes generic ordered
  fallback composition. Runtime behavior is unchanged. Focused stash and
  service metadata tests passed, as did `gofmt` and `git diff --check`.

## Review notes

- Fresh-review failure dispatched for correction: three comments still pointed
  readers toward the retired adapter/registry architecture in
  `internal/service/tool.go`, `internal/tool/stash/stash.go`, and
  `internal/tool/stash/categories.go`.
- Review-fix worker updated all three comments to describe current behavior
  only. Independent re-review remains pending.
- Fresh re-review (2026-08-23): **PASS**. The reviewer confirmed the retired
  root-tool files remain absent, `cache.go`/`cache_test.go` are byte-unchanged,
  live `BuiltinCategorizer` behavior and wiring are preserved, and all stale
  adapter/registry references are gone. No findings remain.
