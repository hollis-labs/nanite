# Extend `event_log` postmortem logging to Orphan Sweep, Recovery Pack, and interrupted-turn detection

**Phase:** 5
**Status:** not-started
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
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
