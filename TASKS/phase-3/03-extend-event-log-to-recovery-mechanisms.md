# Extend `event_log` postmortem logging to Orphan Sweep, Recovery Pack, and interrupted-turn detection

**Phase:** 3
**Status:** implemented
**Depends on:** `TASKS/phase-0/32-rename-recovery-namespace.md` (moves the four recovery mechanisms into `internal/recovery/*` — this task's file paths assume that landed)
**Touches:** `internal/recovery/orphansweep/` (formerly `internal/runtime/agent/orphan_sweep.go`), `internal/recovery/pack/` (formerly `internal/service/recovery_pack.go`), `internal/recovery/` top-level interrupted-turn-detection logic (thin `*API` wrapper stays in `internal/api/sessions.go` per `32`'s own scoping), `internal/store/events.go` (`LogEvent` — read-only reuse, no schema change)

## Context

Architecture doc `06-session-lifecycle-and-recovery.md`: *"Only the Recovery Broker currently writes a queryable trail... extend `event_log` logging to all four."* Decision log §26 confirms this is Phase 5's job, sequenced after Phase 0 #32 sets up the namespace.

### Exact current state per mechanism, verified against real code

- **Recovery Broker** — writes only to `nanite_recovery_breadcrumbs` (`internal/store/migrations/055_recovery_breadcrumbs.sql`), via `broker.go`'s `writeBreadcrumb`. **No `event_log` write at all** — its "queryable trail" is a separate, purpose-built table, not `event_log`. This task does not need to add anything here (already has its own real postmortem trail), but confirm during implementation whether a parallel `event_log` entry is also wanted for consistency with the other three, or whether `nanite_recovery_breadcrumbs` remains its sole trail — a real, small scope question to settle, not silently assume.
- **Orphan Sweep** — `slog` only (`slog.Warn`/`slog.Info`). Zero `event_log`/store writes. **Real gap, this task's core job.**
- **Recovery Pack** — a correction to the decision log's original framing: `recovery_pack.go`'s `buildSessionRecoveryPrefix` already calls `s.store.LogEvent(sessionID, "recovery_pack_planted", "recovery", ...)` — a real, if single-marker (not full-postmortem), `event_log` row. This task's job here is enriching it to a real postmortem (what was replayed, how much context, any truncation), not building from zero.
- **Interrupted-turn detection** — `detectInterruptedTurn` (`internal/api/sessions.go:200` pre-rename, confirm post-`32` location) — read-only, no `event_log` write today.

### The write pattern to replicate

`internal/store/events.go:17`: `func (s *Store) LogEvent(sessionID, eventType, category, detail, metadata string)`, inserting into `event_log(session_id, event_type, category, detail, metadata)`. The precedent decision log §14 cites: `internal/service/chat_reflexes.go:35` — `s.store.LogEvent(session.ID, "reflex_action", "reflex", action.ReflexName, string(meta))`, `meta` a JSON-marshaled struct with real reasoning fields, not a bare event name. **Every new write site added by this task must follow this shape — a real, structured `metadata` payload (which branch/reason fired, relevant IDs, timing), not just an event-type string.**

## What to do

1. Add a real `event_log` write to Orphan Sweep on every reconciliation action (stale `agent_runtime` row found, PID confirmed dead, row updated) — `event_type="orphan_sweep_reconciled"`, `category="recovery"`, `metadata` carrying the PID, the row's prior state, and the reconciliation outcome.
2. Enrich Recovery Pack's existing single `"recovery_pack_planted"` marker into a real postmortem — `metadata` carrying what was replayed (message count, any truncation applied, source session).
3. Add a real `event_log` write when interrupted-turn detection actually fires (not just its read-only surfacing to the frontend) — `event_type="interrupted_turn_detected"`, `category="recovery"`, `metadata` carrying the session/turn context that triggered the heuristic.
4. Decide and document whether the Recovery Broker also gets a parallel `event_log` entry alongside its existing `nanite_recovery_breadcrumbs` trail, or stays breadcrumbs-only — this task's Work Log records the decision either way.

## Done means

- All four recovery mechanisms write a real, reasoning-populated `event_log` row (not a bare event name) for their respective postmortem-worthy actions.
- The Recovery Broker's disposition (parallel `event_log` entry, or breadcrumbs-only) is decided and documented.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Verified by triggering at least one real occurrence of each of the three newly-instrumented mechanisms (a real orphan reconciliation, a real recovery-pack replay, a real interrupted-turn detection) and confirming the `event_log` row lands with real, useful metadata.

## Work log

Dependency `TASKS/phase-0/32-rename-recovery-namespace.md` had already landed
in this worktree — verified `internal/recovery/{broker,orphansweep,pack}` and
`internal/recovery/interrupted_turn.go` exist exactly as this task's
`Touches` line describes, so all four file-path claims in the Context
section checked out against real code before any edit.

### 1. Orphan Sweep (`internal/recovery/orphansweep/orphan_sweep.go`)

`agent.RuntimeStore` (the interface `deps.Store` is typed as inside
`orphansweep`) had no logging capability at all — it's a narrow interface
(`CreateRuntimeRow`/`MarkRuntimeFailed`/`SetProviderSessionID`/
`GetCheckpoint`/`ListRunningRows`/`MarkRuntimeOrphaned`), not the concrete
`*store.Store`. Added a `LogEvent(sessionID, eventType, category, detail,
metadata string)` method to that interface
(`internal/runtime/agent/deps.go`), implemented on the one production
adapter `agentRuntimeStore` (`internal/service/agent_deps.go`) as a
straight passthrough to `store.Store.LogEvent`. Updated both `RuntimeStore`
test fakes (`internal/runtime/agent/fakes_test.go`'s `fakeRuntimeStore`,
`internal/recovery/orphansweep/orphan_sweep_test.go`'s separately-declared
`fakeRuntimeStore`) to satisfy the widened interface and capture calls for
assertions.

`sweepOrphansAt` now calls a new `logReconciliation` helper right after
`MarkRuntimeOrphaned` succeeds, writing `event_type=
"orphan_sweep_reconciled"`, `category="recovery"`, with a JSON
`orphanReconciledMeta{RuntimeID, Provider, Mode, StateBefore, PID, Reason,
ParentSessionID, UpdatedAtAge}` metadata payload — the row's prior state,
PID, and which of the three reconciliation reasons
(`dead_pid`/`no_live_session`/`pid_zero_stale`) fired, matching the
`chat_reflexes.go` "real struct, not bare event-type string" precedent.
`session_id` on the event_log row is the runtime row's ID (chat session ID
for `ModeLongLived` rows; a scoped subagent/background run ID otherwise —
`event_log.session_id` has no FK constraint, confirmed against
`001_schema.sql`, so this is always a safe write).

### 2. Recovery Pack (`internal/recovery/pack/pack.go`,
`internal/service/recovery_pack_glue.go`)

Confirmed the task's correction to the decision-log framing: the
`"recovery_pack_planted"` `LogEvent` call already existed
(`buildSessionRecoveryPrefix`), but with a hardcoded `"{}"` metadata blob —
a bare marker, not a postmortem. Added an exported
`pack.CountCharTruncatedMessages(history)` helper (reuses the package's
existing `recoveryMessageMaxChars` threshold rather than duplicating the
constant across packages) and enriched the glue-layer write with a
`recoveryPackPlantedMeta{SourceSessionID, Reason, MessagesReplayed,
MessagesAvailable, HistoryWindowCapped, MessagesCharTruncated,
RecoveryHistoryMax, PackPath}` JSON payload — what was actually replayed,
whether the 20-message trailing window truncated a longer history, and how
many individual messages were char-truncated at the 1500-char per-message
cap.

### 3. Interrupted-turn detection (`internal/api/sessions.go`)

Confirmed zero `event_log` write existed on this path — `detectInterruptedTurn`
was purely read-only, exactly as the task's Context stated. Added
`logInterruptedTurnDetected`, called only when
`recovery.DetectInterruptedTurn(...)` actually returns non-nil (i.e. the
heuristic fires), writing `event_type="interrupted_turn_detected"`,
`category="recovery"`, with an `interruptedTurnDetectedMeta{SessionID,
LastMessageID, LastMessageRole, LastActivityAt, Reason}` payload. This
handler fires on session (re)load / stalled-stream reconcile
(`ui/src/hooks/useChat.ts`'s `loadMessages`/`reconcileStreamingState`), not
on a tight poll loop, so no dedup/rate-limiting was added — matches the
existing `reflex_action` write's no-dedup precedent for a similarly-shaped
per-fire event.

### 4. Recovery Broker disposition — decided: breadcrumbs-only, no
parallel `event_log` write

This was the one open scope question the task flagged rather than
resolved. Decision: the Recovery Broker keeps `nanite_recovery_breadcrumbs`
as its sole postmortem trail; no parallel `event_log` write was added.

Reasoning:
- `Breadcrumb` (`internal/recovery/broker/types.go`) is already a fully
  structured, purpose-built row (`Class`, `Cause`, `Remediation`, `Action`,
  `Outcome`, `AttemptCount`, `DurationFromFailure`, `Reason`) — strictly
  richer for postmortem queries than `event_log`'s generic
  (`event_type`, `category`, `detail`, `metadata` JSON blob) shape. A
  parallel write would either re-derive the same fields into a JSON blob
  (redundant storage, two write sites that must stay in lockstep) or drop
  fidelity.
- Checked for an existing consumer that would need a unified
  `category="recovery"` view spanning all four mechanisms — none exists.
  The one hit for `event_log` + "recovery" context
  (`chat_broker_dispatch_integration_test.go`'s `agent_broker_decision`
  event) turned out to be an unrelated subsystem: the Agent Broker
  (profile-selection classifier in `internal/service`), not the Recovery
  Broker (crash recovery in `internal/recovery/broker`) — a real instance
  of the "broker" name being heavily overloaded in this codebase, worth
  flagging for anyone else who greps for "broker" + "event_log" later.
- The three mechanisms this task actually instruments (Orphan Sweep,
  Recovery Pack, interrupted-turn detection) had **no** existing structured
  trail before this task — extending `event_log` to them closes a real
  zero-coverage gap. The Recovery Broker already has one; duplicating it
  doesn't close a gap, it just adds a second copy with no current reader.
- This does not contradict the architecture doc's "extend event_log
  logging to all four" framing being wrong — per the task's own Context
  and per worker step 7 of `EXECUTION-PROCESS.md`, correcting the
  rationale doesn't reopen a settled `TASKS.md` action. But this specific
  sub-decision (Broker's disposition) was explicitly left open by the task
  itself ("a real, small scope question to settle, not silently assume"),
  not settled by `TASKS.md`, so it was decided here on the merits above
  rather than defaulted either way.

### Verification — real triggers, not just fakes

Per "Done means," each of the three newly-instrumented mechanisms was
verified with a genuine end-to-end trigger against a real SQLite
`*store.Store` (all `t.TempDir()`-rooted, per `EXECUTION-PROCESS.md` step
5's safety rule — none of these touch `AgentConfigService`/`writeManaged`
or any tracked `.nanite/agents/*.md` file), not just a fake/mock
assertion:

- **Orphan Sweep**: new `internal/service/orphan_sweep_smoke_test.go`
  (`TestSmoke_OrphanSweep_LogsRealEventLogRow`) constructs the real
  production `*agentRuntimeStore` adapter over a real store, seeds an
  `agent_runtime` row with a PID guaranteed dead, runs the real
  `orphansweep.SweepOrphans` (the exact function the daemon's
  `RuntimeReaper` calls), and confirms both the real `event_log` row (with
  `runtime_id`, `state_before`, `reason`, `pid` in the metadata) and the
  real `agent_runtime` state transition to `orphaned`. Also added a
  fake-store unit test (`TestSweepOrphans_LogsEventOnReconciliation` in
  `orphan_sweep_test.go`) covering the decision-matrix shape.
- **Recovery Pack**: extended the existing real-store acceptance test
  `TestComposeBootPayload_ColdBootInjectsRecovery`
  (`internal/service/recovery_pack_test.go`) — which already drives
  `composeBootPayload` against a real `*store.Store` with real persisted
  messages for a cold-boot scenario — to also assert the enriched
  `event_log` row (`messages_replayed=2`, real `source_session_id`,
  `history_window_capped=false`, non-empty `reason`).
- **Interrupted-turn detection**: extended the existing real-store HTTP
  handler test `TestHandleGetSession_InterruptedTurn`
  (`internal/api/interrupted_turn_test.go`) to assert the `event_log` row
  lands with `last_message_id`/`last_message_role`/`reason` on the
  positive (interrupted) case, and added negative-path assertions on the
  two non-interrupted subtests (completed turn, live stream) confirming no
  event is falsely logged.

All three real-trigger tests pass (`go test ./internal/service/ -run
TestSmoke_OrphanSweep_LogsRealEventLogRow`, `-run
TestComposeBootPayload_ColdBootInjectsRecovery`, and `go test
./internal/api/ -run TestHandleGetSession_InterruptedTurn`), each run with
`-count=1` to rule out a cached false-positive.

### Baseline checks

- `go build ./cmd/nanite/` — passes.
- `go vet ./...` — 4 pre-existing findings in `internal/service/container.go`
  (lostcancel on `stopReaper`/`stopRuntimeReaper`), confirmed via
  `git stash` to predate this task's diff entirely; not touched by this
  task's changes.
- `go test ./...` — all packages pass, including the new/extended tests
  above.
- `git status --short` checked immediately after every verification run
  (not just at the end) — clean at each check; only the intended files
  under `internal/` were modified, plus the one new test file
  (`internal/service/orphan_sweep_smoke_test.go`).

### Files touched

- `internal/runtime/agent/deps.go` — `RuntimeStore.LogEvent` added.
- `internal/service/agent_deps.go` — `agentRuntimeStore.LogEvent` impl.
- `internal/runtime/agent/fakes_test.go`,
  `internal/recovery/orphansweep/orphan_sweep_test.go` — fake stores
  updated to satisfy the widened interface + capture events; new
  `TestSweepOrphans_LogsEventOnReconciliation`.
- `internal/recovery/orphansweep/orphan_sweep.go` — `logReconciliation` +
  `orphanReconciledMeta`.
- `internal/recovery/pack/pack.go` — `CountCharTruncatedMessages`.
- `internal/service/recovery_pack_glue.go` — enriched
  `recovery_pack_planted` metadata; `recoveryPackPlantedMeta`/
  `recoveryPackPlantedMetadata`.
- `internal/service/recovery_pack_test.go` — extended acceptance test.
- `internal/api/sessions.go` — `logInterruptedTurnDetected` +
  `interruptedTurnDetectedMeta`.
- `internal/api/interrupted_turn_test.go` — extended handler test.
- `internal/service/orphan_sweep_smoke_test.go` — new real-store smoke
  test (real-trigger verification for Orphan Sweep).

No deviation from the task's `What to do` list beyond the explicitly-invited
Recovery Broker scope decision above. No escalation needed — the one
apparent doc/code mismatch (Recovery Broker having its own breadcrumbs
trail rather than `event_log`) was already correctly identified and
pre-resolved as "decide, don't silently assume" by the task file itself,
per worker step 7 / the Context section's own framing, not a genuine
stop-condition ambiguity.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
