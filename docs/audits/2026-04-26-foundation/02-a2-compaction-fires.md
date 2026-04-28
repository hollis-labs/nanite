# A2 Audit: CompactionPipeline wire-or-retire

**Ticket:** CW-20260426-0002
**Date:** 2026-04-26
**Verdict:** WIRE (already wired — finalize the seam)
**Vanta key:** decisions_compaction_pipeline_wire

## Re-confirmation of 2026-04-11 audit

The 2026-04-11 finding ("AssembleSlots + CompactionPipeline are dormant; only `EnforceTokenBudget` runs") is **stale**. Between 2026-04-11 and the current `feat/arch-seq-phase-1-foundation` HEAD (`bb52283`), the pipeline was wired into the chat-generate hot path through three call sites:

| Site | File:Line | Trigger |
|------|-----------|---------|
| Pre-loop budget gate | `internal/service/chat_generate.go:389` (`enforceBudgetOrCompact`) | `result.NeedsCompaction == true` before the first provider call |
| Stream-start recovery | `internal/service/chat_generate.go:572` (`recoverFromContextOverflow`) | Provider returns `IsCompactRecoverable` (context_overflow OR `ErrRequestExceedsRateBudget`) |
| Mid-stream recovery | `internal/service/chat_generate.go:767` (`recoverFromContextOverflow`) | `error` event with context-overflow message during streaming |
| Manual `/compact` | `internal/api/sessions.go:319` (`RunForce`) | User-initiated |

Both `Run()` (gated on `Window.NeedsCompaction()`) and `RunForce()` (rate-budget trigger and manual) are reachable. Stages run in canonical order: `drop_enrichment` → `dedupe_tool_results` → `summarize_oldest` → `strip_tool_blocks` (`internal/context/compaction.go:83`). P7 HandoffStash writes pre-summarization. Tests cover the recovery surface (`internal/service/context_overflow_recovery_test.go`).

Audit finding 04 (`docs/audits/2026-04-11-context-management/04-high-auto-compaction-never-fires.md`) should be marked SUPERSEDED.

## EnforceTokenBudget today

`internal/chat/context_client.go:451`. Unchanged in shape from the 2026-04-11 audit: prune tool_results in-memory (`pruneToolResultsInMemory`) → drop tools from end → drop oldest messages → refuse with error. Called once per loop iteration at `internal/service/chat_generate.go:466`. Refusal triggers `EmitContextBudgetExceeded` and a stream error event. This is now the **hard-ceiling safety net** behind the pipeline, not the primary path — cf. the `enforceBudgetOrCompact` doc comment at `chat_generate.go:1450`.

## CompactionPipeline today

`internal/context/compaction.go`. Four stages defined (`DefaultStages()` line 83); all four run in production. Pipeline is instantiated in three production sites listed above. `CompactionResult` carries `StagesApplied`, `Mode`, `RemovedMessages`, `TokensSaved`, `HandoffStashID`. Dormant code: zero. `AssembleSlots` is called from `chat_generate.go:1369` (the hot path) and `api/sessions.go:311` (manual /compact).

## Decision: WIRE (finalize seam)

- **Capability delta**: the pipeline is strictly more capable than `EnforceTokenBudget` — it includes LLM summarization (`stageSummarizeOldest`) and dedupe (`stageDedupeToolResults`) that `EnforceTokenBudget` cannot do. RETIRE would lose recoverable summary content on every overflow.
- **Blast radius of WIRE**: zero. The wiring already exists; the remaining work is event-emission consistency (see seam below), not integration.
- **Blast radius of RETIRE**: high — would require ripping out `enforceBudgetOrCompact`, both `recoverFromContextOverflow` paths, the `/compact` endpoint, P7 HandoffStash hook, and the dedupe stage. None of that is dormant.
- **Audit-doc rot, not code rot**: the original audit pre-dated PRs that wired the path (P3/P7 era — e.g. `eb9990a feat(service): wire P3 classifier into generateResponse pre-loop`, `284e476 feat(service,api): wire HandoffStash stash writer into compaction pipeline`).
- **Per `feedback_no_compat_shims`**: nothing to delete, nothing to alias.

## compaction_event seam (for P8 part C)

The pipeline already emits three signals on every successful run (`enforceBudgetOrCompact` and both `recoverFromContextOverflow` returns):

1. **`slog.Info "chat-service: compact-recoverable recovery ran"`** — structured log, includes `tokens_before`, `tokens_after`, `stages`, `trigger_kind`, `session_id`. Not persisted.
2. **`chat.EmitSlotChangedEvent(ch, SlotChangedV1{Slot: SlotConversation, Change: SlotChangeSummarized, ...})`** — frontend stream envelope. Not persisted to a DB table.
3. **`pluginHost.EmitContextCompacted(sessionID, tokensSaved, stagesApplied)`** — fires `context.compacted` plugin event (`internal/plugin/events.go:582`). In-memory bus.

**Gap for P8 part C**: there is no `session_events`-table row written for compaction. The infrastructure exists — `messaging.Service.WriteSessionEvent(ctx, sessionID, eventType, channel, payloadJSON)` (`internal/messaging/events.go:123`) — and the chat service already uses it for `pty_turn_start`/`pty_turn_complete`. Adding `event_type="context_compacted"` rows from the three call sites is the minimal seam.

The `EventEmitter.EmitPreCompact`/`EmitPostCompact` interface methods (`internal/service/events.go:28-29`) and `ActivityEmitter.EmitPreCompact` (`internal/chat/activity.go:213`) are defined but **never called from production**. P8 part C should either route through these (cleaner) or write directly via `SessionEventWriter`.

## What the impl sub-agent needs to do

Scope: **emit a persisted compaction event from the three production call sites** so P8 part C has a queryable row.

- Add `s.events.EmitPreCompact(ctx, sessionID, len(chatMessages), triggerReason)` and `s.events.EmitPostCompact(ctx, sessionID, tokensSaved, cr.StagesApplied)` calls at:
  - `internal/service/chat_generate.go:1480` (pre-loop, just before `pipeline.Run`) and `~1505` (after the slot_changed emit)
  - `internal/service/chat_generate.go:1264` (recovery, just before `pipeline.Run/RunForce`) and `~1297` (after slot_changed emit)
  - `internal/api/sessions.go:332` (manual /compact, before `RunForce`) and `~342` (after `UpdateSessionCompaction`)
- Extend `events_composite.go` `EmitPreCompact` / `EmitPostCompact` to also write a `session_events` row (`event_type="context_pre_compact"` / `"context_post_compact"`, channel = trigger_kind, payload includes `tokens_before`, `tokens_after`, `tokens_saved`, `stages_applied`, `mode`).
- Test stub: extend `internal/service/context_overflow_recovery_test.go` with a fake `EventEmitter` capturing emitted events; assert ordering (Pre before Post) and that the payload carries trigger_kind.
- Expected event shape for P8 part C consumption: `{event_type: "context_post_compact", session_id, tokens_before, tokens_after, tokens_saved, stages_applied: [...], mode, trigger_kind}`.
- No deletion of `EnforceTokenBudget` — it remains the per-iteration hard-ceiling safety net.
- Mark `docs/audits/2026-04-11-context-management/04-high-auto-compaction-never-fires.md` SUPERSEDED with a pointer to this audit.
