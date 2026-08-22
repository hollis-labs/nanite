# Decide the fate of reasoning-augmented tool selection (`RankTools`/`SelectWithSignals`/`SelectToolsAugmented`)

**Phase:** Wave 4 — Production islands (per remediation guide §4)
**Status:** not-started
**Depends on:** none within this batch — see this folder's `README.md` for a
non-blocking cross-reference note relating this task to
`04-tool-builder-yaml-architecture.md` and `06-curated-tool-knowledge-matcher.md`.
**Touches:** `internal/toolclient/ranking.go`, `internal/toolclient/broker.go`
(`SelectToolsAugmented`, `SelectToolsAsProvider`, `SetMemoryRecaller`,
`SetSkills`), `internal/service/container.go` (lines 692-710, the per-boot
wiring cost this finding says is still paid regardless of disposition).

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
> - **Blocks:** `11/12` (shares `internal/toolclient/broker.go`)
> - **Parallel-safe with:** `09/01`–`09/04`, `09/06`
> - **Gated on:** AD-10 (wire / defer / retire)
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

### Findings addressed

- **GO-MCPTOOL-002** (medium, confidence high) — Reasoning-augmented tool
  selection (`RankTools`/`SelectWithSignals`/`SelectToolsAugmented` in
  `internal/toolclient`) is dead in the production request path — its former
  consumer (a debug SQL row) was removed by an earlier task
  (`TASKS/phase-0/23-export-and-drop-decision-tables.md`), per
  `ranking.go`'s own comment. The real production selection entry point
  (`SelectToolsAsProvider`) does not call this path at all. Per-boot wiring
  cost is still paid regardless (skills-dir load, memory-recaller
  construction feed only this dead path).

Source: `docs/audits/2026-08-21-go-quality/REPORT.md` §8.7 and
`docs/audits/2026-08-21-go-quality/findings.json`.

### Root cause

`ranking.go`'s own comment documents exactly how this became dead — not
speculative infrastructure that was never finished, but a real feature whose
one production consumer was deliberately removed by a later, unrelated task:

```go
// internal/toolclient/ranking.go:292-297
// Diagnostic signals JSON, returned to the caller alongside the
// selection. Compact key names so the payload stays readable wherever
// it's logged/displayed. (Its former consumer — the broker_decisions
// SQL debug row — was removed by
// TASKS/phase-0/23-export-and-drop-decision-tables.md, which leaves
// SelectWithSignals/SelectToolsAugmented without a production caller
// today; test-only reachable. Flagged as a candidate for a future
// cleanup pass, out of scope for that task.)
```

`TASKS/phase-0/23-export-and-drop-decision-tables.md` removed a debug-table
row that consumed this selection path's diagnostic signals output — once
that consumer was gone, `SelectWithSignals`/`SelectToolsAugmented` had
nothing left calling them in production, and (per the comment's own
admission) that task explicitly flagged the resulting dead-code gap as
future cleanup rather than resolving it inline, which is why this finding
exists today.

### Current behavior

**The dead algorithm and its wrapper:**

```go
// internal/toolclient/ranking.go:64
func RankTools(s RankingSignals) []ScoredTool {
```

```go
// internal/toolclient/ranking.go:189
func (tb *ToolClient) SelectWithSignals(
    ...
) {
    ...
    keywordTools := tb.SelectByIntent(intent, MaxSelectedTools)   // line 206
    scored := RankTools(RankingSignals{                            // line 207
        ...
        MemoryHits:   memHits,
    })
    ...
}
```

```go
// internal/toolclient/broker.go:170-193
// SelectToolsAugmented is the reasoning-augmented selection entry point used
// by the service layer. It wraps SelectWithSignals: gathers the per-call
// memory hits (via the attached MemoryRecaller, if any), and passes the
// attached skills + Config-driven err-toward-more pad through.
func (tb *ToolClient) SelectToolsAugmented(...) (...) {
    var memHits []ToolPatternHit
    if tb.memoryRecaller != nil {
        hits, err := tb.memoryRecaller.RecallToolPatterns(ctx, intent)
        ...
    }
    ...
    return tb.SelectWithSignals(ctx, intent, hints, workspaceID, agentID, windowSize, tb.skills, memHits, pad)
}
```

**The real production selection entry point does not call any of this:**

```go
// internal/toolclient/broker.go:416
func (tb *ToolClient) SelectToolsAsProvider(ctx context.Context, intent string, hints []string, workspaceID, agentID string) (*SelectResult, error) {
    tools, _, err := tb.selectToolsUncapped(ctx, intent, hints, workspaceID, agentID)
    ...
}
```

`SelectToolsAsProvider` calls `selectToolsUncapped`, not
`SelectToolsAugmented`/`SelectWithSignals`/`RankTools` — confirmed by
reading `SelectToolsAsProvider` in full; it goes on to enumerate builtins and
catalog tools directly with no call into the ranking path anywhere in its
body. Grep for `SelectToolsAugmented(`, `SelectWithSignals(`, and
`RankTools(` outside test files turns up only their own definitions and each
other (`SelectToolsAugmented` calling `SelectWithSignals` calling
`RankTools`) — a closed, self-contained chain with no external entry point.

**The per-boot cost that's still paid regardless:**

```go
// internal/service/container.go:692-710
// Phase 5 / D3 (CW-20260419-0011): wire the reasoning-augmented broker
// signals onto the toolclient. Both are nil-safe — when memorySvc is
// nil or the skills directory is missing, the broker behaves exactly
// as before (keyword + token budget). Wiring at this seam keeps the
// toolclient package independent of memory + filesystem details.
if cfg.ToolClient != nil {
    if memorySvc != nil {
        cfg.ToolClient.SetMemoryRecaller(toolclient.NewMemoryRecaller(memorySvc))
    }
    skillsDir := cfg.ToolClient.Config.SkillsDir
    if skillsDir == "" {
        skillsDir = toolclient.DefaultSkillsPath()
    }
    if skillsDir != "" && skillsDir != "off" {
        if loaded, err := toolclient.LoadSkillsFromDir(skillsDir); err == nil && len(loaded) > 0 {
            cfg.ToolClient.SetSkills(loaded)
        }
        ...
    }
}
```

`SetMemoryRecaller` and `SetSkills` are real, executed-at-boot calls — a
memory-service round-trip construction and a skills-directory filesystem
read happen on every `nanite serve` startup. But `tb.memoryRecaller` and
`tb.skills` are read in exactly one place each in non-test code:
`SelectToolsAugmented` (`broker.go:178`, `broker.go:193`) — the dead path.
Confirmed by grep restricted to `internal/toolclient/*.go` excluding tests:
no other production code reads either field. This means the per-boot cost at
`container.go:692-710` is currently pure overhead — real I/O paid on every
boot, feeding a mechanism nothing in the production request path ever
invokes.

**Why this matters for the base report's own complexity findings:** per the
audit, this explains 2 of the base mechanical report's pre-flagged,
previously-unjudged complexity outliers — `RankTools` (cognitive 34) and
`SelectWithSignals` (cognitive 25) — both real, coherent algorithms that
would otherwise have been judged "essential complexity" without anyone
noticing they're unreachable. This is worth keeping in mind when weighing
the wire/defer/retire tradeoffs below: the complexity itself isn't the
problem, unreachability is.

### Desired invariant

Same disposition-clarity invariant as the other islands in this folder. One
addition specific to this island: whichever option is chosen should resolve
the per-boot I/O question explicitly — leaving `SetMemoryRecaller`/`SetSkills`
executing at boot while feeding nothing is not a neutral "wait and see" state
under either "defer" or "retire," since it's real, currently-wasted cost, not
dormant code.

### Scope

- `internal/toolclient/ranking.go` — `RankTools` (line 64),
  `RankingSignals`, `ScoredTool`, `signalsJSON` (referenced near line 297).
- `internal/toolclient/broker.go` — `SelectWithSignals` (line 189),
  `SelectToolsAugmented` (line 170), `SetMemoryRecaller` (line 148),
  `SetSkills` (line 139), `MemoryRecaller`/`Skills` accessors (lines
  157-160), and — for contrast/non-goal clarity — `SelectToolsAsProvider`
  (line 416), the live entry point this task does not modify unless "wire"
  is chosen.
- `internal/service/container.go:692-710` — the per-boot wiring block, in
  scope for removal (retire) or explicit justification (defer/wire).

### All production callers

None for `RankTools`, `SelectWithSignals`, or `SelectToolsAugmented`.
Confirmed by grep restricted to non-test files.

## What to do

1. **Re-verify against current source that this is still unreachable.** Per
   the remediation guide's Wave 4 warning — *"check current source first;
   some were completed after the audited commit"* (audited commit
   `8feeee5c`) — before treating anything above as current:
   - Grep for `SelectToolsAugmented(`, `SelectWithSignals(`, and
     `RankTools(` across `internal/` and `cmd/` excluding
     `internal/toolclient/`'s own files and tests; confirm still zero
     production callers.
   - Re-read `SelectToolsAsProvider` (`broker.go:416`) in full and confirm
     it still does not call into the ranking chain.
   - Re-read `internal/service/container.go:692-710` and confirm
     `SetMemoryRecaller`/`SetSkills` are still the only production writers
     of the fields the dead path reads.
   - If any of this has changed, correct this task's disposition and note
     it in Work log before proceeding.

2. **If still unreachable, present the wire/defer/retire options below to the
   architect.** Do not pick one.

   **Option — wire.** Make `SelectToolsAsProvider` (or whichever selection
   entry point the architect judges correct) call into
   `SelectToolsAugmented`/`SelectWithSignals` instead of, or alongside,
   `selectToolsUncapped`'s current keyword-only path — reviving
   reasoning-augmented selection (memory-hit signals + skills-based bias +
   `RankTools`' scoring) as the live production tool-selection mechanism.
   - Tradeoff: this reactivates real, working infrastructure that's already
     paying its construction cost at boot (`SetMemoryRecaller`/`SetSkills`)
     for no current benefit — wiring it in would make that cost start paying
     off rather than being pure waste.
   - Tradeoff: `SelectToolsAsProvider`'s current keyword-based path
     (`selectToolsUncapped` → `SelectByIntent`) is the thing every existing
     production tool-selection behavior has been validated against — swapping
     in reasoning-augmented selection changes real, observable agent-facing
     behavior (which tools get offered to an agent for a given intent), not
     just internal plumbing. This needs behavioral validation against real
     usage patterns, not just a compile-and-test-pass check, since the old
     debug-row consumer this path was built for is long gone and nobody has
     evaluated its selection quality against current tool catalogs/skills in
     practice.

   **Option — defer.** Leave `RankTools`/`SelectWithSignals`/`SelectToolsAugmented`
   in place, unreachable, but resolve the wasted per-boot cost explicitly —
   per the guide's requirement that a deferred island **must not look
   production-live in docs and should not impose unnecessary boot/runtime
   cost**, this is the one island in this folder where "boot/runtime cost"
   isn't hypothetical, it's measured, real, currently-paid cost with zero
   payoff:
   - Either gate `SetMemoryRecaller`/`SetSkills`'s calls in
     `container.go:692-710` behind an explicit "reasoning-augmented selection
     is enabled" condition that defaults off (so a deferred feature stops
     paying its construction cost until actually wired), or, if the
     memory-recaller/skills-loading machinery has some other latent
     production value independent of this specific dead consumer (check
     before assuming it doesn't — `tb.Skills()`/`tb.MemoryRecaller()` are
     public accessors; confirm nothing else could plausibly read them), leave
     the wiring but document explicitly in `container.go`'s own comment that
     its current sole consumer is dead and the I/O is presently wasted.
   - Record a trigger/owner for a future "wire" reconsideration.

   **Option — retire.** Remove `RankTools`, `SelectWithSignals`,
   `SelectToolsAugmented`, and their now-unnecessary supporting machinery —
   the finding's own recommendation explicitly names this as saving the
   real per-boot I/O cost (`SetMemoryRecaller`'s memory-service round-trip,
   `SetSkills`'s skills-directory filesystem read) as a concrete argument in
   this option's favor, not a hypothetical one. If `SetMemoryRecaller`/
   `SetSkills` have no other production purpose once this path is gone,
   remove `container.go:692-710`'s wiring block too, eliminating the boot
   cost entirely rather than leaving orphaned setters nothing calls.
   - Tradeoff: removes a real, currently-paid boot-time cost (memory-service
     round-trip + filesystem directory read on every `nanite serve` startup)
     for genuinely zero current production benefit — this is a stronger,
     more concrete "retire saves real resources" case than any other island
     in this folder, worth stating plainly since it's a fact, not a
     recommendation.
   - Tradeoff: same "well-built work goes away" cost as the other islands —
     `RankTools`/`SelectWithSignals` are, per the audit's own complexity
     review, genuinely coherent algorithms, not sloppy code; retiring them
     discards real design/implementation effort if reasoning-augmented
     selection was always intended to come back once a new consumer
     materialized.

## Non-goals

- This task does not evaluate or modify `SelectToolsAsProvider`'s current
  keyword-based selection logic (`selectToolsUncapped`/`SelectByIntent`)
  beyond what "wire" would require — it's the live, working production path
  and stays as-is under "defer" or "retire."
- This task does not resolve whether `internal/toolclient/tool_knowledge.go`
  (a related but separately-flagged dead tool-matching mechanism, see
  `06-curated-tool-knowledge-matcher.md`) should be merged with, replace, or
  stay independent of this island's disposition — see this folder's
  `README.md` cross-reference note; the two are related but not sequenced
  against each other by this pass.

## Tests required

- If **wire**: an integration/regression test on the real
  `SelectToolsAsProvider` (or whatever entry point is chosen) production path
  asserting reasoning-augmented selection actually influences the returned
  tool set for at least one realistic case (a memory hit biasing selection,
  a skill-based preference changing ranking) — not just a unit test on
  `RankTools` in isolation, which already exists and already passes despite
  the mechanism being unreachable in practice.
- If **defer**: if the per-boot wiring is gated behind a new
  enabled/disabled condition, a test confirming the gate actually prevents
  the I/O when off (e.g. `SetMemoryRecaller`/`SetSkills` not called, or
  called but demonstrably cheap/no-op) — this is the regression test that
  proves the "should not impose unnecessary boot/runtime cost" requirement
  is actually met, not just claimed.
- If **retire**: confirm `deadcode -test ./internal/toolclient/...` shows no
  remaining symbol depends on the removed functions; confirm
  `container.go`'s boot sequence still passes its existing startup tests
  with the wiring block removed.

## Prevention

- Per the guide's own "Remediation includes prevention" principle: this
  finding is a direct case of a consumer being removed
  (`TASKS/phase-0/23-export-and-drop-decision-tables.md`) without a
  corresponding check for now-orphaned producers feeding it. Consider
  whether a future task that removes a consumer of a shared library/broker
  function should be required to check (and note in its own Work log)
  whether that removal orphans the producer side, rather than leaving it as
  a "flagged, out of scope" note the way `TASKS/phase-0/23...md` did here —
  a candidate rule for `12-quality-ratchet-and-standards/`.

## Done means

- [ ] Current-source reachability re-verified (grep across
      `RankTools`/`SelectWithSignals`/`SelectToolsAugmented` and a direct
      read of `SelectToolsAsProvider`) and confirmed still open, or
      disposition corrected if it's changed since the audit.
- [ ] Architect decision recorded: wire, defer, or retire.
- [ ] The per-boot I/O cost question (`SetMemoryRecaller`/`SetSkills` at
      `container.go:692-710`) is explicitly resolved under whichever option
      is chosen — not left as ongoing wasted cost with no documented reason.
- [ ] If **wire**: real production entry point calls into the
      reasoning-augmented path; behavioral validation performed (not just a
      passing test suite) before considering this "wired" in the full sense
      of the production-reachability proof chain.
- [ ] If **defer**: boot-cost question resolved (gated off, or documented
      as retained for another reason); trigger/owner recorded.
- [ ] If **retire**: `RankTools`, `SelectWithSignals`, `SelectToolsAugmented`
      and their now-unnecessary supporting code removed; `container.go`'s
      wiring block removed or justified independently; `deadcode -test
      ./internal/toolclient/...` confirms no orphaned dependents.
- [ ] `go build ./...` and `go test ./...` pass after whichever direction is
      implemented.

## Work log

<Worker fills this in.>

## Review notes

<Reviewer fills this in.>
