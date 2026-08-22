# Decide the fate of `contextbroker`'s Hadron blueprint context gate (fix required before any "wire")

**Phase:** Wave 4 — Production islands (per remediation guide §4)
**Status:** not-started
**Depends on:** none within this batch.
**Touches:** `internal/contextbroker/gate_hadron_blueprints.go`,
`internal/contextbroker/gate_hadron_blueprints_test.go`,
`internal/service/container.go` (the composition root's `sources` slice,
lines 793-821, where the 4 live `ContextSource`s are registered).

```yaml
requires_architect_decision: true
requires_security_review: false
requires_regression_test: true
```

## Context

### Findings addressed

- **GO-MEM-002** (low as confirmed-dead / would-be-medium if revived
  unfixed, confidence high) — `contextbroker/gate_hadron_blueprints.go`'s
  entire `ContextGate`/`HadronBlueprintGate` (302 LOC + 337 test LOC) is
  confirmed entirely dead — never registered as a `ContextSource` in the
  composition root, unlike its 4 live siblings. Contains a **latent
  unbounded-relevance-score bug**: `calculateRelevance` additively sums up
  to 6 independent bonuses with no clamp, against a documented 0.0–1.0
  contract that the broker's global cross-source sort relies on — a
  revived-but-unfixed version could silently out-rank every correctly-bounded
  item from live sources.

Source: `docs/audits/2026-08-21-go-quality/REPORT.md` §8.11 and
`docs/audits/2026-08-21-go-quality/findings.json`. Note:
`findings.json` records `requires_architect_decision: false` for this
specific finding — one of two islands in this folder where that field is
`false` (the other is `GO-MCPTOOL-003`,
`06-curated-tool-knowledge-matcher.md`; the remaining four are all `true`).
This task file's own frontmatter above nonetheless sets it `true`, since
this folder's operator instruction treats all six islands uniformly as
needing an explicit wire/defer/retire call, and this finding specifically
carries the unclamped-relevance-score bug as a real, independent correctness
concern regardless of how the dead-code disposition question resolves —
worth an architect's attention even where the underlying finding record
leans toward a directive "remove" recommendation.

### Root cause

Same shape as GO-MEM-001 (see `01-grounding-memory-recall.md`): a complete,
well-tested unit was built but never given its one composition-root
registration step. `internal/service/container.go:793-821` builds the
`contextbroker` source list explicitly:

```go
// internal/service/container.go:793-821
var sources []contextbroker.ContextSource
...
sources = append(sources, contextbroker.NewMemorySource(memorySvc))
...
sources = append(sources, contextbroker.NewConduitSource(cfg.MCP))
sources = append(sources, contextbroker.NewPCCSource(".nanite/pcc/global"))
sources = append(sources, contextbroker.NewSessionSource(func(sessionID string, limit int) ([]contextbroker.MessageSummary, error) {
    ...
}))
...
broker := contextbroker.New(contextbroker.DefaultBudget(), sources...)
```

`contextbroker.NewHadronBlueprintGate` (`gate_hadron_blueprints.go:45`) is
never called here or anywhere else in production — its only call sites are
in `gate_hadron_blueprints_test.go` (lines 35, 42, 67, 109, 130, 164, 201,
239, 290). Unlike GO-MEM-001, though, this island also carries a **real
correctness bug** independent of the wiring question — see below.

### Current behavior

**The dead registration point** — `internal/service/container.go`'s 4-source
list (`NewMemorySource`, `NewConduitSource`, `NewPCCSource`,
`NewSessionSource`) has no fifth entry for `NewHadronBlueprintGate`.

**The unclamped relevance-score bug** — `calculateRelevance`
(`gate_hadron_blueprints.go:183-230`) sums independent bonuses with no upper
clamp:

```go
// internal/contextbroker/gate_hadron_blueprints.go:183-230 (bonus sites)
// line 197: relevance += 0.6
// line 203: relevance += 0.8
// line 208: relevance += 0.4
// line 216: relevance += 0.3
// line 221:     relevance += 0.2   (nested)
// line 229: relevance += 0.3
```

Read together, up to 6 bonuses can fire for a single blueprint on a single
call (`calculateRelevance` is invoked per-blueprint at `gate_hadron_blueprints.go:166`).
Even a partial combination of these literal values already exceeds `1.0`
(e.g. `0.6 + 0.8 = 1.4`) with no `min(relevance, 1.0)` or equivalent clamp
anywhere in the function. Every one of the 4 live `ContextSource`
implementations produces a `Relevance` value the broker's global
cross-source sort treats as bounded to `[0.0, 1.0]` — a revived
`HadronBlueprintGate` feeding unbounded values into that same sort would
silently out-rank every correctly-bounded item from the 4 live sources
whenever its bonuses summed past 1.0, corrupting cross-source ranking
without raising any error.

### Desired invariant

Same disposition-clarity invariant as the other islands in this folder (see
`01-grounding-memory-recall.md`'s "Desired invariant" for the general shape),
**plus** a hard precondition specific to this island:

**If "wire" is the architect's decision, `calculateRelevance` must be fixed
to respect the documented 0.0–1.0 `Relevance` contract before the gate is
registered as a live `ContextSource` — not after.** This precondition
applies only to "wire." It does not apply to "defer" (the bug can stay
latent in unreached code, since nothing invokes it) or "retire" (the bug is
removed along with the rest of the file). Stating this explicitly per this
task's own instruction: **do not let a future implementer wire this gate
into the live broker without first fixing the unclamped scoring** — doing so
would introduce a real correctness regression into a currently-healthy
cross-source ranking system.

### Scope

- `internal/contextbroker/gate_hadron_blueprints.go` — `ContextGate`,
  `HadronBlueprintGate`, `NewHadronBlueprintGate` (line 45),
  `calculateRelevance` (lines 183-230, the bug site), and the call site at
  line 166.
- `internal/contextbroker/gate_hadron_blueprints_test.go` — the existing 337
  LOC test file; if "wire" is chosen, this needs new test cases asserting
  the relevance clamp specifically (see "Tests required" below), not just
  reactivation of the existing suite.
- `internal/service/container.go:793-821` — the `sources` slice, if "wire"
  is chosen.
- The broker's global cross-source sort (`internal/contextbroker/broker.go`
  — read, not necessarily modified, to confirm the exact contract
  `calculateRelevance` needs to honor and how it currently degrades or
  doesn't when fed an out-of-range value from a *different* source, as a
  sanity check on the "corrupts ranking" claim before treating it as settled).

### All production callers

None. `NewHadronBlueprintGate` has zero production call sites — confirmed by
grep restricted to non-test files.

## What to do

1. **Re-verify against current source that this is still unreachable.** Per
   the remediation guide's Wave 4 warning — *"check current source first;
   some were completed after the audited commit"* (audited commit
   `8feeee5c`) — before treating anything below as current:
   - Run `deadcode -test ./internal/contextbroker/...` and confirm
     `HadronBlueprintGate`/`NewHadronBlueprintGate` are still flagged.
   - Re-read `internal/service/container.go`'s `sources` construction block
     directly and confirm no fifth `append(sources, contextbroker.NewHadronBlueprintGate(...))`
     has been added since the audit.
   - Re-read `calculateRelevance` in full and confirm the unclamped-sum
     shape is unchanged — if a clamp has since been added, the "wire"
     precondition below is already satisfied and should be noted as such
     rather than re-litigated.
   - If either has changed, correct this task's disposition and note the
     correction in Work log before proceeding.

2. **If still unreachable, present the wire/defer/retire options below to the
   architect.** Do not pick one.

   **Option — wire (precondition: fix `calculateRelevance`'s clamp first).**
   Add a bound (e.g. `relevance = math.Min(relevance, 1.0)` at the end of
   `calculateRelevance`, or clamp each bonus's contribution as it's added) so
   the function's output genuinely respects the documented 0.0–1.0 contract
   under every combination of the up-to-6 bonuses, then register
   `contextbroker.NewHadronBlueprintGate(...)` as a fifth entry in
   `internal/service/container.go`'s `sources` slice alongside the 4 live
   sources.
   - Tradeoff: Hadron blueprint context is real, purpose-built retrieval
     logic (302 LOC, not a stub) — if blueprint-derived context is something
     the product actually wants surfaced to agents, this is a complete
     implementation modulo the one bug, the cheapest of the "genuinely
     activate this" paths among the six islands.
   - Tradeoff: this island has had zero production exposure since it was
     built, meaning its retrieval quality/relevance-scoring judgment (even
     once bounded to a valid range) has never been validated against real
     usage patterns — "the bug is fixed" is a necessary but not sufficient
     condition for "the ranking behavior it produces is actually good,"
     and that's a separate, unaddressed question this task does not resolve.
   - This option requires an `MCPCaller` dependency (`NewHadronBlueprintGate(mcp MCPCaller)`)
     to be available and correctly configured at the container-construction
     call site — confirm what `MCPCaller` implementation, if any, is already
     wired for other purposes in `container.go` before assuming this is a
     drop-in one-line addition.

   **Option — defer.** Leave the file in place but explicitly documented as
   inactive. Per the guide's requirement that a deferred island **must not
   look production-live in docs and should not impose unnecessary boot/runtime
   cost**:
   - The unclamped-relevance bug should still be **flagged in a doc comment
     on `calculateRelevance` itself** even under "defer," specifically so a
     future engineer who stumbles onto this file and considers a quick,
     ad-hoc activation (bypassing this task's own review) sees the warning
     in the one place they're guaranteed to look. This is a deliberate
     exception to "defer" otherwise meaning "leave alone" — the bug is
     latent risk sitting in the tree regardless of activation status, and a
     silent latent bug is worse than a documented one.
   - Confirm the type's package-level doc (if any) doesn't imply it's part
     of the active `ContextSource` set.
   - No boot/runtime cost is currently paid (never constructed) — confirm
     this stays true, same as the other islands.

   **Option — retire.** Remove `gate_hadron_blueprints.go` and its test file.
   - Tradeoff: removes the one island in this folder carrying an
     independently real correctness bug — retiring it is the only option
     that resolves GO-MEM-002's correctness half unconditionally, without
     depending on a future implementer correctly applying the wire
     precondition.
   - Tradeoff: same "well-built work goes away" cost as the other islands'
     retire option, plus specifically here: if blueprint-derived context
     retrieval is a real, wanted future capability, retiring loses a
     302-LOC head start on it, not just boilerplate.

## Non-goals

- This task does not evaluate whether Hadron blueprint context retrieval is
  a *good idea* from a product/UX standpoint — that's implicit in the
  architect's wire/defer/retire call, not a separate analysis this task
  performs.
- This task does not audit the other 4 live `ContextSource` implementations
  for the same unclamped-relevance risk — the audit's cluster review
  (`GO-MEM-004`, informational, in the same REPORT.md §8.11 section) already
  notes each live source computes `Relevance` via its own unrelated method
  with "no shared contract beyond 'roughly 0-1,'" and flags that as a
  separate, lower-priority documentation/enforcement gap outside this
  folder's scope.

## Tests required

- If **wire** is chosen: a new unit test on `calculateRelevance` (or an
  equivalent exported entry point) that constructs a blueprint hitting all 6
  bonus conditions simultaneously and asserts the returned relevance is
  clamped to `<= 1.0` — this is the regression test that proves the
  precondition was actually satisfied, not just claimed. Also: an
  integration-level test constructing the broker with all 5 sources
  (4 live + Hadron) and asserting cross-source sort order is sane for a
  case where Hadron and a live source compete for the same rank position.
- If **defer**: no new test required, but confirm the existing 337-line test
  file still passes as-is (it exercises the gate in isolation, not via the
  broker) so it doesn't silently bit-rot while deferred.
- If **retire**: confirm `deadcode -test ./internal/contextbroker/...`
  shows no other symbol in the package became newly unreachable as a side
  effect of removing this file (i.e., nothing else in the package depended
  on code local to `gate_hadron_blueprints.go`).

## Done means

- [ ] Current-source reachability and bug-presence re-verified against
      current source, disposition corrected if either has changed since the
      audit.
- [ ] Architect decision recorded: wire, defer, or retire.
- [ ] If **wire**: `calculateRelevance`'s unclamped-sum bug fixed and covered
      by a dedicated regression test *before* the gate is registered in
      `container.go`'s `sources` slice; cross-source sort behavior verified
      with all 5 sources present.
- [ ] If **defer**: the unclamped-relevance risk documented directly on
      `calculateRelevance` regardless of deferred status; confirmed no
      boot/runtime cost is paid.
- [ ] If **retire**: file and test file removed; `deadcode -test ./internal/contextbroker/...`
      confirms no orphaned dependents.
- [ ] `go build ./...` and `go test ./...` pass after whichever direction is
      implemented.

## Work log

<Worker fills this in.>

## Review notes

<Reviewer fills this in.>
