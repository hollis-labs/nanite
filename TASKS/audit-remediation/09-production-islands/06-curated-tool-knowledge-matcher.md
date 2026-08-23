# Decide the fate of the curated tool-knowledge intent matcher (`tool_knowledge.go`)

**Phase:** Wave 4 — Production islands (per remediation guide §4)
**Status:** implemented
**Depends on:** none within this batch — see this folder's `README.md` for a
non-blocking cross-reference note relating this task to
`04-tool-builder-yaml-architecture.md` and `05-reasoning-augmented-tool-selection.md`.
**Touches:** `internal/toolclient/tool_knowledge.go`,
`internal/toolclient/tool_knowledge_test.go`.

```yaml
requires_architect_decision: true
requires_security_review: false
requires_regression_test: false
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 4 — production islands · **Dispatch unit:** `W4`
> - **Depends on:** `00/01`'s reachability report
> - **Blocks:** none
> - **Parallel-safe with:** `09/01`–`09/05` — self-contained (`tool_knowledge.go` + its test)
> - **Gated on:** AD-11 (wire / defer / retire). Note this finding is **not** flagged `requires_architect_decision` in `findings.json` — see the batch README's correction 4.
> - **requires_security_review:** false · **requires_regression_test:** true

> ## ✅ AD-11 DECIDED (2026-08-22) — RETIRE
>
> Delete `internal/toolclient/tool_knowledge.go` and its test (405 / 162).
>
> Decided jointly with **AD-10** — see that task for the shared reasoning. This
> is the third parallel intent-matching mechanism; the live `intent.go` keyword
> scorer remains and becomes the only one.
>
> Self-contained: confirm zero references outside the file and its test before
> deleting.

## Context

### Findings addressed

- **GO-MCPTOOL-003** (low, confidence high) — `tool_knowledge.go`'s entire
  curated-catalog intent-matching mechanism (405 lines) has zero callers —
  the **third** parallel "what tools match this intent" mechanism found in
  the audit, alongside the live `intent.go` keyword scorer and the dead
  `RankTools` (`05-reasoning-augmented-tool-selection.md`'s finding,
  GO-MCPTOOL-002).

Source: `docs/audits/2026-08-21-go-quality/REPORT.md` §8.7 and
`docs/audits/2026-08-21-go-quality/findings.json`. Note: `findings.json`
records `requires_architect_decision: false` for this specific finding —
one of two islands in this folder where that field is `false` (the other is
`GO-MEM-002`, `02-hadron-context-gate.md`; the remaining four —
`GO-MEM-001`, `GO-SVCEXEC-003`, `GO-MCPTOOL-001`, `GO-MCPTOOL-002` — are all
`true`). This finding's own recommendation is a plain "confirm dead, then
remove," the most directive of any of the six. This task file nonetheless
follows this folder's uniform wire/defer/retire framing per this batch's
operator instruction and the remediation guide's Wave 4 treatment of all six
seed candidates as a single decision table — but an architect working
through this queue should weight that `findings.json` signal: of the six
islands here, this is the one the audit itself leans hardest toward a
specific disposition on.

### The three-way "what tools match this intent" relationship

This audit's §8.7 cluster review found **five independent tool-classification/
selection mechanisms** across `internal/tool`/`internal/toolclient`, of
which only two are live:

- **Live:** `stash.BuiltinCategorizer` (bucketing) and
  `toolclient.SelectByIntent` (`internal/toolclient/intent.go:19`,
  keyword-scoring selection — the mechanism `SelectToolsAsProvider`, the real
  production entry point, actually uses via `selectToolsUncapped`).
- **Dead:** `register.go`'s hardcoded category maps (part of
  `04-tool-builder-yaml-architecture.md`'s finding), `RankTools`
  (`05-reasoning-augmented-tool-selection.md`'s finding), and this task's
  `tool_knowledge.go`.

This task's finding is specifically the third of those three dead
mechanisms, and specifically the one built around a **curated, hand-authored
catalog** rather than either a keyword scorer (`SelectByIntent`,
live) or a signals-based ranker (`RankTools`, dead). The audit's synthesis
paragraph calls the coexistence of 3 dead, textually-independent
classification schemes alongside the 2 live ones "real drift risk" — not
because any of the 5 currently conflict (they don't; only 2 are reachable),
but because a future engineer extending tool selection has no single
obvious place to look and could plausibly build a *sixth* mechanism without
realizing 3 already exist unused.

### Root cause

Straightforward: `tool_knowledge.go` implements a self-contained,
curated-catalog-driven intent matcher, and nothing in the production
codebase ever calls into it. Unlike GO-MCPTOOL-002
(`05-reasoning-augmented-tool-selection.md`), there's no comment or task
reference documenting *why* it became unreachable (no known former consumer
that was later removed) — the evidence available is simply "zero callers,"
which is consistent with either "built speculatively and never adopted" or
"an even earlier consumer than the ranking path's, removed further back with
no comment trail." This task's re-verification step should check git
history/blame if a definitive origin story matters to the architect's
decision, though the audit's own recommendation ("confirm dead, then
remove") suggests it may not be load-bearing either way.

### Current behavior

```go
// internal/toolclient/tool_knowledge.go — 405 lines total
func (tk *ToolKnowledge) ForIntent(intent string) []ToolEntry {     // line 26
func entryMatchesKeywords(entry ToolEntry, keywords []string) bool { // line 51
func (tk *ToolKnowledge) Summary() string {                          // line 67
func containsStr(slice []string, s string) bool {                    // line 108
func DefaultToolKnowledge() *ToolKnowledge {                         // line 119
```

Grep for `tool_knowledge.`, `ForIntent(`, `DefaultToolKnowledge(`, and
`CuratedCatalog` across `internal/` excluding `tool_knowledge.go` and
`tool_knowledge_test.go` themselves returns nothing — zero references in any
other file, production or test. Contrast this with `intent.go`'s
`SelectByIntent`, which has two live call sites
(`internal/toolclient/ranking.go:206` and
`internal/toolclient/meta_tools.go:71`) despite `ranking.go`'s own
consuming function being unreachable — `SelectByIntent` itself is separately
and independently live via `meta_tools.go`.

**Test investment, for scale comparison against the other five islands:**
`tool_knowledge_test.go` exists but is the thinnest test investment of any
island in this folder — a handful of tests scoped entirely to this file's
own package, no integration-level coverage, consistent with the audit's
characterization of "zero test investment beyond its own package."

### Desired invariant

Same disposition-clarity invariant as the other islands in this folder — see
`01-grounding-memory-recall.md`.

### Scope

- `internal/toolclient/tool_knowledge.go` — the full 405-line file:
  `ToolKnowledge`, `ToolEntry`, `ForIntent` (line 26),
  `entryMatchesKeywords` (line 51), `Summary` (line 67), `containsStr` (line
  108), `DefaultToolKnowledge` (line 119).
- `internal/toolclient/tool_knowledge_test.go` — its test file.

### All production callers

None. Confirmed by grep restricted to non-test files outside the two files
above.

## What to do

1. **Re-verify against current source that this is still unreachable.** Per
   the remediation guide's Wave 4 warning — *"check current source first;
   some were completed after the audited commit"* (audited commit
   `8feeee5c`) — before treating anything above as current:
   - Run `deadcode -test ./internal/toolclient/...` and confirm
     `ForIntent`/`DefaultToolKnowledge`/`Summary` are still flagged (or, if
     `deadcode`'s enclosing-type heuristic doesn't drill into these — check
     directly, per the same caveat noted in `03-team-semantic-routing.md`)
     via direct grep for `ForIntent(`, `DefaultToolKnowledge(`.
   - Confirm no new call site has been added in `internal/toolclient/broker.go`,
     `ranking.go`, `intent.go`, or `meta_tools.go` since the audit.
   - If this has changed, correct this task's disposition and note it in
     Work log before proceeding.

2. **If still unreachable, present the wire/defer/retire options below to the
   architect.** Do not pick one, notwithstanding `findings.json`'s own lean
   noted above — the operator instruction for this task-creation pass is
   uniform across all six islands, and the final call stays the architect's.

   **Option — wire.** Make some real selection path (either the live
   `SelectByIntent`, or a revived `RankTools` per
   `05-reasoning-augmented-tool-selection.md` if that's separately chosen,
   or a new call site) consult `ToolKnowledge.ForIntent` as an additional
   signal — e.g. boosting or including curated-catalog matches alongside
   keyword-scored ones.
   - Tradeoff: a hand-curated catalog can encode tool-selection knowledge a
     purely mechanical keyword/signals scorer can't (e.g. "these three tools
     are commonly used together for this class of task" or similar editorial
     judgment) — if that kind of curation was the actual intent, this is
     real, usable infrastructure for it.
   - Tradeoff: this is the least-evidenced "worth reviving" case of the six
     islands in this folder — no design doc, no task reference, no comment
     explaining intended integration, and the audit's own confidence lean is
     toward removal rather than revival. Wiring this in means the architect
     (or whoever executes "wire") has to reconstruct the intended design
     from the code alone, with real risk of building an integration the
     original author never actually planned.
   - This option also interacts with `04-tool-builder-yaml-architecture.md`
     and `05-reasoning-augmented-tool-selection.md`'s own wire options — see
     this folder's `README.md` cross-reference note. A "wire" call here made
     independently of those two risks creating a *fourth* live selection
     mechanism rather than consolidating the existing five down to a
     coherent number.

   **Option — defer.** Leave the file in place, unreachable, with no
   immediate action. Per the guide's requirement that a deferred island
   **must not look production-live in docs and should not impose
   unnecessary boot/runtime cost**:
   - No boot/runtime cost is paid today (`ToolKnowledge`/`DefaultToolKnowledge`
     are never constructed by production code) — confirm this stays true.
   - Given the thin evidence for intended future use (no comment trail, no
     design doc reference, no task citation — contrast every other island in
     this folder, each of which has at least one of those), "defer" here is
     harder to justify with a concrete trigger/owner than for the other five
     islands. If chosen anyway, state explicitly what would need to be true
     for a future "wire" reconsideration to make sense, since nothing in the
     current record answers that.

   **Option — retire.** Remove `tool_knowledge.go` and
   `tool_knowledge_test.go` in full.
   - Tradeoff: given zero test investment beyond its own package, the
     smallest LOC of the six islands, and no documented intended-integration
     trail, this is likely the simplest, lowest-risk "retire" candidate in
     this folder if the architect wants a quick decision to clear from the
     queue — stated here as an observation the evidence supports, not a
     recommendation this task file is making on its own authority.
   - Tradeoff: same generic "discards built work" cost as any retire
     option, though smaller in absolute terms here (405 LOC vs. up to ~800+
     for the larger islands) than for `03`/`04`/`05`.

## Non-goals

- This task does not resolve `04-tool-builder-yaml-architecture.md`'s or
  `05-reasoning-augmented-tool-selection.md`'s own dispositions — the
  three-way relationship is context for the architect's decision here, not a
  mandate to resolve all three together. See this folder's `README.md`.
- This task does not evaluate whether `intent.go`'s live `SelectByIntent`
  keyword scorer should itself be replaced or improved — it's healthy,
  live, and out of scope.

## Tests required

- If **wire**: a test proving `ForIntent`'s output actually reaches the
  chosen real selection entry point and observably affects the returned tool
  set — not just a standalone unit test on `ToolKnowledge` in isolation,
  which already exists.
- If **defer**: none required; confirm the existing thin test file still
  passes.
- If **retire**: confirm `deadcode -test ./internal/toolclient/...` shows no
  remaining symbol depends on the removed file.

## Done means

- [x] Current-source reachability re-verified (deadcode + grep) and
      confirmed still open, or disposition corrected if it's changed since
      the audit.
- [x] Architect decision recorded: wire, defer, or retire.
- [ ] If **wire**: a real production call site consults `ToolKnowledge`, and
      its interaction (or lack thereof) with `04`/`05`'s own dispositions in
      this folder is noted so the resulting selection landscape doesn't grow
      to a sixth or seventh independent mechanism unintentionally.
- [ ] If **defer**: trigger/owner stated explicitly, acknowledging the
      thinner evidentiary basis for deferral versus the folder's other
      islands, per the note above.
- [x] If **retire**: `tool_knowledge.go` and `tool_knowledge_test.go`
      removed; `deadcode -test ./internal/toolclient/...` confirms no
      orphaned dependents.
- [x] `go build ./...` and `go test ./...` pass after whichever direction is
      implemented.

## Work log

- 2026-08-23: Re-verified current-source reachability before editing. `deadcode
  -test ./internal/toolclient/...` did not flag the curated matcher because its
  package-local tests exercised it, so the required direct checks were used:
  a Go-source-only grep outside `tool_knowledge.go` and
  `tool_knowledge_test.go` found no `ToolKnowledge`, `DefaultToolKnowledge`,
  `CuratedCatalog`, or curated `ForIntent` caller. Targeted checks of
  `broker.go`, `ranking.go`, `intent.go`, and `meta_tools.go` likewise found no
  new caller. `deadcode ./...` independently reported
  `ToolKnowledge.ForIntent`, `entryMatchesKeywords`, `ToolKnowledge.Summary`,
  `containsStr`, and `DefaultToolKnowledge` as unreachable. The live
  `intent.go` `SelectByIntent` path remained present through `meta_tools.go`
  and was not changed.
- 2026-08-23: Implemented decided AD-11 **retire** by deleting exactly
  `internal/toolclient/tool_knowledge.go` (405 production lines) and
  `internal/toolclient/tool_knowledge_test.go` (162 test lines). No `09/04` or
  `09/05` files or dispositions were changed.
- 2026-08-23: Post-deletion verification passed. `deadcode -test
  ./internal/toolclient/...` reported only pre-existing symbols in other
  `toolclient` files and no orphaned dependent of the removed matcher;
  `go test ./internal/toolclient/...`, `go build ./...`, `go vet ./...`, and
  `go test ./...` all passed.

## Review notes

<Reviewer fills this in.>
