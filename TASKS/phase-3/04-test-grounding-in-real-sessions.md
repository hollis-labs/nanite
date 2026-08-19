# Test grounding in real sessions

**Phase:** 3
**Status:** not-started
**Depends on:** none
**Touches:** `cmd/nanite/main.go` (`SelfToolsTransport.GroundingRecaller`/`GroundingLogger` wiring — currently absent, see Context, must be fixed before any testing is meaningful), `internal/grounding/*` (no further code changes expected beyond the wiring fix unless testing surfaces a second real bug — this is otherwise an evaluation task, not a build task), `internal/mcp/self_tools_dispatch.go` (`callExecuteTask` — where grounding is invoked today, labeled "E2," running before the "E1" reflex-match step), `NANITE_GROUNDING_ENABLED` env var / eventual `memory.grounding.enabled` settings flag.

## Context

Architecture doc `03-steering.md`: *"Grounding (`internal/grounding`, memory-recall-informed strategy signal) — fully built, disabled by default, never exercised. Worth testing in real sessions as a possible complement to reflexes before deciding to integrate, re-architect, or cut."* Decision log §10: *"the operator wasn't aware this had been built — genuinely fresh evaluation, not a known-and-neglected feature."*

### What it does today, verified against the code

Pre-strategy-loop memory recall (CW-20260419-0028 Phase 5/E2). Before dispatch classification runs, `internal/grounding` queries Vanta (via `internal/memory.Service`) for memories relevant to the current user turn, namespace `user/<user>/project/nanite/memory` (`recall.go:25-30`). Results are typed (`MemoryHit`: key, namespace, summary, body, similarity score, origin, tags, revision ID), not free text. Key thresholds (`types.go:23-39`): `DefaultRecallLimit=5`, `SimilarityThreshold=0.55` (only hits above this surface to the LLM), `MaxSurfacedMemories=3`, `MaxSurfaceTokens=200`. Two logging surfaces: `ConsultationLogger` (one row per hit per turn, `consumed` flag) and `Outcome`/`OutcomeKind` (`accepted`/`refined`/`unknown`, a heuristic derived from the user's follow-up turn, write-back via `RecordOutcome`) — this is how the mechanism evaluates its own usefulness over time.

`IsGroundingEnabled()` (`recall.go:53-63`) checks `NANITE_GROUNDING_ENABLED` (`true`/`1`/`yes`), **defaulting off**. Its own comment: *"The ticket specifies default OFF, matching UAT-time stance. A settings flag (`memory.grounding.enabled`) is the long-term home; for v1 the env var is the toggle."* — the settings-flag promotion path is itself unbuilt, separate scope from this task unless testing concludes grounding should be kept, in which case building that flag becomes a natural follow-up (not required by this task).

Live wiring: called from `internal/mcp/self_tools_dispatch.go`'s `callExecuteTask`, labeled "E2," running *before* "E1" (the reflex-match step) in the same function — deliberately ordered so recalled memory context can influence what the dispatch layer sees. `RecallTimeout = 1500ms` bounds the Vanta call; a timeout produces an empty (not error) result, so grounding can't hang a turn.

### A real, already-confirmed bug: grounding cannot fire today, even with the env var on

Verified directly, not inferred: `callExecuteTask`'s E2 block is entirely guarded by `if st.GroundingRecaller != nil` — and `SelfToolsTransport.GroundingRecaller`/`GroundingLogger` are **never assigned anywhere in `cmd/nanite/main.go`** (grepped the full file; contrast with `selfTools.Broker = agentBrokerInstance` and `selfTools.ReflexSet = ...`, which *are* wired). So `NANITE_GROUNDING_ENABLED=true` alone changes nothing — the field is `nil` at boot, full stop, regardless of the env var. This is not something testing will "surface" as a side effect; it's a structural fact confirmed by direct inspection, and it must be fixed *before* step 1 below can produce any real signal. The fix is small: `grounding.NewRecaller(svc *memory.Service, limit int)` needs a `*memory.Service`, and one is already constructed in the boot path for other memory-recall self-tools (`internal/service/container.go:659`, `memorySvc = memory.NewService(conduitInstance.MemoryStore())`) — wire `selfTools.GroundingRecaller = grounding.NewRecaller(memorySvc, 0)` and `selfTools.GroundingLogger = store` in `main.go` alongside the existing `selfTools.Broker`/`selfTools.ReflexSet` assignments.

## What to do

1. Fix the wiring gap above first: assign `selfTools.GroundingRecaller`/`selfTools.GroundingLogger` in `cmd/nanite/main.go` using the existing `memorySvc`. Confirm via a quick local check that the E2 block's `nil` guard now passes.
2. Flip `NANITE_GROUNDING_ENABLED=true` in a real dev session (not a fixture/unit test).
3. Exercise several real turns across varied topics and confirm: `grounding_consultations` rows land with sane similarity scores; the surfaced-memories block (`grounding.SystemPromptBlock`) actually appears in the assembled system prompt when a hit clears the 0.55 threshold; the `1500ms` timeout doesn't introduce a perceptible latency problem in practice.
4. Judge relevance qualitatively, not just mechanically — are the recalled memories actually useful context for the turn, or noise that happens to clear the similarity bar?
5. Let `Outcome`/`RecordOutcome` accumulate across several sessions; sanity-check whether the `accepted`/`refined`/`unknown` heuristic tracks with what actually happened in the conversation.
6. Based on this real evidence, record a keep/integrate-with-reflexes/re-architect/cut recommendation in this file's Work Log. If "integrate with reflexes" looks right, sketch (don't build) what that would mean — e.g. a grounding hit feeding a reflex predicate — as a forward pointer for whoever picks it up next, not a scope expansion of this task.

## Done means

- The `GroundingRecaller`/`GroundingLogger` wiring gap in `cmd/nanite/main.go` is fixed and verified (grounding actually fires with the env var on, not just "the flag is set").
- Grounding has been exercised in real chat sessions with the flag on, not just read/reviewed statically.
- This file's Work Log contains a concrete, evidence-based recommendation (keep/integrate/re-architect/cut) with the specific observations that led to it (e.g. "N turns tested, M produced relevant hits, X false positives," not a vague impression).
- If any further real bug surfaced during testing (beyond the wiring gap already known going in), it's fixed and noted; if the fix would be a larger redesign, it's flagged instead of attempted inline.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
